package tools

import (
	"fmt"
	"regexp"
	"strings"
)

// Replacer represents a strategy for finding strings to replace
type Replacer interface {
	FindMatches(content, find string) []string
}

// SimpleReplacer finds exact string matches
type SimpleReplacer struct{}

func (r *SimpleReplacer) FindMatches(content, find string) []string {
	if strings.Contains(content, find) {
		return []string{find}
	}
	return []string{}
}

// LineTrimmedReplacer finds matches by comparing trimmed lines
type LineTrimmedReplacer struct{}

func (r *LineTrimmedReplacer) FindMatches(content, find string) []string {
	originalLines := strings.Split(content, "\n")
	searchLines := strings.Split(find, "\n")

	// Remove trailing empty line if present
	if len(searchLines) > 0 && searchLines[len(searchLines)-1] == "" {
		searchLines = searchLines[:len(searchLines)-1]
	}

	for i := 0; i <= len(originalLines)-len(searchLines); i++ {
		matches := true

		for j := 0; j < len(searchLines); j++ {
			originalTrimmed := strings.TrimSpace(originalLines[i+j])
			searchTrimmed := strings.TrimSpace(searchLines[j])

			if originalTrimmed != searchTrimmed {
				matches = false
				break
			}
		}

		if matches {
			// Calculate the actual substring in the original content
			matchStart := 0
			for k := 0; k < i; k++ {
				matchStart += len(originalLines[k]) + 1 // +1 for newline
			}

			matchEnd := matchStart
			for k := 0; k < len(searchLines); k++ {
				matchEnd += len(originalLines[i+k])
				if k < len(searchLines)-1 {
					matchEnd += 1 // Add newline character except for the last line
				}
			}

			actualMatch := content[matchStart:matchEnd]
			return []string{actualMatch}
		}
	}

	return []string{}
}

// WhitespaceNormalizedReplacer normalizes whitespace before matching
type WhitespaceNormalizedReplacer struct{}

func (r *WhitespaceNormalizedReplacer) FindMatches(content, find string) []string {
	normalizeWhitespace := func(text string) string {
		return regexp.MustCompile(`\s+`).ReplaceAllString(strings.TrimSpace(text), " ")
	}

	normalizedFind := normalizeWhitespace(find)
	var matches []string

	// Handle single line matches
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if normalizeWhitespace(line) == normalizedFind {
			matches = append(matches, line)
		}
	}

	// Handle multi-line matches
	findLines := strings.Split(find, "\n")
	if len(findLines) > 1 {
		for i := 0; i <= len(lines)-len(findLines); i++ {
			block := strings.Join(lines[i:i+len(findLines)], "\n")
			if normalizeWhitespace(block) == normalizedFind {
				matches = append(matches, block)
			}
		}
	}

	return matches
}

// IndentationFlexibleReplacer ignores indentation differences
type IndentationFlexibleReplacer struct{}

func (r *IndentationFlexibleReplacer) FindMatches(content, find string) []string {
	removeIndentation := func(text string) string {
		lines := strings.Split(text, "\n")
		nonEmptyLines := []string{}
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				nonEmptyLines = append(nonEmptyLines, line)
			}
		}

		if len(nonEmptyLines) == 0 {
			return text
		}

		minIndent := len(nonEmptyLines[0]) // Start with max possible
		for _, line := range nonEmptyLines {
			leadingSpaces := 0
			for _, char := range line {
				if char == ' ' || char == '\t' {
					leadingSpaces++
				} else {
					break
				}
			}
			if leadingSpaces < minIndent {
				minIndent = leadingSpaces
			}
		}

		result := []string{}
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				result = append(result, line)
			} else if len(line) > minIndent {
				result = append(result, line[minIndent:])
			} else {
				result = append(result, line)
			}
		}

		return strings.Join(result, "\n")
	}

	normalizedFind := removeIndentation(find)
	contentLines := strings.Split(content, "\n")
	findLines := strings.Split(find, "\n")

	var matches []string
	for i := 0; i <= len(contentLines)-len(findLines); i++ {
		block := strings.Join(contentLines[i:i+len(findLines)], "\n")
		if removeIndentation(block) == normalizedFind {
			matches = append(matches, block)
		}
	}

	return matches
}

// ReplaceInContent performs string replacement using multiple strategies
func ReplaceInContent(content, oldString, newString string, replaceAll bool) (string, error) {
	if oldString == newString {
		return "", fmt.Errorf("oldString and newString must be different")
	}

	replacers := []Replacer{
		&SimpleReplacer{},
		&LineTrimmedReplacer{},
		&WhitespaceNormalizedReplacer{},
		&IndentationFlexibleReplacer{},
	}

	for _, replacer := range replacers {
		matches := replacer.FindMatches(content, oldString)
		if len(matches) == 0 {
			continue
		}

		// If we found matches, handle them
		if !replaceAll && len(matches) > 1 {
			return "", fmt.Errorf("ambiguous replacement: found %d matches for the provided text. Please provide more context (more surrounding lines) to make the match unique", len(matches))
		}

		// Check for uniqueness of the specific match string in the whole content
		match := matches[0]
		if !replaceAll {
			firstIndex := strings.Index(content, match)
			lastIndex := strings.LastIndex(content, match)
			if firstIndex != lastIndex {
				// Count occurrences
				count := strings.Count(content, match)
				return "", fmt.Errorf("ambiguous replacement: the text matches %d different locations in the file. Please include more surrounding lines in 'oldString' to uniquely identify the target", count)
			}
		}

		// Perform the replacement
		if replaceAll {
			return strings.ReplaceAll(content, match, newString), nil
		}
		return strings.Replace(content, match, newString, 1), nil
	}

	return "", fmt.Errorf("text not found: the provided 'oldString' does not match any content in the file. Check for typos, indentation, or missing lines")
}
