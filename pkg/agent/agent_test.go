package agent

import (
	"strings"
	"testing"

	"coding-agent/pkg/types"
	"github.com/sashabaranov/go-openai"
)

func TestTrimContext(t *testing.T) {
	// Setup a mock agent with a small token limit for testing
	modelName := "test-model"
	ag := &types.Agent{
		Config: &types.Config{
			CurrentModel: modelName,
			Models: map[string]types.Model{
				modelName: {
					Name:      modelName,
					MaxTokens: 1000, // Small limit for testing (budget = 500)
				},
			},
		},
	}

	// Create a conversation that definitely exceeds 500 tokens
	// 1000 'hello ' strings will be ~1000-2000 tokens depending on tokenizer
	longText := strings.Repeat("hello world ", 500)

	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "System prompt"},
		{Role: openai.ChatMessageRoleUser, Content: "Message 1 " + longText},
		{Role: openai.ChatMessageRoleAssistant, Content: "Reply 1"},
		{Role: openai.ChatMessageRoleUser, Content: "Message 2 " + longText},
		{Role: openai.ChatMessageRoleAssistant, Content: "Reply 2"},
		{Role: openai.ChatMessageRoleUser, Content: "Message 3"},
		{Role: openai.ChatMessageRoleAssistant, Content: "Reply 3"},
	}

	trimmed := TrimContext(ag, messages)

	// Verifications:
	// 1. System message must be kept
	if len(trimmed) == 0 || trimmed[0].Role != openai.ChatMessageRoleSystem {
		t.Error("System message was not preserved")
	}

	// 2. Must be shorter than original
	if len(trimmed) >= len(messages) {
		t.Errorf("Context was not trimmed: got %d messages", len(trimmed))
	}

	// 3. Last message must be preserved (recency priority)
	lastOrig := messages[len(messages)-1].Content
	lastTrimmed := trimmed[len(trimmed)-1].Content
	if lastOrig != lastTrimmed {
		t.Error("Recent messages were not prioritized")
	}
}

func TestTruncateForLLM(t *testing.T) {
	modelName := "test-model"
	ag := &types.Agent{
		Config: &types.Config{
			CurrentModel: modelName,
			Models: map[string]types.Model{
				modelName: {
					MaxTokens: 1000, // 50% of this * 4 chars/token = 2000 chars limit
				},
			},
		},
	}

	// 10,000 chars should definitely be truncated to ~2000
	hugeText := strings.Repeat("a", 10000)
	truncated := TruncateForLLM(ag, hugeText, 0)

	if len(truncated) > 3000 {
		t.Errorf("TruncateForLLM failed to respect model-based limit: got len %d", len(truncated))
	}

	if !strings.Contains(truncated, "Output truncated") {
		t.Error("Truncation message missing")
	}
}
