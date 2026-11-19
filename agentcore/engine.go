package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/rs/xid"
)

type Engine struct {
	bgCtx   context.Context
	storage Storage
	agent   adk.TypedAgent[*schema.AgenticMessage]

	mu      sync.Mutex
	closed  bool
	runners map[string]*sessionRunner
	events  map[string]*sessionEvents
}

func NewEngine(
	bgCtx context.Context,
	storage Storage,
	agent adk.TypedAgent[*schema.AgenticMessage],
) *Engine {
	return &Engine{
		bgCtx:   bgCtx,
		storage: storage,
		agent:   agent,
		runners: make(map[string]*sessionRunner),
		events:  make(map[string]*sessionEvents),
	}
}

// CreateSession создает сессию в БД и возвращает сгенерированный xid.
func (e *Engine) CreateSession(ctx context.Context) (string, error) {
	session := Session{
		ID:        xid.New().String(),
		CreatedAt: time.Now().UTC(),
	}

	if err := e.storage.CreateSession(ctx, session); err != nil {
		return "", fmt.Errorf("create session in storage: %w", err)
	}

	return session.ID, nil
}

func (e *Engine) RenameSession(ctx context.Context, sessionID, title string) error {
	title = strings.TrimSpace(title)
	if title == "" || strings.ContainsAny(title, "\r\n\x00") {
		return ErrInvalidSessionTitle
	}
	return e.storage.RenameSession(ctx, sessionID, title)
}

// ListSessions uses one-based pages. A zero pageSize selects the default of 50.
func (e *Engine) ListSessions(ctx context.Context, page, pageSize int32) (SessionPage, error) {
	if page < 1 {
		return SessionPage{}, fmt.Errorf("%w: page must be at least 1", ErrInvalidSessionPage)
	}
	if pageSize == 0 {
		pageSize = 50
	}
	if pageSize < 1 || pageSize > 100 {
		return SessionPage{}, fmt.Errorf("%w: page size must be between 1 and 100", ErrInvalidSessionPage)
	}
	offset := int64(page-1) * int64(pageSize)
	rows, err := e.storage.ListSessions(ctx, int(pageSize), offset)
	if err != nil {
		return SessionPage{}, fmt.Errorf("list sessions: %w", err)
	}
	if rows == nil {
		rows = []Session{}
	}
	for i := range rows {
		if rows[i].Title == "" {
			rows[i].Title = untitledSession
		}
	}
	return SessionPage{Sessions: rows}, nil
}

// Вызывается под e.mu. Окна и последовательные исполнители чата используют один поток.
func (e *Engine) sessionEventsLocked(id string) *sessionEvents {
	events := e.events[id]
	if events == nil {
		events = &sessionEvents{sessionID: id, subscribers: make(map[chan Event]struct{})}
		e.events[id] = events
	}
	return events
}

func (e *Engine) getOrCreateRunner(ctx context.Context, sessionID string) (*sessionRunner, error) {
	if err := e.storage.SessionExists(ctx, sessionID); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed || e.bgCtx.Err() != nil {
		return nil, ErrSessionStopped
	}
	if runner, exists := e.runners[sessionID]; exists {
		return runner, nil
	}

	events := e.sessionEventsLocked(sessionID)
	runner := newSessionRunner(e.bgCtx, sessionID, e.storage, e.agent, events, func(reason stopReason, err error) {
		e.mu.Lock()
		defer e.mu.Unlock()
		delete(e.runners, sessionID)
		if reason != stopByUser {
			events.Close(err)
			delete(e.events, sessionID)
			return
		}
		if err != nil {
			events.Publish(Event{Type: EventError, Text: err.Error()})
		}
		events.Publish(Event{Type: EventTurnStopped})
		if len(events.subscribers) == 0 {
			delete(e.events, sessionID)
		}
	})
	e.runners[sessionID] = runner
	return runner, nil
}

func (e *Engine) Send(ctx context.Context, sessionID string, text string, preempt bool) error {
	runner, err := e.getOrCreateRunner(ctx, sessionID)
	if err != nil {
		return err
	}
	return runner.Send(ctx, text, preempt)
}

// StopTurn останавливает исполнитель чата и отбрасывает ожидающие команды.
// Подписки окон остаются открытыми для следующего Send.
func (e *Engine) StopTurn(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	runner := e.runners[sessionID]
	closed := e.closed || e.bgCtx.Err() != nil
	e.mu.Unlock()
	if closed {
		return ErrSessionStopped
	}
	if runner == nil {
		return e.storage.SessionExists(ctx, sessionID)
	}

	runner.Stop(stopByUser)
	select {
	case <-runner.done:
		return runner.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Engine) Subscribe(ctx context.Context, sessionID string) (iter.Seq[Event], error) {
	if err := e.storage.SessionExists(ctx, sessionID); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.bgCtx.Err() != nil {
		return nil, ErrSessionStopped
	}

	return func(yield func(Event) bool) {
		e.mu.Lock()
		if e.closed || e.bgCtx.Err() != nil || ctx.Err() != nil {
			e.mu.Unlock()
			return
		}
		events := e.sessionEventsLocked(sessionID)
		ch := events.register()
		e.mu.Unlock()
		defer func() {
			e.mu.Lock()
			defer e.mu.Unlock()
			events.unregister(ch)
			if e.events[sessionID] == events && e.runners[sessionID] == nil && len(events.subscribers) == 0 {
				delete(e.events, sessionID)
			}
		}()
		events.listen(ctx, e.bgCtx, ch, yield)
	}, nil
}

type HistoryMessage struct {
	ID        int64          `json:"id,string"`
	CreatedAt time.Time      `json:"createdAt"`
	Role      string         `json:"role"`
	Text      string         `json:"text"`
	Blocks    []HistoryBlock `json:"blocks"`
}

type HistoryBlock struct {
	Type string          `json:"type"`
	Text string          `json:"text,omitempty"`
	Tool json.RawMessage `json:"tool,omitempty"`
}

func historyBlock(role schema.AgenticRoleType, block *schema.ContentBlock) (HistoryBlock, error) {
	switch {
	case role == schema.AgenticRoleTypeUser && block.Type == schema.ContentBlockTypeUserInputText && block.UserInputText != nil:
		if text := block.UserInputText.Text; text != "" {
			return HistoryBlock{Type: "text", Text: text}, nil
		}
	case role == schema.AgenticRoleTypeAssistant && block.Type == schema.ContentBlockTypeAssistantGenText && block.AssistantGenText != nil:
		if text := block.AssistantGenText.Text; text != "" {
			return HistoryBlock{Type: "text", Text: text}, nil
		}
	default:
		kind, data, err := encodeToolBlock(block)
		return HistoryBlock{Type: string(kind), Tool: data}, err
	}
	return HistoryBlock{}, nil
}

func (e *Engine) History(ctx context.Context, sessionID string) ([]HistoryMessage, error) {
	if err := e.storage.SessionExists(ctx, sessionID); err != nil {
		return nil, err
	}
	messages, err := e.storage.GetMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	history := make([]HistoryMessage, 0, len(messages))
	for _, record := range messages {
		msg := record.Message
		if msg == nil {
			continue
		}
		entry := HistoryMessage{ID: record.ID, CreatedAt: record.CreatedAt, Role: string(msg.Role)}
		var text strings.Builder
		for _, block := range msg.ContentBlocks {
			if block == nil {
				continue
			}
			part, err := historyBlock(msg.Role, block)
			if err != nil {
				return nil, fmt.Errorf("encode history block: %w", err)
			}
			if part.Type != "" {
				entry.Blocks = append(entry.Blocks, part)
				text.WriteString(part.Text)
			}
		}
		if len(entry.Blocks) > 0 {
			entry.Text = text.String()
			history = append(history, entry)
		}
	}
	return history, nil
}

func (e *Engine) Shutdown() {
	e.mu.Lock()
	e.closed = true
	// Исполнители удаляются из карты по мере завершения.
	runners := maps.Clone(e.runners)
	e.mu.Unlock()

	for _, runner := range runners {
		runner.Stop(stopOnShutdown)
	}
	for _, runner := range runners {
		<-runner.done
	}

	e.mu.Lock()
	e.runners = nil
	for _, events := range e.events {
		events.Close(nil)
	}
	e.events = nil
	e.mu.Unlock()
}
