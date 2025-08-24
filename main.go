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
	"time"

	"github.com/sashabaranov/go-openai"
	"github.com/spf13/cobra"
)

type Agent struct {
	client       *openai.Client
	conversation []openai.ChatCompletionMessage
	tools        map[string]func(map[string]interface{}) (string, error)
}

func NewAgent() *Agent {
	config := openai.DefaultConfig("")
	config.BaseURL = "http://localhost:1234/v1"
	client := openai.NewClientWithConfig(config)

	agent := &Agent{
		client:       client,
		conversation: []openai.ChatCompletionMessage{},
		tools:        make(map[string]func(map[string]interface{}) (string, error)),
	}

	agent.registerTools()
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

	cmd := exec.Command("bash", "-c", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("command failed: %v", err)
	}

	return string(output), nil
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

func (a *Agent) clearContext() {
	a.conversation = []openai.ChatCompletionMessage{}
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
	case "/help":
		a.showHelp()
		return false, nil
	default:
		fmt.Printf("❌ Unknown command: %s\n", parts[0])
		fmt.Println("Available commands: /exit, /init, /new, /help")
		return false, nil
	}
}

func (a *Agent) initializeProject() error {
	fmt.Println("🚀 Initializing project...")
	
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
	
	// Create AGENTS.md content
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	content := fmt.Sprintf(`# %s - AI Agent Documentation

## Project Overview
**Project Name:** %s  
**Location:** %s  
**Initialized:** %s  

## AI Agent Configuration

### Primary Agent
- **Type:** MCode CLI
- **Model:** lmstudio-community/qwen3-coder-30b-a3b-instruct-mlx@8bit
- **Local Server:** http://localhost:1234
- **Tools Available:**
  - 📖 **read_file** - Read file contents
  - 📁 **list_files** - List directory contents
  - ⚡ **bash_command** - Execute shell commands
  - ✏️ **edit_file** - Create/modify files
  - 🔍 **search_code** - Search for code patterns

### Agent Capabilities
- Code generation and modification
- File system operations
- Command execution with user confirmation
- Interactive development assistance
- Code analysis and refactoring

### Usage Instructions
1. Start the agent: mcode-cli
2. Use natural language to request coding tasks
3. Review and approve tool executions when prompted
4. Use slash commands for special operations:
   - /init - Initialize project documentation
   - /exit - Exit the agent
   - /help - Show available commands

### Project Structure
*To be updated as the project evolves*

### Agent Interaction History
*Key decisions and modifications made by the AI agent will be documented here*

---
*This file was generated by MCode CLI on %s*
`, projectName, projectName, cwd, timestamp, timestamp)

	// Write the file
	err = os.WriteFile(agentsFile, []byte(content), 0644)
	if err != nil {
		return fmt.Errorf("error creating AGENTS.md: %v", err)
	}
	
	fmt.Printf("✅ Project initialized successfully!\n")
	fmt.Printf("📄 Created: %s\n", agentsFile)
	fmt.Printf("📂 Project: %s\n", projectName)
	
	return nil
}

func (a *Agent) showHelp() {
	fmt.Println("\n🤖 MCode CLI - Help")
	fmt.Println("========================")
	fmt.Println()
	fmt.Println("Slash Commands:")
	fmt.Println("  /init   - Initialize project and create AGENTS.md")
	fmt.Println("  /new    - Clear conversation context (start fresh)")
	fmt.Println("  /exit   - Exit the agent")
	fmt.Println("  /help   - Show this help message")
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
	fmt.Println("  - Review and approve tool executions (y/n/s)")
	fmt.Println("  - Use slash commands for special operations")
	fmt.Println("  - Use /new to start a fresh conversation")
	fmt.Println()
}

func (a *Agent) Chat(ctx context.Context, message string) error {
	// Add system message if this is the first message
	if len(a.conversation) == 0 {
		a.conversation = append(a.conversation, openai.ChatCompletionMessage{
			Role: openai.ChatMessageRoleSystem,
			Content: `You are a helpful coding agent. You have access to tools that allow you to:
- Read and write files
- Execute bash commands  
- List directory contents
- Search for code patterns

Use these tools to help the user with their coding tasks. Always be clear about what you're doing and why.`,
		})
	}

	a.conversation = append(a.conversation, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: message,
	})

	for {
		req := openai.ChatCompletionRequest{
			Model:     "lmstudio-community/qwen3-coder-30b-a3b-instruct-mlx@8bit",
			Messages:  a.conversation,
			Tools:     a.getToolDefinitions(),
			MaxTokens: 4000,
		}

		resp, err := a.client.CreateChatCompletion(ctx, req)
		if err != nil {
			return fmt.Errorf("error calling API: %v", err)
		}

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
				
				// Ask for confirmation
				fmt.Print("\n❓ Execute this tool? (Y/n/s to skip): ")
				scanner := bufio.NewScanner(os.Stdin)
				scanner.Scan()
				response := strings.ToLower(strings.TrimSpace(scanner.Text()))
				
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
	return nil
}

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
				fmt.Println("MCode CLI - Connected to LM Studio at http://localhost:1234")
				fmt.Println("Model: lmstudio-community/qwen3-coder-30b-a3b-instruct-mlx@8bit")
				fmt.Println("Enter your message (type '/help' for commands, 'exit' to quit):")
				
				scanner := bufio.NewScanner(os.Stdin)
				for {
					fmt.Print("> ")
					if !scanner.Scan() {
						break
					}
					
					input := strings.TrimSpace(scanner.Text())
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