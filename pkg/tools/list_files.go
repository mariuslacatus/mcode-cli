package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sashabaranov/go-openai"
)

type ListFilesTool struct {
	BaseTool
}

func (t *ListFilesTool) Name() string {
	return "list_files"
}

func (t *ListFilesTool) Definition() openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        t.Name(),
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
	}
}

func (t *ListFilesTool) Execute(params map[string]interface{}) (string, error) {
	var args ListFilesArgs
	if err := t.Unmarshal(params, &args); err != nil {
		return "", err
	}

	path := args.Path
	if path == "" {
		path = "."
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return "", fmt.Errorf("error listing directory: %v", err)
	}

	var files []string
	count := 0
	const maxItems = 100

	for _, entry := range entries {
		if count >= maxItems {
			files = append(files, fmt.Sprintf("\n[... Truncated: only first %d items shown ...]", maxItems))
			break
		}
		if entry.IsDir() {
			files = append(files, entry.Name()+"/")
		} else {
			files = append(files, entry.Name())
		}
		count++
	}

	return strings.Join(files, "\n"), nil
}

func (t *ListFilesTool) Preview(params map[string]interface{}) (string, error) {
	return "", nil
}

func (t *ListFilesTool) GetDisplayInfo(params map[string]interface{}) string {
	var args ListFilesArgs
	if err := t.Unmarshal(params, &args); err != nil {
		return ""
	}

	absPath := args.Path
	relPath, err := filepath.Rel(".", absPath)
	if err == nil {
		return fmt.Sprintf("<%s>", relPath)
	}
	return fmt.Sprintf("<%s>", absPath)
}
