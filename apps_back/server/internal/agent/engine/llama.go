package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const maxEventBytes = 2 << 20

var (
	ErrIncompleteStream = errors.New("llama.cpp stream ended before its completion marker")
	ErrOutputLimit      = errors.New("llama.cpp reached the configured output token limit")
)

type llamaModel struct {
	config  Config
	client  *http.Client
	baseURL string
}

var _ model.BaseChatModel = (*llamaModel)(nil)

type requestMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type completionRequest struct {
	Model       string           `json:"model"`
	Messages    []requestMessage `json:"messages"`
	Stream      bool             `json:"stream"`
	MaxTokens   int              `json:"max_tokens"`
	Temperature float32          `json:"temperature"`
}

type completionChunk struct {
	Error   json.RawMessage `json:"error"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Content   string          `json:"content"`
			ToolCalls json.RawMessage `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

func (m *llamaModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	reader, err := m.Stream(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	var text strings.Builder
	for {
		chunk, err := reader.Recv()
		if errors.Is(err, io.EOF) {
			return &schema.Message{Role: schema.Assistant, Content: text.String()}, nil
		}
		if err != nil {
			return nil, err
		}
		text.WriteString(chunk.Content)
	}
}

func (m *llamaModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	options := model.GetCommonOptions(&model.Options{}, opts...)
	if len(options.Tools) > 0 || len(options.DeferredTools) > 0 || options.ToolSearchTool != nil {
		return nil, errors.New("Core llama.cpp adapter does not enable tools")
	}
	payload := completionRequest{Model: m.config.Model, Stream: true, MaxTokens: m.config.MaxOutputTokens, Temperature: m.config.Temperature}
	if options.Model != nil {
		payload.Model = *options.Model
	}
	if options.MaxTokens != nil {
		payload.MaxTokens = *options.MaxTokens
	}
	if options.Temperature != nil {
		payload.Temperature = *options.Temperature
	}
	for _, message := range input {
		if message == nil {
			continue
		}
		payload.Messages = append(payload.Messages, requestMessage{Role: string(message.Role), Content: message.Content})
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode model input: %w", err)
	}
	req, err := m.request(ctx, http.MethodPost, "/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	res, err := m.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("connect to llama.cpp: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("llama.cpp rejected the request (HTTP %d)", res.StatusCode)
	}
	if !strings.HasPrefix(strings.ToLower(res.Header.Get("Content-Type")), "text/event-stream") {
		res.Body.Close()
		return nil, errors.New("llama.cpp did not return an event stream")
	}
	reader, writer := schema.Pipe[*schema.Message](8)
	go func() {
		defer res.Body.Close()
		defer writer.Close()
		err := readEvents(res.Body, func(chunk *schema.Message) bool { return !writer.Send(chunk, nil) })
		if err != nil {
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			writer.Send(nil, err)
		}
	}()
	return reader, nil
}

// readEvents handles SSE framing independently of network packet boundaries.
// A closed consumer returns false and stops parsing immediately.
func readEvents(r io.Reader, emit func(*schema.Message) bool) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), maxEventBytes)
	var data strings.Builder
	var outputLimit bool
	dispatch := func() (bool, error) {
		if data.Len() == 0 {
			return false, nil
		}
		payload := strings.TrimSuffix(data.String(), "\n")
		data.Reset()
		if payload == "[DONE]" {
			if outputLimit {
				return true, ErrOutputLimit
			}
			return true, nil
		}
		var chunk completionChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return true, fmt.Errorf("decode llama.cpp stream event: %w", err)
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return true, errors.New("llama.cpp returned a stream error")
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				continue
			}
			if len(choice.Delta.ToolCalls) > 0 && string(choice.Delta.ToolCalls) != "null" && string(choice.Delta.ToolCalls) != "[]" {
				return true, errors.New("llama.cpp returned an unexpected tool call in Core mode")
			}
			if choice.Delta.Content != "" && !emit(&schema.Message{Role: schema.Assistant, Content: choice.Delta.Content}) {
				return true, nil
			}
			if choice.FinishReason == "length" {
				outputLimit = true
			} else if choice.FinishReason != "" && choice.FinishReason != "stop" {
				return true, fmt.Errorf("llama.cpp stopped generation: %s", choice.FinishReason)
			}
		}
		return false, nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			done, err := dispatch()
			if done || err != nil {
				return err
			}
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		value := strings.TrimPrefix(line, "data:")
		value = strings.TrimPrefix(value, " ")
		if data.Len()+len(value)+1 > maxEventBytes {
			return errors.New("llama.cpp stream event exceeds size limit")
		}
		data.WriteString(value)
		data.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read llama.cpp stream: %w", err)
	}
	if done, err := dispatch(); done || err != nil {
		return err
	}
	return ErrIncompleteStream
}

func (m *llamaModel) request(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, m.baseURL+path, body)
	if err != nil {
		return nil, errors.New("invalid llama.cpp endpoint")
	}
	if m.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.config.APIKey)
	}
	return req, nil
}

func (m *llamaModel) check(ctx context.Context) error {
	req, err := m.request(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return err
	}
	res, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("check llama.cpp: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("llama.cpp models endpoint returned HTTP %d", res.StatusCode)
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&list); err != nil {
		return fmt.Errorf("decode llama.cpp model list: %w", err)
	}
	for _, item := range list.Data {
		if item.ID == m.config.Model {
			return nil
		}
	}
	return errors.New("LLAMA_MODEL is not listed by llama.cpp; use the server's configured model alias")
}
