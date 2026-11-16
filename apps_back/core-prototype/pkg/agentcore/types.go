package agentcore

import "github.com/cloudwego/eino/schema"

type TurnStatus string

const (
	TurnCompleted TurnStatus = "completed"
	TurnFailed    TurnStatus = "failed"
	TurnCancelled TurnStatus = "cancelled"
	TurnPreempted TurnStatus = "preempted"
)

type TurnResult struct {
	Messages []*schema.AgenticMessage `json:"messages,omitempty"`
	Status   TurnStatus               `json:"status"`
	Error    string                   `json:"error,omitempty"`
}

type EventType string

const (
	EventTurnStart   EventType = "turn_start"
	EventUserMsg     EventType = "user_msg"
	EventThinking    EventType = "thinking_delta"
	EventText        EventType = "text_delta"
	EventTurnPreempt EventType = "turn_preempted"
	EventTurnEnd     EventType = "turn_end"
	EventError       EventType = "error"

	// Задел под будущее расширение (Tools):
	EventToolCall   EventType = "tool_call"
	EventToolResult EventType = "tool_result"
)

type Event struct {
	SessionID string     `json:"sessionId"`
	Type      EventType  `json:"type"`
	AgentName string     `json:"agentName,omitempty"`
	Text      string     `json:"text,omitempty"`
	Status    TurnStatus `json:"status,omitempty"`
}

// UserCommand — сообщение или уточнение, попадающее в буфер TurnLoop.
type UserCommand struct {
	Text    string
	Preempt bool // true = прервать текущий ответ, false = добавить в очередь уточнений
}
