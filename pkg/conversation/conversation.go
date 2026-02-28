package conversation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Message represents a single conversation message
type Message struct {
	Role      string            `json:"role"`
	Content   string            `json:"content"`
	ToolID    string            `json:"tool_call_id,omitempty"`
	ToolCalls []ToolCall        `json:"tool_calls,omitempty"`
}

// ToolCall represents a saved tool call
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall represents a saved function call
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Conversation represents a complete conversation session
type Conversation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Messages  []Message `json:"messages"`
	TokensUsed int      `json:"tokens_used,omitempty"`
	Model     string    `json:"model"`
}

// Manager handles conversation save/load operations
type Manager struct {
	// conversation directory (will be set during initialization)
	ConversationDir string
}

// NewManager creates a new conversation manager
func NewManager(conversationDir string) *Manager {
	return &Manager{
		ConversationDir: conversationDir,
	}
}

// Save saves a conversation to disk
func (m *Manager) Save(conv *Conversation) error {
	// Ensure conversation directory exists
	if err := os.MkdirAll(m.ConversationDir, 0755); err != nil {
		return fmt.Errorf("failed to create conversation directory: %w", err)
	}

	// Update timestamps
	now := time.Now()
	conv.UpdatedAt = now
	if conv.CreatedAt.IsZero() {
		conv.CreatedAt = now
	}

	// Save to file
	filename := filepath.Join(m.ConversationDir, fmt.Sprintf("%s.json", conv.ID))
	data, err := json.MarshalIndent(conv, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal conversation: %w", err)
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write conversation file: %w", err)
	}

	return nil
}

// Load loads a conversation by ID
func (m *Manager) Load(id string) (*Conversation, error) {
	filename := filepath.Join(m.ConversationDir, fmt.Sprintf("%s.json", id))
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read conversation file: %w", err)
	}

	var conv Conversation
	if err := json.Unmarshal(data, &conv); err != nil {
		return nil, fmt.Errorf("failed to unmarshal conversation: %w", err)
	}

	return &conv, nil
}

// List lists all available conversations
func (m *Manager) List() ([]Conversation, error) {
	if err := os.MkdirAll(m.ConversationDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create conversation directory: %w", err)
	}

	files, err := os.ReadDir(m.ConversationDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read conversation directory: %w", err)
	}

	var conversations []Conversation
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		if filepath.Ext(file.Name()) != ".json" {
			continue
		}

		id := strings.TrimSuffix(file.Name(), ".json")
		conv, err := m.Load(id)
		if err != nil {
			continue // Skip corrupted files
		}

		conversations = append(conversations, *conv)
	}

	// Sort by most recent
	for i := 0; i < len(conversations); i++ {
		for j := i + 1; j < len(conversations); j++ {
			if conversations[i].UpdatedAt.After(conversations[j].UpdatedAt) {
				conversations[i], conversations[j] = conversations[j], conversations[i]
			}
		}
	}

	return conversations, nil
}

// Delete deletes a conversation by ID
func (m *Manager) Delete(id string) error {
	filename := filepath.Join(m.ConversationDir, fmt.Sprintf("%s.json", id))
	return os.Remove(filename)
}

// GetDefaultConversationDir returns the default conversation directory
func GetDefaultConversationDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	// Use .mcode/conversations directory in home
	convDir := filepath.Join(homeDir, ".mcode", "conversations")
	return convDir, nil
}

// GenerateID generates a unique ID for a conversation
func GenerateID() string {
	timestamp := time.Now().UnixNano()
	return fmt.Sprintf("conv-%d", timestamp)
}

// AutoSaveConfig defines automatic save behavior
type AutoSaveConfig struct {
	MaxMessages  int `json:"max_messages"`  // Auto-save after this many messages
	MaxMinutes   int `json:"max_minutes"`   // Auto-save after this many minutes
	KeepLast     int `json:"keep_last"`     // Keep only last N conversations
}

// DefaultAutoSaveConfig returns the default auto-save configuration
func DefaultAutoSaveConfig() AutoSaveConfig {
	return AutoSaveConfig{
		MaxMessages:  10,
		MaxMinutes:   30,
		KeepLast:     20,
	}
}
