package tools

import "github.com/sashabaranov/go-openai"

// Tool defines the interface that all agent tools must implement
type Tool interface {
	// Name returns the unique identifier for the tool
	Name() string
	
	// Definition returns the OpenAI tool definition
	Definition() openai.Tool
	
	// Execute performs the tool's action
	Execute(params map[string]interface{}) (string, error)
	
	// Preview returns a string representation of the changes this tool would make
	Preview(params map[string]interface{}) (string, error)
	
	// GetDisplayInfo returns a user-friendly string describing the tool call for the UI
	GetDisplayInfo(params map[string]interface{}) string
}

// BaseTool provides common functionality for tools
type BaseTool struct {
	manager *Manager
}

func (b *BaseTool) Unmarshal(params map[string]interface{}, target interface{}) error {
	return b.manager.UnmarshalParams(params, target)
}
