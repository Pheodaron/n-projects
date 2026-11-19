package agentcore

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/cloudwego/eino/schema"
)

const subscriberBufferSize = 256

type sessionEvents struct {
	sessionID string
	mu        sync.Mutex

	subscribers map[chan Event]struct{} // nil после закрытия подписок
	exitErr     error

	turnActive bool
	agentName  string
	thinking   []byte
	text       []byte
	toolEvents []Event
}

func (e *sessionEvents) PublishChunk(agentName string, chunk *schema.AgenticMessage) {
	for _, block := range chunk.ContentBlocks {
		if block == nil {
			continue
		}

		var eventType EventType
		var text string
		switch block.Type {
		case schema.ContentBlockTypeReasoning:
			if block.Reasoning == nil {
				continue
			}
			eventType = EventThinking
			text = block.Reasoning.Text
		case schema.ContentBlockTypeAssistantGenText:
			if block.AssistantGenText == nil {
				continue
			}
			eventType = EventText
			text = block.AssistantGenText.Text
		default:
			continue
		}

		if text != "" {
			e.Publish(Event{Type: eventType, AgentName: agentName, Text: text})
		}
	}
}

// PublishToolEvents получает собранное сообщение: аргументы одного вызова
// могут приходить частями, вперемешку с аргументами другого вызова.
func (e *sessionEvents) PublishToolEvents(agentName string, message *schema.AgenticMessage) {
	for _, block := range message.ContentBlocks {
		if block == nil {
			continue
		}

		eventType, data, err := encodeToolBlock(block)
		if err != nil {
			e.Publish(Event{Type: EventError, AgentName: agentName, Text: "encode tool event: " + err.Error()})
			continue
		}
		if data != nil {
			e.Publish(Event{Type: eventType, AgentName: agentName, Tool: data})
		}
	}
}

func encodeToolBlock(block *schema.ContentBlock) (EventType, json.RawMessage, error) {
	var eventType EventType
	var payload any
	switch {
	case block.FunctionToolCall != nil:
		eventType, payload = EventToolCall, block.FunctionToolCall
	case block.FunctionToolResult != nil:
		eventType, payload = EventToolResult, block.FunctionToolResult
	case block.ToolSearchFunctionToolResult != nil:
		eventType, payload = EventToolResult, block.ToolSearchFunctionToolResult
	case block.ServerToolCall != nil:
		eventType, payload = EventToolCall, block.ServerToolCall
	case block.ServerToolResult != nil:
		eventType, payload = EventToolResult, block.ServerToolResult
	case block.MCPToolCall != nil:
		eventType, payload = EventToolCall, block.MCPToolCall
	case block.MCPToolResult != nil:
		eventType, payload = EventToolResult, block.MCPToolResult
	default:
		return "", nil, nil
	}

	data, err := json.Marshal(payload)
	return eventType, data, err
}

func (e *sessionEvents) Publish(event Event) {
	event.SessionID = e.sessionID

	e.mu.Lock()
	defer e.mu.Unlock()

	switch event.Type {
	case EventTurnStart:
		e.turnActive = true
		e.agentName = ""
	case EventThinking:
		e.agentName = event.AgentName
		e.thinking = append(e.thinking, event.Text...)
	case EventText:
		e.agentName = event.AgentName
		e.text = append(e.text, event.Text...)
	case EventToolCall, EventToolResult:
		e.toolEvents = append(e.toolEvents, event)
	case EventTurnEnd:
		e.turnActive = false
		e.thinking = nil
		e.text = nil
		e.toolEvents = nil
	}

	for ch := range e.subscribers {
		select {
		case ch <- event:
		default:
			if event.Type == EventTurnStopped {
				// Освобождаем место для остановки даже у отстающего окна.
				// Писатели сериализованы e.mu, читатель мог уже освободить место.
				select {
				case <-ch:
				default:
				}
				ch <- event
			}
		}
	}
}

func (e *sessionEvents) listen(ctx, engineCtx context.Context, ch <-chan Event, yield func(Event) bool) {
	for {
		var event Event
		var open bool
		select {
		case <-engineCtx.Done():
			return
		case <-ctx.Done():
			return
		case event, open = <-ch:
		}

		if !open {
			break
		}
		if !yield(event) {
			return
		}
	}

	// Close записывает причину до закрытия канала. После чтения последнего
	// события подписчик получит ошибку, даже если буфер был полон.
	if e.exitErr != nil && ctx.Err() == nil {
		yield(Event{
			SessionID: e.sessionID,
			Type:      EventError,
			Text:      e.exitErr.Error(),
		})
	}
}

func (e *sessionEvents) register() chan Event {
	e.mu.Lock()
	defer e.mu.Unlock()

	ch := make(chan Event, subscriberBufferSize+len(e.toolEvents))
	if e.subscribers == nil {
		close(ch)
		return ch
	}

	// Снимок и регистрация под одним мьютексом: между ними нельзя пропустить дельту.
	if e.turnActive {
		ch <- Event{SessionID: e.sessionID, Type: EventTurnStart}
		if len(e.thinking) > 0 {
			ch <- Event{
				SessionID: e.sessionID,
				Type:      EventThinking,
				AgentName: e.agentName,
				Text:      string(e.thinking),
			}
		}
		if len(e.text) > 0 {
			ch <- Event{
				SessionID: e.sessionID,
				Type:      EventText,
				AgentName: e.agentName,
				Text:      string(e.text),
			}
		}
		for _, event := range e.toolEvents {
			ch <- event
		}
	}

	e.subscribers[ch] = struct{}{}
	return ch
}

func (e *sessionEvents) unregister(ch chan Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.subscribers, ch)
}

func (e *sessionEvents) Close(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.exitErr = err
	for ch := range e.subscribers {
		close(ch)
	}
	e.subscribers = nil
}
