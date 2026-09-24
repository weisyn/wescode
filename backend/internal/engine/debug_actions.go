package engine

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/weisyn/wescode/internal/codeintel"
)

// TestFailureInfo describes a single test failure received from the Extension TestBridge.
type TestFailureInfo struct {
	Name    string `json:"name"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// HandleDebugEvent processes a unified debug event from the IDE DebugSensor (D1).
func (s *Service) HandleDebugEvent(event DebugEvent) {
	s.mu.Lock()
	store := s.debugStore
	s.mu.Unlock()
	if store == nil {
		return
	}
	store.Push(event)

	switch event.Kind {
	case "exception", "crash":
		slog.Info("[debug] exception captured",
			"kind", event.Kind,
			"file", frameFile(event.Frames),
			"line", frameLine(event.Frames),
			"reason", event.StopReason,
		)
	case "breakpoint_hit":
		slog.Debug("[debug] breakpoint hit",
			"file", frameFile(event.Frames),
			"line", frameLine(event.Frames),
			"vars", len(event.Variables),
		)
	default:
		slog.Debug("[debug] event", "kind", event.Kind)
	}
}

// HandleCrashReport processes a crash report from terminal/task output (D3).
func (s *Service) HandleCrashReport(source, text, language string) {
	s.mu.Lock()
	store := s.debugStore
	s.mu.Unlock()
	if store == nil {
		return
	}

	event := DebugEvent{
		Kind:      "crash",
		Source:    source,
		CrashText: truncateCrash(text, 5000),
		Language:  language,
	}

	frames := parseCrashFrames(text, language)
	if len(frames) > 0 {
		event.Frames = frames
	}

	store.Push(event)
	slog.Info("[debug] crash report",
		"source", source,
		"language", language,
		"frames", len(frames),
		"text_len", len(text),
	)
}

// DebugContext returns the current debug state for the debug_context tool.
func (s *Service) DebugContext() map[string]any {
	s.mu.Lock()
	store := s.debugStore
	s.mu.Unlock()
	if store == nil {
		return map[string]any{"active": false}
	}
	recent := store.Recent(10)
	lastExc := store.LastException()

	result := map[string]any{
		"event_count":  store.Len(),
		"recent_count": len(recent),
		"active":       true,
	}
	if lastExc != nil {
		result["last_exception"] = map[string]any{
			"kind":      lastExc.Kind,
			"file":      frameFile(lastExc.Frames),
			"line":      frameLine(lastExc.Frames),
			"timestamp": lastExc.Timestamp,
		}
	}
	return result
}

// DebugEvents returns recent debug events for tool consumption.
func (s *Service) DebugEvents(n int) []DebugEvent {
	s.mu.Lock()
	store := s.debugStore
	s.mu.Unlock()
	if store == nil {
		return nil
	}
	return store.Recent(n)
}

// DebugEventsRaw returns recent debug events in a codeintel-compatible format
// (breaks import cycle between codeintel and engine packages).
func (s *Service) DebugEventsRaw(n int) []codeintel.DebugEventRaw {
	events := s.DebugEvents(n)
	out := make([]codeintel.DebugEventRaw, len(events))
	for i, e := range events {
		raw := codeintel.DebugEventRaw{
			Kind:       e.Kind,
			Timestamp:  e.Timestamp,
			StopReason: e.StopReason,
			CrashText:  e.CrashText,
		}
		for _, f := range e.Frames {
			raw.Frames = append(raw.Frames, codeintel.DebugFrameRaw{
				Name: f.Name, Source: f.Source, Line: f.Line,
			})
		}
		for _, v := range e.Variables {
			raw.Variables = append(raw.Variables, codeintel.DebugVariableRaw{
				Scope: v.Scope, Name: v.Name, Value: v.Value, Type: v.Type,
			})
		}
		if e.Exception != nil {
			raw.Exception = &codeintel.DebugExceptionRaw{
				Description: e.Exception.Description,
				StackTrace:  e.Exception.StackTrace,
			}
		}
		out[i] = raw
	}
	return out
}

// GetPendingDebugOverlay returns a formatted debug overlay if an unhandled
// exception or crash was captured since the last call. Returns "" if none.
// Consumes the exception event so it's not injected again on the next Run.
// INV-DEBUG-04: used for PendingOverlay injection.
func (s *Service) GetPendingDebugOverlay() string {
	s.mu.Lock()
	store := s.debugStore
	s.mu.Unlock()
	if store == nil {
		return ""
	}
	exc := store.ConsumeLastException()
	if exc == nil {
		return ""
	}
	return FormatCrashOverlay(*exc)
}

// HandleTestFailures processes test failure details from TestBridge or QualityGate L2.
// Each failure is pushed as a "test_failure" event to DebugEventStore.
func (s *Service) HandleTestFailures(failures []TestFailureInfo) {
	s.mu.Lock()
	store := s.debugStore
	s.mu.Unlock()
	if store == nil || len(failures) == 0 {
		return
	}
	for _, f := range failures {
		store.Push(DebugEvent{
			Kind:      "test_failure",
			Timestamp: time.Now(),
			CrashText: fmt.Sprintf("FAIL %s\n  %s:%d\n  %s", f.Name, f.File, f.Line, f.Message),
			Frames:    []DebugFrame{{Name: f.Name, Source: f.File, Line: f.Line}},
		})
	}
	slog.Info("[debug] test failures captured",
		"count", len(failures),
	)
}

// HandleLogAnomalies processes error-level log matches from background task output.
// Pushes them as "log_anomaly" events to DebugEventStore.
func (s *Service) HandleLogAnomalies(source string, matches []LogErrorMatch) {
	s.mu.Lock()
	store := s.debugStore
	s.mu.Unlock()
	if store == nil || len(matches) == 0 {
		return
	}

	hasCrashLevel := false
	var sb strings.Builder
	for _, m := range matches {
		sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", m.Level, m.Pattern, m.Line))
		if m.Level == "crash" {
			hasCrashLevel = true
		}
	}

	kind := "log_anomaly"
	if hasCrashLevel {
		kind = "crash"
	}
	event := DebugEvent{
		Kind:      kind,
		Source:    source,
		CrashText: truncateCrash(sb.String(), 3000),
	}
	store.Push(event)
	slog.Info("[debug] log anomalies detected",
		"source", source,
		"count", len(matches),
		"kind", kind,
	)
}

func frameFile(frames []DebugFrame) string {
	if len(frames) > 0 {
		return frames[0].Source
	}
	return ""
}

func frameLine(frames []DebugFrame) int {
	if len(frames) > 0 {
		return frames[0].Line
	}
	return 0
}

func truncateCrash(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n... (truncated)"
}

// parseCrashFrames attempts to extract stack frames from crash text.
func parseCrashFrames(text, language string) []DebugFrame {
	lines := strings.Split(text, "\n")
	var frames []DebugFrame

	switch language {
	case "go":
		for i, line := range lines {
			// Go panic: goroutine stack lines look like
			//   /path/to/file.go:123 +0x...
			// preceded by function name
			if strings.HasSuffix(line, ")") && i+1 < len(lines) {
				next := strings.TrimSpace(lines[i+1])
				if fl := parseGoFileLine(next); fl != nil {
					frames = append(frames, DebugFrame{
						Name:   strings.TrimSpace(line),
						Source: fl.file,
						Line:   fl.line,
					})
				}
			}
			if len(frames) >= 20 {
				break
			}
		}
	case "python":
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "File \"") {
				// File "/path/to/file.py", line 42, in func_name
				parts := strings.SplitN(trimmed, "\"", 3)
				if len(parts) >= 2 {
					file := parts[1]
					lineNum := 0
					if lineIdx := strings.Index(trimmed, "line "); lineIdx >= 0 {
						fmt.Sscanf(trimmed[lineIdx:], "line %d", &lineNum)
					}
					funcName := ""
					if inIdx := strings.LastIndex(trimmed, "in "); inIdx >= 0 {
						funcName = trimmed[inIdx+3:]
					}
					_ = i
					frames = append(frames, DebugFrame{
						Name: funcName, Source: file, Line: lineNum,
					})
				}
			}
			if len(frames) >= 20 {
				break
			}
		}
	case "javascript", "typescript":
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "at ") {
				// at funcName (/path/to/file.js:42:10)
				frame := parseJSFrame(trimmed)
				if frame != nil {
					frames = append(frames, *frame)
				}
			}
			if len(frames) >= 20 {
				break
			}
		}
	}
	return frames
}

type goFileLine struct {
	file string
	line int
}

func parseGoFileLine(line string) *goFileLine {
	line = strings.TrimSpace(line)
	colonIdx := strings.LastIndex(line, ":")
	if colonIdx < 0 {
		return nil
	}
	file := line[:colonIdx]
	if !strings.Contains(file, "/") && !strings.Contains(file, "\\") {
		return nil
	}
	lineNum := 0
	rest := line[colonIdx+1:]
	fmt.Sscanf(rest, "%d", &lineNum)
	if lineNum <= 0 {
		return nil
	}
	return &goFileLine{file: file, line: lineNum}
}

func parseJSFrame(line string) *DebugFrame {
	// "at funcName (/path/to/file.js:42:10)"
	// "at /path/to/file.js:42:10"
	line = strings.TrimPrefix(line, "at ")
	parenStart := strings.LastIndex(line, "(")
	parenEnd := strings.LastIndex(line, ")")

	var name, loc string
	if parenStart >= 0 && parenEnd > parenStart {
		name = strings.TrimSpace(line[:parenStart])
		loc = line[parenStart+1 : parenEnd]
	} else {
		loc = strings.TrimSpace(line)
	}

	parts := strings.Split(loc, ":")
	if len(parts) < 2 {
		return nil
	}
	file := parts[0]
	lineNum := 0
	fmt.Sscanf(parts[1], "%d", &lineNum)
	if lineNum <= 0 {
		return nil
	}
	return &DebugFrame{Name: name, Source: file, Line: lineNum}
}
