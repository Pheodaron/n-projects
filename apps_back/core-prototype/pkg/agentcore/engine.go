package agentcore

import (
	"context"
	"sync"
)

type EngineConfig struct {
	Store   HistoryStore
	Factory AgentFactory
}

type Engine struct {
	bgCtx    context.Context
	cfg      EngineConfig
	sessions sync.Map // key: string (sessionID), value: *Session
}

func NewEngine(bgCtx context.Context, cfg EngineConfig) *Engine {
	if cfg.Store == nil {
		cfg.Store = NewMemoryStore()
	}
	return &Engine{
		bgCtx: bgCtx,
		cfg:   cfg,
	}
}

func (e *Engine) getOrCreate(sessionID string) *Session {
	if v, ok := e.sessions.Load(sessionID); ok {
		return v.(*Session)
	}
	sess := newSession(e.bgCtx, sessionID, e.cfg.Store, e.cfg.Factory)
	actual, _ := e.sessions.LoadOrStore(sessionID, sess)
	return actual.(*Session)
}

func (e *Engine) Shutdown() {
	e.sessions.Range(func(_, value any) bool {
		value.(*Session).Shutdown()
		return true
	})
}

func (e *Engine) Send(sessionID string, text string, preempt bool) {
	e.getOrCreate(sessionID).Send(text, preempt)
}

func (e *Engine) Stop(sessionID string) {
	if v, ok := e.sessions.Load(sessionID); ok {
		v.(*Session).Stop()
	}
}

func (e *Engine) Subscribe(sessionID string) (<-chan Event, func()) {
	return e.getOrCreate(sessionID).Subscribe()
}
