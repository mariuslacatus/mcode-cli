package markdown

import (
	"testing"
)

func TestProcessThinkTags(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name:     "No think tags",
			content:  "Hello world",
			expected: "Hello world",
		},
		{
			name:     "Single think tag",
			content:  "<think>Thinking...</think> Done.",
			expected: "_Thinking..._ Done.",
		},
		{
			name:     "Multiline think tag",
			content:  "<think>\nLine 1\nLine 2\n</think>",
			expected: "\n_Line 1_\n_Line 2_\n",
		},
		{
			name:     "Unclosed think tag",
			content:  "Start <think>Thinking...",
			expected: "Start _Thinking..._",
		},
		{
			name:     "Multiple think tags",
			content:  "<think>One</think> then <think>Two</think>",
			expected: "_One_ then _Two_",
		},
		{
			name:     "Think tag with other markdown",
			content:  "<think>**Bold**</think>",
			expected: "_**Bold**_",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProcessThinkTags(tt.content)
			if got != tt.expected {
				t.Errorf("ProcessThinkTags() = %q, want %q", got, tt.expected)
			}
		})
	}
}
