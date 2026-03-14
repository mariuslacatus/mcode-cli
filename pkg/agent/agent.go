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

	var provider llm.Provider
	if currentModel.Provider == "gemini" || strings.Contains(strings.ToLower(currentModel.Name), "gemini") {
		// Initialize Gemini provider
		geminiProvider, err := llm.NewGeminiProvider(context.Background(), currentModel.APIKey)
		if err != nil {
			ui.PrintfSafe("Error initializing Gemini provider: %v. Falling back to OpenAI provider.\n", err)
			clientConfig := openai.DefaultConfig(currentModel.APIKey)
			clientConfig.BaseURL = currentModel.BaseURL
			client := openai.NewClientWithConfig(clientConfig)
			provider = llm.NewOpenAIProvider(client)
		} else {
			provider = geminiProvider
		}
	} else {
		// Configure OpenAI client
		clientConfig := openai.DefaultConfig(currentModel.APIKey)
		clientConfig.BaseURL = currentModel.BaseURL
		client := openai.NewClientWithConfig(clientConfig)
		provider = llm.NewOpenAIProvider(client)
	}

	// Convert approved folders slice to map for faster lookup
	approvedFolders := make(map[string]bool)
	for _, folder := range cfg.ApprovedFolders {
		approvedFolders[folder] = true
	}

	agent := &types.Agent{
		LLM:             provider,
		Conversation:    []types.Message{},
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

func convertToOpenAIMessages(messages []types.Message) []openai.ChatCompletionMessage {
	var res []openai.ChatCompletionMessage
	for _, m := range messages {
		res = append(res, openai.ChatCompletionMessage{
			Role:       m.Role,
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
			ToolCalls:  m.ToolCalls,
		})
	}
	return res
}

func convertToTypesMessages(messages []openai.ChatCompletionMessage) []types.Message {
	var res []types.Message
	for _, m := range messages {
		res = append(res, types.Message{
			Role:       m.Role,
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
			ToolCalls:  m.ToolCalls,
		})
	}
	return res
}

func convertToLLMMessages(messages []types.Message) []llm.Message {
	var res []llm.Message
	for _, m := range messages {
		res = append(res, llm.Message{
			Role:             m.Role,
			Content:          m.Content,
			Reasoning:        m.Reasoning,
			ThoughtSignature: m.ThoughtSignature,
			Name:             m.Name,
			ToolCallID:       m.ToolCallID,
			ToolCalls:        m.ToolCalls,
		})
	}
	return res
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

func isPathWithinRoot(path, root string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}

	if absPath == absRoot {
		return true
	}

	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}

	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func canAutoApproveEditForFolder(a *types.Agent, folderPath string) bool {
	if !a.AutoApproveEdit || a.AutoApproveEditRoot == "" {
		return false
	}

	return isPathWithinRoot(folderPath, a.AutoApproveEditRoot)
}

func setAutoApproveEditScope(a *types.Agent, folderPath string) {
	a.AutoApproveEdit = true
	a.AutoApproveEditRoot = folderPath
}

func clearAutoApproveEditScope(a *types.Agent) {
	a.AutoApproveEdit = false
	a.AutoApproveEditRoot = ""
}

// RequestFolderPermission requests permission for folder access
func RequestFolderPermission(a *types.Agent, folderPath string) (bool, error) {
	// Normalize the path
	absPath, err := filepath.Abs(folderPath)
	if err != nil {
		ui.PrintfSafe("Error resolving path: %v\n", err)
		return false, nil
	}

	// Check if already approved (including parent folder check)
	if IsFolderApproved(a, folderPath) {
		return true, nil
	}

	ui.PrintfSafe("🔒 Request folder access: %s\n", absPath)
	ui.PrintSafe("❓ Allow tool access in this folder and all subfolders? This includes read, search, preview, and approved edits. (Y/n/Esc to cancel): ")

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
	} else if response == "i" {
		ui.PrintlnSafe("cancel")
		return false, ui.ErrInterrupted
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
		return true, nil
	}

	ui.PrintfSafe("❌ Folder access denied\n")
	return false, nil
}

// TrimContext reduces conversation history to stay within a token budget.
// It prioritizes keeping system messages and the most recent interactions.
func TrimContext(a *types.Agent, messages []types.Message) []types.Message {
	if len(messages) <= 3 {
		return messages
	}

	modelName := a.Config.CurrentModel
	currentModel, ok := a.Config.Models[modelName]
	if !ok {
		// Fallback if model not found
		currentModel = types.Model{Name: modelName, MaxTokens: 8000}
	}

	totalLimit := currentModel.MaxTokens
	if totalLimit <= 0 {
		totalLimit = 8000 // Default fallback
	}

	tokenBudget := int(float64(totalLimit) * 0.5)

	if tokenBudget > 100000 {
		tokenBudget = 100000
	}

	var systemMessages []types.Message
	var otherMessages []types.Message

	for _, msg := range messages {
		if msg.Role == openai.ChatMessageRoleSystem {
			systemMessages = append(systemMessages, msg)
		} else {
			otherMessages = append(otherMessages, msg)
		}
	}

	var trimmed []types.Message
	currentTokens := 0

	for i := len(otherMessages) - 1; i >= 0; i-- {
		msgTokens := tokens.CountMessagesTokens(currentModel.Name, []types.Message{otherMessages[i]})
		if currentTokens+msgTokens > tokenBudget && len(trimmed) >= 4 {
			break
		}
		trimmed = append([]types.Message{otherMessages[i]}, trimmed...)
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

	var systemMessages []types.Message
	var recentMessages []types.Message
	var toSummarize []types.Message

	conversationLen := len(a.Conversation)
	keepRecent := 4

	for i, msg := range a.Conversation {
		if msg.Role == openai.ChatMessageRoleSystem {
			systemMessages = append(systemMessages, msg)
		} else if i >= conversationLen-keepRecent {
			recentMessages = append(recentMessages, msg)
		} else {
			if msg.Role != openai.ChatMessageRoleSystem {
				toSummarize = append(toSummarize, msg)
			}
		}
	}

	if len(toSummarize) == 0 {
		return fmt.Errorf("no messages to compact")
	}

	summaryPrompt := "Please provide a detailed, technical summary of the above conversation. \n" +
		"Focus on preserving context relevant to the current coding goal. \n" +
		"You may discard code snippets or details that are no longer relevant to the current task. \n" +
		"Preserve key technical decisions and any active constraints or instructions."

	summaryConv := append([]types.Message{}, toSummarize...)
	summaryConv = append(summaryConv, types.Message{
		Role:    openai.ChatMessageRoleUser,
		Content: summaryPrompt,
	})

	currentModel, exists := a.Config.Models[a.Config.CurrentModel]
	if !exists {
		for _, m := range a.Config.Models {
			currentModel = m
			break
		}
	}

	req := llm.Request{
		Model:     currentModel.Name,
		Messages:  convertToLLMMessages(summaryConv),
		MaxTokens: 4000,
		Stream:    true,
	}

	spinner := ui.NewSpinner("Compacting context...")
	spinner.Start()

	streamChan, err := a.LLM.CreateStream(context.Background(), req)
	if err != nil {
		spinner.Stop()
		return fmt.Errorf("failed to start summary stream: %v", err)
	}

	var summaryBuilder strings.Builder
	ui.PrintSafe(types.ColorCyan)

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

	ui.PrintSafe(types.ColorReset)
	ui.PrintlnSafe()

	summaryContent := summaryBuilder.String()

	var newHistory []types.Message
	newHistory = append(newHistory, systemMessages...)
	newHistory = append(newHistory, types.Message{
		Role:    openai.ChatMessageRoleSystem,
		Content: fmt.Sprintf("For context, here is a detailed summary of the previous conversation history:\n\n%s", summaryContent),
	})
	newHistory = append(newHistory, recentMessages...)

	oldLen := len(a.Conversation)
	a.Conversation = newHistory

	newTokens := tokens.CountMessagesTokens(currentModel.Name, newHistory)

	ui.PrintfSafe("✅ Context compacted: %d → %d messages (%d tokens)\n", oldLen, len(a.Conversation), newTokens)

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
	ui.UpdateStatusDisplay(modelName, tokens, a.AutoApproveEdit)
}

// InitConversation initializes the conversation with system prompts
func InitConversation(a *types.Agent) {
	projectManager := project.NewManager(a)
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

	systemPrompt := basePrompt
	if agentsContent != "" {
		systemPrompt += fmt.Sprintf("\n\n--- PROJECT CONTEXT (AGENTS.md) ---\n%s\n--- END PROJECT CONTEXT ---\n\nIMPORTANT: Pay special attention to any 'Permanent Instructions' in the project context above and follow them consistently.", agentsContent)
	}

	a.Conversation = []types.Message{
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

	if len(a.Conversation) == 0 {
		InitConversation(a)
	}

	a.Conversation = append(a.Conversation, types.Message{
		Role:    openai.ChatMessageRoleUser,
		Content: message,
	})

	if strings.TrimSpace(message) == "/compact" {
		if err := CompactContext(a); err != nil {
			return err
		}
		return nil
	}

	renderer, _ := markdown.NewNoMarginTermRenderer()

	sessionCtx, cancelSession := ui.StartInterruptMonitor(ctx, func() {
		if a.AutoApproveEdit {
			clearAutoApproveEditScope(a)
		} else {
			setAutoApproveEditScope(a, ".")
		}
		status := "Off"
		if a.AutoApproveEdit {
			status = "On"
		}
		ui.PrintfSafe("\r\n%s[Auto-approve edits: %s]%s\r\n", types.ColorCyan, status, types.ColorReset)
	})
	defer cancelSession()

	for {
		if sessionCtx.Err() != nil {
			return ui.ErrInterrupted
		}

		UpdateStatusDisplay(a)

		currentModel, exists := a.Config.Models[a.Config.CurrentModel]
		if !exists {
			return fmt.Errorf("current model '%s' not found in configuration", a.Config.CurrentModel)
		}

		messages := a.Conversation

		currentTokens := 0
		if a.LastTokenUsage != nil {
			currentTokens = a.LastTokenUsage.TotalTokens
		}

		threshold := 30000
		if currentModel.MaxTokens > 0 {
			threshold = int(float64(currentModel.MaxTokens) * 0.8)
		}

		spinner := ui.NewSpinner("")
		spinner.Start()

		if currentTokens > threshold {
			spinner.Stop()
			ui.PrintfSafe("\n⚠️  Context threshold reached (%d/%d tokens). Auto-compacting...\n", currentTokens, currentModel.MaxTokens)
			err := CompactContext(a)

			if err != nil {
				ui.PrintfSafe("Warning: Auto-compaction failed: %v\n", err)
			} else {
				messages = a.Conversation
			}
			spinner.Start()
		}

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

		req := llm.Request{
			Model:       currentModel.Name,
			Messages:    convertToLLMMessages(messages),
			Tools:       toolManager.GetToolDefinitions(),
			MaxTokens:   maxTokens,
			Temperature: 0.7,
			TopP:        1.0,
			Stream:      true,
		}

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

				reqFallback := llm.Request{
					Model:     currentModel.Name,
					Messages:  convertToLLMMessages(messages),
					MaxTokens: 2000,
				}

				ui.PrintlnSafe("🔄 Retrying with simplified request...")
				spinner.Start()

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

				assistantMessage := types.Message{
					Role:             openai.ChatMessageRoleAssistant,
					Content:          resp.Content,
					Reasoning:        resp.Reasoning,
					ThoughtSignature: resp.ThoughtSignature,
					ToolCalls:        resp.ToolCalls,
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
		var fullReasoning strings.Builder
		var thoughtSignature []byte
		var toolCalls []openai.ToolCall

		genStartTime := time.Now()
		contextTokens := GetContextTokens(a)

		updateStats := func(usage *openai.Usage) {
			genTokens := 0
			if usage != nil {
				genTokens = usage.CompletionTokens
			} else {
				genLen := fullContent.Len() + fullReasoning.Len()
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

			modelName := a.Config.Models[a.Config.CurrentModel].Name
			title := fmt.Sprintf("MCode | %s | %d ctx | %d gen (%.1f t/s)", modelName, contextTokens, genTokens, speed)
			spinner.SetTitle(title)

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

			if len(response.ThoughtSignature) > 0 {
				thoughtSignature = append(thoughtSignature, response.ThoughtSignature...)
			}

			if response.Content != "" || response.Reasoning != "" || len(response.ToolCalls) > 0 {
				spinner.Start()
			}

			if response.Reasoning != "" {
				fullReasoning.WriteString(response.Reasoning)
				spinner.Stop()
				ui.PrintSafe(types.ColorGray + response.Reasoning + types.ColorReset)
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

		responseTokens := tokens.CountTokens(currentModel.Name, fullContent.String()) + tokens.CountTokens(currentModel.Name, fullReasoning.String())
		for _, tc := range toolCalls {
			responseTokens += tokens.CountTokens(currentModel.Name, tc.Function.Name)
			responseTokens += tokens.CountTokens(currentModel.Name, tc.Function.Arguments)
		}

		if responseTokens < 1 {
			responseTokens = 1
		}

		contextEstimate := tokens.CountMessagesTokens(currentModel.Name, a.Conversation)

		a.LastTokenUsage = &openai.Usage{
			PromptTokens:     contextEstimate,
			CompletionTokens: responseTokens,
			TotalTokens:      contextEstimate + responseTokens,
		}
		a.TotalTokensUsed += responseTokens

		assistantMessage := types.Message{
			Role:             openai.ChatMessageRoleAssistant,
			Content:          fullContent.String(),
			Reasoning:        fullReasoning.String(),
			ThoughtSignature: thoughtSignature,
			ToolCalls:        toolCalls,
		}

		if assistantMessage.Content == "" && assistantMessage.Reasoning == "" && len(assistantMessage.ToolCalls) == 0 {
			assistantMessage.Content = " "
		}

		a.Conversation = append(a.Conversation, assistantMessage)

		spinner.Stop()

		if finishReason == "length" {
			fmt.Printf("\n⚠️  Warning: Generation truncated due to length limit!\n")
		}

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

	fmt.Println()

	if a.LastTokenUsage != nil {
		contextTokens := a.LastTokenUsage.PromptTokens
		responseTokens := a.LastTokenUsage.CompletionTokens
		totalSessionTokens := a.TotalTokensUsed

		if contextTokens > 0 {
			ui.PrintfSafe("%s[Context: %d tokens | Response: %d tokens | Session: %d tokens]%s\n",
				types.ColorBlue, contextTokens, responseTokens, totalSessionTokens, types.ColorReset)
		}

		UpdateStatusDisplay(a)
	}

	return nil
}

// TruncateForLLM truncates string content to a safe length for LLM context.
func TruncateForLLM(a *types.Agent, s string, maxChars int) string {
	limit := 8000

	if model, ok := a.Config.Models[a.Config.CurrentModel]; ok && model.MaxTokens > 0 {
		limit = int(float64(model.MaxTokens) * 0.5 * 4)
	}

	if maxChars > 0 && maxChars < limit {
		limit = maxChars
	}

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
		if ctx.Err() != nil {
			return ui.ErrInterrupted
		}

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

			fmt.Println(errResult)
			if len(toolCall.Function.Arguments) > 0 {
				argLen := len(toolCall.Function.Arguments)
				previewLen := 300
				if argLen < previewLen {
					previewLen = argLen
				}
				fmt.Printf("Partial JSON (len=%d): %s...\n", argLen, toolCall.Function.Arguments[:previewLen])
			}

			a.Conversation = append(a.Conversation, types.Message{
				Role:       openai.ChatMessageRoleTool,
				Content:    errResult,
				ToolCallID: toolCall.ID,
			})
			continue
		}

		toolDisplay := fmt.Sprintf("🔧 %s%s%s", types.ColorCyan, toolCall.Function.Name, types.ColorReset)
		displayInfo := toolManager.GetDisplayInfo(toolCall.Function.Name, params)
		if displayInfo != "" {
			toolDisplay += displayInfo
		}

		isLongRunning := false
		if toolCall.Function.Name == "bash_command" {
			if cmdParam, exists := params["command"]; exists {
				if cmdStr, ok := cmdParam.(string); ok {
					isLongRunning = tools.IsLongRunningCommand(cmdStr)
				}
			}
		}

		shouldAutoExecute := false
		var permissionError string
		var folderPath string

		isEditTool := toolCall.Function.Name == "edit_file" || toolCall.Function.Name == "write_file"

		if toolCall.Function.Name == "list_files" || toolCall.Function.Name == "read_file" || toolCall.Function.Name == "preview_edit" || toolCall.Function.Name == "search_code" || toolCall.Function.Name == "edit_file" || toolCall.Function.Name == "write_file" {
			// Try "path" first, then "filePath"
			pathVal := params["path"]
			if pathVal == nil {
				pathVal = params["filePath"]
			}

			if pathVal != nil {
				if pathStr, ok := pathVal.(string); ok {
					if toolCall.Function.Name == "read_file" || toolCall.Function.Name == "preview_edit" || toolCall.Function.Name == "edit_file" || toolCall.Function.Name == "write_file" {
						folderPath = filepath.Dir(pathStr)
					} else {
						folderPath = pathStr
					}
				}
			} else if dirParam, exists := params["directory"]; exists {
				if dirStr, ok := dirParam.(string); ok {
					folderPath = dirStr
				}
			} else if toolCall.Function.Name == "search_code" {
				folderPath = "."
			}

			if folderPath != "" {
				if IsFolderApproved(a, folderPath) {
					if toolCall.Function.Name == "list_files" || toolCall.Function.Name == "read_file" || toolCall.Function.Name == "preview_edit" || toolCall.Function.Name == "search_code" {
						shouldAutoExecute = true
					} else if isEditTool && canAutoApproveEditForFolder(a, folderPath) {
						shouldAutoExecute = true
					}
				} else {
					spinner.Stop()
					approved, err := RequestFolderPermission(a, folderPath)
					if err == ui.ErrInterrupted {
						// Interrupted by user, skip tool call
						found := false
						for _, tc := range toolCalls {
							if found {
								a.Conversation = append(a.Conversation, types.Message{
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

					if !approved {
						permissionError = "Permission denied for folder access"
					} else {
						// Folder was just approved. We auto-execute read-only tools.
						if toolCall.Function.Name == "list_files" || toolCall.Function.Name == "read_file" || toolCall.Function.Name == "preview_edit" || toolCall.Function.Name == "search_code" {
							shouldAutoExecute = true
						} else if isEditTool && canAutoApproveEditForFolder(a, folderPath) {
							shouldAutoExecute = true
						}
						spinner.Start()
					}
				}
			}
		}

		if permissionError != "" {
			spinner.Stop()
			a.Conversation = append(a.Conversation, types.Message{
				Role:       openai.ChatMessageRoleTool,
				Content:    permissionError,
				ToolCallID: toolCall.ID,
			})
			continue
		}

		var preview string
		if toolCall.Function.Name == "edit_file" || toolCall.Function.Name == "write_file" {
			preview, _ = toolManager.GetPreview(toolCall.Function.Name, params)
		}

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
			} else if isEditTool {
				autoApproveStatus := "Off"
				if a.AutoApproveEdit {
					autoApproveStatus = "On"
				}
				prompt = fmt.Sprintf("\n❓ Execute this tool? (Y/n/s to skip/Esc to cancel/⇥/Ctrl+T Auto-approve edits [%s]): ", autoApproveStatus)
			}
			playNotificationSound()
			ui.PrintSafe(prompt)

			ui.PauseInterruptMonitor()
			response = ui.ReadConfirmation()
			ui.ResumeInterruptMonitor()

			// Handle toggle (Shift+Tab/Ctrl+T)
			for response == "t" {
				if a.AutoApproveEdit {
					clearAutoApproveEditScope(a)
				} else if isEditTool && folderPath != "" {
					setAutoApproveEditScope(a, folderPath)
				} else {
					setAutoApproveEditScope(a, ".")
				}
				autoApproveStatus := "Off"
				if a.AutoApproveEdit {
					autoApproveStatus = "On"
				}

				// Clear the line and go back to start
				ui.PrintSafe("\r\033[K")

				// Re-create the prompt text so it includes the updated status if it's an edit tool
				if isEditTool {
					prompt = fmt.Sprintf("\n❓ Execute this tool? (Y/n/s to skip/Esc to cancel/⇥/Ctrl+T Auto-approve edits [%s]): ", autoApproveStatus)
					// We use \033[A to move cursor up one line, clear it, print status, then print prompt
					ui.PrintSafe("\033[A\r\033[K")
					ui.PrintfSafe("%s[Auto-approve edits: %s]%s", types.ColorCyan, autoApproveStatus, types.ColorReset)
					ui.PrintSafe(prompt)

					// If they toggled it on for the current folder, proceed with this edit.
					if canAutoApproveEditForFolder(a, folderPath) {
						ui.PrintlnSafe("y")
						response = "y"
						break
					}
				} else {
					ui.PrintfSafe("%s[Auto-approve edits: %s]%s", types.ColorCyan, autoApproveStatus, types.ColorReset)
					ui.PrintSafe(prompt)
				}

				ui.PauseInterruptMonitor()
				response = ui.ReadConfirmation()
				ui.ResumeInterruptMonitor()
			}
			if response == "\r" || response == "\n" {
				response = ""
			}

			if response == "" {
				ui.PrintlnSafe("y")
			} else if response == "i" {
				ui.PrintlnSafe("cancel")
			} else {
				ui.PrintlnSafe(response)
			}
		}

		result, shouldContinue, err := executeToolBasedOnResponse(ctx, a, response, toolCall, params, isLongRunning, toolManager)

		if err != nil {
			found := false
			for _, tc := range toolCalls {
				if found {
					a.Conversation = append(a.Conversation, types.Message{
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
			found := false
			for _, tc := range toolCalls {
				if found {
					a.Conversation = append(a.Conversation, types.Message{
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

		if result != "" && (response == "" || response == "y" || response == "yes" || response == "b" || response == "background") {
			if strings.HasPrefix(result, "Error:") {
				ui.PrintfSafe("\n%s> %s%s\n", types.ColorRed, result, types.ColorReset)
			} else if toolCall.Function.Name == "edit_file" || toolCall.Function.Name == "write_file" {
				ui.PrintlnSafe()
				if preview == "" {
					streamOutput(result)
				} else {
					ui.PrintfSafe("✅ %s applied successfully\n\n", toolCall.Function.Name)
				}
			} else if toolCall.Function.Name == "read_file" {
				ui.PrintlnSafe()
				offset := 0
				if v, ok := params["offset"].(float64); ok {
					offset = int(v)
				}

				actualResult := result
				if idx := strings.Index(result, "\n\n[... File truncated."); idx != -1 {
					actualResult = result[:idx]
				}

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
			} else if toolCall.Function.Name == "search_code" {
				ui.PrintlnSafe()
				lineCount := strings.Count(result, "\n")
				ui.PrintfSafe("%s> Found %d matches%s\n", types.ColorCyan, lineCount, types.ColorReset)
			} else if toolCall.Function.Name == "list_files" {
				ui.PrintlnSafe()
				lineCount := strings.Count(result, "\n")
				ui.PrintfSafe("%s> Listed %d items%s\n", types.ColorCyan, lineCount, types.ColorReset)
			} else if toolCall.Function.Name != "read_file" && toolCall.Function.Name != "list_files" && toolCall.Function.Name != "bash_command" {
				ui.PrintlnSafe()
				ui.PrintfSafe("%s> Tool Output:%s\n", types.ColorCyan, types.ColorReset)
				if len(result) > 2000 {
					ui.PrintlnSafe(result[:2000] + "... (truncated)")
				} else {
					ui.PrintlnSafe(result)
				}
			}
		}

		truncatedResult := TruncateForLLM(a, result, 8000)
		if truncatedResult == "" {
			truncatedResult = " "
		}
		a.Conversation = append(a.Conversation, types.Message{
			Role:       openai.ChatMessageRoleTool,
			Content:    truncatedResult,
			Name:       toolCall.Function.Name,
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
	fmt.Print("\a")
}

// executeToolBasedOnResponse executes a tool based on user response
func executeToolBasedOnResponse(ctx context.Context, a *types.Agent, response string, toolCall openai.ToolCall, params map[string]interface{}, isLongRunning bool, toolManager *tools.Manager) (string, bool, error) {
	var result string

	if response == "i" {
		return "", false, ui.ErrInterrupted
	}

	if response == "" || response == "y" || response == "yes" {
		tool, exists := toolManager.GetTool(toolCall.Function.Name)
		if !exists {
			fmt.Printf("Unknown tool: %s\n", toolCall.Function.Name)
			result = "Error: Unknown tool"
		} else {
			spinner := ui.NewSpinner(fmt.Sprintf("Executing %s...", toolCall.Function.Name))
			spinner.Start()

			var err error
			result, err = tool.Execute(ctx, params)
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
	} else {
		result = "Tool execution denied by user"
		fmt.Printf("❌ Tool execution denied\n")
	}

	return result, true, nil
}

// streamOutput simulates streaming output for content
func streamOutput(content string) {
	chunkSize := 10
	for i := 0; i < len(content); i += chunkSize {
		end := i + chunkSize
		if end > len(content) {
			end = len(content)
		}

		chunk := content[i:end]
		ui.PrintSafe(chunk)
		time.Sleep(1 * time.Millisecond)
	}
	ui.PrintlnSafe()
}
