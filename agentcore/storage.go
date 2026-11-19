package agentcore

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
)

var ErrSessionNotFound = errors.New("Session not found")

var ErrInvalidSessionPage = errors.New("invalid session page")

var ErrInvalidSessionTitle = errors.New("session title must contain a single nonempty line")

type Session struct {
	ID             string     `json:"id"`
	Title          string     `json:"title"`
	CreatedAt      time.Time  `json:"createdAt"`
	LastActivityAt *time.Time `json:"lastActivityAt"`
}

type StoredMessage struct {
	ID        int64
	CreatedAt time.Time
	Message   *schema.AgenticMessage
}

type SessionPage struct {
	Sessions []Session `json:"sessions"`
}

const untitledSession = "Новый чат"

// InitialSessionTitle returns a candidate title for a user message.
// Other roles leave the stored title untouched.
func InitialSessionTitle(msg *schema.AgenticMessage) string {
	if msg == nil || msg.Role != schema.AgenticRoleTypeUser {
		return ""
	}
	const maxRunes = 60
	for _, block := range msg.ContentBlocks {
		if block == nil || block.UserInputText == nil || block.Type != schema.ContentBlockTypeUserInputText {
			continue
		}
		for line := range strings.Lines(block.UserInputText.Text) {
			title := strings.TrimSpace(line)
			if title == "" {
				continue
			}
			runes := []rune(title)
			if len(runes) > maxRunes {
				return string(runes[:maxRunes-1]) + "…"
			}
			return title
		}
	}
	return untitledSession
}

// Storage — исходящий порт (Driven Port) для работы с БД.
type Storage interface {
	CreateSession(ctx context.Context, session Session) error
	// ListSessions returns nonempty sessions ordered by last activity and ID, both descending.
	ListSessions(ctx context.Context, limit int, offset int64) ([]Session, error)
	RenameSession(ctx context.Context, sessionID, title string) error
	SessionExists(ctx context.Context, sessionID string) error
	AppendMessage(ctx context.Context, sessionID string, msg *schema.AgenticMessage) error
	// GetMessages возвращает сообщения в порядке их добавления.
	GetMessages(ctx context.Context, sessionID string) ([]StoredMessage, error)
}
