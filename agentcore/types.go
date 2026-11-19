package agentcore

import "encoding/json"

type EventType string

const (
	EventTurnStart   EventType = "turn_start"
	EventUserMsg     EventType = "user_msg"
	EventThinking    EventType = "thinking_delta"
	EventText        EventType = "text_delta"
	EventToolCall    EventType = "tool_call"
	EventToolResult  EventType = "tool_result"
	EventTurnPreempt EventType = "turn_preempted"
	EventTurnStopped EventType = "turn_stopped"
	EventTurnEnd     EventType = "turn_end"
	EventError       EventType = "error"
)

type Event struct {
	SessionID string          `json:"sessionId"`
	Type      EventType       `json:"type"`
	AgentName string          `json:"agentName,omitempty"`
	Text      string          `json:"text,omitempty"`
	Tool      json.RawMessage `json:"tool,omitempty"`
}

type UserCommand struct {
	Text    string
	Preempt bool
}
