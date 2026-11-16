package agent

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrNotFound  = errors.New("сессия не найдена")
	ErrClosed    = errors.New("движок остановлен")
	ErrQueueFull = errors.New("очередь сообщений заполнена")
	ErrCursor    = errors.New("номер события опережает историю сессии")
)

const maxPending = 64

type Event struct {
	ID       uint64    `json:"id"`
	Type     string    `json:"type"`
	Messages []Message `json:"messages,omitempty"`
	Text     string    `json:"text,omitempty"`
}

type Engine struct {
	ctx      context.Context
	cancel   context.CancelFunc
	factory  func() Agent
	mu       sync.Mutex
	sessions map[string]*session
	workers  sync.WaitGroup
}

func New(factory func() Agent) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{ctx: ctx, cancel: cancel, factory: factory, sessions: make(map[string]*session)}
}

func (e *Engine) Send(id, text string) (Message, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ctx.Err() != nil {
		return Message{}, ErrClosed
	}
	s := e.sessions[id]
	if s == nil {
		s = &session{
			ctx:     e.ctx,
			agent:   e.factory(),
			wake:    make(chan struct{}, 1),
			changed: make(chan struct{}),
		}
		e.sessions[id] = s
		e.workers.Add(1)
		go func() {
			defer e.workers.Done()
			s.run()
		}()
	}
	return s.send(text)
}

func (e *Engine) find(id string) (*session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ctx.Err() != nil {
		return nil, ErrClosed
	}
	s := e.sessions[id]
	if s == nil {
		return nil, ErrNotFound
	}
	return s, nil
}

func (e *Engine) Stop(id string) error {
	s, err := e.find(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Отмена и очистка очереди происходят вместе: старые сообщения не запустят агента снова.
	s.append(Event{Type: "stop_requested", Messages: s.pending})
	s.pending = nil
	if s.cancel != nil {
		s.cancel()
	} else {
		s.append(Event{Type: "stopped"})
	}
	return nil
}

func (e *Engine) Close() {
	e.mu.Lock()
	e.cancel()
	e.mu.Unlock()
	e.workers.Wait()
}

type session struct {
	ctx   context.Context
	agent Agent
	wake  chan struct{}

	mu      sync.Mutex
	pending []Message
	nextID  uint64
	cancel  context.CancelFunc
	history []Event
	changed chan struct{}
}

func (s *session) send(text string) (Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) >= maxPending {
		return Message{}, ErrQueueFull
	}
	s.nextID++
	message := Message{ID: s.nextID, Text: text}
	s.pending = append(s.pending, message)
	s.append(Event{Type: "queued", Messages: []Message{message}})
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return message, nil
}

// append вызывается под s.mu. Подписчики читают общий журнал и не задерживают агента.
func (s *session) append(event Event) {
	event.ID = uint64(len(s.history)) + 1
	// Агент получает собственный набор сообщений и не может изменить уже записанное событие.
	event.Messages = append([]Message(nil), event.Messages...)
	s.history = append(s.history, event)
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *session) events(after uint64) ([]Event, <-chan struct{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if after > uint64(len(s.history)) {
		return nil, nil, ErrCursor
	}
	// Снимок и канал следующего изменения получаем под одним замком, без потери событий.
	return append([]Event(nil), s.history[after:]...), s.changed, nil
}

func (s *session) run() {
	for {
		s.mu.Lock()
		if s.ctx.Err() != nil {
			s.mu.Unlock()
			return
		}
		if len(s.pending) == 0 {
			s.mu.Unlock()
			select {
			case <-s.ctx.Done():
				return
			case <-s.wake:
				continue
			}
		}
		ctx, cancel := context.WithCancel(s.ctx)
		s.cancel = cancel
		s.mu.Unlock()

		s.generate(ctx)
		cancel()
	}
}

func (s *session) generate(ctx context.Context) {
	emit := func(text string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if ctx.Err() == nil {
			s.append(Event{Type: "delta", Text: text})
		}
	}
	for {
		s.mu.Lock()
		if ctx.Err() != nil {
			s.append(Event{Type: "stopped"})
			s.cancel = nil
			s.mu.Unlock()
			return
		}
		messages := s.pending
		s.pending = nil
		s.append(Event{Type: "step_started", Messages: messages})
		s.mu.Unlock()

		more, err := s.agent.Step(ctx, messages, emit)

		s.mu.Lock()
		switch {
		case ctx.Err() != nil:
			s.append(Event{Type: "stopped"})
		case err != nil:
			s.append(Event{Type: "error", Text: err.Error()})
		case more || len(s.pending) > 0:
			s.mu.Unlock()
			continue
		default:
			s.append(Event{Type: "completed"})
		}
		s.cancel = nil
		s.mu.Unlock()
		return
	}
}
