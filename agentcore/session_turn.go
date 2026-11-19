package agentcore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

const replyWriteTimeout = 5 * time.Second

func (s *sessionRunner) genInput(
	ctx context.Context,
	_ *adk.TurnLoop[*UserCommand, adk.AgenticMessage],
	commands []*UserCommand,
) (*adk.GenInputResult[*UserCommand, adk.AgenticMessage], error) {
	for index, command := range commands {
		message := schema.UserAgenticMessage(command.Text)
		err := s.storage.AppendMessage(ctx, s.id, message)
		if err != nil {
			return nil, fmt.Errorf("append user message %d of %d: %w", index+1, len(commands), err)
		}
		s.events.Publish(Event{Type: EventUserMsg, Text: command.Text})
	}

	history, err := s.storage.GetMessages(ctx, s.id)
	if err != nil {
		return nil, fmt.Errorf("load session history: %w", err)
	}
	messages := make([]*schema.AgenticMessage, 0, len(history))
	for _, record := range history {
		messages = append(messages, record.Message)
	}
	return &adk.GenInputResult[*UserCommand, adk.AgenticMessage]{
		Input: &adk.TypedAgentInput[adk.AgenticMessage]{
			Messages:        messages,
			EnableStreaming: true,
		},
		Consumed: commands,
	}, nil
}

// Все ходы этой сессии обслуживает один агент.
func (s *sessionRunner) prepareAgent(
	context.Context,
	*adk.TurnLoop[*UserCommand, adk.AgenticMessage],
	[]*UserCommand,
) (adk.TypedAgent[adk.AgenticMessage], error) {
	return s.agent, nil
}

func (s *sessionRunner) onAgentEvents(
	ctx context.Context,
	turn *adk.TurnContext[*UserCommand, adk.AgenticMessage],
	events *adk.AsyncIterator[*adk.TypedAgentEvent[adk.AgenticMessage]],
) error {
	s.events.Publish(Event{Type: EventTurnStart})
	defer func() {
		select {
		case <-turn.Preempted:
			s.events.Publish(Event{Type: EventTurnPreempt})
		default:
		}
		s.events.Publish(Event{Type: EventTurnEnd})
	}()

	for {
		event, ok := events.Next()
		if !ok {
			return nil
		}
		if event == nil {
			continue
		}
		if event.Err != nil {
			if !isTurnCancellation(event.Err) {
				s.events.Publish(Event{Type: EventError, Text: event.Err.Error()})
			}
			continue
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}

		output := event.Output.MessageOutput
		reply := output.Message
		var streamErr error
		if output.IsStreaming && output.MessageStream != nil {
			reply, streamErr = s.readReplyStream(event.AgentName, output.MessageStream)
			if streamErr != nil && !isTurnCancellation(streamErr) {
				s.events.Publish(Event{Type: EventError, Text: streamErr.Error()})
			}
		}
		if reply == nil {
			continue
		}
		if streamErr == nil {
			s.events.PublishToolEvents(event.AgentName, reply)
		}
		s.saveReply(ctx, reply)
	}
}

func isTurnCancellation(err error) bool {
	var cancelErr *adk.CancelError
	return errors.As(err, &cancelErr) || errors.Is(err, context.Canceled)
}

func (s *sessionRunner) readReplyStream(
	agentName string,
	stream adk.AgenticMessageStream,
) (adk.AgenticMessage, error) {
	defer stream.Close()

	var chunks []adk.AgenticMessage
	var streamErr error
	for {
		chunk, err := stream.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				streamErr = err
			}
			break
		}
		if chunk == nil {
			continue
		}
		chunks = append(chunks, chunk)
		s.events.PublishChunk(agentName, chunk)
	}

	// При обрыве потока сохраняем ту часть ответа, которую успели получить.
	if len(chunks) == 0 {
		return nil, streamErr
	}
	reply, err := schema.ConcatAgenticMessages(chunks)
	if err != nil {
		return nil, err
	}
	return reply, streamErr
}

func (s *sessionRunner) saveReply(ctx context.Context, reply adk.AgenticMessage) {
	// Запись частичного ответа может завершиться уже после отмены хода.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), replyWriteTimeout)
	defer cancel()

	if err := s.storage.AppendMessage(writeCtx, s.id, reply); err != nil {
		s.events.Publish(Event{Type: EventError, Text: err.Error()})
	}
}
