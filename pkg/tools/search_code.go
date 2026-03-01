package tools

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/sashabaranov/go-openai"
)

type SearchCodeTool struct {
	BaseTool
}

func (t *SearchCodeTool) Name() string {
	return "search_code"
}

func (t *SearchCodeTool) Definition() openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        t.Name(),
			Description: "Search for code patterns in files. Returns up to 100 results with line numbers. Use more specific patterns if needed.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pattern": map[string]interface{}{
						"type":        "string",
						"description": "Pattern to search for",
					},
					"directory": map[string]interface{}{
						"type":        "string",
						"description": "Directory to search in (defaults to current directory)",
					},
				},
				"required": []string{"pattern"},
			},
		},
	}
}

func (t *SearchCodeTool) Execute(params map[string]interface{}) (string, error) {
	var args SearchCodeArgs
	if err := t.Unmarshal(params, &args); err != nil {
		return "", err
	}

	if args.Pattern == "" {
		return "", fmt.Errorf("pattern parameter is required")
	}

	directory := args.Directory
	if directory == "" {
		directory = "."
	}

	// Use -E for extended regex support (e.g. | operator)
	cmd := exec.Command("bash", "-c", fmt.Sprintf("grep -rEnI %q %s | head -n 100", args.Pattern, directory))
	output, _ := cmd.CombinedOutput()

	result := string(output)
	if result == "" {
		return fmt.Sprintf("No results found for pattern %q in directory %s", args.Pattern, directory), nil
	}

	if strings.Count(result, "\n") >= 100 {
		result += "\n\n[... Search results truncated to 100 lines. Use more specific patterns if needed. ...]"
	}

	return result, nil
}

func (t *SearchCodeTool) Preview(params map[string]interface{}) (string, error) {
	return "", nil
}

func (t *SearchCodeTool) GetDisplayInfo(params map[string]interface{}) string {
	var args SearchCodeArgs
	if err := t.Unmarshal(params, &args); err != nil {
		return ""
	}
	return fmt.Sprintf(" \"%s\"", args.Pattern)
}
