package agentcore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

var ErrSessionClosed = errors.New("session stopped")

// Session owns one TurnLoop. Callbacks read the model and persist turns;
// emit hands events to the application independently of client connections.
type Session struct {
	id      string
	store   Store
	factory AgentFactory
	emit    func(Event)
	loop    *adk.TurnLoop[*UserCommand, *schema.AgenticMessage]
	turn    atomic.Pointer[turnControl]
	done    chan struct{}
	exitErr error // Read only after done closes.
}

type turnControl struct {
	cancel  context.CancelFunc
	stopped atomic.Bool
}

func NewSession(ctx context.Context, id string, store Store, factory AgentFactory, emit func(Event)) *Session {
	s := &Session{
		id:      id,
		store:   store,
		factory: factory,
		emit:    emit,
		done:    make(chan struct{}),
	}
	s.loop = adk.NewTurnLoop(adk.TurnLoopConfig[*UserCommand, *schema.AgenticMessage]{
		GenInput:      s.genInput,
		PrepareAgent:  s.prepareAgent,
		OnAgentEvents: s.onAgentEvents,
	})
	s.loop.Run(ctx)
	go s.wait()
	return s
}

func (s *Session) Send(text string, preempt bool) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("message must not be empty")
	}
	select {
	case <-s.done:
		return ErrSessionClosed
	default:
	}
	command := &UserCommand{Text: text, Preempt: preempt}
	var options []adk.PushOption[*UserCommand, *schema.AgenticMessage]
	if preempt {
		options = append(options, adk.WithPreemptTimeout[*UserCommand, *schema.AgenticMessage](adk.AnySafePoint, 2*time.Second))
	}
	if accepted, _ := s.loop.Push(command, options...); !accepted {
		return ErrSessionClosed
	}
	return nil
}

// Stop cancels the turn observed by this call, leaving the loop and queue alive.
func (s *Session) Stop() {
	if turn := s.turn.Load(); turn != nil {
		turn.stopped.Store(true)
		turn.cancel()
	}
}

func (s *Session) Shutdown() {
	s.Stop()
	s.loop.Stop(adk.WithGracefulTimeout(2 * time.Second))
	<-s.done
}

func (s *Session) Wait() error {
	<-s.done
	return s.exitErr
}

func (s *Session) wait() {
	s.exitErr = s.loop.Wait().ExitReason
	s.releaseTurn(s.turn.Load())
	if s.exitErr != nil && !isCancellation(s.exitErr) {
		s.publish(Event{Type: EventError, Text: s.exitErr.Error()})
	}
	close(s.done)
}

func (s *Session) releaseTurn(turn *turnControl) {
	if turn != nil {
		s.turn.CompareAndSwap(turn, nil)
		turn.cancel()
	}
}

func (s *Session) genInput(ctx context.Context, _ *adk.TurnLoop[*UserCommand, *schema.AgenticMessage], items []*UserCommand) (*adk.GenInputResult[*UserCommand, *schema.AgenticMessage], error) {
	turnCtx, cancel := context.WithCancel(ctx)
	control := &turnControl{cancel: cancel}
	s.turn.Store(control)
	message := schema.UserAgenticMessage(items[0].Text)
	if err := s.store.AppendUser(ctx, s.id, message); err != nil {
		s.releaseTurn(control)
		return nil, err
	}
	history, err := s.store.Load(ctx, s.id)
	if err != nil {
		s.releaseTurn(control)
		return nil, err
	}
	s.publish(Event{Type: EventUserMsg, Text: items[0].Text})
	return &adk.GenInputResult[*UserCommand, *schema.AgenticMessage]{
		RunCtx: turnCtx,
		Input: &adk.TypedAgentInput[*schema.AgenticMessage]{
			Messages:        history,
			EnableStreaming: true,
		},
		Consumed:  items[:1],
		Remaining: items[1:],
	}, nil
}

func (s *Session) prepareAgent(ctx context.Context, _ *adk.TurnLoop[*UserCommand, *schema.AgenticMessage], commands []*UserCommand) (adk.TypedAgent[*schema.AgenticMessage], error) {
	agent, err := s.factory(ctx, commands)
	if err != nil {
		s.releaseTurn(s.turn.Load())
	}
	return agent, err
}

func (s *Session) onAgentEvents(ctx context.Context, turn *adk.TurnContext[*UserCommand, *schema.AgenticMessage], events *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.AgenticMessage]]) error {
	control := s.turn.Load()
	defer s.releaseTurn(control)
	s.publish(Event{Type: EventTurnStart})
	result := TurnResult{Status: TurnCompleted}
	var generationErr error
	for {
		event, ok := events.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}
		generationErr = errors.Join(generationErr, event.Err)
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		output := event.Output.MessageOutput
		if output.IsStreaming {
			if output.MessageStream == nil {
				generationErr = errors.Join(generationErr, errors.New("missing message stream"))
				continue
			}
			message, err := s.readStream(event.AgentName, output.MessageStream)
			generationErr = errors.Join(generationErr, err)
			if message != nil {
				result.Messages = append(result.Messages, message)
			}
		} else if output.Message != nil {
			s.publishMessage(event.AgentName, output.Message)
			result.Messages = append(result.Messages, output.Message)
		}
	}
	generationErr = errors.Join(generationErr, ctx.Err())
	if generationErr != nil {
		result.Status, result.Error = TurnFailed, generationErr.Error()
		if isCancellation(generationErr) {
			result.Status = TurnCancelled
		}
	}
	if control != nil && control.stopped.Load() {
		result.Status = TurnCancelled
	}
	select {
	case <-turn.Preempted:
		result.Status = TurnPreempted
	default:
	}

	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.store.SaveTurn(saveCtx, s.id, result); err != nil {
		return fmt.Errorf("save turn: %w", err)
	}
	if result.Status == TurnFailed {
		s.publish(Event{Type: EventError, Text: result.Error})
	}
	if result.Status == TurnPreempted {
		s.publish(Event{Type: EventTurnPreempt})
	}
	s.publish(Event{Type: EventTurnEnd, Status: result.Status})
	return nil
}

func (s *Session) readStream(agentName string, stream *schema.StreamReader[*schema.AgenticMessage]) (*schema.AgenticMessage, error) {
	defer stream.Close()
	var chunks []*schema.AgenticMessage
	var streamErr error
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			streamErr = err
			break
		}
		if chunk != nil {
			chunks = append(chunks, chunk)
			s.publishMessage(agentName, chunk)
		}
	}
	if len(chunks) == 0 {
		return nil, streamErr
	}
	message, concatErr := schema.ConcatAgenticMessages(chunks)
	return message, errors.Join(streamErr, concatErr)
}

func (s *Session) publishMessage(agentName string, message *schema.AgenticMessage) {
	for _, block := range message.ContentBlocks {
		if block == nil {
			continue
		}
		switch block.Type {
		case schema.ContentBlockTypeReasoning:
			if block.Reasoning != nil && block.Reasoning.Text != "" {
				s.publish(Event{Type: EventThinking, AgentName: agentName, Text: block.Reasoning.Text})
			}
		case schema.ContentBlockTypeAssistantGenText:
			if block.AssistantGenText != nil && block.AssistantGenText.Text != "" {
				s.publish(Event{Type: EventText, AgentName: agentName, Text: block.AssistantGenText.Text})
			}
		}
	}
}

func (s *Session) publish(event Event) {
	event.SessionID = s.id
	if s.emit != nil {
		s.emit(event)
	}
}

func isCancellation(err error) bool {
	var cancelErr *adk.CancelError
	return errors.As(err, &cancelErr) || errors.Is(err, context.Canceled) || errors.Is(err, adk.ErrStreamCanceled)
}
