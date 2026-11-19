// Package postgres persists agent sessions and their ordered messages in PostgreSQL.
package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/schema"
	"github.com/jackc/pgx/v5/pgxpool"

	"my-gitlab/ai-support/n-projects/agentcore"
)

type Storage struct {
	pool *pgxpool.Pool
}

var _ agentcore.Storage = (*Storage)(nil)

// New uses the application's pool. The caller owns its lifetime.
func New(pool *pgxpool.Pool) *Storage {
	return &Storage{pool: pool}
}

func (s *Storage) CreateSession(ctx context.Context, session agentcore.Session) error {
	_, err := s.pool.Exec(ctx,
		"INSERT INTO agent_sessions (id, created_at) VALUES ($1, $2)",
		session.ID, session.CreatedAt,
	)
	return err
}

func (s *Storage) ListSessions(ctx context.Context, limit int, offset int64) ([]agentcore.Session, error) {
	const query = `
		SELECT id, COALESCE(title, ''), created_at, last_activity_at
		FROM agent_sessions
		WHERE last_activity_at IS NOT NULL
		ORDER BY last_activity_at DESC, id DESC
		LIMIT $1 OFFSET $2`
	rows, err := s.pool.Query(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sessions := make([]agentcore.Session, 0)
	for rows.Next() {
		var session agentcore.Session
		if err := rows.Scan(&session.ID, &session.Title, &session.CreatedAt, &session.LastActivityAt); err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func (s *Storage) RenameSession(ctx context.Context, sessionID, title string) error {
	tag, err := s.pool.Exec(ctx, "UPDATE agent_sessions SET title = $2 WHERE id = $1", sessionID, title)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return agentcore.ErrSessionNotFound
	}
	return nil
}

func (s *Storage) SessionExists(ctx context.Context, sessionID string) error {
	var exists bool
	err := s.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM agent_sessions WHERE id = $1)", sessionID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return agentcore.ErrSessionNotFound
	}
	return nil
}

func (s *Storage) AppendMessage(ctx context.Context, sessionID string, msg *schema.AgenticMessage) error {
	if msg == nil {
		return errors.New("cannot store a nil agent message")
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("encode agent message: %w", err)
	}

	// Update the session before allocating a message ID, serializing concurrent
	// appends to this chat. Both writes use the same timestamp and commit together.
	const appendSQL = `
		WITH updated_session AS (
			UPDATE agent_sessions
			SET last_activity_at = GREATEST(last_activity_at, clock_timestamp()),
			    title = COALESCE(title, NULLIF($3::text, ''))
			WHERE id = $1
			RETURNING id, last_activity_at
		)
		INSERT INTO agent_messages (session_id, created_at, payload)
		SELECT id, last_activity_at, $2::json FROM updated_session`
	tag, err := s.pool.Exec(ctx, appendSQL, sessionID, payload, agentcore.InitialSessionTitle(msg))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return agentcore.ErrSessionNotFound
	}
	return nil
}

func (s *Storage) GetMessages(ctx context.Context, sessionID string) ([]agentcore.StoredMessage, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT id, created_at, payload FROM agent_messages WHERE session_id = $1 ORDER BY id",
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := make([]agentcore.StoredMessage, 0)
	for rows.Next() {
		var record agentcore.StoredMessage
		var payload []byte
		if err := rows.Scan(&record.ID, &record.CreatedAt, &payload); err != nil {
			return nil, err
		}
		var message schema.AgenticMessage
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.UseNumber() // Preserve numeric values in tool payloads and extension fields.
		if err := decoder.Decode(&message); err != nil {
			return nil, fmt.Errorf("decode agent message: %w", err)
		}
		record.Message = &message
		messages = append(messages, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return messages, nil
}
