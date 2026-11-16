package agentcore

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/agenticopenai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

type LLMConfig struct {
	BaseURL        string // Например: "http://127.0.0.1:8080/v1"
	APIKey         string // Для llama.cpp любая строка
	Model          string // Например: "qwen3-8b"
	SystemPrompt   string
	EnableThinking bool
}

func NewLlamaCppAgentFactory(cfg LLMConfig) (AgentFactory, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://127.0.0.1:8080/v1"
	}
	if cfg.APIKey == "" {
		cfg.APIKey = "llama-cpp"
	}

	ctx := context.Background()

	// OpenAI-совместимая AgenticModel (/v1/chat/completions) для llama.cpp + Qwen
	llm, err := agenticopenai.NewChatModel(ctx, &agenticopenai.ChatConfig{
		BaseURL: cfg.BaseURL,
		APIKey:  cfg.APIKey,
		Model:   cfg.Model,
		ExtraFields: map[string]any{
			"chat_template_kwargs": map[string]any{
				"enable_thinking": cfg.EnableThinking,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create llama.cpp chat model: %w", err)
	}

	agent, err := adk.NewTypedChatModelAgent[*schema.AgenticMessage](ctx, &adk.TypedChatModelAgentConfig[*schema.AgenticMessage]{
		Name:        "assistant",
		Description: "Main Assistant",
		Instruction: cfg.SystemPrompt,
		Model:       llm,
		// [Точка расширения 1]: ToolsConfig (сюда добавятся инструменты и субагенты)
		// [Точка расширения 2]: Handlers (сюда добавится мидлварь скиллов skill.NewTyped)
	})
	if err != nil {
		return nil, fmt.Errorf("create typed agent: %w", err)
	}

	return func(ctx context.Context, consumed []*UserCommand) (adk.TypedAgent[*schema.AgenticMessage], error) {
		return agent, nil
	}, nil
}
