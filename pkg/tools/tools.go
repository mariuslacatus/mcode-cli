package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"

	"coding-agent/pkg/types"
	"github.com/sashabaranov/go-openai"
)

// Manager handles tool registration and execution
type Manager struct {
	agent *types.Agent
}

// NewManager creates a new tool manager
func NewManager(agent *types.Agent) *Manager {
	return &Manager{agent: agent}
}

// RegisterTools registers all available tools
func (m *Manager) RegisterTools() {
	m.agent.Tools["read_file"] = m.ReadFile
	m.agent.Tools["list_files"] = m.ListFiles
	m.agent.Tools["bash_command"] = m.BashCommand
	m.agent.Tools["edit_file"] = m.EditFile
	m.agent.Tools["search_code"] = m.SearchCode
}

// GetToolDefinitions returns OpenAI tool definitions
func (m *Manager) GetToolDefinitions() []openai.Tool {
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
				Description: "Perform incremental edits to a file using find-and-replace. MUCH FASTER than full file rewrites. For new files, use newString only. For edits, use oldString+newString.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"filePath": map[string]interface{}{
							"type":        "string",
							"description": "The absolute path to the file to modify",
						},
						"oldString": map[string]interface{}{
							"type":        "string",
							"description": "The text to replace (for editing existing files). Supports fuzzy matching for whitespace differences.",
						},
						"newString": map[string]interface{}{
							"type":        "string",
							"description": "The replacement text. For new files, provide this without oldString. For edits, use with oldString.",
						},
						"replaceAll": map[string]interface{}{
							"type":        "boolean",
							"description": "Replace all occurrences of oldString (default: false - only replace if unique match)",
						},
					},
					"required": []string{"filePath", "newString"},
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

// ReadFile reads the contents of a file
func (m *Manager) ReadFile(params map[string]interface{}) (string, error) {
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

// ListFiles lists files in a directory
func (m *Manager) ListFiles(params map[string]interface{}) (string, error) {
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

// BashCommand executes a bash command
func (m *Manager) BashCommand(params map[string]interface{}) (string, error) {
	command, ok := params["command"].(string)
	if !ok {
		return "", fmt.Errorf("command parameter is required")
	}

	// Create a context with timeout (default 30 seconds for most commands)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fmt.Printf("%sExecuting: %s%s\n", types.ColorYellow, command, types.ColorReset)
	fmt.Printf("%s(Press Ctrl+C to interrupt if it hangs)%s\n", types.ColorBlue, types.ColorReset)

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

// BashCommandWithTimeout executes a bash command with a custom timeout
func (m *Manager) BashCommandWithTimeout(params map[string]interface{}, timeout time.Duration) string {
	command, ok := params["command"].(string)
	if !ok {
		return "Error: command parameter is required"
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	fmt.Printf("%sExecuting: %s%s\n", types.ColorYellow, command, types.ColorReset)

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

// BashCommandBackground executes a bash command in the background
func (m *Manager) BashCommandBackground(params map[string]interface{}) string {
	command, ok := params["command"].(string)
	if !ok {
		return "Error: command parameter is required"
	}

	fmt.Printf("%sStarting in background: %s%s\n", types.ColorYellow, command, types.ColorReset)

	cmd := exec.Command("bash", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Start the command without waiting for it to complete
	err := cmd.Start()
	if err != nil {
		return fmt.Sprintf("Failed to start command in background: %v", err)
	}

	return fmt.Sprintf("Command started in background with PID %d. Use 'ps aux | grep \"%s\"' to check status.", cmd.Process.Pid, command)
}

// EditFile creates or edits a file using OpenCode-compatible parameters.
func (m *Manager) EditFile(params map[string]interface{}) (string, error) {
	filePath, ok := params["filePath"].(string)
	if !ok {
		return "", fmt.Errorf("filePath parameter is required")
	}

	// Check for incremental edit (oldString + newString)
	if oldStringParam, exists := params["oldString"]; exists {
		oldString, ok := oldStringParam.(string)
		if !ok {
			return "", fmt.Errorf("oldString parameter must be a string")
		}

		newString, ok := params["newString"].(string)
		if !ok {
			return "", fmt.Errorf("newString parameter is required when using oldString")
		}

		replaceAll := false
		if replaceAllParam, exists := params["replaceAll"]; exists {
			if replaceAllBool, ok := replaceAllParam.(bool); ok {
				replaceAll = replaceAllBool
			}
		}

		// Perform incremental edit
		result, err := m.performIncrementalEdit(filePath, oldString, newString, replaceAll)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("🚀🚀🚀 INCREMENTAL EDIT MODE (FAST!) 🚀🚀🚀\n%s", result), nil
	}

	// For new file creation, use newString parameter
	if newStringParam, exists := params["newString"]; exists {
		newString, ok := newStringParam.(string)
		if !ok {
			return "", fmt.Errorf("newString parameter must be a string")
		}

		// Create new file (oldString = "")
		result, err := m.performIncrementalEdit(filePath, "", newString, false)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("🚀🚀🚀 NEW FILE CREATION (FAST!) 🚀🚀🚀\n%s", result), nil
	}

	// Fallback: if content parameter exists, use full replacement
	if contentParam, exists := params["content"]; exists {
		content, ok := contentParam.(string)
		if !ok {
			return "", fmt.Errorf("content parameter must be a string")
		}

		result, err := m.performFullFileEdit(filePath, content)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("📄📄📄 FULL REPLACEMENT MODE (SLOW!) 📄📄📄\n%s", result), nil
	}

	return "", fmt.Errorf("either newString (for new files) or oldString+newString (for edits) or content (for full replacement) must be provided")
}

// performIncrementalEdit handles incremental file editing
func (m *Manager) performIncrementalEdit(path, oldString, newString string, replaceAll bool) (string, error) {
	// Handle new file creation (empty oldString)
	if oldString == "" {
		err := os.WriteFile(path, []byte(newString), 0644)
		if err != nil {
			return "", fmt.Errorf("error creating file: %v", err)
		}
		return fmt.Sprintf("File %s has been created", path), nil
	}

	// Read existing content
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("error reading file: %v", err)
	}

	oldContent := string(content)

	// Perform incremental replacement
	newContent, err := ReplaceInContent(oldContent, oldString, newString, replaceAll)
	if err != nil {
		return "", fmt.Errorf("replacement failed: %v", err)
	}

	// Write the updated content
	err = os.WriteFile(path, []byte(newContent), 0644)
	if err != nil {
		return "", fmt.Errorf("error writing file: %v", err)
	}

	// Generate and return a focused diff
	diff := GenerateFocusedDiff(oldContent, newContent, path, oldString, newString)
	return diff, nil
}

// performFullFileEdit handles full file replacement (original behavior)
func (m *Manager) performFullFileEdit(path, content string) (string, error) {
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

	// Return appropriate message without showing diff again
	if oldContent == "" {
		return fmt.Sprintf("File %s has been created", path), nil
	} else if oldContent != content {
		return fmt.Sprintf("File %s has been modified", path), nil
	}

	return fmt.Sprintf("File %s unchanged", path), nil
}

// PreviewEdit shows what changes would be made to a file without writing
func (m *Manager) PreviewEdit(params map[string]interface{}) (string, error) {
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

	// Generate and display diff
	if oldContent != content {
		diff := GenerateDiff(oldContent, content, path)
		fmt.Print(diff)
		if oldContent == "" {
			return fmt.Sprintf("Preview: Would create new file %s", path), nil
		}
		return fmt.Sprintf("Preview: Would modify file %s", path), nil
	}

	return fmt.Sprintf("Preview: No changes would be made to %s", path), nil
}

// SearchCode searches for code patterns in files
func (m *Manager) SearchCode(params map[string]interface{}) (string, error) {
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

// IsLongRunningCommand checks if a command is likely to be long-running
func IsLongRunningCommand(command string) bool {
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

// Replacer represents a strategy for finding strings to replace
type Replacer interface {
	FindMatches(content, find string) []string
}

// SimpleReplacer finds exact string matches
type SimpleReplacer struct{}

func (r *SimpleReplacer) FindMatches(content, find string) []string {
	if strings.Contains(content, find) {
		return []string{find}
	}
	return []string{}
}

// LineTrimmedReplacer finds matches by comparing trimmed lines
type LineTrimmedReplacer struct{}

func (r *LineTrimmedReplacer) FindMatches(content, find string) []string {
	originalLines := strings.Split(content, "\n")
	searchLines := strings.Split(find, "\n")

	// Remove trailing empty line if present
	if len(searchLines) > 0 && searchLines[len(searchLines)-1] == "" {
		searchLines = searchLines[:len(searchLines)-1]
	}

	for i := 0; i <= len(originalLines)-len(searchLines); i++ {
		matches := true

		for j := 0; j < len(searchLines); j++ {
			originalTrimmed := strings.TrimSpace(originalLines[i+j])
			searchTrimmed := strings.TrimSpace(searchLines[j])

			if originalTrimmed != searchTrimmed {
				matches = false
				break
			}
		}

		if matches {
			// Calculate the actual substring in the original content
			matchStart := 0
			for k := 0; k < i; k++ {
				matchStart += len(originalLines[k]) + 1 // +1 for newline
			}

			matchEnd := matchStart
			for k := 0; k < len(searchLines); k++ {
				matchEnd += len(originalLines[i+k])
				if k < len(searchLines)-1 {
					matchEnd += 1 // Add newline character except for the last line
				}
			}

			actualMatch := content[matchStart:matchEnd]
			return []string{actualMatch}
		}
	}

	return []string{}
}

// WhitespaceNormalizedReplacer normalizes whitespace before matching
type WhitespaceNormalizedReplacer struct{}

func (r *WhitespaceNormalizedReplacer) FindMatches(content, find string) []string {
	normalizeWhitespace := func(text string) string {
		return regexp.MustCompile(`\s+`).ReplaceAllString(strings.TrimSpace(text), " ")
	}

	normalizedFind := normalizeWhitespace(find)
	var matches []string

	// Handle single line matches
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if normalizeWhitespace(line) == normalizedFind {
			matches = append(matches, line)
		}
	}

	// Handle multi-line matches
	findLines := strings.Split(find, "\n")
	if len(findLines) > 1 {
		for i := 0; i <= len(lines)-len(findLines); i++ {
			block := strings.Join(lines[i:i+len(findLines)], "\n")
			if normalizeWhitespace(block) == normalizedFind {
				matches = append(matches, block)
			}
		}
	}

	return matches
}

// IndentationFlexibleReplacer ignores indentation differences
type IndentationFlexibleReplacer struct{}

func (r *IndentationFlexibleReplacer) FindMatches(content, find string) []string {
	removeIndentation := func(text string) string {
		lines := strings.Split(text, "\n")
		nonEmptyLines := []string{}
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				nonEmptyLines = append(nonEmptyLines, line)
			}
		}

		if len(nonEmptyLines) == 0 {
			return text
		}

		minIndent := len(nonEmptyLines[0]) // Start with max possible
		for _, line := range nonEmptyLines {
			leadingSpaces := 0
			for _, char := range line {
				if char == ' ' || char == '\t' {
					leadingSpaces++
				} else {
					break
				}
			}
			if leadingSpaces < minIndent {
				minIndent = leadingSpaces
			}
		}

		result := []string{}
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				result = append(result, line)
			} else if len(line) > minIndent {
				result = append(result, line[minIndent:])
			} else {
				result = append(result, line)
			}
		}

		return strings.Join(result, "\n")
	}

	normalizedFind := removeIndentation(find)
	contentLines := strings.Split(content, "\n")
	findLines := strings.Split(find, "\n")

	var matches []string
	for i := 0; i <= len(contentLines)-len(findLines); i++ {
		block := strings.Join(contentLines[i:i+len(findLines)], "\n")
		if removeIndentation(block) == normalizedFind {
			matches = append(matches, block)
		}
	}

	return matches
}

// ReplaceInContent performs string replacement using multiple strategies
func ReplaceInContent(content, oldString, newString string, replaceAll bool) (string, error) {
	if oldString == newString {
		return "", fmt.Errorf("oldString and newString must be different")
	}

	replacers := []Replacer{
		&SimpleReplacer{},
		&LineTrimmedReplacer{},
		&WhitespaceNormalizedReplacer{},
		&IndentationFlexibleReplacer{},
	}

	for _, replacer := range replacers {
		matches := replacer.FindMatches(content, oldString)
		for _, match := range matches {
			// Check if the match appears only once (unless replaceAll is true)
			if !replaceAll {
				firstIndex := strings.Index(content, match)
				lastIndex := strings.LastIndex(content, match)
				if firstIndex != lastIndex {
					continue // Multiple occurrences, skip this match
				}
			}

			// Perform the replacement
			if replaceAll {
				return strings.ReplaceAll(content, match, newString), nil
			}
			return strings.Replace(content, match, newString, 1), nil
		}
	}

	return "", fmt.Errorf("oldString not found in content or multiple ambiguous matches found")
}

// GenerateFocusedDiff generates a diff focused around the changed area
func GenerateFocusedDiff(oldContent, newContent, filename, oldString, newString string) string {
	var result strings.Builder
	result.WriteString(fmt.Sprintf("📝 Incremental edit applied to: %s\n", filename))
	result.WriteString("🔄 Changes:\n")
	result.WriteString(fmt.Sprintf("  - Removed: %q\n", truncateString(oldString, 80)))
	result.WriteString(fmt.Sprintf("  + Added: %q\n", truncateString(newString, 80)))

	// Always show the context diff for incremental edits
	result.WriteString("\n" + GenerateDiff(oldContent, newContent, filename))

	return result.String()
}

// truncateString truncates a string to maxLength with ellipsis
func truncateString(s string, maxLength int) string {
	if len(s) <= maxLength {
		return s
	}
	return s[:maxLength-3] + "..."
}