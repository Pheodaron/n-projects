package agentcore

// Deprecated: MemoryStorage is disabled. Use adapters/postgres for session storage.

/*
import (
	"context"
	"sync"

	"github.com/cloudwego/eino/schema"
)

type MemoryStorage struct {
	mu       sync.RWMutex
	sessions map[string]Session
	messages map[string][]*schema.AgenticMessage
}

func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		sessions: make(map[string]Session),
		messages: make(map[string][]*schema.AgenticMessage),
	}
}

func (m *MemoryStorage) CreateSession(_ context.Context, session Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// TODO: сделать проверку на существование сессии и возвращать ошибку если сессия уже существует

	m.sessions[session.ID] = session
	return nil
}

func (m *MemoryStorage) SessionExists(_ context.Context, sessionID string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.sessions[sessionID]
	if !ok {
		return ErrSessionNotFound
	}
	return nil
}

func (m *MemoryStorage) AppendMessage(_ context.Context, sessionID string, msg *schema.AgenticMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.messages[sessionID] = append(m.messages[sessionID], msg)
	return nil
}

func (m *MemoryStorage) GetMessages(_ context.Context, sessionID string) ([]*schema.AgenticMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	hist := m.messages[sessionID]
	cp := make([]*schema.AgenticMessage, len(hist))
	copy(cp, hist)
	return cp, nil
}
*/
