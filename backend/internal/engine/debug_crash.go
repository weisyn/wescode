package engine

import (
	"fmt"
	"regexp"
	"strings"
)

// CrashDetector detects crash/exception patterns in process output.
// INV-DEBUG-07: best-effort regex matching, false positives are harmless.
type CrashDetector struct{}

var crashPatterns = []struct {
	language string
	re       *regexp.Regexp
}{
	{"go", regexp.MustCompile(`goroutine \d+ \[running\]:`)},
	{"go", regexp.MustCompile(`panic: `)},
	{"python", regexp.MustCompile(`Traceback \(most recent call last\):`)},
	{"javascript", regexp.MustCompile(`(?m)^\w*Error:.*\n\s+at `)},
	{"typescript", regexp.MustCompile(`(?m)^\w*Error:.*\n\s+at `)},
	{"java", regexp.MustCompile(`Exception in thread "`)},
	{"rust", regexp.MustCompile(`thread '.*' panicked at`)},
	{"ruby", regexp.MustCompile(`(?m)^\S+\.rb:\d+:in `)},
}

// DetectCrash checks if the text contains a crash pattern.
// Returns the detected language and whether a crash was found.
func DetectCrash(text string) (language string, isCrash bool) {
	for _, p := range crashPatterns {
		if p.re.MatchString(text) {
			return p.language, true
		}
	}
	return "", false
}

// ExtractCrashSection extracts the relevant crash section from output,
// trimming unrelated lines before the crash.
func ExtractCrashSection(text, language string) string {
	lines := strings.Split(text, "\n")
	startIdx := 0

	switch language {
	case "go":
		for i, line := range lines {
			if strings.HasPrefix(line, "goroutine ") || strings.HasPrefix(line, "panic: ") {
				startIdx = i
				break
			}
		}
	case "python":
		for i, line := range lines {
			if strings.Contains(line, "Traceback (most recent call last):") {
				startIdx = i
				break
			}
		}
	case "javascript", "typescript":
		for i, line := range lines {
			if strings.Contains(line, "Error:") {
				startIdx = i
				break
			}
		}
	}

	end := len(lines)
	if end-startIdx > 50 {
		end = startIdx + 50
	}
	return strings.Join(lines[startIdx:end], "\n")
}

// FormatCrashOverlay formats a crash/exception/test_failure event as a debug overlay
// XML block for PendingOverlay injection into the Agent's context.
// INV-DEBUG-04: injected as PendingOverlay, not written to session history.
func FormatCrashOverlay(event DebugEvent) string {
	if event.Kind == "test_failure" {
		return formatTestFailureOverlay(event)
	}
	var sb strings.Builder
	sb.WriteString("<debug-exception>\n")

	if event.Kind == "exception" && event.Exception != nil {
		sb.WriteString("Exception: ")
		sb.WriteString(event.Exception.Description)
		sb.WriteByte('\n')
	}

	if len(event.Frames) > 0 {
		sb.WriteString("\nStack trace:\n")
		for i, f := range event.Frames {
			if i >= 15 {
				sb.WriteString("  ... (truncated)\n")
				break
			}
			sb.WriteString("  ")
			sb.WriteString(f.Source)
			sb.WriteString(":")
			sb.WriteString(strings.Repeat(" ", 0))
			sb.WriteString(fmt.Sprintf("%d", f.Line))
			sb.WriteString(" — ")
			sb.WriteString(f.Name)
			sb.WriteByte('\n')
		}
	}

	if event.CrashText != "" {
		sb.WriteString("\nOutput:\n")
		text := event.CrashText
		if len(text) > 2000 {
			text = text[:2000] + "\n... (truncated)"
		}
		sb.WriteString(text)
		sb.WriteByte('\n')
	}

	if len(event.Variables) > 0 {
		sb.WriteString("\nVariables at crash point:\n")
		for _, v := range event.Variables {
			if len(sb.String()) > 3000 {
				break
			}
			sb.WriteString(fmt.Sprintf("  %s = %s\n", v.Name, v.Value))
		}
	}

	sb.WriteString("</debug-exception>")
	return sb.String()
}

// formatTestFailureOverlay formats a test_failure event with a dedicated tag.
func formatTestFailureOverlay(event DebugEvent) string {
	var sb strings.Builder
	sb.WriteString("<debug-test-failure>\n")

	if len(event.Frames) > 0 {
		sb.WriteString("Failed tests:\n")
		for _, f := range event.Frames {
			sb.WriteString(fmt.Sprintf("  %s — %s:%d\n", f.Name, f.Source, f.Line))
		}
	}

	if event.CrashText != "" {
		sb.WriteString("\nOutput:\n")
		text := event.CrashText
		if len(text) > 2000 {
			text = text[:2000] + "\n... (truncated)"
		}
		sb.WriteString(text)
		sb.WriteByte('\n')
	}

	sb.WriteString("</debug-test-failure>")
	return sb.String()
}
