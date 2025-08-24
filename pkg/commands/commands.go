package commands

import (
	"fmt"
	"path/filepath"
	"strings"

	"coding-agent/pkg/config"
	"coding-agent/pkg/project"
	"coding-agent/pkg/types"
	"github.com/sashabaranov/go-openai"
)

// Handler handles slash commands
type Handler struct {
	agent          *types.Agent
	projectManager *project.Manager
}

// NewHandler creates a new command handler
func NewHandler(agent *types.Agent, projectManager *project.Manager) *Handler {
	return &Handler{
		agent:          agent,
		projectManager: projectManager,
	}
}

// Handle processes slash commands
func (h *Handler) Handle(command string) (bool, error) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return false, nil
	}

	switch parts[0] {
	case "/exit", "/quit":
		fmt.Println("👋 Goodbye!")
		return true, nil
	case "/init":
		err := h.projectManager.Initialize()
		return false, err
	case "/new":
		h.clearContext()
		return false, nil
	case "/export":
		err := h.projectManager.ExportContext(parts)
		return false, err
	case "/models":
		err := h.handleModelsCommand(parts)
		return false, err
	case "/permissions":
		err := h.handlePermissionsCommand(parts)
		return false, err
	case "/help":
		h.showHelp()
		return false, nil
	default:
		fmt.Printf("❌ Unknown command: %s\n", parts[0])
		fmt.Println("Available commands: /exit, /init, /new, /export, /models, /permissions, /help")
		return false, nil
	}
}

// clearContext clears the conversation context
func (h *Handler) clearContext() {
	h.agent.Conversation = []openai.ChatCompletionMessage{}
	h.agent.LastTokenUsage = nil
	// Don't reset TotalTokensUsed - keep session total

	// Clear terminal
	fmt.Print("\033[2J\033[H")

	fmt.Printf("%s🔄 Conversation context cleared - Starting fresh!%s\n", types.ColorGreen, types.ColorReset)
}

// handleModelsCommand handles /models command
func (h *Handler) handleModelsCommand(parts []string) error {
	if len(parts) == 1 {
		// List available models
		return h.listModels()
	}

	if len(parts) == 2 {
		// Switch to model
		return h.switchModel(parts[1])
	}

	fmt.Println("Usage:")
	fmt.Println("  /models           - List available models")
	fmt.Println("  /models <name>    - Switch to model")
	return nil
}

// listModels lists all available models
func (h *Handler) listModels() error {
	fmt.Println("\n🤖 Available Models")
	fmt.Println("==================")

	for key, model := range h.agent.Config.Models {
		status := ""
		if key == h.agent.Config.CurrentModel {
			status = " (current)"
		}

		fmt.Printf("📱 %s%s\n", key, status)
		fmt.Printf("   Name: %s\n", model.Name)
		fmt.Printf("   URL:  %s\n", model.BaseURL)
		if model.APIKey != "" {
			if len(model.APIKey) > 4 {
				fmt.Printf("   API Key: ***%s\n", model.APIKey[len(model.APIKey)-4:])
			} else {
				fmt.Printf("   API Key: ***\n")
			}
		} else {
			fmt.Printf("   API Key: (none)\n")
		}
		fmt.Println()
	}

	return nil
}

// switchModel switches to a different model
func (h *Handler) switchModel(modelKey string) error {
	model, exists := h.agent.Config.Models[modelKey]
	if !exists {
		fmt.Printf("❌ Model '%s' not found\n", modelKey)
		fmt.Println("Available models:")
		for key := range h.agent.Config.Models {
			fmt.Printf("  - %s\n", key)
		}
		return nil
	}

	// Update config
	h.agent.Config.CurrentModel = modelKey

	// Save config
	if err := config.Save(h.agent.ConfigPath, h.agent.Config); err != nil {
		return fmt.Errorf("failed to save config: %v", err)
	}

	// Update client
	clientConfig := openai.DefaultConfig(model.APIKey)
	clientConfig.BaseURL = model.BaseURL
	h.agent.Client = openai.NewClientWithConfig(clientConfig)

	fmt.Printf("✅ Switched to model: %s\n", modelKey)
	fmt.Printf("📱 Name: %s\n", model.Name)
	fmt.Printf("🌐 URL: %s\n", model.BaseURL)

	return nil
}

// handlePermissionsCommand handles /permissions command
func (h *Handler) handlePermissionsCommand(parts []string) error {
	if len(parts) == 1 {
		// List approved folders
		return h.listApprovedFolders()
	}

	if len(parts) == 3 && parts[1] == "remove" {
		// Remove folder permission
		return h.removeFolderPermission(parts[2])
	}

	fmt.Println("Usage:")
	fmt.Println("  /permissions           - List approved folders")
	fmt.Println("  /permissions remove <path> - Remove folder permission")
	return nil
}

// listApprovedFolders lists all approved folders
func (h *Handler) listApprovedFolders() error {
	fmt.Println("\n🔒 Approved Folders")
	fmt.Println("===================")

	if len(h.agent.Config.ApprovedFolders) == 0 {
		fmt.Println("No folders have been approved yet.")
		return nil
	}

	for i, folder := range h.agent.Config.ApprovedFolders {
		fmt.Printf("%d. %s\n", i+1, folder)
	}

	fmt.Printf("\nTotal: %d folder(s)\n", len(h.agent.Config.ApprovedFolders))
	return nil
}

// removeFolderPermission removes folder permission
func (h *Handler) removeFolderPermission(folderPath string) error {
	// Normalize the path
	absPath, err := filepath.Abs(folderPath)
	if err != nil {
		return fmt.Errorf("error resolving path: %v", err)
	}

	// Check if folder is in approved list
	found := false
	newApproved := make([]string, 0, len(h.agent.Config.ApprovedFolders))
	for _, folder := range h.agent.Config.ApprovedFolders {
		if folder != absPath {
			newApproved = append(newApproved, folder)
		} else {
			found = true
		}
	}

	if !found {
		fmt.Printf("❌ Folder not found in approved list: %s\n", absPath)
		return nil
	}

	// Update config and save
	h.agent.Config.ApprovedFolders = newApproved
	delete(h.agent.ApprovedFolders, absPath)

	if err := config.Save(h.agent.ConfigPath, h.agent.Config); err != nil {
		return fmt.Errorf("failed to save config: %v", err)
	}

	fmt.Printf("✅ Removed folder permission: %s\n", absPath)
	return nil
}

// showHelp displays help information
func (h *Handler) showHelp() {
	fmt.Println("\n🤖 MCode CLI - Help")
	fmt.Println("========================")
	fmt.Println()
	fmt.Println("Slash Commands:")
	fmt.Println("  /init        - Initialize project and create AGENTS.md")
	fmt.Println("  /new         - Clear conversation context (start fresh)")
	fmt.Println("  /export      - Export conversation context to text file")
	fmt.Println("  /models      - List or switch between available models")
	fmt.Println("  /permissions - Manage folder permissions")
	fmt.Println("  /exit        - Exit the agent")
	fmt.Println("  /help        - Show this help message")
	fmt.Println()
	fmt.Println("Available Tools:")
	fmt.Println("  📖 read_file    - Read file contents")
	fmt.Println("  📁 list_files   - List directory contents")
	fmt.Println("  ⚡ bash_command - Execute shell commands")
	fmt.Println("  ✏️ edit_file    - Create/modify files (shows colored diffs)")
	fmt.Println("  🔍 search_code  - Search for code patterns")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  - Type natural language requests for coding tasks")
	fmt.Println("  - Review and approve tool executions (Y/n/s/i/b)")
	fmt.Println("    • Y/Enter: Execute tool (default)")
	fmt.Println("    • n: Deny execution")
	fmt.Println("    • s: Skip execution")
	fmt.Println("    • i: Interrupt and provide alternative instruction")
	fmt.Println("    • b: Background execution (for long-running commands)")
	fmt.Println("  - Use slash commands for special operations")
	fmt.Println("  - Use /new to start a fresh conversation")
	fmt.Println("  - Use # commands to add permanent instructions")
	fmt.Println("    Example: #always use python3 instead of python")
	fmt.Println()
}