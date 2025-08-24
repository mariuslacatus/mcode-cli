package project

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"coding-agent/pkg/types"
	"github.com/sashabaranov/go-openai"
)

// Manager handles project operations
type Manager struct {
	agent *types.Agent
}

// NewManager creates a new project manager
func NewManager(agent *types.Agent) *Manager {
	return &Manager{agent: agent}
}

// LoadAgentsMD loads AGENTS.md content if it exists
func (m *Manager) LoadAgentsMD() string {
	agentsFile := "AGENTS.md"
	content, err := os.ReadFile(agentsFile)
	if err != nil {
		return ""
	}
	return string(content)
}

// LoadProjectContext loads project context into agent conversation
func (m *Manager) LoadProjectContext() {
	agentsFile := "AGENTS.md"
	if content, err := os.ReadFile(agentsFile); err == nil {
		// Add AGENTS.md content as system context
		agentsContent := string(content)
		if strings.TrimSpace(agentsContent) != "" {
			systemMsg := openai.ChatCompletionMessage{
				Role:    "system",
				Content: "Project context from AGENTS.md:\n\n" + agentsContent,
			}
			m.agent.Conversation = append(m.agent.Conversation, systemMsg)
		}
	}
}

// AddPermanentInstruction adds an instruction to AGENTS.md
func (m *Manager) AddPermanentInstruction(instruction string) error {
	agentsFile := "AGENTS.md"

	// Read current content
	var content string
	if existingContent, err := os.ReadFile(agentsFile); err == nil {
		content = string(existingContent)
	} else {
		// If AGENTS.md doesn't exist, create a basic one first
		fmt.Println("📄 AGENTS.md not found, creating with basic template...")
		if err := m.CreateBasicAgentsMD(filepath.Base("."), "."); err != nil {
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

// ExportContext exports conversation context to a file
func (m *Manager) ExportContext(parts []string) error {
	if len(m.agent.Conversation) == 0 {
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

	if m.agent.LastTokenUsage != nil {
		content.WriteString(fmt.Sprintf("Context Tokens: %d\n", m.agent.LastTokenUsage.PromptTokens))
		content.WriteString(fmt.Sprintf("Total Session Tokens: %d\n", m.agent.TotalTokensUsed))
	}

	content.WriteString("\n" + strings.Repeat("=", 80) + "\n\n")

	for i, msg := range m.agent.Conversation {
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
	content.WriteString(fmt.Sprintf("End of context export (%d messages)\n", len(m.agent.Conversation)))

	// Write to file
	err := os.WriteFile(filename, []byte(content.String()), 0644)
	if err != nil {
		return fmt.Errorf("failed to write export file: %v", err)
	}

	fmt.Printf("✅ Context exported successfully!\n")
	fmt.Printf("📄 File: %s\n", filename)
	fmt.Printf("📊 Messages: %d\n", len(m.agent.Conversation))
	if m.agent.LastTokenUsage != nil {
		fmt.Printf("🔢 Context tokens: %d\n", m.agent.LastTokenUsage.PromptTokens)
	}

	return nil
}

// Initialize initializes a new project with AGENTS.md
func (m *Manager) Initialize() error {
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

	// For now, use basic template - LLM analysis can be added later
	llmAnalysis := ""

	if llmAnalysis == "" {
		fmt.Println("⚠️  Using basic template - LLM analysis can be implemented later")
		return m.CreateBasicAgentsMD(projectName, cwd)
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

// CreateBasicAgentsMD creates a basic AGENTS.md template
func (m *Manager) CreateBasicAgentsMD(projectName, cwd string) error {
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