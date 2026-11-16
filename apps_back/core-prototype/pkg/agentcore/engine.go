package agentcore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
)

var ErrEngineClosed = errors.New("engine stopped")

type EngineConfig struct {
	Store   Store
	Factory AgentFactory
	// Emit must accept events promptly and support concurrent sessions.
	Emit func(Event)
}

type Engine struct {
	ctx      context.Context
	cancel   context.CancelFunc
	cfg      EngineConfig
	sessions sync.Map // string -> *sessionEntry
	closed   atomic.Bool
	done     chan struct{}
}

type sessionEntry struct {
	ready   chan struct{}
	session *Session // Published by closing ready.
}

func NewEngine(ctx context.Context, cfg EngineConfig) *Engine {
	if cfg.Store == nil {
		cfg.Store = NewMemoryStore()
	}
	ctx, cancel := context.WithCancel(ctx)
	return &Engine{
		ctx:    ctx,
		cancel: cancel,
		cfg:    cfg,
		done:   make(chan struct{}),
	}
}

func (e *Engine) getOrCreate(id string) (*Session, error) {
	if e.closed.Load() || e.ctx.Err() != nil {
		return nil, ErrEngineClosed
	}
	if strings.TrimSpace(id) == "" || e.cfg.Factory == nil {
		return nil, errors.New("session ID and agent factory are required")
	}
	entry := &sessionEntry{ready: make(chan struct{})}
	value, loaded := e.sessions.LoadOrStore(id, entry)
	entry = value.(*sessionEntry)
	if !loaded {
		entry.session = NewSession(e.ctx, id, e.cfg.Store, e.cfg.Factory, e.cfg.Emit)
		close(entry.ready)
	}
	<-entry.ready
	if e.closed.Load() || e.ctx.Err() != nil {
		entry.session.Shutdown()
		return nil, ErrEngineClosed
	}
	return entry.session, nil
}

func (e *Engine) Send(id, text string, preempt bool) error {
	session, err := e.getOrCreate(id)
	if err != nil {
		return err
	}
	return session.Send(text, preempt)
}

func (e *Engine) Stop(id string) {
	if value, ok := e.sessions.Load(id); ok {
		entry := value.(*sessionEntry)
		<-entry.ready
		entry.session.Stop()
	}
}

func (e *Engine) Shutdown() {
	if !e.closed.CompareAndSwap(false, true) {
		<-e.done
		return
	}
	e.cancel()
	e.sessions.Range(func(_, value any) bool {
		entry := value.(*sessionEntry)
		<-entry.ready
		entry.session.Shutdown()
		return true
	})
	close(e.done)
}
