package tools

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sashabaranov/go-openai"
)

type WriteFileTool struct {
	BaseTool
}

func (t *WriteFileTool) Name() string {
	return "write_file"
}

func (t *WriteFileTool) Definition() openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        t.Name(),
			Description: "Write content to a file. This creates a new file or overwrites an existing one. Use this when you want to completely replace a file's contents.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "The absolute path to the file to write",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "The content to write to the file",
					},
				},
				"required": []string{"path", "content"},
			},
		},
	}
}

func (t *WriteFileTool) Execute(params map[string]interface{}) (string, error) {
	var args WriteFileArgs
	if err := t.Unmarshal(params, &args); err != nil {
		return "", err
	}

	if args.Path == "" {
		return "", fmt.Errorf("path parameter is required")
	}

	// Ensure parent directories exist
	dir := filepath.Dir(args.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("error creating directories: %v", err)
	}

	var oldContent string
	if _, err := os.Stat(args.Path); err == nil {
		existingContent, err := os.ReadFile(args.Path)
		if err != nil {
			return "", fmt.Errorf("error reading existing file: %v", err)
		}
		oldContent = string(existingContent)
	}

	err := os.WriteFile(args.Path, []byte(args.Content), 0644)
	if err != nil {
		return "", fmt.Errorf("error writing file: %v", err)
	}

	if oldContent == "" {
		return fmt.Sprintf("✅ File created: %s\n%s", args.Path, truncatePreview(args.Content, 200)), nil
	} else if oldContent != args.Content {
		return fmt.Sprintf("✅ File overwritten: %s\n%s", args.Path, truncatePreview(args.Content, 200)), nil
	}

	return fmt.Sprintf("✅ File unchanged: %s", args.Path), nil
}

func (t *WriteFileTool) Preview(params map[string]interface{}) (string, error) {
	var args WriteFileArgs
	if err := t.Unmarshal(params, &args); err != nil {
		return "", nil
	}

	if args.Path == "" {
		return "", nil
	}

	var oldContent string
	if existingContent, err := os.ReadFile(args.Path); err == nil {
		oldContent = string(existingContent)
	}

	if oldContent == "" {
		return fmt.Sprintf("📝 New file will be created: %s\n\n%s", args.Path, truncatePreview(args.Content, 500)), nil
	} else if oldContent != args.Content {
		return GenerateDiff(oldContent, args.Content, args.Path), nil
	}
	return "", nil
}

func (t *WriteFileTool) GetDisplayInfo(params map[string]interface{}) string {
	var args WriteFileArgs
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
