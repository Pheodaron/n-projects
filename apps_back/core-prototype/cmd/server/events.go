package main

import (
	"context"

	"github.com/Pheodaron/n-projects/apps_back/core-prototype/pkg/agentcore"
)

type subscription struct {
	sessionID string
	events    chan agentcore.Event
}

// eventHub belongs to the HTTP adapter. Only run sends to and closes subscribers.
type eventHub struct {
	events      chan agentcore.Event
	subscribe   chan subscription
	unsubscribe chan chan agentcore.Event
	done        chan struct{}
}

func newEventHub(ctx context.Context) *eventHub {
	h := &eventHub{
		events:      make(chan agentcore.Event),
		subscribe:   make(chan subscription),
		unsubscribe: make(chan chan agentcore.Event),
		done:        make(chan struct{}),
	}
	go h.run(ctx)
	return h
}

func (h *eventHub) run(ctx context.Context) {
	subscribers := make(map[chan agentcore.Event]string)
	defer func() {
		for ch := range subscribers {
			close(ch)
		}
		close(h.done)
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-h.subscribe:
			subscribers[request.events] = request.sessionID
		case ch := <-h.unsubscribe:
			if _, exists := subscribers[ch]; exists {
				delete(subscribers, ch)
				close(ch)
			}
		case event := <-h.events:
			for ch, id := range subscribers {
				if id != event.SessionID {
					continue
				}
				select {
				case ch <- event:
				default:
					delete(subscribers, ch)
					close(ch)
				}
			}
		}
	}
}

func (h *eventHub) publish(event agentcore.Event) {
	select {
	case h.events <- event:
	case <-h.done:
	}
}

func (h *eventHub) Subscribe(id string) (<-chan agentcore.Event, func()) {
	ch := make(chan agentcore.Event, 256)
	select {
	case h.subscribe <- subscription{sessionID: id, events: ch}:
	case <-h.done:
		close(ch)
		return ch, func() {}
	}
	return ch, func() {
		select {
		case h.unsubscribe <- ch:
		case <-h.done:
		}
	}
}
