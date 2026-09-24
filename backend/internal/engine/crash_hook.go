package engine

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/weisyn/wesgine/tool"
)

// crashDetectHook returns a PostCallHook that scans exec tool output for
// crash patterns (Go panic, Python traceback, JS errors, etc.) and
// error-level log patterns (ERROR/FATAL, unhandled exception, OOM, etc.).
// Scans both foreground (action=run) and background (action=poll/wait) output.
// INV-DEBUG-07: best-effort, does not block exec, false positives harmless.
func (s *Service) crashDetectHook() tool.PostCallHook {
	return func(_ context.Context, call tool.ToolCall, result *tool.ToolResult, _ *tool.ToolContext) {
		if result == nil || result.Content == "" {
			return
		}
		if !isExecLikeTool(call.Name) {
			return
		}

		isPollOrWait := isExecPollOrWait(call)
		if !isPollOrWait && !looksLikeCrashOutput(result.Content) {
			return
		}

		language, isCrash := DetectCrash(result.Content)
		if isCrash {
			section := ExtractCrashSection(result.Content, language)
			s.HandleCrashReport("exec", section, language)
			return
		}

		if isPollOrWait {
			if matches := ScanLogErrors(result.Content); len(matches) > 0 {
				s.HandleLogAnomalies("exec:poll", matches)
			}
		}
	}
}

func isExecLikeTool(name string) bool {
	return name == "exec"
}

// isExecPollOrWait checks if the tool call is an exec poll or wait action.
func isExecPollOrWait(call tool.ToolCall) bool {
	var args struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(call.Input, &args); err != nil {
		return false
	}
	return args.Action == "poll" || args.Action == "wait"
}

// looksLikeCrashOutput is a cheap pre-filter to avoid running regex on
// every trivial exec result (ls, grep, etc.). We only scan outputs that
// contain a non-zero exit code or are long enough to plausibly contain a
// stack trace.
func looksLikeCrashOutput(content string) bool {
	if strings.Contains(content, "Exit code: 0") {
		return false
	}
	if len(content) < 40 {
		return false
	}
	return true
}

// LogErrorMatch represents a single error pattern match in log output.
type LogErrorMatch struct {
	Level   string // "error", "crash", "warning"
	Pattern string // which pattern matched
	Line    string // the matched line
}

var logErrorPatterns = []struct {
	pattern *regexp.Regexp
	level   string
	label   string
}{
	{regexp.MustCompile(`(?i)\b(ERROR|FATAL|CRITICAL)\b`), "error", "error_keyword"},
	{regexp.MustCompile(`(?i)unhandled\s+(rejection|exception)`), "error", "unhandled_exception"},
	{regexp.MustCompile(`(?i)segmentation\s+fault`), "crash", "segfault"},
	{regexp.MustCompile(`(?i)out\s+of\s+memory`), "crash", "oom"},
	{regexp.MustCompile(`(?i)connection\s+refused`), "warning", "connection_refused"},
	{regexp.MustCompile(`(?i)EADDRINUSE`), "error", "address_in_use"},
	{regexp.MustCompile(`(?i)bind:\s+address already in use`), "error", "address_in_use"},
	{regexp.MustCompile(`(?i)EACCES.*permission denied`), "error", "permission_denied"},
	{regexp.MustCompile(`(?i)MODULE_NOT_FOUND`), "error", "module_not_found"},
	{regexp.MustCompile(`(?i)Cannot find module`), "error", "module_not_found"},
	{regexp.MustCompile(`(?i)ImportError:\s+`), "error", "import_error"},
	{regexp.MustCompile(`(?i)SIGTERM|SIGKILL|SIGSEGV`), "crash", "signal"},
}

// ScanLogErrors scans text for error-level log patterns beyond crash detection.
// Returns up to 10 matches to avoid flooding.
func ScanLogErrors(text string) []LogErrorMatch {
	const maxMatches = 10
	lines := strings.Split(text, "\n")
	var matches []LogErrorMatch
	seen := make(map[string]bool)

	for _, line := range lines {
		if len(matches) >= maxMatches {
			break
		}
		trimmed := strings.TrimSpace(line)
		if len(trimmed) < 5 {
			continue
		}
		for _, p := range logErrorPatterns {
			if p.pattern.MatchString(trimmed) {
				key := p.label + ":" + trimmed
				if seen[key] {
					continue
				}
				seen[key] = true
				matches = append(matches, LogErrorMatch{
					Level:   p.level,
					Pattern: p.label,
					Line:    truncateLineForMatch(trimmed, 200),
				})
				break
			}
		}
	}
	return matches
}

func truncateLineForMatch(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
