package agent

import "context"

// ModelMessage is the provider-independent context passed to the agent engine.
type ModelMessage struct {
	Role    string
	Content string
}

// Engine streams only newly generated text. The callback must not be called
// concurrently, and returning an error from it must stop generation.
type Engine interface {
	Generate(context.Context, []ModelMessage, func(string) error) error
}
