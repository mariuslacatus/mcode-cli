package tokens

import (
	"testing"

	"coding-agent/pkg/types"
	"github.com/sashabaranov/go-openai"
)

func TestCountTokens(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		text      string
		minTokens int
	}{
		{
			name:      "Simple English",
			model:     "gpt-4",
			text:      "Hello, world!",
			minTokens: 2,
		},
		{
			name:      "Code snippet",
			model:     "gpt-4",
			text:      `func main() { fmt.Println("test") }`,
			minTokens: 10,
		},
		{
			name:      "Fallback for unknown model",
			model:     "unknown-model",
			text:      "This should fallback to cl100k_base",
			minTokens: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountTokens(tt.model, tt.text)
			if got < tt.minTokens {
				t.Errorf("CountTokens() = %v, want at least %v", got, tt.minTokens)
			}
		})
	}
}

func TestCountMessagesTokens(t *testing.T) {
	messages := []types.Message{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: "You are a helpful assistant.",
		},
		{
			Role:    openai.ChatMessageRoleUser,
			Content: "Hello!",
		},
	}

	model := "gpt-4"
	got := CountMessagesTokens(model, messages)

	// System: ~7 tokens, User: ~1 token, Overhead: ~7 tokens
	// Should be around 15-20 tokens
	if got < 10 || got > 30 {
		t.Errorf("CountMessagesTokens() = %v, want between 10 and 30", got)
	}
}
