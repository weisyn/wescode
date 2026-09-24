package codeintel

import "context"

// ContextOverlay injects code-intelligence fragments into the LLM context.
// Each overlay is independently testable, configurable, and budget-aware.
// The pipeline orchestrator (CodeAssembler) calls overlays sequentially;
// each overlay appends to the shared fragments slice within its token budget.
type ContextOverlay interface {
	// Name returns the overlay identifier (for logging/metrics).
	Name() string

	// Enabled reports whether this overlay should participate in the current Assemble.
	Enabled() bool

	// Inject appends fragments to the accumulator. budget is the remaining
	// token budget after prior overlays; implementations must respect it.
	Inject(ctx context.Context, params OverlayParams, fragments *[]CodeFragment) error
}

// OverlayParams carries the shared context for all overlays within a single Assemble call.
type OverlayParams struct {
	State       EditorState
	UserMessage string
	Need        ContextNeed
	Budget      int // remaining token budget for code context
}
