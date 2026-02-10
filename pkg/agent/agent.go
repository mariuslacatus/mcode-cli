package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"coding-agent/pkg/config"
	"coding-agent/pkg/markdown"
	"coding-agent/pkg/project"
	"coding-agent/pkg/tools"
	"coding-agent/pkg/types"

	"github.com/sashabaranov/go-openai"
)

// New creates a new agent instance
func New() *types.Agent {
	configPath := config.GetConfigPath()
	cfg, err := config.LoadOrCreateConfig(configPath)
	if err != nil {
		fmt.Printf("Warning: Failed to load config, using defaults: %v\n", err)
		// Fallback to hardcoded defaults
		cfg = &types.Config{
			CurrentModel: "qwen3-coder",
			Models: map[string]types.Model{
				"qwen3-coder": {
					Name:    "lmstudio-community/qwen3-coder-30b-a3b-instruct-mlx@8bit",
					BaseURL: "http://localhost:1234/v1",
				},
			},
		}
	}

	// Get current model configuration
	currentModel, exists := cfg.Models[cfg.CurrentModel]
	if !exists {
		fmt.Printf("Warning: Current model '%s' not found, using first available model\n", cfg.CurrentModel)
		for _, model := range cfg.Models {
			currentModel = model
			break
		}
	}

	// Configure OpenAI client
	clientConfig := openai.DefaultConfig(currentModel.APIKey)
	clientConfig.BaseURL = currentModel.BaseURL
	client := openai.NewClientWithConfig(clientConfig)

	// Convert approved folders slice to map for faster lookup
	approvedFolders := make(map[string]bool)
	for _, folder := range cfg.ApprovedFolders {
		approvedFolders[folder] = true
	}

	agent := &types.Agent{
		Client:          client,
		Conversation:    []openai.ChatCompletionMessage{},
		Tools:           make(map[string]func(map[string]interface{}) (string, error)),
		Config:          cfg,
		ConfigPath:      configPath,
		ApprovedFolders: approvedFolders,
	}

	// Initialize tools
	toolManager := tools.NewManager(agent)
	toolManager.RegisterTools()

	// Load project context
	projectManager := project.NewManager(agent)
	projectManager.LoadProjectContext()

	return agent
}

// GetContextTokens returns the number of context tokens from the last API call
func GetContextTokens(a *types.Agent) int {
	if a.LastTokenUsage != nil {
		return a.LastTokenUsage.PromptTokens
	}
	return 0 // No API call made yet
}

// GetTotalTokensUsed returns the total tokens used in the session
func GetTotalTokensUsed(a *types.Agent) int {
	return a.TotalTokensUsed
}

// IsFolderApproved checks if a folder has been approved for access
func IsFolderApproved(a *types.Agent, folderPath string) bool {
	// Normalize the path
	absPath, err := filepath.Abs(folderPath)
	if err != nil {
		return false
	}

	// Check exact match first (for performance)
	if a.ApprovedFolders[absPath] {
		return true
	}

	// Check if this path is within any approved parent folder
	for approvedFolder := range a.ApprovedFolders {
		// Check if absPath is within approvedFolder
		rel, err := filepath.Rel(approvedFolder, absPath)
		if err != nil {
			continue
		}
		// If relative path doesn't start with "..", it's within the approved folder
		if !strings.HasPrefix(rel, "..") && rel != "." {
			return true
		}
	}

	return false
}

// RequestFolderPermission requests permission for folder access
func RequestFolderPermission(a *types.Agent, folderPath string) bool {
	// Normalize the path
	absPath, err := filepath.Abs(folderPath)
	if err != nil {
		fmt.Printf("Error resolving path: %v\n", err)
		return false
	}

	// Check if already approved (including parent folder check)
	if IsFolderApproved(a, folderPath) {
		return true
	}

	fmt.Printf("🔒 Request folder access: %s\n", absPath)
	fmt.Print("❓ Allow list_files and read_file operations in this folder and all subfolders? (Y/n): ")

	// Play notification sound
	go func() {
		cmd := exec.Command("osascript", "-e", `tell application "System Events" to get name of first application process whose frontmost is true`)
		output, err := cmd.Output()
		if err == nil {
			frontmostApp := strings.TrimSpace(string(output))
			isTerminalForeground := strings.Contains(frontmostApp, "Terminal") ||
				strings.Contains(frontmostApp, "iTerm") ||
				strings.Contains(frontmostApp, "Alacritty") ||
				strings.Contains(frontmostApp, "Kitty")

			if !isTerminalForeground {
				soundCmd := exec.Command("afplay", "/System/Library/Sounds/Glass.aiff")
				soundCmd.Run()
			}
		}
	}()

	fmt.Print("\a") // ASCII bell

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	response := strings.ToLower(strings.TrimSpace(scanner.Text()))

	if response == "" || response == "y" || response == "yes" {
		a.ApprovedFolders[absPath] = true

		// Add to config and save persistently
		a.Config.ApprovedFolders = append(a.Config.ApprovedFolders, absPath)
		if err := config.Save(a.ConfigPath, a.Config); err != nil {
			fmt.Printf("⚠️  Warning: Failed to save folder permission: %v\n", err)
		}

		fmt.Printf("✅ Folder access granted: %s (includes all subfolders)\n", absPath)
		return true
	}

	fmt.Printf("❌ Folder access denied\n")
	return false
}

// TrimContext trims conversation context when it gets too large
func TrimContext(a *types.Agent, messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	if len(messages) <= 3 {
		return messages // Keep at least a few messages
	}

	var trimmed []openai.ChatCompletionMessage

	// Always keep system messages (like AGENTS.md content)
	for _, msg := range messages {
		if msg.Role == openai.ChatMessageRoleSystem {
			trimmed = append(trimmed, msg)
		}
	}

	// Keep the last 4-6 messages (recent conversation)
	keepCount := 6
	if len(messages) > keepCount {
		recentMessages := messages[len(messages)-keepCount:]
		for _, msg := range recentMessages {
			if msg.Role != openai.ChatMessageRoleSystem { // Don't duplicate system messages
				trimmed = append(trimmed, msg)
			}
		}
	} else {
		// If we have few messages, keep all non-system ones
		for _, msg := range messages {
			if msg.Role != openai.ChatMessageRoleSystem {
				trimmed = append(trimmed, msg)
			}
		}
	}

	fmt.Printf("📉 Context trimmed: %d → %d messages\n", len(messages), len(trimmed))
	return trimmed
}

// CompactContext uses the LLM to summarize the conversation history
func CompactContext(a *types.Agent) error {
	if len(a.Conversation) <= 4 {
		return fmt.Errorf("conversation too short to compact")
	}

	fmt.Printf("\n🗜️  Compacting conversation context... please wait\n")

	// Identify what to keep and what to summarize
	// Keep system message
	var systemMessages []openai.ChatCompletionMessage
	// Keep last few messages to maintain immediate flow
	var recentMessages []openai.ChatCompletionMessage
	// Messages to summarize
	var toSummarize []openai.ChatCompletionMessage

	// Separate messages
	conversationLen := len(a.Conversation)
	keepRecent := 4

	for i, msg := range a.Conversation {
		if msg.Role == openai.ChatMessageRoleSystem {
			systemMessages = append(systemMessages, msg)
		} else if i >= conversationLen-keepRecent {
			recentMessages = append(recentMessages, msg)
		} else {
			// Don't summarize other system messages if they exist in middle (rare but possible)
			if msg.Role != openai.ChatMessageRoleSystem {
				toSummarize = append(toSummarize, msg)
			}
		}
	}

	if len(toSummarize) == 0 {
		return fmt.Errorf("no messages to compact")
	}

	// Create a summarization prompt
	summaryPrompt := "Please provide a detailed, technical summary of the above conversation. \n" +
		"Focus on preserving context relevant to the current coding goal. \n" +
		"You may discard code snippets or details that are no longer relevant to the current task. \n" +
		"Preserve key technical decisions and any active constraints or instructions."

	// Create a temporary conversation for the summarizer model
	summaryConv := append([]openai.ChatCompletionMessage{}, toSummarize...)
	summaryConv = append(summaryConv, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: summaryPrompt,
	})

	// Use the current model for summarization
	// Get current model configuration
	currentModel, exists := a.Config.Models[a.Config.CurrentModel]
	if !exists {
		// Fallback to finding any model
		for _, m := range a.Config.Models {
			currentModel = m
			break
		}
	}

	req := openai.ChatCompletionRequest{
		Model:     currentModel.Name,
		Messages:  summaryConv,
		MaxTokens: 4000, // Allow decent size for detailed summary
		Stream:    true,
	}

	// Execute request with streaming
	stream, err := a.Client.CreateChatCompletionStream(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to start summary stream: %v", err)
	}
	defer stream.Close()

	var summaryBuilder strings.Builder
	fmt.Print(types.ColorCyan) // Use cyan for summary generation

	for {
		response, err := stream.Recv()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return fmt.Errorf("error receiving summary stream: %v", err)
		}

		if len(response.Choices) > 0 {
			content := response.Choices[0].Delta.Content
			if content != "" {
				fmt.Print(content)
				summaryBuilder.WriteString(content)
			}
		}
	}

	fmt.Print(types.ColorReset) // Reset color
	fmt.Println()               // Newline after summary

	summaryContent := summaryBuilder.String()

	// Construct new conversation history
	var newHistory []openai.ChatCompletionMessage
	newHistory = append(newHistory, systemMessages...)
	newHistory = append(newHistory, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleSystem,
		Content: fmt.Sprintf("For context, here is a detailed summary of the previous conversation history:\n\n%s", summaryContent),
	})
	newHistory = append(newHistory, recentMessages...)

	// Update agent state
	oldLen := len(a.Conversation)
	a.Conversation = newHistory

	// Print clear success message
	// Calculate new token estimate
	newTokens := 0
	for _, msg := range newHistory {
		newTokens += len(msg.Content) / 4
	}

	// Print clear success message
	fmt.Printf("✅ Context compacted: %d → %d messages (~%d tokens)\n", oldLen, len(a.Conversation), newTokens)

	// Force update status display
	UpdateStatusDisplay(a)

	return nil
}

// UpdateStatusDisplay shows the current context size in the upper right
// UpdateStatusDisplay updates the fixed header at the top of the terminal
func UpdateStatusDisplay(a *types.Agent) {
	// Calculate token count
	tokens := GetContextTokens(a)

	// Format token string
	usageStr := fmt.Sprintf("%d", tokens)
	if tokens >= 1000 {
		usageStr = fmt.Sprintf("%.1fk", float64(tokens)/1000.0)
	}

	// Update window title using ANSI escape sequence: \033]0;TITLE\007
	// This shows status in the terminal tab/window title instead of a sticky header
	title := fmt.Sprintf("MCode | %s | %s tokens", a.Config.Models[a.Config.CurrentModel].Name, usageStr)
	fmt.Printf("\033]0;%s\007", title)
}

// Chat handles conversation with the AI model
func Chat(a *types.Agent, ctx context.Context, message string) error {
	toolManager := tools.NewManager(a)
	projectManager := project.NewManager(a)

	// Add system message if this is the first message
	if len(a.Conversation) == 0 {
		// Load AGENTS.md content for context
		agentsContent := projectManager.LoadAgentsMD()

		basePrompt := `You are a helpful coding agent. You have access to tools that allow you to:
- Read and write files
- Execute bash commands  
- List directory contents
- Search for code patterns

Use these tools to help the user with their coding tasks. Always be clear about what you're doing and why.`

		// Add AGENTS.md context if available
		systemPrompt := basePrompt
		if agentsContent != "" {
			systemPrompt += fmt.Sprintf("\n\n--- PROJECT CONTEXT (AGENTS.md) ---\n%s\n--- END PROJECT CONTEXT ---\n\nIMPORTANT: Pay special attention to any 'Permanent Instructions' in the project context above and follow them consistently.", agentsContent)
		}

		a.Conversation = append(a.Conversation, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: systemPrompt,
		})
	}

	a.Conversation = append(a.Conversation, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: message,
	})

	// Check if user requested compaction via command
	if strings.TrimSpace(message) == "/compact" {
		if err := CompactContext(a); err != nil {
			return err
		}
		// Don't continue to LLM after strict command
		return nil
	}

	for {
		// Update status display before generating
		UpdateStatusDisplay(a)

		// Get current model name
		currentModel, exists := a.Config.Models[a.Config.CurrentModel]
		if !exists {
			return fmt.Errorf("current model '%s' not found in configuration", a.Config.CurrentModel)
		}

		// Check if context is getting too large and trim if needed
		messages := a.Conversation

		// Auto-compaction check (approx 30k tokens)
		// We use the last known token usage plus estimation
		currentTokens := 0
		if a.LastTokenUsage != nil {
			currentTokens = a.LastTokenUsage.TotalTokens
		}

		// If we're over the limit, compact automatically
		if currentTokens > 30000 {
			spinnerDone := make(chan bool)
			spinnerCleared := make(chan bool)
			fmt.Printf("\n⚠️  Context limit reached (%d tokens). Auto-compacting...\n", currentTokens)
			go startSpinner(spinnerDone, spinnerCleared)

			err := CompactContext(a)

			// Stop spinner
			spinnerDone <- true
			<-spinnerCleared

			if err != nil {
				fmt.Printf("Warning: Auto-compaction failed: %v\n", err)
			} else {
				// Update messages reference after compaction
				messages = a.Conversation
			}
		}

		// Use a reasonable default max tokens without context window restrictions
		maxTokens := 40000

		req := openai.ChatCompletionRequest{
			Model:     currentModel.Name,
			Messages:  messages,
			Tools:     toolManager.GetToolDefinitions(),
			MaxTokens: maxTokens,
			Stream:    true, // Enable streaming
		}

		// Create streaming request
		stream, err := a.Client.CreateChatCompletionStream(ctx, req)
		if err != nil {
			// Check for context overflow or tool calling errors
			errStr := err.Error()
			if strings.Contains(errStr, "tool call") || strings.Contains(errStr, "Failed to parse") ||
				strings.Contains(errStr, "Unexpected end") || strings.Contains(errStr, "context") ||
				strings.Contains(errStr, "too long") || strings.Contains(errStr, "maximum") {

				fmt.Printf("\n⚠️  Request failed: %v\n", err)

				if strings.Contains(errStr, "context") || strings.Contains(errStr, "too long") ||
					strings.Contains(errStr, "maximum") || a.LastTokenUsage != nil && a.LastTokenUsage.PromptTokens > 6000 {
					fmt.Println("💡 This looks like a context window overflow. Trimming context and retrying...")
					messages = TrimContext(a, a.Conversation)
					// Update the conversation permanently to the trimmed version
					a.Conversation = messages
				} else {
					fmt.Printf("💡 This may be a tool calling format issue with model '%s'.\n", currentModel.Name)
					fmt.Println("   Try switching to a more compatible model with: /models")
				}

				// Try with trimmed context and no tools as fallback (non-streaming)
				reqFallback := openai.ChatCompletionRequest{
					Model:     currentModel.Name,
					Messages:  messages,
					MaxTokens: 2000, // Reduce max tokens to leave more room
				}

				fmt.Println("🔄 Retrying with simplified request...")
				resp, err := a.Client.CreateChatCompletion(ctx, reqFallback)
				if err != nil {
					return fmt.Errorf("error calling API (even after fallback): %v", err)
				}

				// Handle non-streaming fallback response
				a.LastTokenUsage = &resp.Usage
				a.TotalTokensUsed += resp.Usage.TotalTokens

				choice := resp.Choices[0]
				assistantMessage := openai.ChatCompletionMessage{
					Role:      openai.ChatMessageRoleAssistant,
					Content:   choice.Message.Content,
					ToolCalls: choice.Message.ToolCalls,
				}

				a.Conversation = append(a.Conversation, assistantMessage)

				if choice.Message.Content != "" {
					fmt.Print(choice.Message.Content)
				}

				// Check for tool calls in fallback response
				if len(choice.Message.ToolCalls) > 0 {
					if err := handleToolCalls(a, choice.Message.ToolCalls, toolManager); err != nil {
						return err
					}
				} else {
					break
				}
				continue
			} else {
				return fmt.Errorf("error calling API: %v", err)
			}
		}
		defer stream.Close()

		// Save cursor position removed - using calculated height for clearing

		// Process streaming response
		var fullContent strings.Builder
		var toolCalls []openai.ToolCall

		// Spinner for tool calls
		var spinnerShown bool
		var spinnerDone chan bool
		var spinnerCleared chan bool

		// For real-time rendering
		renderer, _ := markdown.NewNoMarginTermRenderer()
		tokenCount := 0

		// Save cursor position at the start of assistant response
		fmt.Print("\033[s")

		for {
			response, err := stream.Recv()
			if err != nil {
				if err.Error() == "EOF" {
					break
				}
				return fmt.Errorf("error receiving stream: %v", err)
			}

			if len(response.Choices) > 0 {
				delta := response.Choices[0].Delta

				// Stream content as it arrives
				if delta.Content != "" {
					fullContent.WriteString(delta.Content)
					tokenCount++

					// Only re-render on newlines, first token, or every 10 tokens to save scrollback
					if tokenCount == 1 || strings.Contains(delta.Content, "\n") || tokenCount%10 == 0 {
						// Restore cursor to the start of the response and clear everything below
						fmt.Print("\033[u\033[J")

						// Render full content
						rendered, err := renderer.Render(fullContent.String())
						if err == nil {
							fmt.Print(rendered)
						} else {
							fmt.Print(fullContent.String())
						}
					}
				}

				// Collect tool calls - show animated spinner when tool calls detected
				if len(delta.ToolCalls) > 0 {
					if !spinnerShown {
						// Final render of content before showing tools
						fmt.Print("\033[u\033[J")
						rendered, err := renderer.Render(fullContent.String())
						if err == nil {
							fmt.Print(rendered)
						} else {
							fmt.Print(fullContent.String())
						}

						// Move "save" point to after the content so tools don't overwrite it
						fmt.Print("\033[s")

						spinnerDone = make(chan bool)
						spinnerCleared = make(chan bool)
						go startSpinner(spinnerDone, spinnerCleared)
						spinnerShown = true
					}

					for _, toolCall := range delta.ToolCalls {
						// Handle the fact that Index might be nil or a pointer
						idx := 0
						if toolCall.Index != nil {
							idx = *toolCall.Index
						}

						// Extend slice if needed
						for len(toolCalls) <= idx {
							toolCalls = append(toolCalls, openai.ToolCall{
								Function: openai.FunctionCall{},
							})
						}

						// Accumulate tool call data
						if toolCall.ID != "" {
							toolCalls[idx].ID = toolCall.ID
						}
						if toolCall.Type != "" {
							toolCalls[idx].Type = toolCall.Type
						}
						if toolCall.Function.Name != "" {
							toolCalls[idx].Function.Name = toolCall.Function.Name
						}
						if toolCall.Function.Arguments != "" {
							toolCalls[idx].Function.Arguments += toolCall.Function.Arguments
						}
					}
				}
			}

			// Note: Usage information is typically only available in the final chunk
			// for streaming responses, but some implementations may provide it elsewhere
		}

		// Final re-render to ensure full content is shown
		if fullContent.Len() > 0 {
			// Restore cursor and clear
			fmt.Print("\033[u\033[J")
			rendered, err := renderer.Render(fullContent.String())
			if err == nil {
				fmt.Print(rendered)
			} else {
				fmt.Print(fullContent.String())
			}
		}

		// Stop spinner if it's still running
		if spinnerShown && spinnerDone != nil {
			spinnerDone <- true
			if spinnerCleared != nil {
				<-spinnerCleared
			}
			close(spinnerDone)
		}

		// Rough estimation: ~4 characters per token for response
		responseTokens := len(fullContent.String()) / 4
		if responseTokens < 1 {
			responseTokens = 1
		}

		// Estimate context tokens by looking at conversation history
		contextEstimate := 0
		for _, msg := range a.Conversation {
			contextEstimate += len(msg.Content) / 4
		}

		a.LastTokenUsage = &openai.Usage{
			PromptTokens:     contextEstimate,
			CompletionTokens: responseTokens,
			TotalTokens:      contextEstimate + responseTokens,
		}
		a.TotalTokensUsed += responseTokens

		// Create assistant message with accumulated content and tool calls
		assistantMessage := openai.ChatCompletionMessage{
			Role:      openai.ChatMessageRoleAssistant,
			Content:   fullContent.String(),
			ToolCalls: toolCalls,
		}

		a.Conversation = append(a.Conversation, assistantMessage)

		// Check if the response contains tool calls
		if len(toolCalls) > 0 {
			if err := handleToolCalls(a, toolCalls, toolManager); err != nil {
				return err
			}
		} else {
			break
		}
	}

	fmt.Println()

	// Show token usage info
	if a.LastTokenUsage != nil {
		contextTokens := a.LastTokenUsage.PromptTokens
		responseTokens := a.LastTokenUsage.CompletionTokens
		totalSessionTokens := a.TotalTokensUsed

		if contextTokens > 0 {
			fmt.Printf("%s[Context: %d tokens | Response: %d tokens | Session: %d tokens]%s\n",
				types.ColorBlue, contextTokens, responseTokens, totalSessionTokens, types.ColorReset)
		}

		// Update persistent status display
		UpdateStatusDisplay(a)
	}

	return nil
}

// handleToolCalls processes tool calls from the AI model
func handleToolCalls(a *types.Agent, toolCalls []openai.ToolCall, toolManager *tools.Manager) error {
	for _, toolCall := range toolCalls {
		var params map[string]interface{}
		if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &params); err != nil {
			fmt.Printf("Error parsing tool parameters: %v\n", err)
			continue
		}

		// Display condensed tool call format with useful parameters
		toolDisplay := fmt.Sprintf("🔧 %s%s%s", types.ColorCyan, toolCall.Function.Name, types.ColorReset)

		// Add relevant parameters for different tools
		switch toolCall.Function.Name {
		case "read_file", "preview_edit", "write_file":
			if path, exists := params["path"]; exists {
				absPath := fmt.Sprintf("%v", path)
				relPath, err := filepath.Rel(".", absPath)
				if err == nil {
					toolDisplay += fmt.Sprintf(" <%s>", relPath)
				} else {
					toolDisplay += fmt.Sprintf(" <%s>", absPath)
				}
			}
		case "edit_file":
			// Handle both old and new parameter names
			var path string
			if filePath, exists := params["filePath"]; exists {
				path = fmt.Sprintf("%v", filePath)
			} else if oldPath, exists := params["path"]; exists {
				path = fmt.Sprintf("%v", oldPath)
			}

			// Use relative path for display
			relPath, err := filepath.Rel(".", path)
			if err == nil {
				path = relPath
			}

			// Show the mode being used
			if oldString, hasOld := params["oldString"]; hasOld && oldString != "" {
				// Incremental mode - FAST!
				toolDisplay += fmt.Sprintf(" 🚀 %s [INCREMENTAL]", path)
			} else if _, hasNewString := params["newString"]; hasNewString {
				// New file mode - FAST!
				toolDisplay += fmt.Sprintf(" 🚀 %s [NEW FILE]", path)
			} else {
				// Full replacement mode - SLOW!
				toolDisplay += fmt.Sprintf(" 📄 %s [FULL REPLACEMENT]", path)
			}
		case "list_files":
			if path, exists := params["path"]; exists {
				absPath := fmt.Sprintf("%v", path)
				relPath, err := filepath.Rel(".", absPath)
				if err == nil {
					toolDisplay += fmt.Sprintf(" <%s>", relPath)
				} else {
					toolDisplay += fmt.Sprintf(" <%s>", absPath)
				}
			}
		case "bash_command":
			if command, exists := params["command"]; exists {
				cmdStr := fmt.Sprintf("%v", command)
				toolDisplay += fmt.Sprintf(" `%s`", cmdStr)
			}
		case "search_code":
			if pattern, exists := params["pattern"]; exists {
				toolDisplay += fmt.Sprintf(" \"%v\"", pattern)
			}
		}

		fmt.Printf("\n%s\n", toolDisplay)

		// Check if this looks like a long-running command
		isLongRunning := false
		if toolCall.Function.Name == "bash_command" {
			if cmdParam, exists := params["command"]; exists {
				if cmdStr, ok := cmdParam.(string); ok {
					isLongRunning = tools.IsLongRunningCommand(cmdStr)
				}
			}
		}

		// Check if this is a folder operation that needs permission
		shouldAutoExecute := false
		if toolCall.Function.Name == "list_files" || toolCall.Function.Name == "read_file" || toolCall.Function.Name == "preview_edit" {
			var folderPath string
			if pathParam, exists := params["path"]; exists {
				if pathStr, ok := pathParam.(string); ok {
					if toolCall.Function.Name == "read_file" || toolCall.Function.Name == "preview_edit" {
						// For read_file and preview_edit, get the directory of the file
						folderPath = filepath.Dir(pathStr)
					} else {
						// For list_files, use the path directly
						folderPath = pathStr
					}

					// Check if folder is already approved
					if IsFolderApproved(a, folderPath) {
						shouldAutoExecute = true
					} else {
						// Request permission for this folder
						if !RequestFolderPermission(a, folderPath) {
							// Add permission denied result and continue to next tool
							a.Conversation = append(a.Conversation, openai.ChatCompletionMessage{
								Role:       openai.ChatMessageRoleTool,
								Content:    "Permission denied for folder access",
								ToolCallID: toolCall.ID,
							})
							continue
						}
						shouldAutoExecute = true
					}
				}
			}
		}

		var response string

		if shouldAutoExecute {
			// Auto-execute approved folder operations
			response = "y"
		} else {
			// Ask for confirmation for other operations
			prompt := "\n❓ Execute this tool? (Y/n/s to skip/i to interrupt): "
			if isLongRunning {
				fmt.Printf("%s⚠️  This looks like a long-running command!%s\n", types.ColorYellow, types.ColorReset)
				prompt = "\n❓ Execute this tool? (Y/n/s to skip/i to interrupt/b for background): "
			}

			// Play notification sound
			playNotificationSound()

			fmt.Print(prompt)

			inputScanner := bufio.NewScanner(os.Stdin)
			inputScanner.Scan()
			response = strings.ToLower(strings.TrimSpace(inputScanner.Text()))
		}

		// Execute tool based on response
		result, shouldContinue := executeToolBasedOnResponse(a, response, toolCall, params, isLongRunning, toolManager)

		// Display tool output to user
		if result != "" && (response == "" || response == "y" || response == "yes" || response == "b" || response == "background") {
			fmt.Println() // Add blank line after tool call
			if toolCall.Function.Name == "edit_file" {
				// Stream diffs for nice effect
				streamOutput(result)
			} else if toolCall.Function.Name != "read_file" && toolCall.Function.Name != "list_files" && toolCall.Function.Name != "bash_command" {
				// Generic output display (skip read_file, list_files, and bash_command to avoid clutter/duplication)
				fmt.Printf("%s> Tool Output:%s\n", types.ColorCyan, types.ColorReset)
				if len(result) > 2000 {
					fmt.Println(result[:2000] + "... (truncated)")
				} else {
					fmt.Println(result)
				}
			}
		}

		// Add tool result to conversation
		a.Conversation = append(a.Conversation, openai.ChatCompletionMessage{
			Role:       openai.ChatMessageRoleTool,
			Content:    result,
			ToolCallID: toolCall.ID,
		})

		if !shouldContinue {
			break
		}
	}
	return nil
}

// playNotificationSound plays a notification sound
func playNotificationSound() {
	go func() {
		// Check if terminal is in foreground on macOS
		cmd := exec.Command("osascript", "-e", `tell application "System Events" to get name of first application process whose frontmost is true`)
		output, err := cmd.Output()
		if err == nil {
			frontmostApp := strings.TrimSpace(string(output))
			isTerminalForeground := strings.Contains(frontmostApp, "Terminal") ||
				strings.Contains(frontmostApp, "iTerm") ||
				strings.Contains(frontmostApp, "Alacritty") ||
				strings.Contains(frontmostApp, "Kitty")

			if !isTerminalForeground {
				soundCmd := exec.Command("afplay", "/System/Library/Sounds/Glass.aiff")
				soundCmd.Run()
			}
		}
	}()

	// Always show ASCII bell (for taskbar notification)
	fmt.Print("\a")
}

// executeToolBasedOnResponse executes a tool based on user response
func executeToolBasedOnResponse(a *types.Agent, response string, toolCall openai.ToolCall, params map[string]interface{}, isLongRunning bool, toolManager *tools.Manager) (string, bool) {
	var result string

	if response == "" || response == "y" || response == "yes" {
		// Execute the tool
		toolFunc, exists := a.Tools[toolCall.Function.Name]
		if !exists {
			fmt.Printf("Unknown tool: %s\n", toolCall.Function.Name)
			result = "Error: Unknown tool"
		} else {
			var err error
			result, err = toolFunc(params)
			if err != nil {
				result = fmt.Sprintf("Error: %v", err)
			}
		}
	} else if response == "s" || response == "skip" {
		result = "Tool execution skipped by user"
		fmt.Printf("⏭️  Tool execution skipped\n")
	} else if response == "b" || response == "background" {
		if isLongRunning {
			fmt.Printf("🚀 Starting command in background...\n")
			result = toolManager.BashCommandBackground(params)
			fmt.Printf("✅ Command started in background\n")
		} else {
			result = "Background execution only available for long-running commands"
			fmt.Printf("⚠️  Background execution only available for long-running commands\n")
		}
	} else if response == "i" || response == "interrupt" {
		fmt.Print("\n💬 What would you like me to do instead? ")
		interruptScanner := bufio.NewScanner(os.Stdin)
		interruptScanner.Scan()
		userInstruction := strings.TrimSpace(interruptScanner.Text())
		if userInstruction != "" {
			fmt.Printf("🔄 Interrupting with new instruction: %s\n", userInstruction)
			result = fmt.Sprintf("Tool execution interrupted by user. New instruction: %s", userInstruction)

			// Add the interrupt result to conversation
			a.Conversation = append(a.Conversation, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    result,
				ToolCallID: toolCall.ID,
			})

			// Add the new user message and continue the conversation
			a.Conversation = append(a.Conversation, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleUser,
				Content: userInstruction,
			})

			// Return early to skip adding the result again
			return result, false
		} else {
			result = "Tool execution interrupted but no alternative instruction provided"
			fmt.Printf("⚠️  No alternative instruction provided\n")
		}
	} else {
		result = "Tool execution denied by user"
		fmt.Printf("❌ Tool execution denied\n")
	}

	return result, true
}

// startSpinner shows an animated spinner until stopped
func startSpinner(done chan bool, cleared chan bool) {
	spinnerChars := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	i := 0
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			// Clear the spinner completely
			fmt.Print("\r\033[K") // Clear current line entirely
			os.Stdout.Sync()
			if cleared != nil {
				cleared <- true // Signal that spinner is cleared
			}
			return
		case <-ticker.C:
			fmt.Printf("\r%s ", spinnerChars[i%len(spinnerChars)])
			os.Stdout.Sync()
			i++
		}
	}
}

// streamOutput simulates streaming output for content
func streamOutput(content string) {
	// Stream the content in small chunks
	chunkSize := 10 // Larger chunk size for faster display
	for i := 0; i < len(content); i += chunkSize {
		end := i + chunkSize
		if end > len(content) {
			end = len(content)
		}

		chunk := content[i:end]
		fmt.Print(chunk)
		os.Stdout.Sync() // Force immediate flush after each chunk

		// Very small delay
		time.Sleep(1 * time.Millisecond)
	}
	fmt.Println() // Ensure newline at end
}
