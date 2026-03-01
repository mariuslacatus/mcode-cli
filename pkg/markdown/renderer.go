package markdown

import (
	"os"
	"regexp"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"golang.org/x/term"
)

// Renderer wraps glamour.TermRenderer to provide extra processing
type Renderer struct {
	*glamour.TermRenderer
}

// Render renders the markdown content after preprocessing
func (r *Renderer) Render(content string) (string, error) {
	return r.TermRenderer.Render(ProcessThinkTags(content))
}

// Render renders the markdown content using glamour
func Render(content string) (string, error) {
	r, err := NewTermRenderer()
	if err != nil {
		return "", err
	}

	return r.Render(content)
}

// NewTermRenderer creates a new terminal renderer
func NewTermRenderer() (*Renderer, error) {
	// Get terminal width
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		width = 80 // Default fallback
	}
	if width > 2 {
		width -= 2
	}

	tr, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil, err
	}

	return &Renderer{tr}, nil
}

// NewNoMarginTermRenderer creates a new terminal renderer with no margins
func NewNoMarginTermRenderer() (*Renderer, error) {
	// Get terminal width
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		width = 80 // Default fallback
	}
	if width > 2 {
		width -= 2
	}

	// Use DarkStyleConfig as base but remove margins
	style := styles.DarkStyleConfig
	style.Document.Margin = uintPtr(0)

	tr, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil, err
	}

	return &Renderer{tr}, nil
}

// ProcessThinkTags replaces <think> tags with italicized markdown
func ProcessThinkTags(content string) string {
	if !strings.Contains(content, "<think>") {
		return content
	}

	// Handle unclosed <think> tag at the end (for streaming)
	processed := content
	if strings.HasSuffix(processed, "<think>") {
		processed = strings.TrimSuffix(processed, "<think>") + "\n\n_Thinking..._\n"
		return processed
	}

	re := regexp.MustCompile(`(?s)<think>(.*?)(?:</think>|$)`)
	return re.ReplaceAllStringFunc(processed, func(match string) string {
		inner := strings.TrimPrefix(match, "<think>")
		inner = strings.TrimSuffix(inner, "</think>")

		if inner == "" {
			return "_Thinking..._"
		}

		// Wrap lines in italics
		lines := strings.Split(inner, "\n")
		var result []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				result = append(result, "_"+trimmed+"_")
			} else {
				result = append(result, "")
			}
		}
		return strings.Join(result, "\n")
	})
}

func uintPtr(u uint) *uint { return &u }
