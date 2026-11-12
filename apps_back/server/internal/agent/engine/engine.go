// Package engine adapts Eino's TurnLoop and ChatModelAgent to the application's
// streaming execution boundary. Chat history and run persistence belong to the caller.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Pheodaron/n-projects/apps_back/server/internal/agent"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// Config identifies a single llama-server model. BaseURL includes the /v1 path.
type Config struct {
	BaseURL         string
	Model           string
	APIKey          string
	MaxOutputTokens int
	Temperature     float32
	RequestTimeout  time.Duration
	HTTPClient      *http.Client
}

type Engine struct {
	model *llamaModel
}

var _ agent.Engine = (*Engine)(nil)

func New(cfg Config) (*Engine, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("LLAMA_BASE_URL must be an HTTP(S) URL without credentials, query or fragment")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("LLAMA_MODEL must not be empty")
	}
	if cfg.MaxOutputTokens == 0 {
		cfg.MaxOutputTokens = 2048
	}
	if cfg.MaxOutputTokens < 1 || cfg.Temperature < 0 || cfg.Temperature > 2 {
		return nil, errors.New("invalid model generation limits")
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 5 * time.Minute
	}
	if cfg.RequestTimeout < 0 {
		return nil, errors.New("model request timeout must be positive")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cfg.RequestTimeout}
	}
	return &Engine{model: &llamaModel{config: cfg, client: client, baseURL: strings.TrimRight(cfg.BaseURL, "/")}}, nil
}

// Check verifies that the configured model is served by the endpoint.
func (e *Engine) Check(ctx context.Context) error {
	return e.model.check(ctx)
}

type turnInput struct {
	messages []*schema.Message
}

// Generate runs one Core turn through Eino. A fresh bounded loop is used for each
// accepted run, with the caller's saved history as input. It is stopped after the
// turn; no idle goroutine or in-memory chat history survives the call.
func (e *Engine) Generate(ctx context.Context, messages []agent.ModelMessage, onDelta func(string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(messages) == 0 || onDelta == nil {
		return errors.New("messages and a delta consumer are required")
	}
	input := make([]*schema.Message, len(messages))
	for i, message := range messages {
		role := schema.RoleType(message.Role)
		if role != schema.System && role != schema.User && role != schema.Assistant {
			return fmt.Errorf("unsupported Core message role %q", message.Role)
		}
		input[i] = &schema.Message{Role: role, Content: message.Content}
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var consumerErr error
	loop := adk.NewTurnLoop(adk.TurnLoopConfig[turnInput, *schema.Message]{
		GenInput: func(_ context.Context, _ *adk.TurnLoop[turnInput, *schema.Message], items []turnInput) (*adk.GenInputResult[turnInput, *schema.Message], error) {
			return &adk.GenInputResult[turnInput, *schema.Message]{
				Input:    &adk.AgentInput{Messages: items[0].messages, EnableStreaming: true},
				Consumed: items[:1], Remaining: items[1:],
			}, nil
		},
		PrepareAgent: func(ctx context.Context, _ *adk.TurnLoop[turnInput, *schema.Message], _ []turnInput) (adk.Agent, error) {
			return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
				Name: "assistant", Description: "Conversational project assistant",
				Model: e.model, MaxIterations: 1,
			})
		},
		OnAgentEvents: func(_ context.Context, tc *adk.TurnContext[turnInput, *schema.Message], events *adk.AsyncIterator[*adk.AgentEvent]) error {
			// Always drain events and close their readers, including after a failed
			// persistence callback. Cancelling the request releases the HTTP body.
			for {
				event, ok := events.Next()
				if !ok {
					break
				}
				if event == nil {
					continue
				}
				if event.Err != nil && consumerErr == nil {
					consumerErr = event.Err
					cancel()
				}
				if event.Output == nil || event.Output.MessageOutput == nil {
					continue
				}
				output := event.Output.MessageOutput
				emit := func(message *schema.Message) error {
					if message == nil || message.Content == "" || consumerErr != nil {
						return nil
					}
					if output.Role != schema.Assistant && message.Role != schema.Assistant {
						return nil
					}
					return onDelta(message.Content)
				}
				var err error
				if output.IsStreaming && output.MessageStream != nil {
					err = consume(output.MessageStream, emit)
				} else {
					err = emit(output.Message)
				}
				if err != nil && consumerErr == nil {
					consumerErr = err
					cancel()
				}
			}
			// A Core run has one turn. Without this explicit stop a TurnLoop
			// correctly waits for the next Push and Generate would never return.
			tc.Loop.Stop(adk.WithSkipCheckpoint())
			return consumerErr
		},
	})
	loop.Push(turnInput{messages: input})
	stopCancellation := context.AfterFunc(ctx, func() {
		loop.Stop(adk.WithImmediate(), adk.WithSkipCheckpoint())
	})
	defer stopCancellation()
	loop.Run(runCtx)
	exit := loop.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	if consumerErr != nil {
		return consumerErr
	}
	return exit.ExitReason
}

func consume(reader *schema.StreamReader[*schema.Message], emit func(*schema.Message) error) error {
	defer reader.Close()
	for {
		message, err := reader.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := emit(message); err != nil {
			return err
		}
	}
}
