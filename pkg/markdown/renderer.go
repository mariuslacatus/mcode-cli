package markdown

import (
	"os"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"golang.org/x/term"
)

// Render renders the markdown content using glamour
func Render(content string) (string, error) {
	r, err := NewTermRenderer()
	if err != nil {
		return "", err
	}

	return r.Render(content)
}

// NewTermRenderer creates a new glamour terminal renderer
func NewTermRenderer() (*glamour.TermRenderer, error) {
	// Get terminal width
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		width = 80 // Default fallback
	}

	return glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
}

// NewNoMarginTermRenderer creates a new glamour terminal renderer with no margins
func NewNoMarginTermRenderer() (*glamour.TermRenderer, error) {
	// Get terminal width
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		width = 80 // Default fallback
	}

	// Use DarkStyleConfig as base but remove margins
	style := styles.DarkStyleConfig
	style.Document.Margin = uintPtr(0)

	return glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
	)
}

func uintPtr(u uint) *uint { return &u }