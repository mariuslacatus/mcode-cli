package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/chzyer/readline"
	"github.com/sashabaranov/go-openai"
	"github.com/spf13/cobra"
)

type Config struct {
	CurrentModel     string            `json:"current_model"`
	Models           map[string]Model  `json:"models"`
	ApprovedFolders  []string          `json:"approved_folders"`
}

type Model struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key,omitempty"`
}

type Agent struct {
	client       *openai.Client
	conversation []openai.ChatCompletionMessage
	tools        map[string]func(map[string]interface{}) (string, error)
	lastTokenUsage *openai.Usage
	totalTokensUsed int
	config       *Config
	configPath   string
	approvedFolders map[string]bool // Track folders user has granted access to
}

func (a *Agent) getContextTokens() int {
	if a.lastTokenUsage != nil {
		return a.lastTokenUsage.PromptTokens
	}
	return 0 // No API call made yet
}

func (a *Agent) getTotalTokensUsed() int {
	return a.totalTokensUsed
}

func (a *Agent) isFolderApproved(folderPath string) bool {
	// Normalize the path
	absPath, err := filepath.Abs(folderPath)
	if err != nil {
		return false
	}
	
	// Check exact match first (for performance)
	if a.approvedFolders[absPath] {
		return true
	}
	
	// Check if this path is within any approved parent folder
	for approvedFolder := range a.approvedFolders {
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

func (a *Agent) requestFolderPermission(folderPath string) bool {
	// Normalize the path
	absPath, err := filepath.Abs(folderPath)
	if err != nil {
		fmt.Printf("Error resolving path: %v\n", err)
		return false
	}
	
	// Check if already approved (including parent folder check)
	if a.isFolderApproved(folderPath) {
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
		a.approvedFolders[absPath] = true
		
		// Add to config and save persistently
		a.config.ApprovedFolders = append(a.config.ApprovedFolders, absPath)
		if err := saveConfig(a.configPath, a.config); err != nil {
			fmt.Printf("⚠️  Warning: Failed to save folder permission: %v\n", err)
		}
		
		fmt.Printf("✅ Folder access granted: %s (includes all subfolders)\n", absPath)
		return true
	}
	
	fmt.Printf("❌ Folder access denied\n")
	return false
}

func (a *Agent) trimContext(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
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

func getConfigPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ".mcode-config.json"
	}
	return filepath.Join(homeDir, ".mcode-config.json")
}

func loadOrCreateConfig(configPath string) (*Config, error) {
	// Try to load existing config
	if data, err := os.ReadFile(configPath); err == nil {
		var config Config
		if json.Unmarshal(data, &config) == nil {
			return &config, nil
		}
	}

	// Create default config
	defaultConfig := &Config{
		CurrentModel: "qwen3-coder",
		Models: map[string]Model{
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
	if err := saveConfig(configPath, defaultConfig); err != nil {
		return nil, fmt.Errorf("failed to save default config: %v", err)
	}

	return defaultConfig, nil
}

func saveConfig(configPath string, config *Config) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0644)
}

func NewAgent() *Agent {
	configPath := getConfigPath()
	config, err := loadOrCreateConfig(configPath)
	if err != nil {
		fmt.Printf("Warning: Failed to load config, using defaults: %v\n", err)
		// Fallback to hardcoded defaults
		config = &Config{
			CurrentModel: "qwen3-coder",
			Models: map[string]Model{
				"qwen3-coder": {
					Name:    "lmstudio-community/qwen3-coder-30b-a3b-instruct-mlx@8bit",
					BaseURL: "http://localhost:1234/v1",
				},
			},
		}
	}

	// Get current model configuration
	currentModel, exists := config.Models[config.CurrentModel]
	if !exists {
		fmt.Printf("Warning: Current model '%s' not found, using first available model\n", config.CurrentModel)
		for _, model := range config.Models {
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
	for _, folder := range config.ApprovedFolders {
		approvedFolders[folder] = true
	}

	agent := &Agent{
		client:          client,
		conversation:    []openai.ChatCompletionMessage{},
		tools:           make(map[string]func(map[string]interface{}) (string, error)),
		config:          config,
		configPath:      configPath,
		approvedFolders: approvedFolders,
	}

	agent.registerTools()
	agent.loadProjectContext()
	return agent
}

func (a *Agent) registerTools() {
	a.tools["read_file"] = a.readFile
	a.tools["list_files"] = a.listFiles
	a.tools["bash_command"] = a.bashCommand
	a.tools["edit_file"] = a.editFile
	a.tools["search_code"] = a.searchCode
}

func (a *Agent) getToolDefinitions() []openai.Tool {
	return []openai.Tool{
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "read_file",
				Description: "Read the contents of a file",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Path to the file to read",
						},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "list_files",
				Description: "List files in a directory",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Directory path to list",
						},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "bash_command",
				Description: "Execute a bash command",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"command": map[string]interface{}{
							"type":        "string",
							"description": "Command to execute",
						},
					},
					"required": []string{"command"},
				},
			},
		},
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "edit_file",
				Description: "Edit or create a file",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Path to the file",
						},
						"content": map[string]interface{}{
							"type":        "string",
							"description": "New content for the file",
						},
					},
					"required": []string{"path", "content"},
				},
			},
		},
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "search_code",
				Description: "Search for code patterns in files",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"pattern": map[string]interface{}{
							"type":        "string",
							"description": "Pattern to search for",
						},
						"directory": map[string]interface{}{
							"type":        "string",
							"description": "Directory to search in",
						},
					},
					"required": []string{"pattern"},
				},
			},
		},
	}
}

func (a *Agent) readFile(params map[string]interface{}) (string, error) {
	path, ok := params["path"].(string)
	if !ok {
		return "", fmt.Errorf("path parameter is required")
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("error reading file: %v", err)
	}

	return string(content), nil
}

func (a *Agent) listFiles(params map[string]interface{}) (string, error) {
	path, ok := params["path"].(string)
	if !ok {
		path = "."
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return "", fmt.Errorf("error listing directory: %v", err)
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			files = append(files, entry.Name()+"/")
		} else {
			files = append(files, entry.Name())
		}
	}

	return strings.Join(files, "\n"), nil
}

func (a *Agent) bashCommand(params map[string]interface{}) (string, error) {
	command, ok := params["command"].(string)
	if !ok {
		return "", fmt.Errorf("command parameter is required")
	}

	// Create a context with timeout (default 30 seconds for most commands)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fmt.Printf("%sExecuting: %s%s\n", ColorYellow, command, ColorReset)
	fmt.Printf("%s(Press Ctrl+C to interrupt if it hangs)%s\n", ColorBlue, ColorReset)

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	
	// Set process group so we can kill the entire group if needed
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	output, err := cmd.CombinedOutput()
	
	if ctx.Err() == context.DeadlineExceeded {
		return string(output), fmt.Errorf("command timed out after 30 seconds. Output so far: %s", string(output))
	}
	
	if err != nil {
		return string(output), fmt.Errorf("command failed: %v", err)
	}

	return string(output), nil
}

func (a *Agent) bashCommandWithTimeout(params map[string]interface{}, timeout time.Duration) string {
	command, ok := params["command"].(string)
	if !ok {
		return "Error: command parameter is required"
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	fmt.Printf("%sExecuting: %s%s\n", ColorYellow, command, ColorReset)

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	output, err := cmd.CombinedOutput()
	
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("Command started and timed out after %v (likely running in background). Output so far: %s", timeout, string(output))
	}
	
	if err != nil {
		return fmt.Sprintf("Command failed: %v. Output: %s", err, string(output))
	}

	return string(output)
}

func (a *Agent) bashCommandBackground(params map[string]interface{}) string {
	command, ok := params["command"].(string)
	if !ok {
		return "Error: command parameter is required"
	}

	fmt.Printf("%sStarting in background: %s%s\n", ColorYellow, command, ColorReset)

	cmd := exec.Command("bash", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	
	// Start the command without waiting for it to complete
	err := cmd.Start()
	if err != nil {
		return fmt.Sprintf("Failed to start command in background: %v", err)
	}

	return fmt.Sprintf("Command started in background with PID %d. Use 'ps aux | grep \"%s\"' to check status.", cmd.Process.Pid, command)
}

func (a *Agent) editFile(params map[string]interface{}) (string, error) {
	path, ok := params["path"].(string)
	if !ok {
		return "", fmt.Errorf("path parameter is required")
	}

	content, ok := params["content"].(string)
	if !ok {
		return "", fmt.Errorf("content parameter is required")
	}

	// Read existing content for diff
	var oldContent string
	if existingContent, err := os.ReadFile(path); err == nil {
		oldContent = string(existingContent)
	}

	// Write the new content
	err := os.WriteFile(path, []byte(content), 0644)
	if err != nil {
		return "", fmt.Errorf("error writing file: %v", err)
	}

	// Generate and display diff
	if oldContent != "" && oldContent != content {
		diff := a.generateDiff(oldContent, content, path)
		fmt.Print(diff)
		return fmt.Sprintf("File %s has been modified", path), nil
	} else if oldContent == "" {
		fmt.Printf("%s📄 Created new file: %s%s\n", ColorGreen, path, ColorReset)
		return fmt.Sprintf("File %s has been created", path), nil
	}

	return fmt.Sprintf("File %s unchanged", path), nil
}

func (a *Agent) searchCode(params map[string]interface{}) (string, error) {
	pattern, ok := params["pattern"].(string)
	if !ok {
		return "", fmt.Errorf("pattern parameter is required")
	}

	directory, ok := params["directory"].(string)
	if !ok {
		directory = "."
	}

	cmd := exec.Command("grep", "-r", pattern, directory)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), nil // grep returns error when no matches found
	}

	return string(output), nil
}

// ANSI color codes
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorCyan   = "\033[36m"
)

func (a *Agent) generateDiff(oldContent, newContent, filename string) string {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")
	
	var diff strings.Builder
	diff.WriteString(fmt.Sprintf("%s📝 File changes: %s%s\n", ColorCyan, filename, ColorReset))
	diff.WriteString(fmt.Sprintf("%s%s%s\n", ColorBlue, strings.Repeat("=", 60), ColorReset))
	
	maxLines := len(oldLines)
	if len(newLines) > maxLines {
		maxLines = len(newLines)
	}
	
	for i := 0; i < maxLines; i++ {
		lineNum := fmt.Sprintf("%3d", i+1)
		
		var oldLine, newLine string
		if i < len(oldLines) {
			oldLine = oldLines[i]
		}
		if i < len(newLines) {
			newLine = newLines[i]
		}
		
		if oldLine != newLine {
			if i < len(oldLines) && oldLine != "" {
				diff.WriteString(fmt.Sprintf("%s-%s │ %s%s\n", ColorRed, lineNum, oldLine, ColorReset))
			}
			if i < len(newLines) && newLine != "" {
				diff.WriteString(fmt.Sprintf("%s+%s │ %s%s\n", ColorGreen, lineNum, newLine, ColorReset))
			}
		}
	}
	
	// Summary
	addedLines := len(newLines) - len(oldLines)
	if addedLines > 0 {
		diff.WriteString(fmt.Sprintf("%s+%d lines added%s\n", ColorGreen, addedLines, ColorReset))
	} else if addedLines < 0 {
		diff.WriteString(fmt.Sprintf("%s%d lines removed%s\n", ColorRed, -addedLines, ColorReset))
	}
	
	return diff.String()
}

func (a *Agent) isLongRunningCommand(command string) bool {
	longRunningPatterns := []string{
		"python", "python3", "node", "npm start", "npm run", "go run",
		"serve", "server", "uvicorn", "gunicorn", "flask run",
		"rails server", "php -S", "java -jar", "./", "watch",
		"tail -f", "ping", "curl.*-w", "sleep", "while true",
	}
	
	cmdLower := strings.ToLower(command)
	for _, pattern := range longRunningPatterns {
		if strings.Contains(cmdLower, pattern) {
			return true
		}
	}
	return false
}

func (a *Agent) loadAgentsMD() string {
	agentsFile := "AGENTS.md"
	content, err := os.ReadFile(agentsFile)
	if err != nil {
		return ""
	}
	return string(content)
}

func (a *Agent) loadProjectContext() {
	agentsFile := "AGENTS.md"
	if content, err := os.ReadFile(agentsFile); err == nil {
		// Add AGENTS.md content as system context
		agentsContent := string(content)
		if strings.TrimSpace(agentsContent) != "" {
			systemMsg := openai.ChatCompletionMessage{
				Role:    "system",
				Content: "Project context from AGENTS.md:\n\n" + agentsContent,
			}
			a.conversation = append(a.conversation, systemMsg)
		}
	}
}

func (a *Agent) addPermanentInstruction(instruction string) error {
	agentsFile := "AGENTS.md"
	
	// Read current content
	var content string
	if existingContent, err := os.ReadFile(agentsFile); err == nil {
		content = string(existingContent)
	} else {
		// If AGENTS.md doesn't exist, create a basic one first
		fmt.Println("📄 AGENTS.md not found, creating with basic template...")
		if err := a.createBasicAgentsMD(filepath.Base("."), "."); err != nil {
			return fmt.Errorf("failed to create AGENTS.md: %v", err)
		}
		if newContent, err := os.ReadFile(agentsFile); err == nil {
			content = string(newContent)
		} else {
			return fmt.Errorf("failed to read newly created AGENTS.md: %v", err)
		}
	}
	
	// Find the "Permanent Instructions" section
	permInstructionsSection := "### Permanent Instructions"
	permIndex := strings.Index(content, permInstructionsSection)
	
	if permIndex == -1 {
		// If section doesn't exist, add it before the last section or at the end
		lastSectionIndex := strings.LastIndex(content, "\n### ")
		if lastSectionIndex == -1 {
			// No sections found, add at the end
			content += fmt.Sprintf("\n\n## AI Agent Instructions\n\n### Permanent Instructions\n- %s\n", instruction)
		} else {
			// Insert before the last section
			content = content[:lastSectionIndex] + fmt.Sprintf("\n### Permanent Instructions\n- %s\n", instruction) + content[lastSectionIndex:]
		}
	} else {
		// Section exists, add to it
		sectionStart := permIndex + len(permInstructionsSection)
		
		// Find the next section or end of file
		nextSectionIndex := strings.Index(content[permIndex:], "\n### ")
		var sectionEnd int
		if nextSectionIndex == -1 {
			sectionEnd = len(content)
		} else {
			sectionEnd = permIndex + nextSectionIndex
		}
		
		// Get the section content
		sectionContent := content[sectionStart:sectionEnd]
		
		// Check if there are already instructions (look for existing bullet points)
		if strings.Contains(sectionContent, "\n- ") || strings.Contains(sectionContent, "- ") {
			// Find the last bullet point and add after it
			lastBulletIndex := strings.LastIndex(sectionContent, "\n- ")
			if lastBulletIndex == -1 {
				// Must be first bullet point at the start
				bulletIndex := strings.Index(sectionContent, "- ")
				if bulletIndex != -1 {
					// Find end of this line
					lineEnd := strings.Index(sectionContent[bulletIndex:], "\n")
					if lineEnd == -1 {
						lineEnd = len(sectionContent) - bulletIndex
					}
					insertPos := sectionStart + bulletIndex + lineEnd
					content = content[:insertPos] + fmt.Sprintf("\n- %s", instruction) + content[insertPos:]
				}
			} else {
				// Find end of the last bullet line
				lastBulletStart := sectionStart + lastBulletIndex + 1
				lineEnd := strings.Index(content[lastBulletStart:], "\n")
				if lineEnd == -1 {
					lineEnd = sectionEnd - lastBulletStart
				}
				insertPos := lastBulletStart + lineEnd
				content = content[:insertPos] + fmt.Sprintf("\n- %s", instruction) + content[insertPos:]
			}
		} else {
			// First instruction - replace placeholder or add after header
			if strings.Contains(sectionContent, "*Use #command") {
				// Replace the placeholder line
				placeholderStart := strings.Index(sectionContent, "*Use #command")
				placeholderEnd := strings.Index(sectionContent[placeholderStart:], "\n")
				if placeholderEnd == -1 {
					placeholderEnd = len(sectionContent) - placeholderStart
				}
				replaceStart := sectionStart + placeholderStart
				replaceEnd := replaceStart + placeholderEnd
				content = content[:replaceStart] + fmt.Sprintf("- %s", instruction) + content[replaceEnd:]
			} else {
				// Add after the section header
				content = content[:sectionStart] + fmt.Sprintf("\n- %s", instruction) + content[sectionStart:]
			}
		}
	}
	
	// Write back to file
	return os.WriteFile(agentsFile, []byte(content), 0644)
}

func (a *Agent) exportContext(parts []string) error {
	if len(a.conversation) == 0 {
		fmt.Println("❌ No conversation context to export")
		return nil
	}
	
	// Determine filename
	filename := "context.txt"
	if len(parts) > 1 {
		filename = parts[1]
		if !strings.HasSuffix(filename, ".txt") {
			filename += ".txt"
		}
	}
	
	fmt.Printf("📤 Exporting context to %s...\n", filename)
	
	// Format the conversation
	var content strings.Builder
	content.WriteString(fmt.Sprintf("# MCode CLI Context Export\n"))
	content.WriteString(fmt.Sprintf("Exported: %s\n", time.Now().Format("2006-01-02 15:04:05")))
	
	if a.lastTokenUsage != nil {
		content.WriteString(fmt.Sprintf("Context Tokens: %d\n", a.lastTokenUsage.PromptTokens))
		content.WriteString(fmt.Sprintf("Total Session Tokens: %d\n", a.totalTokensUsed))
	}
	
	content.WriteString("\n" + strings.Repeat("=", 80) + "\n\n")
	
	for i, msg := range a.conversation {
		// Add separator between messages
		if i > 0 {
			content.WriteString("\n" + strings.Repeat("-", 40) + "\n\n")
		}
		
		switch msg.Role {
		case openai.ChatMessageRoleSystem:
			content.WriteString("🔧 SYSTEM MESSAGE:\n")
			content.WriteString(msg.Content)
			content.WriteString("\n")
			
		case openai.ChatMessageRoleUser:
			content.WriteString("👤 USER:\n")
			content.WriteString(msg.Content)
			content.WriteString("\n")
			
		case openai.ChatMessageRoleAssistant:
			content.WriteString("🤖 ASSISTANT:\n")
			if msg.Content != "" {
				content.WriteString(msg.Content)
				content.WriteString("\n")
			}
			
			// Add tool calls
			for _, toolCall := range msg.ToolCalls {
				content.WriteString(fmt.Sprintf("\n🔧 TOOL CALL: %s\n", toolCall.Function.Name))
				content.WriteString(fmt.Sprintf("Arguments: %s\n", toolCall.Function.Arguments))
			}
			
		case openai.ChatMessageRoleTool:
			content.WriteString("⚙️ TOOL RESULT:\n")
			content.WriteString(msg.Content)
			content.WriteString("\n")
		}
	}
	
	content.WriteString("\n" + strings.Repeat("=", 80) + "\n")
	content.WriteString(fmt.Sprintf("End of context export (%d messages)\n", len(a.conversation)))
	
	// Write to file
	err := os.WriteFile(filename, []byte(content.String()), 0644)
	if err != nil {
		return fmt.Errorf("failed to write export file: %v", err)
	}
	
	fmt.Printf("✅ Context exported successfully!\n")
	fmt.Printf("📄 File: %s\n", filename)
	fmt.Printf("📊 Messages: %d\n", len(a.conversation))
	if a.lastTokenUsage != nil {
		fmt.Printf("🔢 Context tokens: %d\n", a.lastTokenUsage.PromptTokens)
	}
	
	return nil
}

func (a *Agent) handleModelsCommand(parts []string) error {
	if len(parts) == 1 {
		// List available models
		return a.listModels()
	}
	
	if len(parts) == 2 {
		// Switch to model
		return a.switchModel(parts[1])
	}
	
	fmt.Println("Usage:")
	fmt.Println("  /models           - List available models")
	fmt.Println("  /models <name>    - Switch to model")
	return nil
}

func (a *Agent) listModels() error {
	fmt.Println("\n🤖 Available Models")
	fmt.Println("==================")
	
	for key, model := range a.config.Models {
		status := ""
		if key == a.config.CurrentModel {
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

func (a *Agent) switchModel(modelKey string) error {
	model, exists := a.config.Models[modelKey]
	if !exists {
		fmt.Printf("❌ Model '%s' not found\n", modelKey)
		fmt.Println("Available models:")
		for key := range a.config.Models {
			fmt.Printf("  - %s\n", key)
		}
		return nil
	}
	
	// Update config
	a.config.CurrentModel = modelKey
	
	// Save config
	if err := saveConfig(a.configPath, a.config); err != nil {
		return fmt.Errorf("failed to save config: %v", err)
	}
	
	// Update client
	clientConfig := openai.DefaultConfig(model.APIKey)
	clientConfig.BaseURL = model.BaseURL
	a.client = openai.NewClientWithConfig(clientConfig)
	
	fmt.Printf("✅ Switched to model: %s\n", modelKey)
	fmt.Printf("📱 Name: %s\n", model.Name)
	fmt.Printf("🌐 URL: %s\n", model.BaseURL)
	
	return nil
}

func (a *Agent) handlePermissionsCommand(parts []string) error {
	if len(parts) == 1 {
		// List approved folders
		return a.listApprovedFolders()
	}
	
	if len(parts) == 3 && parts[1] == "remove" {
		// Remove folder permission
		return a.removeFolderPermission(parts[2])
	}
	
	fmt.Println("Usage:")
	fmt.Println("  /permissions           - List approved folders")
	fmt.Println("  /permissions remove <path> - Remove folder permission")
	return nil
}

func (a *Agent) listApprovedFolders() error {
	fmt.Println("\n🔒 Approved Folders")
	fmt.Println("===================")
	
	if len(a.config.ApprovedFolders) == 0 {
		fmt.Println("No folders have been approved yet.")
		return nil
	}
	
	for i, folder := range a.config.ApprovedFolders {
		fmt.Printf("%d. %s\n", i+1, folder)
	}
	
	fmt.Printf("\nTotal: %d folder(s)\n", len(a.config.ApprovedFolders))
	return nil
}

func (a *Agent) removeFolderPermission(folderPath string) error {
	// Normalize the path
	absPath, err := filepath.Abs(folderPath)
	if err != nil {
		return fmt.Errorf("error resolving path: %v", err)
	}
	
	// Check if folder is in approved list
	found := false
	newApproved := make([]string, 0, len(a.config.ApprovedFolders))
	for _, folder := range a.config.ApprovedFolders {
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
	a.config.ApprovedFolders = newApproved
	delete(a.approvedFolders, absPath)
	
	if err := saveConfig(a.configPath, a.config); err != nil {
		return fmt.Errorf("failed to save config: %v", err)
	}
	
	fmt.Printf("✅ Removed folder permission: %s\n", absPath)
	return nil
}

func (a *Agent) clearContext() {
	a.conversation = []openai.ChatCompletionMessage{}
	a.lastTokenUsage = nil
	// Don't reset totalTokensUsed - keep session total
	fmt.Printf("%s🔄 Conversation context cleared - Starting fresh!%s\n", ColorGreen, ColorReset)
}

func (a *Agent) handleSlashCommand(command string) (bool, error) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return false, nil
	}

	switch parts[0] {
	case "/exit", "/quit":
		fmt.Println("👋 Goodbye!")
		return true, nil
	case "/init":
		err := a.initializeProject()
		return false, err
	case "/new":
		a.clearContext()
		return false, nil
	case "/export":
		err := a.exportContext(parts)
		return false, err
	case "/models":
		err := a.handleModelsCommand(parts)
		return false, err
	case "/permissions":
		err := a.handlePermissionsCommand(parts)
		return false, err
	case "/help":
		a.showHelp()
		return false, nil
	default:
		fmt.Printf("❌ Unknown command: %s\n", parts[0])
		fmt.Println("Available commands: /exit, /init, /new, /export, /models, /permissions, /help")
		return false, nil
	}
}

func (a *Agent) initializeProject() error {
	fmt.Println("🚀 Analyzing project and initializing...")
	
	// Get current directory info
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("error getting current directory: %v", err)
	}
	
	projectName := filepath.Base(cwd)
	
	// Check if AGENTS.md already exists
	agentsFile := "AGENTS.md"
	if _, err := os.Stat(agentsFile); err == nil {
		fmt.Print("⚠️  AGENTS.md already exists. Overwrite? (y/n): ")
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		if strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" {
			fmt.Println("❌ Project initialization cancelled")
			return nil
		}
	}
	
	fmt.Println("🔍 Analyzing project structure and inferring coding standards...")
	
	// Use LLM to analyze the project
	analysisPrompt := fmt.Sprintf(`Analyze this project and provide structured markdown content for AGENTS.md.

PROJECT: %s at %s

INSTRUCTIONS:
1. Use the available tools to analyze the project structure and code
2. Provide ONLY the markdown content for these sections (no conversational text):

## Project Structure
[Describe key directories, files, and organization]

## Development Guidelines  
[Infer coding standards, patterns, and conventions from existing code]

## Project Context
[Important information about project purpose, technologies, dependencies]

IMPORTANT: 
- Respond with ONLY the markdown sections above
- No conversational text like "I'll analyze" or "Let me check"
- Use the tools to gather actual information about the project
- Format as clean markdown suitable for direct insertion into AGENTS.md`, projectName, cwd)

	// Create a temporary conversation context for the analysis
	originalConversation := a.conversation
	a.conversation = []openai.ChatCompletionMessage{}
	
	// Get LLM analysis
	ctx := context.Background()
	err = a.Chat(ctx, analysisPrompt)
	if err != nil {
		// Restore original conversation and fallback to basic template
		a.conversation = originalConversation
		return a.createBasicAgentsMD(projectName, cwd)
	}
	
	// Extract the analysis from the conversation
	var llmAnalysis string
	if len(a.conversation) >= 2 {
		// Find the assistant's response
		for _, msg := range a.conversation {
			if msg.Role == openai.ChatMessageRoleAssistant && msg.Content != "" {
				llmAnalysis = msg.Content
				break
			}
		}
	}
	
	// Clean up the analysis - remove any conversational elements
	if llmAnalysis != "" {
		// Remove common conversational starters
		conversationalPhrases := []string{
			"I'll analyze this project systematically",
			"Let me analyze this project",
			"First, let me explore",
			"I'll start by",
			"Let me examine",
		}
		
		for _, phrase := range conversationalPhrases {
			if strings.Contains(llmAnalysis, phrase) {
				// Find the first ## heading and start from there
				firstHeading := strings.Index(llmAnalysis, "## ")
				if firstHeading != -1 {
					llmAnalysis = llmAnalysis[firstHeading:]
				}
				break
			}
		}
		
		// Ensure it starts with a proper section
		if !strings.HasPrefix(strings.TrimSpace(llmAnalysis), "## ") {
			llmAnalysis = ""
		}
	}
	
	// Restore original conversation
	a.conversation = originalConversation
	
	if llmAnalysis == "" {
		fmt.Println("⚠️  LLM analysis failed, using basic template")
		return a.createBasicAgentsMD(projectName, cwd)
	}
	
	// Create enhanced AGENTS.md content with LLM analysis
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	content := fmt.Sprintf(`# %s - AI Agent Instructions

## Project Overview
**Project Name:** %s  
**Location:** %s  
**Initialized:** %s  

%s

## AI Agent Instructions

### Permanent Instructions
*Use #command to add permanent instructions for AI agents working on this project*
`, projectName, projectName, cwd, timestamp, llmAnalysis)

	// Write the file
	err = os.WriteFile(agentsFile, []byte(content), 0644)
	if err != nil {
		return fmt.Errorf("error creating AGENTS.md: %v", err)
	}
	
	fmt.Printf("✅ Project initialized with AI analysis!\n")
	fmt.Printf("📄 Created: %s\n", agentsFile)
	fmt.Printf("📂 Project: %s\n", projectName)
	fmt.Printf("🤖 Analysis included project structure and coding standards\n")
	
	return nil
}

func (a *Agent) createBasicAgentsMD(projectName, cwd string) error {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	content := fmt.Sprintf(`# %s - AI Agent Instructions

## Project Overview
**Project Name:** %s  
**Location:** %s  
**Initialized:** %s  

## Project Structure
*Document your project structure and key files here*

## Development Guidelines
*Add project-specific coding standards, patterns, and conventions here*

## AI Agent Instructions

### Permanent Instructions
*Use #command to add permanent instructions for AI agents working on this project*

### Project Context
*Key information about this project that AI agents should know*
`, projectName, projectName, cwd, timestamp)

	return os.WriteFile("AGENTS.md", []byte(content), 0644)
}

func (a *Agent) showHelp() {
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

func (a *Agent) Chat(ctx context.Context, message string) error {
	// Add system message if this is the first message
	if len(a.conversation) == 0 {
		// Load AGENTS.md content for context
		agentsContent := a.loadAgentsMD()
		
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

		a.conversation = append(a.conversation, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: systemPrompt,
		})
	}

	a.conversation = append(a.conversation, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: message,
	})

	for {
		// Get current model name
		currentModel, exists := a.config.Models[a.config.CurrentModel]
		if !exists {
			return fmt.Errorf("current model '%s' not found in configuration", a.config.CurrentModel)
		}

		// Check if context is getting too large and trim if needed
		messages := a.conversation
		if a.lastTokenUsage != nil && a.lastTokenUsage.PromptTokens > 25000 {
			fmt.Printf("⚠️  Context getting large (%d tokens), trimming older messages...\n", a.lastTokenUsage.PromptTokens)
			messages = a.trimContext(a.conversation)
		}

		// Calculate appropriate MaxTokens based on context usage
		// Tool calls (especially edit_file) can generate very long responses, so use more tokens
		maxTokens := 8000
		if a.lastTokenUsage != nil {
			contextTokens := a.lastTokenUsage.PromptTokens
			remainingTokens := 32000 - contextTokens - 1000 // 1k safety buffer
			if remainingTokens < maxTokens {
				maxTokens = remainingTokens
				if maxTokens < 1000 {
					maxTokens = 1000 // Minimum usable response size
				}
			}
		}

		req := openai.ChatCompletionRequest{
			Model:     currentModel.Name,
			Messages:  messages,
			Tools:     a.getToolDefinitions(),
			MaxTokens: maxTokens,
		}


		resp, err := a.client.CreateChatCompletion(ctx, req)
		if err != nil {
			// Check for context overflow or tool calling errors
			errStr := err.Error()
			if strings.Contains(errStr, "tool call") || strings.Contains(errStr, "Failed to parse") || 
			   strings.Contains(errStr, "Unexpected end") || strings.Contains(errStr, "context") ||
			   strings.Contains(errStr, "too long") || strings.Contains(errStr, "maximum") {
				
				fmt.Printf("\n⚠️  Request failed: %v\n", err)
				
				if strings.Contains(errStr, "context") || strings.Contains(errStr, "too long") || 
				   strings.Contains(errStr, "maximum") || a.lastTokenUsage != nil && a.lastTokenUsage.PromptTokens > 6000 {
					fmt.Println("💡 This looks like a context window overflow. Trimming context and retrying...")
					messages = a.trimContext(a.conversation)
					// Update the conversation permanently to the trimmed version
					a.conversation = messages
				} else {
					fmt.Printf("💡 This may be a tool calling format issue with model '%s'.\n", currentModel.Name)
					fmt.Println("   Try switching to a more compatible model with: /models")
				}
				
				// Try with trimmed context and no tools as fallback
				reqFallback := openai.ChatCompletionRequest{
					Model:     currentModel.Name,
					Messages:  messages,
					MaxTokens: 2000, // Reduce max tokens to leave more room
				}
				
				fmt.Println("🔄 Retrying with simplified request...")
				resp, err = a.client.CreateChatCompletion(ctx, reqFallback)
				if err != nil {
					return fmt.Errorf("error calling API (even after fallback): %v", err)
				}
			} else {
				return fmt.Errorf("error calling API: %v", err)
			}
		}

		// Update token usage from API response
		a.lastTokenUsage = &resp.Usage
		a.totalTokensUsed += resp.Usage.TotalTokens

		choice := resp.Choices[0]
		assistantMessage := openai.ChatCompletionMessage{
			Role:      openai.ChatMessageRoleAssistant,
			Content:   choice.Message.Content,
			ToolCalls: choice.Message.ToolCalls,
		}

		a.conversation = append(a.conversation, assistantMessage)

		// Print the assistant's response
		if choice.Message.Content != "" {
			fmt.Print(choice.Message.Content)
		}

		// Check if the response contains tool calls
		if len(choice.Message.ToolCalls) > 0 {
			for _, toolCall := range choice.Message.ToolCalls {
				// Display tool call details
				fmt.Printf("\n🔧 Tool Call: %s\n", toolCall.Function.Name)
				
				var params map[string]interface{}
				if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &params); err != nil {
					fmt.Printf("Error parsing tool parameters: %v\n", err)
					continue
				}
				
				// Show parameters nicely
				for key, value := range params {
					fmt.Printf("   %s: %v\n", key, value)
				}
				
				// Check if this looks like a long-running command
				isLongRunning := false
				if toolCall.Function.Name == "bash_command" {
					if cmdParam, exists := params["command"]; exists {
						if cmdStr, ok := cmdParam.(string); ok {
							isLongRunning = a.isLongRunningCommand(cmdStr)
						}
					}
				}
				
				// Check if this is a folder operation that needs permission
				shouldAutoExecute := false
				if toolCall.Function.Name == "list_files" || toolCall.Function.Name == "read_file" {
					var folderPath string
					if pathParam, exists := params["path"]; exists {
						if pathStr, ok := pathParam.(string); ok {
							if toolCall.Function.Name == "read_file" {
								// For read_file, get the directory of the file
								folderPath = filepath.Dir(pathStr)
							} else {
								// For list_files, use the path directly
								folderPath = pathStr
							}
							
							// Check if folder is already approved
							if a.isFolderApproved(folderPath) {
								shouldAutoExecute = true
							} else {
								// Request permission for this folder
								if !a.requestFolderPermission(folderPath) {
									// Add permission denied result and continue to next tool
									a.conversation = append(a.conversation, openai.ChatCompletionMessage{
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
					if toolCall.Function.Name == "list_files" {
						fmt.Printf("📁 Listing files (auto-approved folder)\n")
					} else if toolCall.Function.Name == "read_file" {
						fmt.Printf("📖 Reading file (auto-approved folder)\n")
					}
				} else {
					// Ask for confirmation for other operations
					prompt := "\n❓ Execute this tool? (Y/n/s to skip/i to interrupt): "
					if isLongRunning {
						fmt.Printf("%s⚠️  This looks like a long-running command!%s\n", ColorYellow, ColorReset)
						prompt = "\n❓ Execute this tool? (Y/n/s to skip/i to interrupt/b for background): "
					}
					
					// Play notification - sound if terminal not in foreground, bell if it is
					go func() {
						// Check if terminal is in foreground on macOS
						cmd := exec.Command("osascript", "-e", `tell application "System Events" to get name of first application process whose frontmost is true`)
						output, err := cmd.Output()
						if err == nil {
							frontmostApp := strings.TrimSpace(string(output))
							// Check if it's a terminal app (Terminal, iTerm2, etc.)
							isTerminalForeground := strings.Contains(frontmostApp, "Terminal") || 
												  strings.Contains(frontmostApp, "iTerm") ||
												  strings.Contains(frontmostApp, "Alacritty") ||
												  strings.Contains(frontmostApp, "Kitty")
							
							if !isTerminalForeground {
								// Terminal not in foreground - play sound
								soundCmd := exec.Command("afplay", "/System/Library/Sounds/Glass.aiff")
								soundCmd.Run()
							}
						}
					}()
					
					// Always show ASCII bell (for taskbar notification)
					fmt.Print("\a")
					fmt.Print(prompt)
					
					inputScanner := bufio.NewScanner(os.Stdin)
					inputScanner.Scan()
					response = strings.ToLower(strings.TrimSpace(inputScanner.Text()))
				}
				
				var result string
				if response == "" || response == "y" || response == "yes" {
					// Execute the tool
					toolFunc, exists := a.tools[toolCall.Function.Name]
					if !exists {
						fmt.Printf("Unknown tool: %s\n", toolCall.Function.Name)
						result = "Error: Unknown tool"
					} else {
						var err error
						result, err = toolFunc(params)
						if err != nil {
							result = fmt.Sprintf("Error: %v", err)
						}
						fmt.Printf("✅ Tool executed successfully\n")
					}
				} else if response == "s" || response == "skip" {
					result = "Tool execution skipped by user"
					fmt.Printf("⏭️  Tool execution skipped\n")
				} else if response == "b" || response == "background" {
					if isLongRunning {
						fmt.Printf("🚀 Starting command in background...\n")
						result = a.bashCommandBackground(params)
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
						a.conversation = append(a.conversation, openai.ChatCompletionMessage{
							Role:       openai.ChatMessageRoleTool,
							Content:    result,
							ToolCallID: toolCall.ID,
						})
						
						// Add the new user message and continue the conversation
						a.conversation = append(a.conversation, openai.ChatCompletionMessage{
							Role:    openai.ChatMessageRoleUser,
							Content: userInstruction,
						})
						
						// Skip adding the result again below
						continue
					} else {
						result = "Tool execution interrupted but no alternative instruction provided"
						fmt.Printf("⚠️  No alternative instruction provided\n")
					}
				} else {
					result = "Tool execution denied by user"
					fmt.Printf("❌ Tool execution denied\n")
				}

				// Add tool result to conversation
				a.conversation = append(a.conversation, openai.ChatCompletionMessage{
					Role:       openai.ChatMessageRoleTool,
					Content:    result,
					ToolCallID: toolCall.ID,
				})
			}
		} else {
			break
		}
	}

	fmt.Println()
	
	// Show token usage info
	if a.lastTokenUsage != nil {
		contextTokens := a.lastTokenUsage.PromptTokens
		responseTokens := a.lastTokenUsage.CompletionTokens
		totalSessionTokens := a.totalTokensUsed
		
		if contextTokens > 0 {
			fmt.Printf("%s[Context: %d tokens | Response: %d tokens | Session: %d tokens]%s\n", 
				ColorBlue, contextTokens, responseTokens, totalSessionTokens, ColorReset)
		}
	}
	
	return nil
}

var completer = readline.NewPrefixCompleter(
	readline.PcItem("/help"),
	readline.PcItem("/init"),
	readline.PcItem("/new"),
	readline.PcItem("/export"),
	readline.PcItem("/models"),
	readline.PcItem("/permissions"),
	readline.PcItem("/exit"),
	readline.PcItem("#"),
)

func main() {
	var rootCmd = &cobra.Command{
		Use:   "mcode-cli",
		Short: "A coding agent CLI built with Go that uses LM Studio locally",
		Run: func(cmd *cobra.Command, args []string) {
			agent := NewAgent()
			ctx := context.Background()

			if len(args) > 0 {
				message := strings.Join(args, " ")
				if err := agent.Chat(ctx, message); err != nil {
					fmt.Printf("Error: %v\n", err)
					os.Exit(1)
				}
			} else {
				// Get current model info for display
				currentModel, exists := agent.config.Models[agent.config.CurrentModel]
				if !exists {
					currentModel = Model{Name: "unknown", BaseURL: "unknown"}
				}
				
				fmt.Printf("MCode CLI - Connected to %s\n", currentModel.BaseURL)
				fmt.Printf("Model: %s (%s)\n", currentModel.Name, agent.config.CurrentModel)
				fmt.Println("Enter your message (type '/help' for commands, '#instruction' for permanent memory, 'exit' to quit):")
				
				// Setup readline with history
				rl, err := readline.NewEx(&readline.Config{
					Prompt:          "> ",
					HistoryFile:     filepath.Join(os.TempDir(), ".mcode_history"),
					AutoComplete:    completer,
					InterruptPrompt: "^C",
					EOFPrompt:       "exit",
				})
				if err != nil {
					fmt.Printf("Error setting up readline: %v\n", err)
					return
				}
				defer rl.Close()

				for {
					// Update prompt with token count
					tokens := agent.getContextTokens()
					if tokens > 0 {
						if tokens >= 1000 {
							rl.SetPrompt(fmt.Sprintf("[%dk tokens] > ", tokens/1000))
						} else {
							rl.SetPrompt(fmt.Sprintf("[%d tokens] > ", tokens))
						}
					} else {
						rl.SetPrompt("> ")
					}
					
					line, err := rl.Readline()
					if err != nil { // io.EOF or interrupt
						break
					}
					
					input := strings.TrimSpace(line)
					if input == "exit" || input == "quit" {
						break
					}
					
					if input == "" {
						continue
					}

					// Handle slash commands
					if strings.HasPrefix(input, "/") {
						shouldExit, err := agent.handleSlashCommand(input)
						if err != nil {
							fmt.Printf("Error: %v\n", err)
						}
						if shouldExit {
							break
						}
						continue
					}

					// Handle permanent instruction commands
					if strings.HasPrefix(input, "#") {
						instruction := strings.TrimSpace(input[1:])
						if instruction == "" {
							fmt.Println("❌ Please provide an instruction after #")
							continue
						}
						
						fmt.Printf("💾 Adding permanent instruction: %s\n", instruction)
						if err := agent.addPermanentInstruction(instruction); err != nil {
							fmt.Printf("Error saving instruction: %v\n", err)
						} else {
							fmt.Printf("✅ Permanent instruction saved to AGENTS.md\n")
						}
						continue
					}

					// Regular chat message
					if err := agent.Chat(ctx, input); err != nil {
						fmt.Printf("Error: %v\n", err)
					}
				}
			}
		},
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}