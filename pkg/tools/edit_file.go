package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sashabaranov/go-openai"
)

type EditFileTool struct {
	BaseTool
}

func (t *EditFileTool) Name() string {
	return "edit_file"
}

func (t *EditFileTool) Definition() openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        t.Name(),
			Description: "Perform incremental edits to a file using find-and-replace. MUCH FASTER than full file rewrites. For new files, use newString only. For edits, use oldString+newString.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"filePath": map[string]interface{}{
						"type":        "string",
						"description": "The absolute path to the file to modify",
					},
					"oldString": map[string]interface{}{
						"type":        "string",
						"description": "The text to replace (for editing existing files). Supports fuzzy matching for whitespace differences.",
					},
					"newString": map[string]interface{}{
						"type":        "string",
						"description": "The replacement text. For new files, provide this without oldString. For edits, use with oldString.",
					},
					"replaceAll": map[string]interface{}{
						"type":        "boolean",
						"description": "Replace all occurrences of oldString (default: false - only replace if unique match)",
					},
				},
				"required": []string{"filePath", "newString"},
			},
		},
	}
}

func (t *EditFileTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	var args EditFileArgs
	if err := t.Unmarshal(params, &args); err != nil {
		return "", err
	}

	path := args.GetFilePath()
	if path == "" {
		return "", fmt.Errorf("filePath parameter is required")
	}

	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	// Check for incremental edit (oldString + newString)
	if args.OldString != "" {
		result, err := t.manager.performIncrementalEdit(path, args.OldString, args.NewString, args.ReplaceAll)
		if err != nil {
			return "", err
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return fmt.Sprintf("🚀🚀🚀 INCREMENTAL EDIT MODE (FAST!) 🚀🚀🚀\n%s", result), nil
	}

	// For new file creation, use newString parameter
	if args.NewString != "" {
		result, err := t.manager.performIncrementalEdit(path, "", args.NewString, false)
		if err != nil {
			return "", err
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return fmt.Sprintf("🚀🚀🚀 NEW FILE CREATION (FAST!) 🚀🚀🚀\n%s", result), nil
	}

	return "", fmt.Errorf("either newString (for new files) or oldString+newString (for edits) must be provided")
}

func (t *EditFileTool) Preview(params map[string]interface{}) (string, error) {
	var args EditFileArgs
	if err := t.Unmarshal(params, &args); err != nil {
		return "", nil
	}

	path := args.GetFilePath()
	if path == "" {
		return "", nil
	}

	if args.OldString != "" {
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Sprintf("⚠️  Preview Failed: Error reading file %s: %v", path, err), nil
		}
		oldContent := string(content)

		newContent, err := ReplaceInContent(oldContent, args.OldString, args.NewString, args.ReplaceAll)
		if err != nil {
			return fmt.Sprintf("⚠️  Preview Failed: %v\n(The tool will likely fail if executed)", err), nil
		}

		return GenerateFocusedDiff(oldContent, newContent, path, args.OldString, args.NewString), nil
	}

	if args.NewString != "" {
		return fmt.Sprintf("📝 New file will be created: %s\n\n%s", path, truncatePreview(args.NewString, 500)), nil
	}

	return "", nil
}

func (t *EditFileTool) GetDisplayInfo(params map[string]interface{}) string {
	var args EditFileArgs
	if err := t.Unmarshal(params, &args); err != nil {
		return ""
	}

	path := args.GetFilePath()
	if path == "" {
		return ""
	}

	relPath, err := filepath.Rel(".", path)
	if err == nil {
		path = relPath
	}

	if args.OldString != "" {
		return fmt.Sprintf(" 🚀 %s [INCREMENTAL]", path)
	} else if args.NewString != "" {
		return fmt.Sprintf(" 🚀 %s [NEW FILE]", path)
	}
	return path
}
