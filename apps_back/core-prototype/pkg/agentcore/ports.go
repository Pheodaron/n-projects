package agentcore

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// AgentFactory создает или возвращает агента для витка.
type AgentFactory func(ctx context.Context, consumed []*UserCommand) (adk.TypedAgent[*schema.AgenticMessage], error)

// HistoryStore — интерфейс БД (позже реализуется для SQLite и Postgres).
type HistoryStore interface {
	Load(ctx context.Context, sessionID string) ([]*schema.AgenticMessage, error)
	Append(ctx context.Context, sessionID string, msg *schema.AgenticMessage) error
}

// MemoryStore — простая In-Memory реализация на RWMutex.
type MemoryStore struct {
	mu sync.RWMutex
	db map[string][]*schema.AgenticMessage
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		db: make(map[string][]*schema.AgenticMessage),
	}
}

func (m *MemoryStore) Load(_ context.Context, sessionID string) ([]*schema.AgenticMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	hist := m.db[sessionID]
	cp := make([]*schema.AgenticMessage, len(hist))
	copy(cp, hist)
	return cp, nil
}

func (m *MemoryStore) Append(_ context.Context, sessionID string, msg *schema.AgenticMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.db[sessionID] = append(m.db[sessionID], msg)
	return nil
}
