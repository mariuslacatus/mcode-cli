package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"

	"coding-agent/pkg/types"
	"coding-agent/pkg/ui"
	"github.com/sashabaranov/go-openai"
)

type BashCommandTool struct {
	BaseTool
}

func (t *BashCommandTool) Name() string {
	return "bash_command"
}

func (t *BashCommandTool) Definition() openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        t.Name(),
			Description: "Execute a bash command",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{
						"type":        "string",
						"description": "Command to execute",
					},
				},
				"required": []string{"command"},
			},
		},
	}
}

func (t *BashCommandTool) Execute(params map[string]interface{}) (string, error) {
	var args BashCommandArgs
	if err := t.Unmarshal(params, &args); err != nil {
		return "", err
	}

	if args.Command == "" {
		return "", fmt.Errorf("command parameter is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ui.PrintfSafe("%sExecuting: %s%s\n", types.ColorYellow, args.Command, types.ColorReset)
	ui.PrintfSafe("%s(Press Ctrl+C to interrupt if it hangs)%s\n", types.ColorBlue, types.ColorReset)

	cmd := exec.CommandContext(ctx, "bash", "-c", args.Command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdoutBuf, stderrBuf bytes.Buffer
	safeWriter := ui.NewSafeOutputWriter()
	cmd.Stdout = io.MultiWriter(safeWriter, &stdoutBuf)
	cmd.Stderr = io.MultiWriter(safeWriter, &stderrBuf)

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start command: %v", err)
	}

	err := cmd.Wait()
	output := stdoutBuf.String() + stderrBuf.String()

	if ctx.Err() == context.DeadlineExceeded {
		return output, fmt.Errorf("command timed out after 30 seconds. Output so far: %s", output)
	}

	if err != nil {
		return output, fmt.Errorf("command failed: %v", err)
	}

	return output, nil
}

func (t *BashCommandTool) Preview(params map[string]interface{}) (string, error) {
	return "", nil
}

func (t *BashCommandTool) GetDisplayInfo(params map[string]interface{}) string {
	var args BashCommandArgs
	if err := t.Unmarshal(params, &args); err != nil {
		return ""
	}
	return fmt.Sprintf(" `%s`", args.Command)
}
