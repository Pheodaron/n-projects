package chat

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound    = errors.New("not found")
	ErrConflict    = errors.New("active run")
	ErrIdempotency = errors.New("idempotency mismatch")
	ErrInvalid     = errors.New("invalid input")
)

type Actor struct{ UserID, WorkspaceID string }
type Session struct {
	ID, Title, ActiveRunID, LastRunID string
	CreatedAt, UpdatedAt              time.Time
	Messages                          []Message `json:"messages,omitempty"`
}
type Message struct {
	ID, SessionID, RunID, Role, Content, Status string
	Seq                                         int64
	CreatedAt                                   time.Time
}
type Run struct {
	ID, SessionID, Status, ErrorCode, ErrorMessage string
	Revision                                       int64
	ContextTruncated                               bool
	CreatedAt, UpdatedAt                           time.Time
	Message                                        Message
}
type Store struct{ Pool *pgxpool.Pool }

func NewStore(p *pgxpool.Pool) *Store { return &Store{p} }
func ID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func (s *Store) transaction(ctx context.Context, a Actor, method, key string, payload any, fn func(pgx.Tx) (any, error), out any) error {
	if key == "" || len(key) > 128 {
		return ErrInvalid
	}
	b, _ := json.Marshal(payload)
	hash := sha256.Sum256(b)
	h := hex.EncodeToString(hash[:])
	k := a.WorkspaceID + ":" + a.UserID + ":" + method + ":" + key
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, k); e != nil {
		return e
	}
	var previous string
	var result []byte
	e = tx.QueryRow(ctx, `SELECT hash,result FROM receipts WHERE key=$1`, k).Scan(&previous, &result)
	if e == nil {
		if previous != h {
			return ErrIdempotency
		}
		if out != nil {
			e = json.Unmarshal(result, out)
		}
		return e
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	value, e := fn(tx)
	if e != nil {
		return e
	}
	result, e = json.Marshal(value)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO receipts VALUES($1,$2,$3)`, k, h, result); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	if out != nil {
		return json.Unmarshal(result, out)
	}
	return nil
}
func sessionTx(ctx context.Context, tx pgx.Tx, a Actor, id string) (Session, error) {
	var b []byte
	var v Session
	e := tx.QueryRow(ctx, `SELECT data FROM sessions WHERE id=$1 AND owner_id=$2 AND workspace_id=$3 FOR UPDATE`, id, a.UserID, a.WorkspaceID).Scan(&b)
	if errors.Is(e, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	if e == nil {
		e = json.Unmarshal(b, &v)
	}
	return v, e
}
func saveSession(ctx context.Context, tx pgx.Tx, v Session) error {
	v.UpdatedAt = time.Now().UTC()
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `UPDATE sessions SET data=$2 WHERE id=$1`, v.ID, b)
	return e
}
func (s *Store) CreateSession(ctx context.Context, a Actor, key, title string) (v Session, e error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Новый чат"
	}
	if len(title) > 200 {
		return v, ErrInvalid
	}
	e = s.transaction(ctx, a, "create", key, title, func(tx pgx.Tx) (any, error) {
		now := time.Now().UTC()
		r := Session{ID: ID(), Title: title, CreatedAt: now, UpdatedAt: now}
		b, _ := json.Marshal(r)
		_, e := tx.Exec(ctx, `INSERT INTO sessions VALUES($1,$2,$3,$4)`, r.ID, a.UserID, a.WorkspaceID, b)
		return r, e
	}, &v)
	return
}
func (s *Store) ListSessions(ctx context.Context, a Actor) ([]Session, error) {
	rows, e := s.Pool.Query(ctx, `SELECT data FROM sessions WHERE owner_id=$1 AND workspace_id=$2 ORDER BY data->>'UpdatedAt' DESC`, a.UserID, a.WorkspaceID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var b []byte
		var v Session
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &v); e != nil {
			return nil, e
		}
		v.Messages = nil
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) GetSession(ctx context.Context, a Actor, id string) (v Session, e error) {
	var b []byte
	e = s.Pool.QueryRow(ctx, `SELECT data FROM sessions WHERE id=$1 AND owner_id=$2 AND workspace_id=$3`, id, a.UserID, a.WorkspaceID).Scan(&b)
	if errors.Is(e, pgx.ErrNoRows) {
		e = ErrNotFound
	}
	if e == nil {
		e = json.Unmarshal(b, &v)
	}
	return
}
func (s *Store) UpdateSession(ctx context.Context, a Actor, id, key, title string) (v Session, e error) {
	title = strings.TrimSpace(title)
	if title == "" || len(title) > 200 {
		return v, ErrInvalid
	}
	e = s.transaction(ctx, a, "update", key, []string{id, title}, func(tx pgx.Tx) (any, error) {
		r, e := sessionTx(ctx, tx, a, id)
		if e != nil {
			return nil, e
		}
		r.Title = title
		e = saveSession(ctx, tx, r)
		return r, e
	}, &v)
	return
}
func (s *Store) DeleteSession(ctx context.Context, a Actor, id, key string) error {
	return s.transaction(ctx, a, "delete", key, id, func(tx pgx.Tx) (any, error) {
		v, e := sessionTx(ctx, tx, a, id)
		if e != nil {
			return nil, e
		}
		if v.ActiveRunID != "" {
			return nil, ErrConflict
		}
		_, e = tx.Exec(ctx, `DELETE FROM sessions WHERE id=$1`, id)
		return struct{}{}, e
	}, nil)
}
func (s *Store) ListMessages(ctx context.Context, a Actor, id string) ([]Message, error) {
	v, e := s.GetSession(ctx, a, id)
	return v.Messages, e
}
func (s *Store) GetRun(ctx context.Context, a Actor, id string) (v Run, e error) {
	var b []byte
	e = s.Pool.QueryRow(ctx, `SELECT data FROM runs WHERE id=$1 AND owner_id=$2 AND workspace_id=$3`, id, a.UserID, a.WorkspaceID).Scan(&b)
	if errors.Is(e, pgx.ErrNoRows) {
		e = ErrNotFound
	}
	if e == nil {
		e = json.Unmarshal(b, &v)
	}
	return
}
func (s *Store) Start(ctx context.Context, a Actor, id, key, content string) (v Run, fresh bool, e error) {
	e = s.transaction(ctx, a, "start", key, []string{id, content}, func(tx pgx.Tx) (any, error) {
		session, e := sessionTx(ctx, tx, a, id)
		if e != nil {
			return nil, e
		}
		if session.ActiveRunID != "" {
			return nil, ErrConflict
		}
		now := time.Now().UTC()
		r := Run{ID: ID(), SessionID: id, Status: "running", Revision: 1, CreatedAt: now, UpdatedAt: now}
		m := Message{ID: ID(), SessionID: id, RunID: r.ID, Role: "user", Content: content, Status: "completed", Seq: int64(len(session.Messages) + 1), CreatedAt: now}
		r.Message = Message{ID: ID(), SessionID: id, RunID: r.ID, Role: "assistant", Status: "incomplete", Seq: m.Seq + 1, CreatedAt: now}
		session.Messages = append(session.Messages, m)
		session.ActiveRunID = r.ID
		session.LastRunID = r.ID
		if e = saveSession(ctx, tx, session); e != nil {
			return nil, e
		}
		b, _ := json.Marshal(r)
		_, e = tx.Exec(ctx, `INSERT INTO runs VALUES($1,$2,$3,$4,$5)`, r.ID, id, a.UserID, a.WorkspaceID, b)
		fresh = true
		return r, e
	}, &v)
	return
}
func (s *Store) SaveRun(ctx context.Context, a Actor, r Run) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	v, e := sessionTx(ctx, tx, a, r.SessionID)
	if e != nil {
		return e
	}
	var previous []byte
	if e = tx.QueryRow(ctx, `SELECT data FROM runs WHERE id=$1`, r.ID).Scan(&previous); e != nil {
		return e
	}
	var old Run
	if e = json.Unmarshal(previous, &old); e != nil {
		return e
	}
	if old.Status != "running" && old.Status != "cancelling" {
		return ErrConflict
	}
	r.Revision = old.Revision
	if old.Status == "cancelling" && r.Status == "succeeded" {
		r.Status = "cancelled"
	}
	r.Revision++
	r.UpdatedAt = time.Now().UTC()
	if r.Status == "succeeded" {
		r.Message.Status = "completed"
	}
	b, _ := json.Marshal(r)
	_, e = tx.Exec(ctx, `UPDATE runs SET data=$2 WHERE id=$1`, r.ID, b)
	if e != nil {
		return e
	}
	if r.Status != "running" && r.Status != "cancelling" {
		if v.ActiveRunID == r.ID {
			v.ActiveRunID = ""
		}
		r.Message.Status = "incomplete"
		if r.Status == "succeeded" {
			r.Message.Status = "completed"
		}
		v.Messages = append(v.Messages, r.Message)
	}
	if e = saveSession(ctx, tx, v); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) Recover(ctx context.Context) error {
	rows, e := s.Pool.Query(ctx, `SELECT owner_id,workspace_id,data FROM runs WHERE data->>'Status' IN ('running','cancelling')`)
	if e != nil {
		return e
	}
	type entry struct {
		a Actor
		r Run
	}
	var all []entry
	for rows.Next() {
		var v entry
		var b []byte
		if e = rows.Scan(&v.a.UserID, &v.a.WorkspaceID, &b); e != nil {
			rows.Close()
			return e
		}
		if e = json.Unmarshal(b, &v.r); e != nil {
			rows.Close()
			return e
		}
		all = append(all, v)
	}
	rows.Close()
	for _, v := range all {
		v.r.Status = "interrupted"
		v.r.ErrorCode = "server_restarted"
		if e = s.SaveRun(ctx, v.a, v.r); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) LookupStart(ctx context.Context, a Actor, id, key, content string) (v Run, found bool, e error) {
	var hash string
	var b []byte
	e = s.Pool.QueryRow(ctx, `SELECT hash,result FROM receipts WHERE key=$1`, a.WorkspaceID+":"+a.UserID+":start:"+key).Scan(&hash, &b)
	if errors.Is(e, pgx.ErrNoRows) {
		return v, false, nil
	}
	if e != nil {
		return
	}
	payload, _ := json.Marshal([]string{id, content})
	h := sha256.Sum256(payload)
	if hash != hex.EncodeToString(h[:]) {
		return v, false, ErrIdempotency
	}
	e = json.Unmarshal(b, &v)
	return v, true, e
}

func (s *Store) Cancel(ctx context.Context, a Actor, id, key string) (r Run, e error) {
	e = s.transaction(ctx, a, "cancel", key, id, func(tx pgx.Tx) (any, error) {
		current, err := s.GetRun(ctx, a, id)
		if err != nil {
			return nil, err
		}
		_, err = sessionTx(ctx, tx, a, current.SessionID)
		if err != nil {
			return nil, err
		}
		var b []byte
		if err = tx.QueryRow(ctx, `SELECT data FROM runs WHERE id=$1`, id).Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &current); err != nil {
			return nil, err
		}
		if current.Status == "running" {
			current.Status = "cancelling"
			current.Revision++
			current.UpdatedAt = time.Now().UTC()
			b, _ = json.Marshal(current)
			_, err = tx.Exec(ctx, `UPDATE runs SET data=$2 WHERE id=$1`, id, b)
		}
		return current, err
	}, &r)
	return
}
