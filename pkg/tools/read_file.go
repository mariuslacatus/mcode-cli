package tools

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"coding-agent/pkg/types"
	"github.com/sashabaranov/go-openai"
)

type ReadFileTool struct {
	BaseTool
}

func (t *ReadFileTool) Name() string {
	return "read_file"
}

func (t *ReadFileTool) Definition() openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        t.Name(),
			Description: "Read the contents of a file. For large files, use offset and limit to paginate. Defaults to 500 lines.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the file to read",
					},
					"offset": map[string]interface{}{
						"type":        "integer",
						"description": "Optional: 0-based line number to start reading from.",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Optional: Maximum number of lines to read (default 500).",
					},
				},
				"required": []string{"path"},
			},
		},
	}
}

func (t *ReadFileTool) Execute(params map[string]interface{}) (string, error) {
	var args ReadFileArgs
	if err := t.Unmarshal(params, &args); err != nil {
		return "", err
	}

	if args.Path == "" {
		return "", fmt.Errorf("path parameter is required")
	}

	limit := 500
	if args.Limit > 0 {
		limit = args.Limit
	}
	offset := args.Offset

	// Smart path resolution: if file doesn't exist, try to find it
	filePath := args.Path
	if _, err := os.Stat(filePath); os.IsNotExist(err) && !filepath.IsAbs(filePath) {
		// Try to find the file recursively
		cmd := exec.Command("find", ".", "-name", filepath.Base(filePath), "-not", "-path", "*/.*")
		output, _ := cmd.Output()
		foundPaths := strings.Split(strings.TrimSpace(string(output)), "\n")
		if len(foundPaths) > 0 && foundPaths[0] != "" {
			filePath = foundPaths[0]
			fmt.Printf("%s💡 File not found at literal path, using: %s%s\n", types.ColorBlue, filePath, types.ColorReset)
		}
	}

	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("error opening file: %v", err)
	}

	lineScanner := bufio.NewScanner(f)
	totalLines := 0
	for lineScanner.Scan() {
		totalLines++
	}
	f.Close()

	// Re-open using the same (possibly resolved) filePath
	f, err = os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("error opening file: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var lines []string
	currentLine := 0

	for scanner.Scan() {
		if currentLine >= offset {
			lines = append(lines, scanner.Text())
			if limit > 0 && len(lines) >= limit {
				break
			}
		}
		currentLine++
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading file: %v", err)
	}

	content := strings.Join(lines, "\n")
	if limit > 0 && (offset+len(lines) < totalLines) {
		content += fmt.Sprintf("\n\n[... File truncated. %d/%d lines read starting from line %d. Use offset and limit to read more. ...]", len(lines), totalLines, offset)
	}

	return content, nil
}

func (t *ReadFileTool) Preview(params map[string]interface{}) (string, error) {
	return "", nil
}

func (t *ReadFileTool) GetDisplayInfo(params map[string]interface{}) string {
	var args ReadFileArgs
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
