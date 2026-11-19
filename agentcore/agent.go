package agentcore

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/agenticopenai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

type LLMConfig struct {
	BaseURL        string
	APIKey         string
	Model          string
	SystemPrompt   string
	EnableThinking bool
	Tools          []tool.BaseTool
}

func NewLlamaCppAgent(ctx context.Context, cfg LLMConfig) (adk.TypedAgent[*schema.AgenticMessage], error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://127.0.0.1:8080/v1"
	}
	if cfg.APIKey == "" {
		cfg.APIKey = "llama-cpp"
	}

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

	agent, err := adk.NewTypedChatModelAgent(ctx, &adk.TypedChatModelAgentConfig[*schema.AgenticMessage]{
		Name:        "assistant",
		Description: "Main Assistant",
		Instruction: cfg.SystemPrompt,
		Model:       llm,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: cfg.Tools},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create typed agent: %w", err)
	}

	return agent, nil
}
