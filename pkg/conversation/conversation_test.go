package conversation

import (
	"os"
	"testing"
	"time"
)

func TestManager(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "mcode-conv-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	mgr := NewManager(tmpDir)

	conv := &Conversation{
		ID:        "test-conv-1",
		Title:     "Test Conversation",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Messages: []Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi there"},
		},
		Model: "test-model",
	}

	// Test Save
	if err := mgr.Save(conv); err != nil {
		t.Errorf("Save() error = %v", err)
	}

	// Test Load
	loaded, err := mgr.Load("test-conv-1")
	if err != nil {
		t.Errorf("Load() error = %v", err)
	}
	if loaded.Title != "Test Conversation" {
		t.Errorf("Load() title = %v, want %v", loaded.Title, "Test Conversation")
	}
	if len(loaded.Messages) != 2 {
		t.Errorf("Load() messages len = %v, want 2", len(loaded.Messages))
	}

	// Test List
	list, err := mgr.List()
	if err != nil {
		t.Errorf("List() error = %v", err)
	}
	if len(list) != 1 {
		t.Errorf("List() len = %v, want 1", len(list))
	}

	// Test Delete
	if err := mgr.Delete("test-conv-1"); err != nil {
		t.Errorf("Delete() error = %v", err)
	}
	
	_, err = mgr.Load("test-conv-1")
	if err == nil {
		t.Error("Load() should have failed after Delete()")
	}
}
