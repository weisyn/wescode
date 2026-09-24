package codeintel

import (
	"strings"
)

// TrimToCompleteUnit performs post-processing on FIM completion results,
// removing trailing incomplete statements. This allows the model to generate
// multi-line completions while ensuring the result is syntactically clean.
//
// INV-FIM-03: returns original text on any failure (fail-open).
func TrimToCompleteUnit(text string) string {
	if text == "" {
		return text
	}

	lines := strings.Split(text, "\n")
	if len(lines) <= 1 {
		return text
	}

	// Find last line that looks like a complete statement.
	// Heuristic: a line ending with }, ;, ), or being empty after content
	// is likely a statement boundary.
	lastComplete := len(lines) - 1
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		if isCompleteLine(trimmed) {
			lastComplete = i
			break
		}
		// This line looks incomplete — try the previous one
		if i > 0 {
			lastComplete = i - 1
		}
	}

	// Don't trim to nothing
	if lastComplete < 0 {
		return text
	}

	result := strings.Join(lines[:lastComplete+1], "\n")
	if result == "" {
		return text
	}
	return result
}

func isCompleteLine(line string) bool {
	if line == "" {
		return true
	}
	lastChar := line[len(line)-1]
	switch lastChar {
	case '}', ';', ')', ']', ',', ':':
		return true
	}
	// Go: lines ending with a complete assignment or return
	if strings.HasSuffix(line, "= nil") ||
		strings.HasSuffix(line, "{}") ||
		strings.HasSuffix(line, "()") ||
		strings.HasPrefix(line, "return ") ||
		strings.HasPrefix(line, "//") ||
		strings.HasPrefix(line, "#") {
		return true
	}
	return false
}
