package tui

import (
	"fmt"
	"strings"
	"time"

	"coding-agent/pkg/markdown"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sashabaranov/go-openai"
)

// Messages used for communication between agent and TUI

type StreamContentMsg string

type StreamToolMsg struct {
	ToolCalls []openai.ToolCall
}

type StreamDoneMsg struct {
	Err         error
	FullContent string
	ToolCalls   []openai.ToolCall
}

type spinnerTickMsg time.Time

// StreamModel handles the UI for streaming responses
type StreamModel struct {
	content     *strings.Builder
	renderer    *markdown.Renderer
	toolCalls   []openai.ToolCall
	updates     chan interface{}
	finished    bool
	err         error
	
	// Spinner state
	spinnerIndex int
	showingSpinner bool
	
	width  int
	height int
}

func NewStreamModel(updates chan interface{}) *StreamModel {
	renderer, _ := markdown.NewNoMarginTermRenderer()
	return &StreamModel{
		content:  &strings.Builder{},
		updates:  updates,
		renderer: renderer,
	}
}

func (m *StreamModel) Init() tea.Cmd {
	return tea.Batch(
		waitForUpdate(m.updates),
		tickSpinner(),
	)
}

func (m *StreamModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case StreamContentMsg:
		m.content.WriteString(string(msg))
		return m, waitForUpdate(m.updates)

	case StreamToolMsg:
		m.toolCalls = msg.ToolCalls
		m.showingSpinner = true
		return m, waitForUpdate(m.updates)

	case StreamDoneMsg:
		m.finished = true
		m.err = msg.Err
		return m, tea.Quit

	case spinnerTickMsg:
		if m.showingSpinner {
			m.spinnerIndex++
		}
		return m, tickSpinner()
		
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			m.err = fmt.Errorf("interrupted by user")
			return m, tea.Quit
		}
	}

	return m, nil
}

func (m *StreamModel) View() string {
	if m.err != nil {
		return fmt.Sprintf("\n  Error: %v\n", m.err)
	}

	var view strings.Builder

	// Render Markdown Content
	rendered := ""
	if m.content.Len() > 0 {
		var err error
		rendered, err = m.renderer.Render(m.content.String())
		if err != nil {
			rendered = m.content.String()
		}
	}
	
	if rendered != "" {
		lines := strings.Split(strings.TrimSpace(rendered), "\n")
		
		// If content is taller than terminal, truncate the top to avoid duplication
		// We leave room for the spinner and a small margin (height - 4)
		displayLimit := m.height - 4
		if displayLimit < 5 {
			displayLimit = 5 // Minimum fallback
		}

		if len(lines) > displayLimit {
			hiddenCount := len(lines) - displayLimit
			view.WriteString(fmt.Sprintf("\033[90m... (%d lines above)\033[0m\n", hiddenCount))
			view.WriteString(strings.Join(lines[hiddenCount:], "\n"))
		} else {
			view.WriteString(strings.Join(lines, "\n"))
		}
	}
	
	// Render Tool Calls / Spinner at bottom
	if m.showingSpinner && !m.finished {
		// Add spacing if we have content
		if view.Len() > 0 && !strings.HasSuffix(view.String(), "\n") {
			view.WriteString("\n")
		}
		spinnerChars := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		spinner := spinnerChars[m.spinnerIndex%len(spinnerChars)]
		view.WriteString(fmt.Sprintf("\n %s Processing tool calls...", spinner))
	}

	return view.String()
}

func (m *StreamModel) Content() string {
	return m.content.String()
}

func (m *StreamModel) ToolCalls() []openai.ToolCall {
	return m.toolCalls
}

func (m *StreamModel) Err() error {
	return m.err
}

// Commands

func waitForUpdate(sub chan interface{}) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-sub
		if !ok {
			return nil
		}
		
		switch val := msg.(type) {
		case StreamContentMsg:
			return val
		case StreamToolMsg:
			return val
		case StreamDoneMsg:
			return val
		default:
			return nil
		}
	}
}

func tickSpinner() tea.Cmd {
	return tea.Tick(time.Millisecond*100, func(t time.Time) tea.Msg {
		return spinnerTickMsg(t)
	})
}
