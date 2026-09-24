package dap

// StackFrame represents a single frame from a DAP stackTrace response.
type StackFrame struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`   // function name
	Source string `json:"source"` // file path
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// StackTraceEvent is received from the Extension Host when a debug
// breakpoint is hit. The Extension pushes the active thread's stack trace.
type StackTraceEvent struct {
	ThreadID int          `json:"thread_id"`
	Frames   []StackFrame `json:"frames"`
}
