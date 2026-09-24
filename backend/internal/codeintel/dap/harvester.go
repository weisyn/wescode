package dap

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
)

// EdgeRuntimeCalls is the edge kind for runtime-verified call relationships.
const EdgeRuntimeCalls = "runtime_calls"

// Harvester collects runtime call relationships from DAP stack traces.
// Each breakpoint hit produces a chain of RUNTIME_CALLS edges from the
// call stack, providing ground-truth validation for CKG's static CALLS edges.
type Harvester struct {
	mu    sync.Mutex
	edges []RuntimeEdge
}

// RuntimeEdge represents a runtime-verified call relationship.
type RuntimeEdge struct {
	CallerName string
	CallerFile string
	CallerLine int
	CalleeName string
	CalleeFile string
	CalleeLine int
}

func NewHarvester() *Harvester {
	return &Harvester{}
}

// OnStackTrace processes a stack trace from a debug breakpoint hit.
// Creates RUNTIME_CALLS edges: frame[i+1] → frame[i] for consecutive pairs.
func (h *Harvester) OnStackTrace(event StackTraceEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()

	frames := event.Frames
	for i := 0; i < len(frames)-1; i++ {
		caller := frames[i+1]
		callee := frames[i]

		h.edges = append(h.edges, RuntimeEdge{
			CallerName: caller.Name,
			CallerFile: caller.Source,
			CallerLine: caller.Line,
			CalleeName: callee.Name,
			CalleeFile: callee.Source,
			CalleeLine: callee.Line,
		})
	}

	slog.Debug("[dap] stack trace harvested", "frames", len(frames), "edges", len(frames)-1)
}

// Drain returns all collected runtime edges and clears the buffer.
func (h *Harvester) Drain() []RuntimeEdge {
	h.mu.Lock()
	defer h.mu.Unlock()
	edges := h.edges
	h.edges = nil
	return edges
}

// EdgeCount returns the number of collected edges.
func (h *Harvester) EdgeCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.edges)
}

// QualifyFrame generates a qualified name for a stack frame
// suitable for CKG node matching.
func QualifyFrame(f StackFrame) string {
	dir := filepath.Dir(f.Source)
	return fmt.Sprintf("%s:%s:%d", dir, f.Name, f.Line)
}
