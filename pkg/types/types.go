package types

import "github.com/sashabaranov/go-openai"

// Config represents the application configuration
type Config struct {
	CurrentModel    string            `json:"current_model"`
	Models          map[string]Model  `json:"models"`
	ApprovedFolders []string          `json:"approved_folders"`
}

// Model represents an AI model configuration
type Model struct {
	Name                string `json:"name"`
	BaseURL             string `json:"base_url"`
	APIKey              string `json:"api_key,omitempty"`
	MaxTokens           int    `json:"max_tokens,omitempty"`           // Maximum context length in tokens
	MaxCompletionTokens int    `json:"max_completion_tokens,omitempty"` // Maximum tokens to generate
}

// Agent represents the AI agent with its state
type Agent struct {
	Client          *openai.Client
	Conversation    []openai.ChatCompletionMessage
	Tools           map[string]func(map[string]interface{}) (string, error)
	LastTokenUsage  *openai.Usage
	TotalTokensUsed int
	Config          *Config
	ConfigPath      string
	ApprovedFolders map[string]bool // Track folders user has granted access to
	CurrentConvID   string          // ID of the currently active saved conversation
}

// ANSI color codes for console output
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorCyan   = "\033[36m"
	ColorGray   = "\033[90m"
	ColorMagenta = "\033[35m"
)