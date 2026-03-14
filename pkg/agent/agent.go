package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"coding-agent/pkg/config"
	"coding-agent/pkg/llm"
	"coding-agent/pkg/markdown"
	"coding-agent/pkg/project"
	"coding-agent/pkg/tokens"
	"coding-agent/pkg/tools"
	"coding-agent/pkg/types"
	"coding-agent/pkg/ui"

	"github.com/sashabaranov/go-openai"
	"golang.org/x/term"
)

// New creates a new agent instance
func New() *types.Agent {
	configPath := config.GetConfigPath()
	cfg, err := config.LoadOrCreateConfig(configPath)
	if err != nil {
		ui.PrintfSafe("Warning: Failed to load config, using defaults: %v\n", err)
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
		ui.PrintfSafe("Warning: Current model '%s' not found, using first available model\n", cfg.CurrentModel)
		for _, model := range cfg.Models {
			currentModel = model
			break
		}
	}

	provider := CreateProviderForModel(currentModel)

	// Convert approved folders slice to map for faster lookup
	approvedFolders := make(map[string]bool)
	for _, folder := range cfg.ApprovedFolders {
		approvedFolders[folder] = true
	}

	agent := &types.Agent{
		LLM:             provider,
		Conversation:    []openai.ChatCompletionMessage{},
		Tools:           make(map[string]func(map[string]interface{}) (string, error)),
		Config:          cfg,
		ConfigPath:      configPath,
		ApprovedFolders: approvedFolders,
	}

	// Initialize tools
	toolManager := tools.NewManager(agent)
	toolManager.RegisterTools()

	// Initialize conversation with system prompt
	InitConversation(agent)

	return agent
}

// CreateProviderForModel creates the appropriate LLM provider for a model
func CreateProviderForModel(model types.Model) llm.Provider {
	if strings.Contains(strings.ToLower(model.Name), "gemini") {
		return llm.NewGeminiCompatibleProvider(model.APIKey, model.BaseURL)
	}

	// Configure standard OpenAI client
	clientConfig := openai.DefaultConfig(model.APIKey)
	clientConfig.BaseURL = model.BaseURL
	client := openai.NewClientWithConfig(clientConfig)
	return llm.NewOpenAIProvider(client)
}

func normalizeToolName(name string) string {
	if idx := strings.LastIndex(name, ":"); idx != -1 {
		return name[idx+1:]
	}
	return name
}

// GetContextTokens returns the number of context tokens using tiktoken
func GetContextTokens(a *types.Agent) int {
	// If we have actual usage from the last API call, use it
	if a.LastTokenUsage != nil && a.LastTokenUsage.PromptTokens > 0 {
		return a.LastTokenUsage.PromptTokens
	}

	// Otherwise estimate using tiktoken
	modelName := a.Config.CurrentModel
	if model, ok := a.Config.Models[modelName]; ok {
		modelName = model.Name
	}

	return tokens.CountMessagesTokens(modelName, a.Conversation)
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
		ui.PrintfSafe("Error resolving path: %v\n", err)
		return false
	}

	// Check if already approved (including parent folder check)
	if IsFolderApproved(a, folderPath) {
		return true
	}

	ui.PrintfSafe("🔒 Request folder access: %s\n", absPath)
	ui.PrintSafe("❓ Allow list_files and read_file operations in this folder and all subfolders? (Y/n): ")

	// Play notification sound
	playNotificationSound()

	ui.PauseInterruptMonitor()
	response := ui.ReadConfirmation()
	ui.ResumeInterruptMonitor()

	if response == "\r" || response == "\n" {
		response = ""
	}

	// Echo the choice
	if response == "" {
		ui.PrintlnSafe("y")
	} else {
		ui.PrintlnSafe(response)
	}

	if response == "" || response == "y" || response == "yes" {
		a.ApprovedFolders[absPath] = true

		// Add to config and save persistently
		a.Config.ApprovedFolders = append(a.Config.ApprovedFolders, absPath)
		if err := config.Save(a.ConfigPath, a.Config); err != nil {
			ui.PrintfSafe("⚠️  Warning: Failed to save folder permission: %v\n", err)
		}

		ui.PrintfSafe("✅ Folder access granted: %s (includes all subfolders)\n", absPath)
		return true
	}

	ui.PrintfSafe("❌ Folder access denied\n")
	return false
}

// TrimContext reduces conversation history to stay within a token budget.
// It prioritizes keeping system messages and the most recent interactions.
func TrimContext(a *types.Agent, messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	if len(messages) <= 3 {
		return messages
	}

	modelName := a.Config.CurrentModel
	currentModel, ok := a.Config.Models[modelName]
	if !ok {
		// Fallback if model not found
		currentModel = types.Model{Name: modelName, MaxTokens: 8000}
	}

	// Budget Calculation:
	// We want to reserve space for:
	// 1. The System Prompt (~20% or at least 2k)
	// 2. The Current Task/User Message (~20%)
	// 3. The Expected Completion (~20% or MaxCompletionTokens)
	// 4. The History (The remainder, but capped for speed)

	totalLimit := currentModel.MaxTokens
	if totalLimit <= 0 {
		totalLimit = 8000 // Default fallback
	}

	// Budget is 50% of total context.
	// This allows history to scale with the model (e.g. 64k history for 128k model)
	// while still leaving 50% for system prompts, current code, and the output.
	tokenBudget := int(float64(totalLimit) * 0.5)

	// Sane upper bound for memory safety (e.g. 100k tokens of history is huge)
	if tokenBudget > 100000 {
		tokenBudget = 100000
	}

	var systemMessages []openai.ChatCompletionMessage
	var otherMessages []openai.ChatCompletionMessage

	for _, msg := range messages {
		if msg.Role == openai.ChatMessageRoleSystem {
			systemMessages = append(systemMessages, msg)
		} else {
			otherMessages = append(otherMessages, msg)
		}
	}

	// Keep messages from the end until we hit the budget
	var trimmed []openai.ChatCompletionMessage
	currentTokens := 0

	for i := len(otherMessages) - 1; i >= 0; i-- {
		msgTokens := tokens.CountMessagesTokens(currentModel.Name, []openai.ChatCompletionMessage{otherMessages[i]})
		// Always keep at least 2 recent interactions (4 messages: 2 user, 2 assistant/tool)
		if currentTokens+msgTokens > tokenBudget && len(trimmed) >= 4 {
			break
		}
		trimmed = append([]openai.ChatCompletionMessage{otherMessages[i]}, trimmed...)
		currentTokens += msgTokens
	}

	ui.PrintfSafe("📉 Context trimmed: %d → %d messages (%d tokens history)\n", len(messages), len(systemMessages)+len(trimmed), currentTokens)
	return append(systemMessages, trimmed...)
}

// CompactContext uses the LLM to summarize the conversation history
func CompactContext(a *types.Agent) error {
	if len(a.Conversation) <= 4 {
		return fmt.Errorf("conversation too short to compact")
	}

	ui.PrintfSafe("\n🗜️  Compacting conversation context... please wait\n")

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
	spinner := ui.NewSpinner("Compacting context...")
	spinner.Start()

	streamChan, err := a.LLM.CreateStream(context.Background(), req)
	if err != nil {
		spinner.Stop()
		return fmt.Errorf("failed to start summary stream: %v", err)
	}

	var summaryBuilder strings.Builder
	ui.PrintSafe(types.ColorCyan) // Use cyan for summary generation

	for response := range streamChan {
		if response.Error != nil {
			spinner.Stop()
			return fmt.Errorf("error receiving summary stream: %v", response.Error)
		}

		if response.Content != "" {
			spinner.Stop()
			ui.PrintSafe(response.Content)
			summaryBuilder.WriteString(response.Content)
		}
	}

	ui.PrintSafe(types.ColorReset) // Reset color
	ui.PrintlnSafe()               // Newline after summary

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

	// Calculate new token estimate using tiktoken
	newTokens := tokens.CountMessagesTokens(currentModel.Name, newHistory)

	// Print clear success message
	ui.PrintfSafe("✅ Context compacted: %d → %d messages (%d tokens)\n", oldLen, len(a.Conversation), newTokens)

	// Force update status display
	UpdateStatusDisplay(a)

	return nil
}

// UpdateStatusDisplay updates the fixed header at the top of the terminal
func UpdateStatusDisplay(a *types.Agent) {
	tokens := GetContextTokens(a)
	modelName := "unknown"
	if model, exists := a.Config.Models[a.Config.CurrentModel]; exists {
		modelName = model.Name
	}
	ui.UpdateStatusDisplay(modelName, tokens)
}

// InitConversation initializes the conversation with system prompts
func InitConversation(a *types.Agent) {
	projectManager := project.NewManager(a)

	// Load AGENTS.md content for context
	agentsContent := projectManager.LoadAgentsMD()

	basePrompt := `You are a helpful coding agent. You have access to tools to help the user with their coding tasks. 

THOUGHT PROCESS:
Before calling any tool, briefly state your reasoning and plan. This helps you select the most efficient tool for the task.

CORE STRATEGY & EFFICIENCY (LOCAL LLM OPTIMIZED):
1.  **Understand Before Reading:** Use 'list_files' to map the project and 'search_code' to find relevant symbols.
2.  **Read Smartly:** Use 'read_file'. If a file is large (> 300 lines), it will automatically truncate. Use 'offset' and 'limit' to read specific chunks. NEVER read massive files entirely.
3.  **Edit Surgically:** Prefer 'edit_file' (incremental edits) over 'write_file' (full replacement).
4.  **Stay High-Signal:** Your primary priority is preventing context inflation. Keep tool outputs and conversation history focused on the immediate task.

CORRECT WORKFLOW EXAMPLE:
User: "Fix the bug in main.go"
Assistant: "I'll start by reading main.go."
[Tool Call: read_file(path="main.go")]
Assistant: "main.go is 1200 lines (only 300 read). I'll now read the next 300 lines to understand more."
[Tool Call: read_file(path="main.go", offset=300, limit=300)]

Follow these principles to stay within the context window and maintain high performance. Always be clear about your intent and rationale.`

	// Add AGENTS.md context if available
	systemPrompt := basePrompt
	if agentsContent != "" {
		systemPrompt += fmt.Sprintf("\n\n--- PROJECT CONTEXT (AGENTS.md) ---\n%s\n--- END PROJECT CONTEXT ---\n\nIMPORTANT: Pay special attention to any 'Permanent Instructions' in the project context above and follow them consistently.", agentsContent)
	}

	// Always clear and start with this system prompt as message 0
	a.Conversation = []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: systemPrompt,
		},
	}
}

// Chat handles conversation with the AI model
func Chat(a *types.Agent, ctx context.Context, message string) error {
	toolManager := tools.NewManager(a)
	toolManager.RegisterTools()

	// Add system message if this is the first message or it's missing
	if len(a.Conversation) == 0 {
		InitConversation(a)
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

	renderer, _ := markdown.NewNoMarginTermRenderer()

	// Start interrupt monitor for the entire chat session (generation + tool execution)
	sessionCtx, cancelSession := ui.StartInterruptMonitor(ctx)
	defer cancelSession()

	for {
		// Check for session cancellation
		if sessionCtx.Err() != nil {
			return ui.ErrInterrupted
		}

		// Update status display before generating
		UpdateStatusDisplay(a)

		// Get current model name
		currentModel, exists := a.Config.Models[a.Config.CurrentModel]
		if !exists {
			return fmt.Errorf("current model '%s' not found in configuration", a.Config.CurrentModel)
		}

		// Check if context is getting too large and trim if needed
		messages := a.Conversation

		// Auto-compaction check
		currentTokens := 0
		if a.LastTokenUsage != nil {
			currentTokens = a.LastTokenUsage.TotalTokens
		}

		threshold := 30000
		if currentModel.MaxTokens > 0 {
			threshold = int(float64(currentModel.MaxTokens) * 0.8)
		}

		// Start the "thinking" spinner early - it will stay until content arrives or turn ends
		spinner := ui.NewSpinner("")
		spinner.Start()

		if currentTokens > threshold {
			// Temporarily stop thinking spinner to show compaction progress
			spinner.Stop()
			ui.PrintfSafe("\n⚠️  Context threshold reached (%d/%d tokens). Auto-compacting...\n", currentTokens, currentModel.MaxTokens)
			err := CompactContext(a)

			if err != nil {
				ui.PrintfSafe("Warning: Auto-compaction failed: %v\n", err)
			} else {
				messages = a.Conversation
			}
			// Restart thinking spinner
			spinner.Start()
		}

		// Use a reasonable max tokens for completion
		maxTokens := 8192
		if currentModel.MaxCompletionTokens > 0 {
			maxTokens = currentModel.MaxCompletionTokens
		}

		if currentModel.MaxTokens > 0 {
			remainingTokens := currentModel.MaxTokens - currentTokens
			if remainingTokens < maxTokens {
				maxTokens = remainingTokens
			}
			if maxTokens < 500 {
				maxTokens = 500
			}
		}

		req := openai.ChatCompletionRequest{
			Model:     currentModel.Name,
			Messages:  messages,
			Tools:     toolManager.GetToolDefinitions(),
			MaxTokens: maxTokens,
			Stream:    true,
		}

		// Create streaming request
		streamChan, err := a.LLM.CreateStream(sessionCtx, req)
		if err != nil {
			if sessionCtx.Err() != nil {
				return ui.ErrInterrupted
			}
			spinner.Stop()
			errStr := err.Error()
			if strings.Contains(errStr, "tool call") || strings.Contains(errStr, "Failed to parse") ||
				strings.Contains(errStr, "Unexpected end") || strings.Contains(errStr, "context") ||
				strings.Contains(errStr, "too long") || strings.Contains(errStr, "maximum") {

				ui.PrintfSafe("\n⚠️  Request failed: %v\n", err)

				if strings.Contains(errStr, "context") || strings.Contains(errStr, "too long") ||
					strings.Contains(errStr, "maximum") || a.LastTokenUsage != nil && a.LastTokenUsage.PromptTokens > 6000 {

					re := regexp.MustCompile(`context length is (\d+)`)
					matches := re.FindStringSubmatch(errStr)
					if len(matches) > 1 {
						if limit, err := strconv.Atoi(matches[1]); err == nil {
							ui.PrintfSafe("💡 Detected model context limit: %d tokens\n", limit)
							currentModel.MaxTokens = limit
							if model, ok := a.Config.Models[a.Config.CurrentModel]; ok {
								model.MaxTokens = limit
								a.Config.Models[a.Config.CurrentModel] = model
								config.Save(a.ConfigPath, a.Config)
							}
						}
					}

					ui.PrintlnSafe("💡 Context window overflow. Auto-compacting and retrying...")
					if err := CompactContext(a); err != nil {
						ui.PrintlnSafe("⚠️  Compaction failed, falling back to simple trimming...")
						messages = TrimContext(a, a.Conversation)
						a.Conversation = messages
					} else {
						messages = a.Conversation
					}
				}

				reqFallback := openai.ChatCompletionRequest{
					Model:     currentModel.Name,
					Messages:  messages,
					MaxTokens: 2000,
				}

				ui.PrintlnSafe("🔄 Retrying with simplified request...")
				spinner.Start()

				// Monitor interrupt during fallback
				resp, err := a.LLM.CreateCompletion(sessionCtx, reqFallback)
				spinner.Stop()

				if err != nil {
					if sessionCtx.Err() != nil {
						return ui.ErrInterrupted
					}
					return fmt.Errorf("error calling API (even after fallback): %v", err)
				}

				a.LastTokenUsage = resp.Usage
				a.TotalTokensUsed += resp.Usage.TotalTokens

				assistantMessage := openai.ChatCompletionMessage{
					Role:      openai.ChatMessageRoleAssistant,
					Content:   resp.Content,
					ToolCalls: resp.ToolCalls,
				}
				a.Conversation = append(a.Conversation, assistantMessage)

				if resp.Content != "" {
					ui.PrintSafe(resp.Content)
				}

				if len(resp.ToolCalls) > 0 {
					tokenStats := fmt.Sprintf("(%d ctx | %d gen)", a.LastTokenUsage.PromptTokens, a.LastTokenUsage.CompletionTokens)
					if err := handleToolCalls(sessionCtx, a, resp.ToolCalls, toolManager, tokenStats, resp.FinishReason == "length"); err != nil {
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

		var previousLines []string
		getTermHeight := func() int {
			_, height, err := term.GetSize(int(os.Stdout.Fd()))
			if err != nil {
				return 24
			}
			return height
		}

		var fullContent strings.Builder
		var toolCalls []openai.ToolCall

		genStartTime := time.Now()
		contextTokens := GetContextTokens(a)

		updateStats := func(usage *openai.Usage) {
			genTokens := 0
			if usage != nil {
				genTokens = usage.CompletionTokens
			} else {
				genLen := fullContent.Len()
				for _, tc := range toolCalls {
					genLen += len(tc.Function.Name) + len(tc.Function.Arguments)
				}
				genTokens = genLen / 4
			}

			duration := time.Since(genStartTime).Seconds()
			speed := 0.0
			if duration > 0 {
				speed = float64(genTokens) / duration
			}

			// Update window title via spinner
			modelName := a.Config.Models[a.Config.CurrentModel].Name
			title := fmt.Sprintf("MCode | %s | %d ctx | %d gen (%.1f t/s)", modelName, contextTokens, genTokens, speed)
			spinner.SetTitle(title)

			// Update spinner if active
			msg := ""
			if genTokens == 0 {
				msg = "Thinking..."
			} else {
				msg = fmt.Sprintf("%d tokens (%.1f t/s)", genTokens, speed)
			}
			spinner.UpdateMessage(msg)
		}

		var finishReason string

		for response := range streamChan {
			if response.Error != nil {
				spinner.Stop()
				if sessionCtx.Err() != nil {
					return ui.ErrInterrupted
				}
				return fmt.Errorf("error receiving stream: %v", response.Error)
			}

			updateStats(response.Usage)

			if response.FinishReason != "" {
				finishReason = response.FinishReason
			}

			// Ensure spinner is running for any generation signal
			if response.Content != "" || len(response.ToolCalls) > 0 {
				spinner.Start()
			}

			if response.Content != "" {
				fullContent.WriteString(response.Content)
				updateStats(response.Usage)

				rendered, err := renderer.Render(fullContent.String())
				if err != nil {
					spinner.Stop()
					ui.PrintSafe(response.Content)
					spinner.Start()
					continue
				}

				currentLines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
				diffIdx := 0
				for diffIdx < len(previousLines) && diffIdx < len(currentLines) {
					if previousLines[diffIdx] != currentLines[diffIdx] {
						break
					}
					diffIdx++
				}

				linesToBacktrack := len(previousLines) - diffIdx
				termHeight := getTermHeight()
				if linesToBacktrack >= termHeight {
					linesToBacktrack = termHeight - 1
					diffIdx = len(previousLines) - linesToBacktrack
					if diffIdx < 0 {
						diffIdx = 0
					}
				}

				if linesToBacktrack > 0 || diffIdx < len(currentLines) {
					spinner.Stop()
					if linesToBacktrack > 0 {
						ui.PrintfSafe("\033[%dA", linesToBacktrack)
					}
					ui.PrintSafe("\r\033[J")
					for i := diffIdx; i < len(currentLines); i++ {
						ui.PrintlnSafe(currentLines[i])
					}
					previousLines = currentLines
					spinner.Start()
				}
			}

			if len(response.ToolCalls) > 0 {
				for _, tc := range response.ToolCalls {
					idx := 0
					if tc.Index != nil {
						idx = *tc.Index
					}
					for len(toolCalls) <= idx {
						toolCalls = append(toolCalls, openai.ToolCall{
							Type: openai.ToolTypeFunction,
						})
					}
					if tc.ID != "" {
						toolCalls[idx].ID = tc.ID
					}
					if tc.Function.Name != "" {
						toolCalls[idx].Function.Name = tc.Function.Name
					}
					if tc.Function.Arguments != "" {
						toolCalls[idx].Function.Arguments += tc.Function.Arguments
					}
					updateStats(response.Usage)
				}
			}
		}

		// Final check for tool call validity - ensure every tool call has an ID and type
		validToolCalls := make([]openai.ToolCall, 0)
		for _, tc := range toolCalls {
			if tc.Function.Name != "" {
				if tc.Type == "" {
					tc.Type = openai.ToolTypeFunction
				}
				validToolCalls = append(validToolCalls, tc)
			}
		}
		toolCalls = validToolCalls

		// Accurate token counting using tiktoken
		responseTokens := tokens.CountTokens(currentModel.Name, fullContent.String())
		for _, tc := range toolCalls {
			responseTokens += tokens.CountTokens(currentModel.Name, tc.Function.Name)
			responseTokens += tokens.CountTokens(currentModel.Name, tc.Function.Arguments)
		}

		if responseTokens < 1 {
			responseTokens = 1
		}

		// Use accurate context counting
		contextEstimate := tokens.CountMessagesTokens(currentModel.Name, a.Conversation)

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

		// CRITICAL FIX: Ensure content is never truly undefined/nil for providers that are picky.
		// If content is empty and there are no tool calls, some providers reject it.
		if assistantMessage.Content == "" && len(assistantMessage.ToolCalls) == 0 {
			assistantMessage.Content = " " // Use a single space as a fallback
		}

		a.Conversation = append(a.Conversation, assistantMessage)

		// Ensure spinner is stopped before we might enter tool call handling
		// or before we break the loop
		spinner.Stop()

		if finishReason == "length" {
			ui.PrintfSafe("\n⚠️  Warning: Generation truncated due to length limit!\n")
		}

		// Check if the response contains tool calls
		if len(toolCalls) > 0 {
			tokenStats := ""
			if a.LastTokenUsage != nil {
				tokenStats = fmt.Sprintf("(%d ctx | %d gen)", a.LastTokenUsage.PromptTokens, a.LastTokenUsage.CompletionTokens)
			}
			if err := handleToolCalls(sessionCtx, a, toolCalls, toolManager, tokenStats, finishReason == "length"); err != nil {
				return err
			}
		} else {
			break
		}
	}

	ui.PrintlnSafe()

	// Show token usage info
	if a.LastTokenUsage != nil {
		contextTokens := a.LastTokenUsage.PromptTokens
		responseTokens := a.LastTokenUsage.CompletionTokens
		totalSessionTokens := a.TotalTokensUsed

		if contextTokens > 0 {
			ui.PrintfSafe("%s[Context: %d tokens | Response: %d tokens | Session: %d tokens]%s\n",
				types.ColorBlue, contextTokens, responseTokens, totalSessionTokens, types.ColorReset)
		}

		// Update persistent status display
		UpdateStatusDisplay(a)
	}

	return nil
}

// TruncateForLLM truncates string content to a safe length for LLM context.
// Local LLMs work much better with shorter, high-signal contexts.
func TruncateForLLM(a *types.Agent, s string, maxChars int) string {
	limit := 8000 // Default fallback

	// If model config is available, allow tool output to take up to ~50% of context
	if model, ok := a.Config.Models[a.Config.CurrentModel]; ok && model.MaxTokens > 0 {
		// Roughly 4 chars per token
		limit = int(float64(model.MaxTokens) * 0.5 * 4)
	}

	if maxChars > 0 && maxChars < limit {
		limit = maxChars
	}

	// Sane upper bound for character count (e.g. 100k chars is plenty for one tool output)
	if limit > 100000 {
		limit = 100000
	}

	if len(s) <= limit {
		return s
	}
	return s[:limit] + fmt.Sprintf("\n\n[... Output truncated to %d characters for context efficiency. Use pagination or search if more detail is needed. ...]", limit)
}

// handleToolCalls processes tool calls from the AI model
func handleToolCalls(ctx context.Context, a *types.Agent, toolCalls []openai.ToolCall, toolManager *tools.Manager, tokenStats string, truncated bool) error {
	for _, toolCall := range toolCalls {
		// Check for context cancellation
		if ctx.Err() != nil {
			return ui.ErrInterrupted
		}

		// Start a spinner while we process this tool call (parse, check permissions, get preview)
		msg := fmt.Sprintf("Processing %s", toolCall.Function.Name)
		if tokenStats != "" {
			msg += " " + tokenStats
		}
		spinner := ui.NewSpinner(msg)
		spinner.Start()

		var params map[string]interface{}
		if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &params); err != nil {
			spinner.Stop()

			errResult := ""
			if truncated {
				errResult = fmt.Sprintf("Error parsing tool parameters (Generation Truncated): %v", err)
			} else {
				errResult = fmt.Sprintf("Error parsing tool parameters: %v", err)
			}

			if errResult == "" {
				errResult = "Error parsing tool parameters"
			}

			ui.PrintlnSafe(errResult)
			if len(toolCall.Function.Arguments) > 0 {
				argLen := len(toolCall.Function.Arguments)
				previewLen := 300
				if argLen < previewLen {
					previewLen = argLen
				}
				ui.PrintfSafe("Partial JSON (len=%d): %s...\n", argLen, toolCall.Function.Arguments[:previewLen])
			}

			// Always add a tool response to avoid breaking the conversation
			a.Conversation = append(a.Conversation, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    errResult,
				ToolCallID: toolCall.ID,
			})
			continue
		}

		toolName := normalizeToolName(toolCall.Function.Name)

		// Prepare tool display header
		toolDisplay := fmt.Sprintf("🔧 %s%s%s", types.ColorCyan, toolName, types.ColorReset)
		displayInfo := toolManager.GetDisplayInfo(toolName, params)
		if displayInfo != "" {
			toolDisplay += displayInfo
		}

		// Check for long-running and permissions
		isLongRunning := false
		if toolName == "bash_command" {
			if cmdParam, exists := params["command"]; exists {
				if cmdStr, ok := cmdParam.(string); ok {
					isLongRunning = tools.IsLongRunningCommand(cmdStr)
				}
			}
		}

		shouldAutoExecute := false
		var permissionError string
		if toolName == "list_files" || toolName == "read_file" || toolName == "preview_edit" || toolName == "search_code" {
			var folderPath string
			if pathParam, exists := params["path"]; exists {
				if pathStr, ok := pathParam.(string); ok {
					if toolName == "read_file" || toolName == "preview_edit" {
						folderPath = filepath.Dir(pathStr)
					} else {
						folderPath = pathStr
					}
				}
			} else if dirParam, exists := params["directory"]; exists {
				if dirStr, ok := dirParam.(string); ok {
					folderPath = dirStr
				}
			} else if toolName == "search_code" {
				folderPath = "."
			}

			if folderPath != "" {
				if IsFolderApproved(a, folderPath) {
					shouldAutoExecute = true
				} else {
					// We must stop spinner before requesting permission via UI
					spinner.Stop()
					if !RequestFolderPermission(a, folderPath) {
						permissionError = "Permission denied for folder access"
					} else {
						shouldAutoExecute = true
						// Restart spinner for subsequent processing (like GetPreview)
						spinner.Start()
					}
				}
			}
		}

		if permissionError != "" {
			spinner.Stop()
			a.Conversation = append(a.Conversation, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    permissionError,
				ToolCallID: toolCall.ID,
			})
			continue
		}

		// Get preview (potentially slow)
		var preview string
		if toolName == "edit_file" || toolName == "write_file" {
			preview, _ = toolManager.GetPreview(toolName, params)
		}

		// NOW we are ready to show everything and prompt the user
		spinner.Stop()
		ui.PrintfSafe("\n%s\n", toolDisplay)

		if preview != "" {
			ui.PrintfSafe("\n%s--- PREVIEW ---%s\n%s\n%s--- END PREVIEW ---%s\n",
				types.ColorBlue, types.ColorReset, preview, types.ColorBlue, types.ColorReset)
		}

		var response string
		if shouldAutoExecute {
			response = "y"
		} else {
			prompt := "\n❓ Execute this tool? (Y/n/s to skip/Esc to cancel): "
			if isLongRunning {
				ui.PrintfSafe("%s⚠️  This looks like a long-running command!%s\n", types.ColorYellow, types.ColorReset)
				prompt = "\n❓ Execute this tool? (Y/n/s to skip/Esc to cancel/b for background): "
			}
			playNotificationSound()
			ui.PrintSafe(prompt)

			ui.PauseInterruptMonitor()
			response = ui.ReadConfirmation()
			ui.ResumeInterruptMonitor()

			if response == "\r" || response == "\n" {
				response = "" // Treat Enter as default (yes)
			}

			// Echo the choice since raw mode doesn't
			if response == "" {
				ui.PrintlnSafe("y")
			} else if response == "i" {
				// Escape or Ctrl+C from ReadConfirmation
				ui.PrintlnSafe("cancel")
			} else {
				ui.PrintlnSafe(response)
			}
		}

		// executeToolBasedOnResponse already has its own spinner
		result, shouldContinue, err := executeToolBasedOnResponse(ctx, a, response, toolCall, params, isLongRunning, toolManager)

		// If user interrupted or error occurred
		if err != nil {
			// Find this tool call in the list and mark all subsequent ones as skipped
			found := false
			for _, tc := range toolCalls {
				if found {
					a.Conversation = append(a.Conversation, openai.ChatCompletionMessage{
						Role:       openai.ChatMessageRoleTool,
						Content:    "Tool call skipped due to user interruption",
						ToolCallID: tc.ID,
					})
				}
				if tc.ID == toolCall.ID {
					found = true
				}
			}
			return err
		}

		if !shouldContinue {
			// Find this tool call in the list and mark all subsequent ones as skipped
			found := false
			for _, tc := range toolCalls {
				if found {
					a.Conversation = append(a.Conversation, openai.ChatCompletionMessage{
						Role:       openai.ChatMessageRoleTool,
						Content:    "Tool call skipped due to user interruption",
						ToolCallID: tc.ID,
					})
				}
				if tc.ID == toolCall.ID {
					found = true
				}
			}
			break
		}

		// Display tool output to user
		if result != "" && (response == "" || response == "y" || response == "yes" || response == "b" || response == "background") {
			// Always show errors in red
			if strings.HasPrefix(result, "Error:") {
				ui.PrintfSafe("\n%s> %s%s\n", types.ColorRed, result, types.ColorReset)
			} else if toolName == "edit_file" || toolName == "write_file" {
				ui.PrintlnSafe() // Add blank line after tool call
				// Only stream diff/output if it wasn't already shown in preview
				if preview == "" {
					streamOutput(result)
				} else {
					ui.PrintfSafe("✅ %s applied successfully\n\n", toolName)
				}
			} else if toolName == "read_file" {
				ui.PrintlnSafe() // Add blank line after tool call
				offset := 0
				if v, ok := params["offset"].(float64); ok {
					offset = int(v)
				}

				// Find where the truncation message starts to count actual file lines
				actualResult := result
				if idx := strings.Index(result, "\n\n[... File truncated."); idx != -1 {
					actualResult = result[:idx]
				}

				// Count lines (including empty ones)
				var lineCount int
				if actualResult == "" {
					lineCount = 0
				} else {
					lineCount = strings.Count(actualResult, "\n") + 1
				}

				if lineCount > 0 {
					ui.PrintfSafe("%s> Read lines %d-%d (%d lines)%s\n",
						types.ColorCyan, offset, offset+lineCount-1, lineCount, types.ColorReset)
				} else {
					ui.PrintfSafe("%s> Read 0 lines (empty or at end of file)%s\n", types.ColorCyan, types.ColorReset)
				}
			} else if toolName == "search_code" {
				ui.PrintlnSafe() // Add blank line after tool call
				lineCount := strings.Count(result, "\n")
				ui.PrintfSafe("%s> Found %d matches%s\n", types.ColorCyan, lineCount, types.ColorReset)
			} else if toolName == "list_files" {
				ui.PrintlnSafe() // Add blank line after tool call
				lineCount := strings.Count(result, "\n")
				ui.PrintfSafe("%s> Listed %d items%s\n", types.ColorCyan, lineCount, types.ColorReset)
			} else if toolName != "read_file" && toolName != "list_files" && toolName != "bash_command" {
				ui.PrintlnSafe() // Add blank line after tool call
				// Generic output display (skip read_file, list_files and bash_command to avoid clutter/duplication)
				ui.PrintfSafe("%s> Tool Output:%s\n", types.ColorCyan, types.ColorReset)
				if len(result) > 2000 {
					ui.PrintlnSafe(result[:2000] + "... (truncated)")
				} else {
					ui.PrintlnSafe(result)
				}
			}
		}

		// Add tool result to conversation with truncation safety
		truncatedResult := TruncateForLLM(a, result, 8000) // Now strictly 8k max
		if truncatedResult == "" {
			truncatedResult = " " // Fallback to space for strict providers
		}
		a.Conversation = append(a.Conversation, openai.ChatCompletionMessage{
			Role:       openai.ChatMessageRoleTool,
			Content:    truncatedResult,
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
	ui.PrintSafe("\a")
}

// executeToolBasedOnResponse executes a tool based on user response
func executeToolBasedOnResponse(ctx context.Context, a *types.Agent, response string, toolCall openai.ToolCall, params map[string]interface{}, isLongRunning bool, toolManager *tools.Manager) (string, bool, error) {
	var result string
	toolName := normalizeToolName(toolCall.Function.Name)

	if response == "i" {
		return "", false, ui.ErrInterrupted
	}

	if response == "" || response == "y" || response == "yes" {
		// Execute the tool
		toolFunc, exists := a.Tools[toolName]
		if !exists {
			ui.PrintfSafe("Unknown tool: %s\n", toolCall.Function.Name)
			result = "Error: Unknown tool"
		} else {
			// Start spinner for tool execution
			spinner := ui.NewSpinner(fmt.Sprintf("Executing %s...", toolName))
			spinner.Start()

			// Tool execution doesn't support context yet, so we just run it
			// and check context afterward
			var err error
			result, err = toolFunc(params)

			// Stop spinner
			spinner.Stop()

			if ctx.Err() != nil {
				return "", false, ui.ErrInterrupted
			}

			if err != nil {
				result = fmt.Sprintf("Error: %v", err)
			}
		}
	} else if response == "s" || response == "skip" {
		result = "Tool execution skipped by user"
		ui.PrintfSafe("⏭️  Tool execution skipped\n")
	} else if response == "b" || response == "background" {
		if isLongRunning {
			ui.PrintfSafe("🚀 Starting command in background...\n")
			result = toolManager.BashCommandBackground(params)
			ui.PrintfSafe("✅ Command started in background\n")
		} else {
			result = "Background execution only available for long-running commands"
			ui.PrintfSafe("⚠️  Background execution only available for long-running commands\n")
		}
	} else {
		result = "Tool execution denied by user"
		ui.PrintfSafe("❌ Tool execution denied\n")
	}

	return result, true, nil
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
		ui.PrintSafe(chunk)

		// Very small delay
		time.Sleep(1 * time.Millisecond)
	}
	ui.PrintlnSafe() // Ensure newline at end
}
