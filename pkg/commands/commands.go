package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"coding-agent/pkg/agent"
	"coding-agent/pkg/config"
	"coding-agent/pkg/conversation"
	"coding-agent/pkg/llm"
	"coding-agent/pkg/markdown"
	"coding-agent/pkg/project"
	"coding-agent/pkg/types"

	"github.com/sashabaranov/go-openai"
)

// Handler handles slash commands
type Handler struct {
	agent           *types.Agent
	projectManager  *project.Manager
	conversationMgr *conversation.Manager
}

// NewHandler creates a new command handler
func NewHandler(agent *types.Agent, projectManager *project.Manager) *Handler {
	// Get conversation directory
	convDir, err := conversation.GetDefaultConversationDir()
	if err != nil {
		// Fallback to temp directory if home dir is not available
		convDir = filepath.Join(os.TempDir(), "mcode", "conversations")
	}

	return &Handler{
		agent:           agent,
		projectManager:  projectManager,
		conversationMgr: conversation.NewManager(convDir),
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
	case "/prompt":
		h.handlePromptCommand()
		return false, nil
	case "/models":
		err := h.handleModelsCommand(parts)
		return false, err
	case "/permissions":
		err := h.handlePermissionsCommand(parts)
		return false, err
	case "/compact":
		err := agent.CompactContext(h.agent)
		return false, err
	case "/help":
		h.showHelp()
		return false, nil
	case "/save":
		err := h.handleSaveCommand()
		return false, err
	case "/resume":
		err := h.handleResumeCommand(parts)
		return false, err
	case "/conv":
		err := h.handleConvCommand(parts)
		return false, err
	case "/del":
		err := h.handleDelCommand(parts)
		return false, err
	default:
		fmt.Printf("❌ Unknown command: %s\n", parts[0])
		fmt.Println("Available commands: /exit, /init, /new, /export, /models, /permissions, /help, /compact, /save, /resume, /conv, /del")
		return false, nil
	}
}

// clearContext clears the conversation context
func (h *Handler) clearContext() {
	h.agent.Conversation = []types.Message{}
	h.agent.LastTokenUsage = nil
	h.agent.CurrentConvID = ""

	// Clear terminal
	fmt.Print("\033[2J\033[H")

	fmt.Printf("%s🔄 Conversation context cleared - Starting fresh!%s\n", types.ColorGreen, types.ColorReset)
}

// handlePromptCommand handles /prompt command
func (h *Handler) handlePromptCommand() {
	fmt.Println("\n🧠 Current System Prompt(s)")
	fmt.Println("===========================")

	renderer, _ := markdown.NewTermRenderer()

	found := false
	for _, msg := range h.agent.Conversation {
		if msg.Role == openai.ChatMessageRoleSystem {
			found = true
			fmt.Printf("\n%s[System Message]%s\n", types.ColorCyan, types.ColorReset)
			if rendered, err := renderer.Render(msg.Content); err == nil {
				fmt.Print(rendered)
			} else {
				fmt.Println(msg.Content)
			}
		}
	}

	if !found {
		fmt.Println("No system messages found in current conversation.")
	}
	fmt.Println()
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
		if model.Provider != "" {
			fmt.Printf("   Provider: %s\n", model.Provider)
		}
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

	// Update provider
	var provider llm.Provider
	if model.Provider == "gemini" || strings.Contains(strings.ToLower(model.Name), "gemini") {
		geminiProvider, err := llm.NewGeminiProvider(context.Background(), model.APIKey)
		if err != nil {
			fmt.Printf("Error initializing Gemini provider: %v. Falling back to OpenAI.\n", err)
			clientConfig := openai.DefaultConfig(model.APIKey)
			clientConfig.BaseURL = model.BaseURL
			provider = llm.NewOpenAIProvider(openai.NewClientWithConfig(clientConfig))
		} else {
			provider = geminiProvider
		}
	} else {
		clientConfig := openai.DefaultConfig(model.APIKey)
		clientConfig.BaseURL = model.BaseURL
		provider = llm.NewOpenAIProvider(openai.NewClientWithConfig(clientConfig))
	}
	h.agent.LLM = provider

	fmt.Printf("✅ Switched to model: %s\n", modelKey)
	fmt.Printf("📱 Name: %s\n", model.Name)
	if model.BaseURL != "" {
		fmt.Printf("🌐 URL: %s\n", model.BaseURL)
	}

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
	fmt.Println("  /prompt      - List current system instructions/prompts")
	fmt.Println("  /models      - List or switch between available models")
	fmt.Println("  /permissions - Manage folder permissions")
	fmt.Println("  /compact     - Compact conversation context to save tokens")
	fmt.Println("  /save        - Save current conversation to disk")
	fmt.Println("  /resume      - List and resume saved conversations")
	fmt.Println("  /conv        - Manage conversations (list, save, delete, info)")
	fmt.Println("  /del <id>    - Delete a conversation by ID")
	fmt.Println("  /exit        - Exit the agent")
	fmt.Println("  /help        - Show this help message")
	fmt.Println()
	fmt.Println("Conversation Management:")
	fmt.Println("  /save          - Save current conversation with auto-generated title")
	fmt.Println("  /resume        - List all saved conversations")
	fmt.Println("  /resume <id>   - Resume a specific conversation")
	fmt.Println("  /conv list     - List all conversations")
	fmt.Println("  /conv save     - Save current conversation")
	fmt.Println("  /conv delete <id> - Delete a conversation")
	fmt.Println("  /conv info <id>   - Show conversation details and metadata")
	fmt.Println()
	fmt.Println("Available Tools:")
	fmt.Println("  📖 read_file    - Read file contents (with safety limits)")
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
	fmt.Println("  - Use /save to save your conversation at any time")
	fmt.Println("  - Use /resume to continue a previous conversation later")
	fmt.Println("  - Use # commands to add permanent instructions")
	fmt.Println("    Example: #always use python3 instead of python")
	fmt.Println()
}

// handleSaveCommand handles /save command
func (h *Handler) handleSaveCommand() error {
	// Use existing ID if we are in a resumed session, otherwise generate new
	id := h.agent.CurrentConvID
	isNew := false
	if id == "" {
		id = conversation.GenerateID()
		h.agent.CurrentConvID = id
		isNew = true
	}

	// Create a new conversation with current state
	conv := &conversation.Conversation{
		ID:         id,
		Title:      "Untitled Conversation",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		Messages:   convertMessages(h.agent.Conversation),
		TokensUsed: h.agent.TotalTokensUsed,
		Model:      h.agent.Config.CurrentModel,
	}

	// If updating, try to preserve the original CreatedAt
	if !isNew {
		if existing, err := h.conversationMgr.Load(id); err == nil {
			conv.CreatedAt = existing.CreatedAt
			conv.Title = existing.Title
		}
	}

	// Try to set a better title based on first user message if it's still untitled
	if conv.Title == "Untitled Conversation" && len(conv.Messages) > 0 {
		for _, msg := range conv.Messages {
			if msg.Role == "user" && strings.Contains(msg.Content, " ") && !strings.HasPrefix(msg.Content, "/") {
				conv.Title = strings.TrimSpace(msg.Content)
				if len(conv.Title) > 50 {
					conv.Title = conv.Title[:50] + "..."
				}
				break
			}
		}
	}

	// Save to disk
	if err := h.conversationMgr.Save(conv); err != nil {
		return fmt.Errorf("failed to save conversation: %v", err)
	}

	if isNew {
		fmt.Printf("💾 Conversation saved as NEW: %s\n", conv.ID)
	} else {
		fmt.Printf("💾 Conversation UPDATED: %s\n", conv.ID)
	}

	if conv.Title != "Untitled Conversation" {
		fmt.Printf("   Title: %s\n", conv.Title)
	}
	fmt.Printf("   Messages: %d\n", len(conv.Messages))
	fmt.Printf("   Tokens: %d\n", conv.TokensUsed)
	fmt.Printf("   Model: %s\n", conv.Model)

	return nil
}

// handleResumeCommand handles /resume command
func (h *Handler) handleResumeCommand(parts []string) error {
	if len(parts) == 1 {
		// List conversations if no index provided
		return h.listConversations()
	}

	if len(parts) == 2 {
		// Resume specific conversation by index
		return h.resumeConversationByIndex(parts[1])
	}

	fmt.Println("Usage:")
	fmt.Println("  /resume              - List all conversations")
	fmt.Println("  /resume <number>     - Resume conversation by index number")
	return nil
}

// handleConvCommand handles /conv command for conversation management
func (h *Handler) handleConvCommand(parts []string) error {
	if len(parts) < 2 {
		return h.handleResumeCommand([]string{"/resume"})
	}

	switch parts[1] {
	case "list", "ls":
		return h.listConversations()
	case "save":
		return h.handleSaveCommand()
	case "delete", "rm", "del":
		if len(parts) < 3 {
			fmt.Println("Usage: /conv delete <id>")
			return nil
		}
		return h.deleteConversation(parts[2])
	case "info":
		if len(parts) < 3 {
			fmt.Println("Usage: /conv info <id>")
			return nil
		}
		return h.showConvInfo(parts[2])
	default:
		fmt.Printf("❌ Unknown subcommand: %s\n", parts[1])
		fmt.Println("Usage:")
		fmt.Println("  /conv list      - List all conversations")
		fmt.Println("  /conv save      - Save current conversation")
		fmt.Println("  /conv delete <id> - Delete a conversation")
		fmt.Println("  /conv info <id>   - Show conversation details")
		return nil
	}
}

// handleDelCommand handles /del command for deleting conversations
func (h *Handler) handleDelCommand(parts []string) error {
	if len(parts) < 2 {
		fmt.Println("Usage: /del <conversation-id>")
		return nil
	}
	return h.deleteConversation(parts[1])
}

// listConversations lists all available conversations
func (h *Handler) listConversations() error {
	conversations, err := h.conversationMgr.List()
	if err != nil {
		return fmt.Errorf("failed to list conversations: %v", err)
	}

	if len(conversations) == 0 {
		fmt.Println("\n📋 No saved conversations found.")
		return nil
	}

	fmt.Println("\n📋 Saved Conversations")
	fmt.Println("======================")

	for i, conv := range conversations {
		timeStr := conv.UpdatedAt.Format("2006-01-02 15:04")
		title := conv.Title
		if title == "Untitled Conversation" {
			title = "(No title)"
		}

		fmt.Printf("%d. %s (%s)\n", i+1, title, timeStr)
		fmt.Printf("   ID: %s\n", conv.ID)
		fmt.Printf("   Messages: %d | Tokens: %d\n", len(conv.Messages), conv.TokensUsed)
		if len(conv.Messages) > 0 && conv.Messages[0].Role == "user" {
			fmt.Printf("   First message: %s\n", truncateString(conv.Messages[0].Content, 50))
		}
		fmt.Println()
	}

	fmt.Printf("Total: %d conversation(s)\n", len(conversations))
	fmt.Println("To resume a conversation, use: /resume <id>")
	fmt.Println("Or manage conversations with: /conv <list|save|delete|info>")

	return nil
}

// resumeConversationByIndex resumes a conversation by index number from the list
func (h *Handler) resumeConversationByIndex(indexStr string) error {
	// List conversations first to show numbered list
	conversations, err := h.conversationMgr.List()
	if err != nil {
		return fmt.Errorf("failed to list conversations: %v", err)
	}

	if len(conversations) == 0 {
		fmt.Println("\n📋 No saved conversations found.")
		return nil
	}

	// Parse index number
	index, err := parseIndexNumber(indexStr, len(conversations))
	if err != nil {
		return err
	}

	// Get the conversation at the specified index
	conv := conversations[index]

	// Now load and resume with the actual conversation ID
	return h.resumeConversation(conv.ID)
}

// parseIndexNumber parses an index string and validates it against the list length
func parseIndexNumber(indexStr string, listLength int) (int, error) {
	// Try to parse as integer
	index, err := parseNumericIndex(indexStr)
	if err != nil {
		// If not a number, treat as ID and try to load directly
		return -1, fmt.Errorf("invalid index: %s (use /resume to list conversations with numbers)", indexStr)
	}

	if index < 1 || index > listLength {
		return -1, fmt.Errorf("index out of range: %d (must be between 1 and %d)", index, listLength)
	}

	return index - 1, nil // Convert to 0-based index
}

// parseNumericIndex safely parses a string as an integer
func parseNumericIndex(s string) (int, error) {
	var num int
	_, err := fmt.Sscanf(s, "%d", &num)
	return num, err
}

// resumeConversation loads and resumes a conversation
func (h *Handler) resumeConversation(id string) error {
	conv, err := h.conversationMgr.Load(id)
	if err != nil {
		return fmt.Errorf("failed to load conversation: %w", err)
	}

	fmt.Printf("\n🔄 Resuming conversation: %s\n", id)
	if conv.Title != "Untitled Conversation" {
		fmt.Printf("   Title: %s\n", conv.Title)
	}
	fmt.Printf("   Model: %s\n", conv.Model)
	fmt.Printf("   Messages: %d\n", len(conv.Messages))
	fmt.Printf("   Tokens: %d\n", conv.TokensUsed)
	fmt.Println()

	// Convert to agent conversation format
	agentMessages := convertMessagesFromConversation(conv)

	// Update agent state
	h.agent.Conversation = agentMessages
	h.agent.TotalTokensUsed = conv.TokensUsed
	h.agent.Config.CurrentModel = conv.Model
	h.agent.CurrentConvID = id

	// Display all old messages to help user understand context
	fmt.Printf("%s--- Conversation History ---%s\n", types.ColorCyan, types.ColorReset)
	displayConversationHistory(conv.Messages)
	fmt.Printf("%s--- End of History ---%s\n\n", types.ColorCyan, types.ColorReset)

	// Notify user
	if len(agentMessages) > 0 {
		lastMsg := agentMessages[len(agentMessages)-1]
		if lastMsg.Role == "user" {
			fmt.Printf("%s💡 Last message from user: %s%s\n", types.ColorYellow, truncateString(lastMsg.Content, 100), types.ColorReset)
		}
	}

	fmt.Println("✅ Conversation loaded. You can continue from here!")
	fmt.Println()

	return nil
}

// displayConversationHistory displays all messages in a conversation
func displayConversationHistory(messages []conversation.Message) {
	renderer, _ := markdown.NewTermRenderer()

	for _, msg := range messages {
		roleColor := types.ColorReset
		switch msg.Role {
		case "user":
			roleColor = types.ColorYellow
		case "assistant":
			roleColor = types.ColorGreen
		case "system":
			roleColor = types.ColorCyan
		case "tool":
			roleColor = types.ColorMagenta
		}

		fmt.Printf("%s[%s]%s ", roleColor, msg.Role, types.ColorReset)

		// Render with markdown if possible, otherwise fallback to plain text
		content := msg.Content
		if renderer != nil {
			if rendered, err := renderer.Render(content); err == nil {
				fmt.Print(rendered)
			} else {
				fmt.Println(content)
			}
		} else {
			fmt.Println(content)
		}

		// Show tool call ID if present
		if msg.ToolID != "" {
			fmt.Printf("   %s📊 Tool Call ID: %s%s\n", types.ColorGray, msg.ToolID, types.ColorReset)
		}
		fmt.Println()
	}
}

// deleteConversation deletes a conversation by ID
func (h *Handler) deleteConversation(id string) error {
	// Confirm deletion
	fmt.Printf("⚠️  Are you sure you want to delete conversation '%s'? (y/N): ", id)
	var confirm string
	fmt.Scanln(&confirm)
	if strings.ToLower(confirm) != "y" {
		fmt.Println("❌ Deletion cancelled")
		return nil
	}

	if err := h.conversationMgr.Delete(id); err != nil {
		return fmt.Errorf("failed to delete conversation: %v", err)
	}

	fmt.Printf("✅ Conversation deleted: %s\n", id)
	return nil
}

// showConvInfo shows detailed information about a conversation
func (h *Handler) showConvInfo(id string) error {
	conv, err := h.conversationMgr.Load(id)
	if err != nil {
		return fmt.Errorf("failed to load conversation: %w", err)
	}

	fmt.Printf("\n📊 Conversation Information\n")
	fmt.Printf("===========================\n")
	fmt.Printf("ID:           %s\n", conv.ID)
	fmt.Printf("Title:        %s\n", conv.Title)
	fmt.Printf("Model:        %s\n", conv.Model)
	fmt.Printf("Created:      %s\n", conv.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("Updated:      %s\n", conv.UpdatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("Duration:     %s\n", conv.UpdatedAt.Sub(conv.CreatedAt))
	fmt.Printf("Messages:     %d\n", len(conv.Messages))
	fmt.Printf("Tokens:       %d\n", conv.TokensUsed)
	fmt.Printf("\n📄 Last 3 messages:\n")

	// Show last 3 messages
	for i := len(conv.Messages) - 1; i > len(conv.Messages)-4 && i >= 0; i-- {
		msg := conv.Messages[i]
		fmt.Printf("\n[%s] %s\n", msg.Role, truncateString(msg.Content, 150))
		if msg.ToolID != "" {
			fmt.Printf("   Tool Call ID: %s\n", msg.ToolID)
		}
	}

	return nil
}

// convertMessages converts agent conversation to conversation manager format
func convertMessages(agentMsgs []types.Message) []conversation.Message {
	convMsgs := make([]conversation.Message, 0, len(agentMsgs))
	for _, msg := range agentMsgs {
		cm := conversation.Message{
			Role:             msg.Role,
			Content:          msg.Content,
			Reasoning:        msg.Reasoning,
			ThoughtSignature: msg.ThoughtSignature,
		}
		if msg.Role == openai.ChatMessageRoleTool {
			cm.ToolID = msg.ToolCallID
		}

		if len(msg.ToolCalls) > 0 {
			cm.ToolCalls = make([]conversation.ToolCall, len(msg.ToolCalls))
			for i, tc := range msg.ToolCalls {
				cm.ToolCalls[i] = conversation.ToolCall{
					ID:   tc.ID,
					Type: string(tc.Type),
					Function: conversation.FunctionCall{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
		}
		convMsgs = append(convMsgs, cm)
	}
	return convMsgs
}

// convertMessagesFromConversation converts conversation manager format to agent format
func convertMessagesFromConversation(conv *conversation.Conversation) []types.Message {
	agentMsgs := make([]types.Message, 0, len(conv.Messages))
	for _, msg := range conv.Messages {
		am := types.Message{
			Role:             msg.Role,
			Content:          msg.Content,
			Reasoning:        msg.Reasoning,
			ThoughtSignature: msg.ThoughtSignature,
		}
		if msg.Role == openai.ChatMessageRoleTool {
			am.ToolCallID = msg.ToolID
		}

		if len(msg.ToolCalls) > 0 {
			am.ToolCalls = make([]openai.ToolCall, len(msg.ToolCalls))
			for i, tc := range msg.ToolCalls {
				am.ToolCalls[i] = openai.ToolCall{
					ID:   tc.ID,
					Type: openai.ToolType(tc.Type),
					Function: openai.FunctionCall{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
		}
		agentMsgs = append(agentMsgs, am)
	}
	return agentMsgs
}

// truncateString truncates a string to a maximum length
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
