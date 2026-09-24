package engine

import (
	"regexp"
	"strings"
	"sync"
	"time"
)

// TerminalError is a structured error parsed from terminal output.
type TerminalError struct {
	TerminalID string    `json:"terminalId"`
	Timestamp  time.Time `json:"timestamp"`
	Pattern    string    `json:"pattern"`
	Lang       string    `json:"lang"`
	File       string    `json:"file"`
	Line       int       `json:"line"`
	Column     int       `json:"column"`
	Message    string    `json:"message"`
	Context    string    `json:"context"`
}

type errorPattern struct {
	Lang    string
	Name    string
	Regex   *regexp.Regexp
	Extract func(matches []string, context string) *TerminalError
}

var terminalPatterns = []errorPattern{
	{
		Lang: "go", Name: "panic",
		Regex: regexp.MustCompile(`goroutine \d+ \[running\]:`),
		Extract: func(_ []string, ctx string) *TerminalError {
			// Find first file:line in stack
			fileRe := regexp.MustCompile(`\t(/[^\s:]+):(\d+)`)
			if m := fileRe.FindStringSubmatch(ctx); len(m) >= 3 {
				line := 0
				if n, _ := parseInt(m[2]); n > 0 {
					line = n
				}
				return &TerminalError{
					Pattern: "panic",
					Lang:    "go",
					File:    m[1],
					Line:    line,
					Message: extractPanicMessage(ctx),
					Context: truncateContext(ctx, 500),
				}
			}
			return &TerminalError{
				Pattern: "panic",
				Lang:    "go",
				Message: "goroutine panic",
				Context: truncateContext(ctx, 500),
			}
		},
	},
	{
		Lang: "go", Name: "compile",
		Regex: regexp.MustCompile(`^(.+\.go):(\d+):(\d+): (.+)$`),
		Extract: func(matches []string, _ string) *TerminalError {
			if len(matches) < 5 {
				return nil
			}
			line, _ := parseInt(matches[2])
			col, _ := parseInt(matches[3])
			return &TerminalError{
				Pattern: "compile",
				Lang:    "go",
				File:    matches[1],
				Line:    line,
				Column:  col,
				Message: matches[4],
			}
		},
	},
	{
		Lang: "go", Name: "test_fail",
		Regex: regexp.MustCompile(`--- FAIL: (Test\w+)\s`),
		Extract: func(matches []string, ctx string) *TerminalError {
			if len(matches) < 2 {
				return nil
			}
			return &TerminalError{
				Pattern: "test_fail",
				Lang:    "go",
				Message: "FAIL: " + matches[1],
				Context: truncateContext(ctx, 300),
			}
		},
	},
	{
		Lang: "js", Name: "node_error",
		Regex: regexp.MustCompile(`at .+? \((.+?):(\d+):(\d+)\)`),
		Extract: func(matches []string, ctx string) *TerminalError {
			if len(matches) < 4 {
				return nil
			}
			line, _ := parseInt(matches[2])
			col, _ := parseInt(matches[3])
			return &TerminalError{
				Pattern: "node_error",
				Lang:    "js",
				File:    matches[1],
				Line:    line,
				Column:  col,
				Message: extractFirstErrorLine(ctx),
				Context: truncateContext(ctx, 300),
			}
		},
	},
	{
		Lang: "python", Name: "traceback",
		Regex: regexp.MustCompile(`File "(.+?)", line (\d+)`),
		Extract: func(matches []string, ctx string) *TerminalError {
			if len(matches) < 3 {
				return nil
			}
			line, _ := parseInt(matches[2])
			return &TerminalError{
				Pattern: "traceback",
				Lang:    "python",
				File:    matches[1],
				Line:    line,
				Message: extractLastLine(ctx),
				Context: truncateContext(ctx, 300),
			}
		},
	},
	{
		Lang: "rust", Name: "compile",
		Regex: regexp.MustCompile(`error\[E\d+\]: (.+)\n\s*--> (.+?):(\d+):(\d+)`),
		Extract: func(matches []string, _ string) *TerminalError {
			if len(matches) < 5 {
				return nil
			}
			line, _ := parseInt(matches[3])
			col, _ := parseInt(matches[4])
			return &TerminalError{
				Pattern: "compile",
				Lang:    "rust",
				File:    matches[2],
				Line:    line,
				Column:  col,
				Message: matches[1],
			}
		},
	},
}

// TerminalDiagCache stores parsed errors from terminal output.
type TerminalDiagCache struct {
	mu      sync.RWMutex
	entries []TerminalError
	maxSize int
}

func NewTerminalDiagCache() *TerminalDiagCache {
	return &TerminalDiagCache{
		maxSize: 100,
	}
}

func (c *TerminalDiagCache) Add(entry TerminalError) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Dedup: same file+line+message within last 30s
	for _, existing := range c.entries {
		if existing.File == entry.File && existing.Line == entry.Line &&
			existing.Message == entry.Message &&
			time.Since(existing.Timestamp) < 30*time.Second {
			return
		}
	}

	c.entries = append(c.entries, entry)
	if len(c.entries) > c.maxSize {
		c.entries = c.entries[len(c.entries)-c.maxSize:]
	}
}

// Recent returns errors from the last `within` duration.
func (c *TerminalDiagCache) Recent(within time.Duration) []TerminalError {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cutoff := time.Now().Add(-within)
	var result []TerminalError
	for _, e := range c.entries {
		if e.Timestamp.After(cutoff) {
			result = append(result, e)
		}
	}
	return result
}

// All returns all cached entries.
func (c *TerminalDiagCache) All() []TerminalError {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]TerminalError, len(c.entries))
	copy(out, c.entries)
	return out
}

// ClearTerminal removes all entries for a given terminal.
func (c *TerminalDiagCache) ClearTerminal(terminalID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var kept []TerminalError
	for _, e := range c.entries {
		if e.TerminalID != terminalID {
			kept = append(kept, e)
		}
	}
	c.entries = kept
}

// IsEmpty reports whether the cache has no entries.
func (c *TerminalDiagCache) IsEmpty() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries) == 0
}

// TerminalDiagParser parses terminal output and writes errors to the cache.
type TerminalDiagParser struct {
	cache *TerminalDiagCache
	// Per-terminal rolling buffer for multi-line pattern matching
	mu      sync.Mutex
	buffers map[string]*termBuffer
}

type termBuffer struct {
	lines []string
	total int
}

func NewTerminalDiagParser(cache *TerminalDiagCache) *TerminalDiagParser {
	return &TerminalDiagParser{
		cache:   cache,
		buffers: make(map[string]*termBuffer),
	}
}

// Feed processes terminal output for a given terminal session.
func (p *TerminalDiagParser) Feed(terminalID string, data string) {
	p.mu.Lock()
	buf, ok := p.buffers[terminalID]
	if !ok {
		buf = &termBuffer{}
		p.buffers[terminalID] = buf
	}
	p.mu.Unlock()

	newLines := strings.Split(data, "\n")
	buf.lines = append(buf.lines, newLines...)
	buf.total += len(newLines)

	// Keep rolling window of 50 lines for multi-line matching
	if len(buf.lines) > 50 {
		buf.lines = buf.lines[len(buf.lines)-50:]
	}

	window := strings.Join(buf.lines, "\n")

	for _, pat := range terminalPatterns {
		matches := pat.Regex.FindStringSubmatch(window)
		if matches == nil {
			continue
		}
		entry := pat.Extract(matches, window)
		if entry == nil {
			continue
		}
		entry.TerminalID = terminalID
		entry.Timestamp = time.Now()
		p.cache.Add(*entry)
	}

	// Also try line-by-line for single-line patterns
	for _, line := range newLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, pat := range terminalPatterns {
			if pat.Name == "panic" || pat.Name == "traceback" {
				continue // multi-line only
			}
			matches := pat.Regex.FindStringSubmatch(line)
			if matches == nil {
				continue
			}
			entry := pat.Extract(matches, line)
			if entry == nil {
				continue
			}
			entry.TerminalID = terminalID
			entry.Timestamp = time.Now()
			p.cache.Add(*entry)
		}
	}
}

// ClearTerminal removes the buffer for a terminal.
func (p *TerminalDiagParser) ClearTerminal(terminalID string) {
	p.mu.Lock()
	delete(p.buffers, terminalID)
	p.mu.Unlock()
	p.cache.ClearTerminal(terminalID)
}

func parseInt(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func extractPanicMessage(ctx string) string {
	lines := strings.Split(ctx, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "goroutine") && !strings.HasPrefix(line, "\t") {
			return line
		}
	}
	return "panic"
}

func extractFirstErrorLine(ctx string) string {
	lines := strings.Split(ctx, "\n")
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error") || strings.Contains(lower, "exception") {
			return strings.TrimSpace(line)
		}
	}
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0])
	}
	return ""
}

func extractLastLine(ctx string) string {
	lines := strings.Split(strings.TrimSpace(ctx), "\n")
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[len(lines)-1])
}

func truncateContext(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
