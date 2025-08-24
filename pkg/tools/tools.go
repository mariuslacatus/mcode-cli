package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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

// EditFile creates or edits a file
func (m *Manager) EditFile(params map[string]interface{}) (string, error) {
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
		diff := GenerateDiff(oldContent, content, path)
		fmt.Print(diff)
		return fmt.Sprintf("File %s has been modified", path), nil
	} else if oldContent == "" {
		fmt.Printf("%s📄 Created new file: %s%s\n", types.ColorGreen, path, types.ColorReset)
		return fmt.Sprintf("File %s has been created", path), nil
	}

	return fmt.Sprintf("File %s unchanged", path), nil
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