package llm

import (
	"context"

	"github.com/sashabaranov/go-openai"
)

// Message represents a conversation message with optional reasoning
type Message struct {
	Role             string
	Content          string
	Reasoning        string
	ThoughtSignature []byte
	Name             string
	ToolCallID       string
	ToolCalls        []openai.ToolCall
}

// Request represents an LLM request
type Request struct {
	Model       string
	Messages    []Message
	Tools       []openai.Tool
	Temperature float32
	MaxTokens   int
	TopP        float32
	Stream      bool
}

// Response represents a standardized LLM response
type Response struct {
	Content          string
	Reasoning        string
	ThoughtSignature []byte
	ToolCalls        []openai.ToolCall
	Usage            *openai.Usage
	FinishReason     string
}

// StreamResponse represents a single chunk of a streaming response
type StreamResponse struct {
	Content          string
	Reasoning        string
	ThoughtSignature []byte
	ToolCalls        []openai.ToolCall
	Usage            *openai.Usage
	FinishReason     string
	Error            error
}

// Provider defines the interface for LLM services
type Provider interface {
	// CreateCompletion sends a non-streaming request to the LLM
	CreateCompletion(ctx context.Context, req Request) (*Response, error)

	// CreateStream sends a streaming request to the LLM
	// It returns a channel of StreamResponse chunks
	CreateStream(ctx context.Context, req Request) (<-chan StreamResponse, error)
}
