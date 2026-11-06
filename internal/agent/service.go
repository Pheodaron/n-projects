package agent

import (
	"context"
	"errors"
	"github.com/Pheodaron/n-projects/internal/chat"
	"log/slog"
	"strings"
	"sync"
	"time"
)

var ErrBusy = errors.New("agent busy")

type Config struct {
	MaxConcurrent               int
	RunTimeout                  time.Duration
	ContextChars, MaxInputChars int
	SystemPrompt                string
}
type Service struct {
	store  *chat.Store
	engine Engine
	cfg    Config
	ctx    context.Context
	mu     sync.Mutex
	cancel map[string]context.CancelFunc
	slots  chan struct{}
	wg     sync.WaitGroup
}

func NewService(ctx context.Context, store *chat.Store, engine Engine, cfg Config) (*Service, error) {
	if cfg.MaxConcurrent < 1 {
		cfg.MaxConcurrent = 1
	}
	if cfg.RunTimeout == 0 {
		cfg.RunTimeout = 5 * time.Minute
	}
	if cfg.ContextChars == 0 {
		cfg.ContextChars = 12000
	}
	if cfg.MaxInputChars == 0 {
		cfg.MaxInputChars = 6000
	}
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = "Ты полезный помощник. Отвечай ясно на языке пользователя."
	}
	if e := store.Recover(ctx); e != nil {
		return nil, e
	}
	return &Service{store: store, engine: engine, cfg: cfg, ctx: ctx, cancel: map[string]context.CancelFunc{}, slots: make(chan struct{}, cfg.MaxConcurrent)}, nil
}
func (s *Service) StartRun(ctx context.Context, a chat.Actor, id, key, content string) (chat.Run, error) {
	if strings.TrimSpace(content) == "" || len([]rune(content)) > s.cfg.MaxInputChars {
		return chat.Run{}, chat.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Idempotent retries must work even while the execution slot is occupied.
	if r, found, e := s.store.LookupStart(ctx, a, id, key, content); e != nil || found {
		return r, e
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return chat.Run{}, ErrBusy
	}
	r, fresh, e := s.store.Start(ctx, a, id, key, content)
	if e != nil || !fresh {
		<-s.slots
		return r, e
	}
	runCtx, cancel := context.WithTimeout(s.ctx, s.cfg.RunTimeout)
	s.cancel[r.ID] = cancel
	s.wg.Add(1)
	go s.execute(runCtx, cancel, a, r)
	return r, nil
}
func (s *Service) execute(ctx context.Context, cancel context.CancelFunc, a chat.Actor, r chat.Run) {
	defer s.wg.Done()
	defer func() { cancel(); s.mu.Lock(); delete(s.cancel, r.ID); s.mu.Unlock(); <-s.slots }()
	messages, e := s.store.ListMessages(ctx, a, r.SessionID)
	input := []ModelMessage{{Role: "system", Content: s.cfg.SystemPrompt}}
	total := 0
	start := len(messages)
	for start > 0 {
		m := messages[start-1]
		n := len([]rune(m.Content))
		if total+n > s.cfg.ContextChars {
			break
		}
		total += n
		start--
	}
	if start < len(messages) && messages[start].Role == "assistant" {
		start++
	}
	r.ContextTruncated = start > 0
	for _, m := range messages[start:] {
		if m.Status == "completed" {
			input = append(input, ModelMessage{Role: m.Role, Content: m.Content})
		}
	}
	if e == nil {
		e = s.engine.Generate(ctx, input, func(delta string) error { r.Message.Content += delta; r.Revision++; return s.store.SaveRun(ctx, a, r) })
	}
	r.Status = "succeeded"
	if e != nil {
		r.Status = "failed"
		r.ErrorCode = "generation_failed"
		r.ErrorMessage = "Не удалось получить полный ответ модели"
		if errors.Is(ctx.Err(), context.Canceled) {
			r.Status = "cancelled"
			r.ErrorMessage = ""
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			r.ErrorCode = "timeout"
		}
	}
	r.Revision++
	saveCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	if err := s.store.SaveRun(saveCtx, a, r); err != nil {
		slog.Error("persist run completion", "run_id", r.ID, "error", err)
	}
}
func (s *Service) CancelRun(ctx context.Context, a chat.Actor, id, key string) (chat.Run, error) {
	r, e := s.store.Cancel(ctx, a, id, key)
	if e != nil {
		return r, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel := s.cancel[id]; cancel != nil {
		cancel()
	}
	return r, nil
}
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	for _, cancel := range s.cancel {
		cancel()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
