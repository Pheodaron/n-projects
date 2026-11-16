package agentcore

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

type AgentFactory func(context.Context, []*UserCommand) (adk.TypedAgent[*schema.AgenticMessage], error)

// SaveTurn commits the messages and their outcome together. Implementations must
// support concurrent sessions and return messages independent of stored state.
type Store interface {
	Load(context.Context, string) ([]*schema.AgenticMessage, error)
	AppendUser(context.Context, string, *schema.AgenticMessage) error
	SaveTurn(context.Context, string, TurnResult) error
}

// MemoryStore publishes immutable JSON records through sync.Map. There are no
// application mutexes, and caller mutations cannot change persisted messages.
// Provider-specific values in Extra/Extension follow JSON round-trip semantics.
type MemoryStore struct {
	sessions sync.Map // string -> *memoryHistory
}

type memoryHistory struct {
	messages []json.RawMessage
	turns    []json.RawMessage
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{} }

func (m *MemoryStore) Load(ctx context.Context, id string) ([]*schema.AgenticMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, ok := m.sessions.Load(id)
	if !ok {
		return nil, nil
	}
	var messages []*schema.AgenticMessage
	for _, data := range value.(*memoryHistory).messages {
		var message schema.AgenticMessage
		if err := json.Unmarshal(data, &message); err != nil {
			return nil, err
		}
		messages = append(messages, &message)
	}
	return messages, nil
}

func (m *MemoryStore) AppendUser(ctx context.Context, id string, message *schema.AgenticMessage) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return m.update(ctx, id, func(history *memoryHistory) {
		history.messages = append(history.messages, data)
	})
}

func (m *MemoryStore) SaveTurn(ctx context.Context, id string, result TurnResult) error {
	record, err := json.Marshal(result)
	if err != nil {
		return err
	}
	messages := make([]json.RawMessage, 0, len(result.Messages))
	for _, message := range result.Messages {
		data, err := json.Marshal(message)
		if err != nil {
			return err
		}
		messages = append(messages, data)
	}
	return m.update(ctx, id, func(history *memoryHistory) {
		history.messages = append(history.messages, messages...)
		history.turns = append(history.turns, record)
	})
}

// LoadTurns exposes outcomes, including partial responses, to application code.
func (m *MemoryStore) LoadTurns(ctx context.Context, id string) ([]TurnResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, ok := m.sessions.Load(id)
	if !ok {
		return nil, nil
	}
	var turns []TurnResult
	for _, record := range value.(*memoryHistory).turns {
		var result TurnResult
		if err := json.Unmarshal(record, &result); err != nil {
			return nil, err
		}
		turns = append(turns, result)
	}
	return turns, nil
}

func (m *MemoryStore) update(ctx context.Context, id string, change func(*memoryHistory)) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		value, _ := m.sessions.LoadOrStore(id, &memoryHistory{})
		previous := value.(*memoryHistory)
		next := &memoryHistory{
			messages: append([]json.RawMessage(nil), previous.messages...),
			turns:    append([]json.RawMessage(nil), previous.turns...),
		}
		change(next)
		if m.sessions.CompareAndSwap(id, previous, next) {
			return nil
		}
	}
}
