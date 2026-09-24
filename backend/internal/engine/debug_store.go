package engine

import (
	"sync"
	"time"
)

// DebugEventStore is a thread-safe in-memory ring buffer for debug events.
// INV-DEBUG-03: max 50 events, no DB persistence.
type DebugEventStore struct {
	mu     sync.Mutex
	events []DebugEvent
	maxLen int
}

// DebugEvent is the unified debug event protocol (D1).
type DebugEvent struct {
	Kind        string    `json:"kind"` // breakpoint_hit | exception | terminated | output | process_exit | crash | test_failure
	SessionID   string    `json:"sessionId,omitempty"`
	Timestamp   time.Time `json:"-"`
	TimestampMS int64     `json:"timestamp"` // Unix milliseconds from Extension; converted to Timestamp on push

	// breakpoint_hit / exception
	ThreadID   int             `json:"threadId,omitempty"`
	StopReason string          `json:"stopReason,omitempty"` // breakpoint | exception | step | pause
	Frames     []DebugFrame    `json:"frames,omitempty"`
	Variables  []DebugVariable `json:"variables,omitempty"`
	Exception  *DebugException `json:"exception,omitempty"`

	// output (debug console stdout/stderr)
	Output *DebugOutput `json:"output,omitempty"`

	// process_exit / crash
	ExitCode  *int   `json:"exitCode,omitempty"`
	CrashText string `json:"crashText,omitempty"`
	Language  string `json:"language,omitempty"`
	Source    string `json:"source,omitempty"` // terminal | task | dap
}

// DebugFrame represents a single stack frame.
type DebugFrame struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	ModuleID string `json:"moduleId,omitempty"`
}

// DebugVariable represents a variable in a scope.
type DebugVariable struct {
	Scope      string `json:"scope"` // local | closure | global
	Name       string `json:"name"`
	Value      string `json:"value"`
	Type       string `json:"type"`
	ChildCount int    `json:"childCount"`
}

// DebugException holds exception/panic details.
type DebugException struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	StackTrace  string `json:"stackTrace"`
}

// DebugOutput holds debug console output.
type DebugOutput struct {
	Category string `json:"category"` // stdout | stderr | console
	Text     string `json:"text"`
}

// NewDebugEventStore creates a store with the given capacity.
func NewDebugEventStore(maxLen int) *DebugEventStore {
	if maxLen <= 0 {
		maxLen = 50
	}
	return &DebugEventStore{maxLen: maxLen}
}

// Push adds an event, evicting the oldest if at capacity.
func (s *DebugEventStore) Push(e DebugEvent) {
	if e.TimestampMS > 0 && e.Timestamp.IsZero() {
		e.Timestamp = time.UnixMilli(e.TimestampMS)
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) >= s.maxLen {
		s.events = s.events[1:]
	}
	s.events = append(s.events, e)
}

// Recent returns the last n events (most recent last).
func (s *DebugEventStore) Recent(n int) []DebugEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n <= 0 || n > len(s.events) {
		n = len(s.events)
	}
	start := len(s.events) - n
	out := make([]DebugEvent, n)
	copy(out, s.events[start:])
	return out
}

// LastException returns the most recent exception/crash event, or nil.
func (s *DebugEventStore) LastException() *DebugEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.events) - 1; i >= 0; i-- {
		e := s.events[i]
		if isActionableEvent(e.Kind) {
			return &e
		}
	}
	return nil
}

// ConsumeLastException returns and removes the most recent exception/crash/log_anomaly event.
// Returns nil if none exists. Prevents duplicate injection on consecutive Runs.
func (s *DebugEventStore) ConsumeLastException() *DebugEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.events) - 1; i >= 0; i-- {
		if isActionableEvent(s.events[i].Kind) {
			e := s.events[i]
			s.events = append(s.events[:i], s.events[i+1:]...)
			return &e
		}
	}
	return nil
}

func isActionableEvent(kind string) bool {
	return kind == "exception" || kind == "crash" || kind == "log_anomaly" || kind == "test_failure"
}

// Clear removes all events.
func (s *DebugEventStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = nil
}

// Len returns the number of stored events.
func (s *DebugEventStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}
