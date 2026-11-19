package agentcore

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
)

var ErrSessionStopped = errors.New("session is no longer accepting messages")

const (
	preemptTimeout      = 2 * time.Second
	gracefulStopTimeout = 2 * time.Second
)

type stopReason uint8

const (
	stopByUser stopReason = iota + 1
	stopOnShutdown
)

type sessionRunner struct {
	id          string
	storage     Storage
	agent       adk.TypedAgent[adk.AgenticMessage]
	lifetimeCtx context.Context
	events      *sessionEvents

	// Send и Stop под одним мьютексом решают, принимает ли цикл команды.
	// После остановки loop равен nil и больше не создаётся.
	loopMu sync.Mutex
	loop   *adk.TurnLoop[*UserCommand, adk.AgenticMessage]

	stopReason stopReason // защищён loopMu

	// err доступна после закрытия done.
	err  error
	done chan struct{}
}

func newSessionRunner(
	lifetimeCtx context.Context,
	sessionID string,
	storage Storage,
	agent adk.TypedAgent[adk.AgenticMessage],
	events *sessionEvents,
	onExit func(stopReason, error),
) *sessionRunner {
	session := &sessionRunner{
		id:          sessionID,
		storage:     storage,
		agent:       agent,
		lifetimeCtx: lifetimeCtx,
		events:      events,
		done:        make(chan struct{}),
	}

	config := adk.TurnLoopConfig[*UserCommand, adk.AgenticMessage]{
		GenInput:      session.genInput,
		PrepareAgent:  session.prepareAgent,
		OnAgentEvents: session.onAgentEvents,
	}

	loop := adk.NewTurnLoop(config)
	session.loop = loop
	loop.Run(lifetimeCtx)
	go session.waitForExit(loop, onExit)
	return session
}

func (s *sessionRunner) Send(ctx context.Context, text string, preempt bool) error {
	s.loopMu.Lock()
	defer s.loopMu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	if s.loop == nil || s.lifetimeCtx.Err() != nil {
		return ErrSessionStopped
	}

	command := &UserCommand{Text: text, Preempt: preempt}
	var options []adk.PushOption[*UserCommand, adk.AgenticMessage]
	if preempt {
		options = append(options, adk.WithPushStrategy(preemptActiveTurn))
	}

	accepted, _ := s.loop.Push(command, options...)
	if !accepted {
		return ErrSessionStopped
	}
	return nil
}

func preemptActiveTurn(
	_ context.Context,
	turn *adk.TurnContext[*UserCommand, adk.AgenticMessage],
) []adk.PushOption[*UserCommand, adk.AgenticMessage] {
	// Пока Eino готовит вход или выбирает агента, команда просто встаёт в очередь.
	if turn == nil {
		return nil
	}
	preempt := adk.WithPreemptTimeout[*UserCommand, adk.AgenticMessage](
		adk.AnySafePoint,
		preemptTimeout,
	)
	return []adk.PushOption[*UserCommand, adk.AgenticMessage]{preempt}
}

func (s *sessionRunner) Stop(reason stopReason) {
	s.loopMu.Lock()
	defer s.loopMu.Unlock()

	if s.loop == nil {
		return
	}
	s.stopReason = reason
	if reason == stopByUser {
		s.loop.Stop(adk.WithImmediate())
	} else {
		s.loop.Stop(adk.WithGracefulTimeout(gracefulStopTimeout))
	}
	s.loop = nil
}

func (s *sessionRunner) waitForExit(loop *adk.TurnLoop[*UserCommand, adk.AgenticMessage], onExit func(stopReason, error)) {
	exit := loop.Wait()

	s.loopMu.Lock()
	s.loop = nil
	reason := s.stopReason
	s.loopMu.Unlock()

	err := s.exitError(exit, reason)
	if err != nil {
		log.Printf("session=%s: %v", s.id, err)
	}
	s.err = err
	onExit(reason, err)
	close(s.done)
}

func (s *sessionRunner) exitError(exit *adk.TurnLoopExitState[*UserCommand, adk.AgenticMessage], reason stopReason) error {
	if reason == stopByUser {
		// При ручной остановке очередь отбрасывается намеренно.
		if isTurnCancellation(exit.ExitReason) {
			return nil
		}
		return exit.ExitReason
	}

	unhandled := len(exit.UnhandledItems)
	interrupted := len(exit.InterruptedItems)
	if unhandled > 0 || interrupted > 0 {
		// GenInput мог сохранить часть пакета. Автоповтор продублировал бы эти сообщения.
		pendingCommandsErr := fmt.Errorf(
			"session stopped with %d unhandled and %d interrupted commands",
			unhandled, interrupted,
		)
		return errors.Join(pendingCommandsErr, exit.ExitReason)
	}

	if s.lifetimeCtx.Err() != nil && errors.Is(exit.ExitReason, context.Canceled) {
		return nil
	}
	return exit.ExitReason
}
