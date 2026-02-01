package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"coding-agent/pkg/agent"
	"coding-agent/pkg/commands"
	"coding-agent/pkg/project"
	"coding-agent/pkg/types"
	"os/signal"
	"syscall"

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
	// Clear terminal on startup for interactive mode
	fmt.Print("\033[2J\033[H")

	// Setup sticky header
	// 1. Set scrolling region to start at line 2 (preserve line 1)
	fmt.Print("\033[2;r")
	// 2. Move cursor to line 2
	fmt.Print("\033[2;1H")

	// Handle window resize (SIGWINCH) to reset scrolling region
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGWINCH)
	go func() {
		for range c {
			// Re-apply scrolling region on resize
			fmt.Print("\033[2;r")
			// Redraw status immediately
			agent.UpdateStatusDisplay(ag)
		}
	}()

	// Ensure we reset terminal on exit
	defer fmt.Print("\033[r")

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

		// Update prompt with token count
		tokens := agent.GetContextTokens(ag)
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
			fmt.Printf("Error: %v\n", err)
		}
	}
}
