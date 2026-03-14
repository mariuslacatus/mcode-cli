package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"coding-agent/pkg/types"

	"github.com/mitchellh/mapstructure"
	"github.com/sashabaranov/go-openai"
)

// Manager handles tool registration and execution
type Manager struct {
	agent *types.Agent
	tools map[string]Tool
}

// NewManager creates a new tool manager
func NewManager(agent *types.Agent) *Manager {
	m := &Manager{
		agent: agent,
		tools: make(map[string]Tool),
	}
	return m
}

// UnmarshalParams is a helper to convert map[string]interface{} into a structured struct.
// It uses mapstructure with WeaklyTypedInput to handle cases where LLMs send numbers as strings.
func (m *Manager) UnmarshalParams(params map[string]interface{}, target interface{}) error {
	config := &mapstructure.DecoderConfig{
		Metadata:         nil,
		Result:           target,
		WeaklyTypedInput: true, // This is the key: converts strings to ints, etc.
		TagName:          "json",
	}

	decoder, err := mapstructure.NewDecoder(config)
	if err != nil {
		return fmt.Errorf("failed to create decoder: %v", err)
	}

	if err := decoder.Decode(params); err != nil {
		return fmt.Errorf("failed to unmarshal into %T: %v", target, err)
	}
	return nil
}

// RegisterTools registers all available tools
func (m *Manager) RegisterTools() {
	m.addTool(&ReadFileTool{})
	m.addTool(&ListFilesTool{})
	m.addTool(&BashCommandTool{})
	m.addTool(&EditFileTool{})
	m.addTool(&WriteFileTool{})
	m.addTool(&SearchCodeTool{})
	m.addTool(&WebSearchTool{})
	m.addTool(&WebFetchTool{})

	// Maintain the old map for now to avoid breaking types.Agent if it's used elsewhere
	for name, tool := range m.tools {
		m.agent.Tools[name] = func(params map[string]interface{}) (string, error) {
			return tool.Execute(context.Background(), params)
		}
	}
}

func (m *Manager) addTool(tool Tool) {
	// Initialize the tool with manager reference
	switch t := tool.(type) {
	case *ReadFileTool:
		t.manager = m
	case *ListFilesTool:
		t.manager = m
	case *BashCommandTool:
		t.manager = m
	case *EditFileTool:
		t.manager = m
	case *WriteFileTool:
		t.manager = m
	case *SearchCodeTool:
		t.manager = m
	case *WebSearchTool:
		t.manager = m
	case *WebFetchTool:
		t.manager = m
	}
	m.tools[tool.Name()] = tool
}

// GetTool returns a tool by name
func (m *Manager) GetTool(name string) (Tool, bool) {
	tool, ok := m.tools[name]
	return tool, ok
}

// GetToolDefinitions returns OpenAI tool definitions
func (m *Manager) GetToolDefinitions() []openai.Tool {
	var definitions []openai.Tool
	for _, tool := range m.tools {
		definitions = append(definitions, tool.Definition())
	}
	return definitions
}

// GetPreview returns a preview of the changes a tool would make
func (m *Manager) GetPreview(name string, params map[string]interface{}) (string, error) {
	if tool, ok := m.tools[name]; ok {
		return tool.Preview(params)
	}
	return "", nil
}

// GetDisplayInfo returns UI display info for a tool call
func (m *Manager) GetDisplayInfo(name string, params map[string]interface{}) string {
	if tool, ok := m.tools[name]; ok {
		return tool.GetDisplayInfo(params)
	}
	return ""
}

// BashCommandBackground executes a bash command in the background
func (m *Manager) BashCommandBackground(params map[string]interface{}) string {
	var args BashCommandArgs
	if err := m.UnmarshalParams(params, &args); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	if args.Command == "" {
		return "Error: command parameter is required"
	}

	fmt.Printf("%sStarting in background: %s%s\n", types.ColorYellow, args.Command, types.ColorReset)

	cmd := exec.Command("bash", "-c", args.Command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Start the command without waiting for it to complete
	err := cmd.Start()
	if err != nil {
		return fmt.Sprintf("Failed to start command in background: %v", err)
	}

	return fmt.Sprintf("Command started in background with PID %d. Use 'ps aux | grep \"%s\"' to check status.", cmd.Process.Pid, args.Command)
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

// performIncrementalEdit handles incremental file editing
func (m *Manager) performIncrementalEdit(path, oldString, newString string, replaceAll bool) (string, error) {
	// Ensure parent directories exist
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("error creating directories: %v", err)
	}

	if oldString == "" {
		err := os.WriteFile(path, []byte(newString), 0644)
		if err != nil {
			return "", fmt.Errorf("error creating file: %v", err)
		}
		return fmt.Sprintf("File %s has been created", path) + "\n" + truncatePreview(newString, 200), nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("error reading file: %v", err)
	}

	oldContent := string(content)
	newContent, err := ReplaceInContent(oldContent, oldString, newString, replaceAll)
	if err != nil {
		return "", fmt.Errorf("replacement failed: %v", err)
	}

	err = os.WriteFile(path, []byte(newContent), 0644)
	if err != nil {
		return "", fmt.Errorf("error writing file: %v", err)
	}

	return GenerateFocusedDiff(oldContent, newContent, path, oldString, newString), nil
}

// GenerateFocusedDiff generates a diff focused around the changed area
func GenerateFocusedDiff(oldContent, newContent, filename, oldString, newString string) string {
	return GenerateDiff(oldContent, newContent, filename)
}

// truncatePreview truncates content for display in the response
func truncatePreview(content string, maxLength int) string {
	if len(content) <= maxLength {
		return content
	}
	return content[:maxLength-3] + "..."
}
