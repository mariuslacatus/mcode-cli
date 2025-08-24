package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"coding-agent/pkg/types"
)

// GetConfigPath returns the configuration file path
func GetConfigPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ".mcode-config.json"
	}
	return filepath.Join(homeDir, ".mcode-config.json")
}

// LoadOrCreateConfig loads existing config or creates a default one
func LoadOrCreateConfig(configPath string) (*types.Config, error) {
	// Try to load existing config
	if data, err := os.ReadFile(configPath); err == nil {
		var config types.Config
		if json.Unmarshal(data, &config) == nil {
			return &config, nil
		}
	}

	// Create default config
	defaultConfig := &types.Config{
		CurrentModel: "qwen3-coder",
		Models: map[string]types.Model{
			"qwen3-coder": {
				Name:    "lmstudio-community/qwen3-coder-30b-a3b-instruct-mlx@8bit",
				BaseURL: "http://localhost:1234/v1",
			},
			"hermes-3": {
				Name:    "NousResearch/Hermes-3-Llama-3.1-8B-GGUF",
				BaseURL: "http://localhost:1234/v1",
			},
			"llama-3.2": {
				Name:    "bartowski/Llama-3.2-3B-Instruct-GGUF",
				BaseURL: "http://localhost:1234/v1",
			},
			"claude": {
				Name:    "claude-3-5-sonnet-20241022",
				BaseURL: "https://api.anthropic.com/v1",
				APIKey:  "",
			},
			"openai": {
				Name:    "gpt-4",
				BaseURL: "https://api.openai.com/v1",
				APIKey:  "",
			},
		},
		ApprovedFolders: []string{},
	}

	// Save default config
	if err := Save(configPath, defaultConfig); err != nil {
		return nil, fmt.Errorf("failed to save default config: %v", err)
	}

	return defaultConfig, nil
}

// Save saves the configuration to file
func Save(configPath string, config *types.Config) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0644)
}