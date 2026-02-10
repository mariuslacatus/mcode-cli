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
	
	width int
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
	if m.content.Len() > 0 {
		rendered, err := m.renderer.Render(m.content.String())
		if err == nil {
			view.WriteString(rendered)
		} else {
			view.WriteString(m.content.String())
		}
	}
	
	// Render Tool Calls / Spinner at bottom
	if m.showingSpinner && !m.finished {
		// Add spacing if we have content
		if m.content.Len() > 0 && !strings.HasSuffix(view.String(), "\n") {
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