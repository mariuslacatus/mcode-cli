package tokens

import (
	"strings"

	"github.com/pkoukk/tiktoken-go"
	"github.com/sashabaranov/go-openai"
)

// CountTokens returns the number of tokens in a string for a given model
func CountTokens(modelName, text string) int {
	// tiktoken-go doesn't always have all latest models, so we map them to common ones
	encodingModel := modelName
	if strings.Contains(modelName, "gpt-4") {
		encodingModel = "gpt-4o"
	} else if strings.Contains(modelName, "gpt-3.5") {
		encodingModel = "gpt-3.5-turbo"
	} else if strings.Contains(modelName, "qwen") {
		// Qwen models use a similar tokenizer to gpt-4 or cl100k_base
		encodingModel = "gpt-4"
	}

	tkm, err := tiktoken.EncodingForModel(encodingModel)
	if err != nil {
		// Fallback to cl100k_base which is common for modern LLMs
		tkm, err = tiktoken.GetEncoding("cl100k_base")
		if err != nil {
			// Absolute fallback
			return len(text) / 4
		}
	}

	token := tkm.Encode(text, nil, nil)
	return len(token)
}

// CountMessagesTokens returns the total number of tokens for a list of messages
func CountMessagesTokens(modelName string, messages []openai.ChatCompletionMessage) int {
	var tokensPerMessage int
	var tokensPerName int

	if strings.Contains(modelName, "gpt-3.5-turbo") {
		tokensPerMessage = 4
		tokensPerName = -1
	} else {
		// Default for gpt-4 and others
		tokensPerMessage = 3
		tokensPerName = 1
	}

	numTokens := 0
	for _, message := range messages {
		numTokens += tokensPerMessage
		numTokens += CountTokens(modelName, message.Content)
		numTokens += CountTokens(modelName, message.Role)
		if message.Name != "" {
			numTokens += tokensPerName
			numTokens += CountTokens(modelName, message.Name)
		}
		
		// Count tool calls tokens
		if len(message.ToolCalls) > 0 {
			for _, tc := range message.ToolCalls {
				numTokens += CountTokens(modelName, tc.Function.Name)
				numTokens += CountTokens(modelName, tc.Function.Arguments)
			}
		}
	}
	
	numTokens += 3 // every reply is primed with <|start|>assistant<|message|>
	return numTokens
}
