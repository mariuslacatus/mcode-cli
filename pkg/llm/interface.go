package llm

import (
	"context"

	"github.com/sashabaranov/go-openai"
)

// Response represents a standardized LLM response
type Response struct {
	Content      string
	ToolCalls    []openai.ToolCall
	Usage        *openai.Usage
	FinishReason string
}

// StreamResponse represents a single chunk of a streaming response
type StreamResponse struct {
	Content      string
	ToolCalls    []openai.ToolCall
	Usage        *openai.Usage
	FinishReason string
	Error        error
}

// Provider defines the interface for LLM services
type Provider interface {
	// CreateCompletion sends a non-streaming request to the LLM
	CreateCompletion(ctx context.Context, req openai.ChatCompletionRequest) (*Response, error)
	
	// CreateStream sends a streaming request to the LLM
	// It returns a channel of StreamResponse chunks
	CreateStream(ctx context.Context, req openai.ChatCompletionRequest) (<-chan StreamResponse, error)
}
