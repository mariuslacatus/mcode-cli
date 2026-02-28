package config

import (
	"os"
	"path/filepath"
	"testing"

	"coding-agent/pkg/types"
)

func TestLoadOrCreateConfig(t *testing.T) {
	// Create temp directory for test config
	tmpDir, err := os.MkdirTemp("", "mcode-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "config.json")

	// Test case 1: Create default config
	cfg, err := LoadOrCreateConfig(configPath)
	if err != nil {
		t.Errorf("LoadOrCreateConfig() error = %v", err)
	}
	if cfg.CurrentModel == "" {
		t.Error("LoadOrCreateConfig() returned empty current model")
	}

	// Test case 2: Load existing config
	cfg.CurrentModel = "test-model"
	if err := Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}

	cfg2, err := LoadOrCreateConfig(configPath)
	if err != nil {
		t.Errorf("LoadOrCreateConfig() error = %v", err)
	}
	if cfg2.CurrentModel != "test-model" {
		t.Errorf("LoadOrCreateConfig() = %v, want %v", cfg2.CurrentModel, "test-model")
	}
}

func TestSave(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mcode-test-save")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "config.json")
	cfg := &types.Config{
		CurrentModel: "save-test",
	}

	if err := Save(configPath, cfg); err != nil {
		t.Errorf("Save() error = %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("Save() did not create config file")
	}
}
