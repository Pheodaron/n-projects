package agentcore

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

type Session struct {
	id      string
	bgCtx   context.Context
	store   HistoryStore
	factory AgentFactory

	loopOnce sync.Once
	loop     *adk.TurnLoop[*UserCommand, *schema.AgenticMessage]

	cancelTurn atomic.Pointer[context.CancelFunc] // Отмена текущего витка (для кнопки Stop)
	inFlight   atomic.Pointer[[]Event]            // Буфер текущего стрима для реконнекта
	subs       sync.Map                           // key: chan Event, value: struct{}
}

func newSession(bgCtx context.Context, id string, store HistoryStore, factory AgentFactory) *Session {
	s := &Session{
		id:      id,
		store:   store,
		factory: factory,
	}

	s.loop = adk.NewTurnLoop(s.buildLoopConfig())
	s.loop.Run(bgCtx) // Запускается один раз; в простое спит с 0% CPU

	return s
}

// Send напрямую пушит команду в потокобезопасный TurnLoop.
func (s *Session) Send(text string, preempt bool) {
	cmd := &UserCommand{Text: text, Preempt: preempt}
	if !preempt {
		s.loop.Push(cmd)
		return
	}

	_, ack := s.loop.Push(cmd, adk.WithPushStrategy(
		func(ctx context.Context, tc *adk.TurnContext[*UserCommand, *schema.AgenticMessage]) []adk.PushOption[*UserCommand, *schema.AgenticMessage] {
			if tc == nil {
				return nil
			}
			return []adk.PushOption[*UserCommand, *schema.AgenticMessage]{
				adk.WithPreemptTimeout[*UserCommand, *schema.AgenticMessage](adk.AnySafePoint, 2*time.Second),
			}
		},
	))
	<-ack
}

// Stop отменяет только ТЕКУЩИЙ виток генерации. Сам TurnLoop и подписчики продолжают жить!
func (s *Session) Stop() {
	if cancel := s.cancelTurn.Load(); cancel != nil {
		(*cancel)()
	}
}

// Shutdown вызывается только при полном выходе из приложения.
func (s *Session) Shutdown() {
	s.Stop()
	s.loop.Stop(adk.WithGracefulTimeout(2 * time.Second))
	s.loop.Wait()

	s.subs.Range(func(key, _ any) bool {
		if _, loaded := s.subs.LoadAndDelete(key); loaded {
			close(key.(chan Event))
		}
		return true
	})
}

func (s *Session) buildLoopConfig() adk.TurnLoopConfig[*UserCommand, *schema.AgenticMessage] {
	return adk.TurnLoopConfig[*UserCommand, *schema.AgenticMessage]{
		GenInput: func(ctx context.Context, l *adk.TurnLoop[*UserCommand, *schema.AgenticMessage], items []*UserCommand) (*adk.GenInputResult[*UserCommand, *schema.AgenticMessage], error) {
			for _, cmd := range items {
				userMsg := schema.UserAgenticMessage(cmd.Text)
				if err := s.store.Append(ctx, s.id, userMsg); err != nil {
					return nil, err
				}
				s.emit(Event{Type: EventUserMsg, Text: cmd.Text})
			}

			history, err := s.store.Load(ctx, s.id)
			if err != nil {
				return nil, err
			}

			// Контекст конкретного витка: его можно отменить через s.Stop(), не останавливая сам TurnLoop
			turnCtx, cancel := context.WithCancel(ctx)
			s.cancelTurn.Store(&cancel)

			return &adk.GenInputResult[*UserCommand, *schema.AgenticMessage]{
				RunCtx: turnCtx,
				Input: &adk.TypedAgentInput[*schema.AgenticMessage]{
					Messages:        history,
					EnableStreaming: true,
				},
				Consumed:  items,
				Remaining: nil,
			}, nil
		},

		PrepareAgent: func(ctx context.Context, l *adk.TurnLoop[*UserCommand, *schema.AgenticMessage], consumed []*UserCommand) (adk.TypedAgent[*schema.AgenticMessage], error) {
			return s.factory(ctx, consumed)
		},

		OnAgentEvents: func(ctx context.Context, tc *adk.TurnContext[*UserCommand, *schema.AgenticMessage], events *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.AgenticMessage]]) error {
			s.emit(Event{Type: EventTurnStart})
			defer func() {
				if cancel := s.cancelTurn.Swap(nil); cancel != nil {
					(*cancel)()
				}
				s.emit(Event{Type: EventTurnEnd})
			}()

			go func() {
				select {
				case <-tc.Preempted:
					s.emit(Event{Type: EventTurnPreempt})
				case <-ctx.Done():
				}
			}()

			for {
				ev, ok := events.Next()
				if !ok {
					break
				}
				if ev.Err != nil {
					var cancelErr *adk.CancelError
					if errors.As(ev.Err, &cancelErr) || errors.Is(ev.Err, context.Canceled) {
						return nil
					}
					s.emit(Event{Type: EventError, Text: ev.Err.Error()})
					return ev.Err
				}

				if ev.Output == nil || ev.Output.MessageOutput == nil {
					continue
				}
				agentName := ev.AgentName
				mo := ev.Output.MessageOutput

				if mo.IsStreaming && mo.MessageStream != nil {
					var chunks []*schema.AgenticMessage
					for {
						chunk, err := mo.MessageStream.Recv()
						if errors.Is(err, io.EOF) {
							break
						}
						if err != nil {
							break
						}
						chunks = append(chunks, chunk)

						for _, b := range chunk.ContentBlocks {
							switch b.Type {
							case schema.ContentBlockTypeReasoning:
								if b.Reasoning != nil && b.Reasoning.Text != "" {
									s.emit(Event{Type: EventThinking, AgentName: agentName, Text: b.Reasoning.Text})
								}
							case schema.ContentBlockTypeAssistantGenText:
								if b.AssistantGenText != nil && b.AssistantGenText.Text != "" {
									s.emit(Event{Type: EventText, AgentName: agentName, Text: b.AssistantGenText.Text})
								}
							}
						}
					}
					mo.MessageStream.Close()

					if len(chunks) > 0 {
						if fullMsg, err := schema.ConcatAgenticMessages(chunks); err == nil && fullMsg != nil {
							if err := s.store.Append(context.WithoutCancel(ctx), s.id, fullMsg); err != nil {
								return err
							}
						}
					}
				} else if mo.Message != nil {
					if err := s.store.Append(context.WithoutCancel(ctx), s.id, mo.Message); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
}

// emit вызывается только из горутины TurnLoop (единственный писатель в inFlight).
func (s *Session) emit(ev Event) {
	ev.SessionID = s.id

	switch ev.Type {
	case EventTurnStart:
		buf := []Event{ev}
		s.inFlight.Store(&buf)

	case EventThinking, EventText:
		var cur []Event
		if p := s.inFlight.Load(); p != nil {
			cur = append(cur, (*p)...)
		}
		n := len(cur)
		if n > 0 && cur[n-1].Type == ev.Type && cur[n-1].AgentName == ev.AgentName {
			cur[n-1].Text += ev.Text
		} else {
			cur = append(cur, ev)
		}
		s.inFlight.Store(&cur)

	case EventTurnEnd:
		s.inFlight.Store(nil)

	default:
		if p := s.inFlight.Load(); p != nil {
			cur := append([]Event(nil), (*p)...)
			cur = append(cur, ev)
			s.inFlight.Store(&cur)
		}
	}

	s.subs.Range(func(key, _ any) bool {
		ch := key.(chan Event)
		select {
		case ch <- ev:
		default:
		}
		return true
	})
}

func (s *Session) Subscribe() (<-chan Event, func()) {
	var snapshot []Event
	if p := s.inFlight.Load(); p != nil {
		snapshot = *p
	}

	ch := make(chan Event, len(snapshot)+256)
	for _, ev := range snapshot {
		ch <- ev
	}
	s.subs.Store(ch, struct{}{})

	unsub := func() {
		if _, loaded := s.subs.LoadAndDelete(ch); loaded {
			close(ch)
		}
	}
	return ch, unsub
}
