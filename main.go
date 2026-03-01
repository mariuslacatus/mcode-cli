package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"coding-agent/pkg/agent"
	"coding-agent/pkg/commands"
	"coding-agent/pkg/project"
	"coding-agent/pkg/types"
	"coding-agent/pkg/ui"

	"golang.org/x/term"

	"github.com/chzyer/readline"
)

var completer = readline.NewPrefixCompleter(
	readline.PcItem("/help"),
	readline.PcItem("/init"),
	readline.PcItem("/new"),
	readline.PcItem("/export"),
	readline.PcItem("/models"),
	readline.PcItem("/permissions"),
	readline.PcItem("/compact"),
	readline.PcItem("/exit"),
	readline.PcItem("/save"),
	readline.PcItem("/resume"),
	readline.PcItem("/conv"),
	readline.PcItem("/del"),
	readline.PcItem("#"),
)

// getTerminalHeight returns the current terminal height
func getTerminalHeight() int {
	_, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 24 // Fallback
	}
	return height
}

func main() {
	// Create agent instance
	ag := agent.New()
	ctx := context.Background()

	// Create managers
	projectManager := project.NewManager(ag)
	commandHandler := commands.NewHandler(ag, projectManager)

	// Check if we have command line arguments for single command mode
	if len(os.Args) > 1 {
		// Join all arguments as the message
		message := strings.Join(os.Args[1:], " ")

		// Get current model info for display
		currentModel, exists := ag.Config.Models[ag.Config.CurrentModel]
		if !exists {
			currentModel = types.Model{Name: "unknown", BaseURL: "unknown"}
		}

		fmt.Printf("MCode CLI - Connected to %s\n", currentModel.BaseURL)
		fmt.Printf("Model: %s (%s)\n", currentModel.Name, ag.Config.CurrentModel)
		fmt.Printf("Query: %s\n\n", message)

		// Execute the single command and exit
		if err := agent.Chat(ag, ctx, message); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Clear terminal on startup for interactive mode
	fmt.Print("\033[2J\033[H")

	// Get current model info for display
	currentModel, exists := ag.Config.Models[ag.Config.CurrentModel]
	if !exists {
		currentModel = types.Model{Name: "unknown", BaseURL: "unknown"}
	}

	fmt.Printf("MCode CLI - Connected to %s\n", currentModel.BaseURL)
	fmt.Printf("Model: %s (%s)\n", currentModel.Name, ag.Config.CurrentModel)
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
		// Update status display
		agent.UpdateStatusDisplay(ag)

		// Update prompt with model and token count
		tokens := agent.GetContextTokens(ag)
		modelName := ag.Config.CurrentModel

		prompt := fmt.Sprintf("[%s] > ", modelName)
		if tokens > 0 {
			if tokens >= 1000 {
				prompt = fmt.Sprintf("[%s | %.1fk] > ", modelName, float64(tokens)/1000.0)
			} else {
				prompt = fmt.Sprintf("[%s | %d] > ", modelName, tokens)
			}
		}
		rl.SetPrompt(prompt)

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
			shouldExit, err := commandHandler.Handle(input)
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
			if err := projectManager.AddPermanentInstruction(instruction); err != nil {
				fmt.Printf("Error saving instruction: %v\n", err)
			} else {
				fmt.Printf("✅ Permanent instruction saved to AGENTS.md\n")
			}
			continue
		}

		// Regular chat message
		if err := agent.Chat(ag, ctx, input); err != nil {
			if errors.Is(err, ui.ErrInterrupted) {
				fmt.Println("\n❌ Operation cancelled")
			} else {
				fmt.Printf("Error: %v\n", err)
			}
		}
	}
}
