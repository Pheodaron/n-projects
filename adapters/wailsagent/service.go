package wailsagent

import (
	"context"

	"my-gitlab/ai-support/n-projects/agentcore"
)

// Service exposes Engine operations as Wails bindings. The desktop app supplies
// the event emitter so it can route updates to the calling window.
type Service struct {
	engine *agentcore.Engine
	emit   func(context.Context, agentcore.Event)
}

func NewService(engine *agentcore.Engine, emit func(context.Context, agentcore.Event)) *Service {
	return &Service{engine: engine, emit: emit}
}

func (s *Service) CreateSession(ctx context.Context) (string, error) {
	return s.engine.CreateSession(ctx)
}

func (s *Service) ListSessions(ctx context.Context, page, pageSize int32) (agentcore.SessionPage, error) {
	return s.engine.ListSessions(ctx, page, pageSize)
}

func (s *Service) RenameSession(ctx context.Context, sessionID, title string) error {
	return s.engine.RenameSession(ctx, sessionID, title)
}

func (s *Service) Send(ctx context.Context, sessionID, text string, preempt bool) error {
	return s.engine.Send(ctx, sessionID, text, preempt)
}

func (s *Service) History(ctx context.Context, sessionID string) ([]agentcore.HistoryMessage, error) {
	return s.engine.History(ctx, sessionID)
}

// Subscribe runs for the lifetime of the binding call. Cancelling that call
// releases the subscription independently of the agent's current turn.
func (s *Service) Subscribe(ctx context.Context, sessionID string) error {
	events, err := s.engine.Subscribe(ctx, sessionID)
	if err != nil {
		return err
	}
	for event := range events {
		s.emit(ctx, event)
	}
	return nil
}
