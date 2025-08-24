package tools

import (
	"fmt"
	"strings"

	"coding-agent/pkg/types"
)

// GenerateDiff generates a colored diff between old and new content
func GenerateDiff(oldContent, newContent, filename string) string {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	var diff strings.Builder
	diff.WriteString(fmt.Sprintf("%s📝 File changes: %s%s\n", types.ColorCyan, filename, types.ColorReset))
	diff.WriteString(fmt.Sprintf("%s%s%s\n", types.ColorBlue, strings.Repeat("=", 60), types.ColorReset))

	maxLines := len(oldLines)
	if len(newLines) > maxLines {
		maxLines = len(newLines)
	}

	for i := 0; i < maxLines; i++ {
		lineNum := fmt.Sprintf("%3d", i+1)

		var oldLine, newLine string
		if i < len(oldLines) {
			oldLine = oldLines[i]
		}
		if i < len(newLines) {
			newLine = newLines[i]
		}

		if oldLine != newLine {
			if i < len(oldLines) && oldLine != "" {
				diff.WriteString(fmt.Sprintf("%s-%s │ %s%s\n", types.ColorRed, lineNum, oldLine, types.ColorReset))
			}
			if i < len(newLines) && newLine != "" {
				diff.WriteString(fmt.Sprintf("%s+%s │ %s%s\n", types.ColorGreen, lineNum, newLine, types.ColorReset))
			}
		}
	}

	// Summary
	addedLines := len(newLines) - len(oldLines)
	if addedLines > 0 {
		diff.WriteString(fmt.Sprintf("%s+%d lines added%s\n", types.ColorGreen, addedLines, types.ColorReset))
	} else if addedLines < 0 {
		diff.WriteString(fmt.Sprintf("%s%d lines removed%s\n", types.ColorRed, -addedLines, types.ColorReset))
	}

	return diff.String()
}