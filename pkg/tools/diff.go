package tools

import (
	"fmt"
	"strings"

	"coding-agent/pkg/types"
	"github.com/pmezard/go-difflib/difflib"
)

// GenerateDiff generates a colored diff between old and new content with line numbers and context
func GenerateDiff(oldContent, newContent, filename string) string {
	var result strings.Builder
	result.WriteString(fmt.Sprintf("%s📝 File changes: %s%s\n", types.ColorCyan, filename, types.ColorReset))
	result.WriteString(fmt.Sprintf("%s%s%s\n", types.ColorBlue, strings.Repeat("=", 60), types.ColorReset))

	if oldContent == newContent {
		result.WriteString("No changes\n")
		return result.String()
	}

	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	// Use opcodes to get the differences
	matcher := difflib.NewMatcher(oldLines, newLines)
	opcodes := matcher.GetOpCodes()

	contextLines := 3
	
	// Determine the first line we'll actually show
	firstLineShown := len(oldLines) // Default to beyond file end
	for _, opcode := range opcodes {
		if opcode.Tag != 'e' {
			// This is a change - we'll show context before it
			firstLineShown = max(0, opcode.I1-contextLines)
			break
		}
	}
	
	// Show start ellipsis if we're not starting from the beginning
	if firstLineShown > 0 {
		result.WriteString("      ...  │ \n")
	}
	
	for opcodeIdx, opcode := range opcodes {
		tag := opcode.Tag
		i1, i2, j1, j2 := opcode.I1, opcode.I2, opcode.J1, opcode.J2

		switch tag {
		case 'e': // equal - show limited context only around changes
			
			// Check if there's a change before this equal section
			hasPreviousChange := opcodeIdx > 0
			
			// Check if there's a change after this equal section  
			hasNextChange := opcodeIdx < len(opcodes)-1
			
			if hasPreviousChange && hasNextChange {
				// Between changes - show context after previous and before next
				
				// But limit to contextLines around each change
				if i2-i1 > contextLines*2 {
					// Show first contextLines (after previous change)
					for i := i1; i < min(i1+contextLines, i2); i++ {
						oldLineNum := i + 1
						newLineNum := j1 + (i - i1) + 1
						result.WriteString(fmt.Sprintf(" %4d %4d │ %s\n", oldLineNum, newLineNum, oldLines[i]))
					}
					
					// Add ellipsis for gap
					result.WriteString("      ...  │ \n")
					
					// Show last contextLines (before next change)
					for i := max(i2-contextLines, i1+contextLines); i < i2; i++ {
						oldLineNum := i + 1
						newLineNum := j1 + (i - i1) + 1
						result.WriteString(fmt.Sprintf(" %4d %4d │ %s\n", oldLineNum, newLineNum, oldLines[i]))
					}
				} else {
					// Small gap - show all
					for i := i1; i < i2; i++ {
						oldLineNum := i + 1
						newLineNum := j1 + (i - i1) + 1
						result.WriteString(fmt.Sprintf(" %4d %4d │ %s\n", oldLineNum, newLineNum, oldLines[i]))
					}
				}
			} else if hasPreviousChange {
				// After a change - show contextLines after the change
				for i := i1; i < min(i1+contextLines, i2); i++ {
					oldLineNum := i + 1
					newLineNum := j1 + (i - i1) + 1
					result.WriteString(fmt.Sprintf(" %4d %4d │ %s\n", oldLineNum, newLineNum, oldLines[i]))
				}
			} else if hasNextChange {
				// Before a change - show contextLines before the change
				for i := max(i2-contextLines, i1); i < i2; i++ {
					oldLineNum := i + 1
					newLineNum := j1 + (i - i1) + 1
					result.WriteString(fmt.Sprintf(" %4d %4d │ %s\n", oldLineNum, newLineNum, oldLines[i]))
				}
			}
			// If no changes before or after, don't show any context from this equal section

		case 'r': // replace
			// Show deleted lines
			for i := i1; i < i2; i++ {
				oldLineNum := i + 1
				result.WriteString(fmt.Sprintf("%s-%4d      │ %s%s\n", types.ColorRed, oldLineNum, oldLines[i], types.ColorReset))
			}
			// Show added lines
			for j := j1; j < j2; j++ {
				newLineNum := j + 1
				result.WriteString(fmt.Sprintf("%s+     %4d │ %s%s\n", types.ColorGreen, newLineNum, newLines[j], types.ColorReset))
			}

		case 'd': // delete
			for i := i1; i < i2; i++ {
				oldLineNum := i + 1
				result.WriteString(fmt.Sprintf("%s-%4d      │ %s%s\n", types.ColorRed, oldLineNum, oldLines[i], types.ColorReset))
			}

		case 'i': // insert
			for j := j1; j < j2; j++ {
				newLineNum := j + 1
				result.WriteString(fmt.Sprintf("%s+     %4d │ %s%s\n", types.ColorGreen, newLineNum, newLines[j], types.ColorReset))
			}
		}
	}

	// Determine the last line we'll actually show
	lastLineShown := -1
	for i := len(opcodes) - 1; i >= 0; i-- {
		opcode := opcodes[i]
		if opcode.Tag != 'e' {
			// This is a change - we'll show context after it
			lastLineShown = min(len(oldLines)-1, opcode.I2+contextLines-1)
			break
		}
	}
	
	// Show end ellipsis if we're not ending at the last line
	if lastLineShown < len(oldLines)-1 {
		result.WriteString("      ...  │ \n")
	}

	return result.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}