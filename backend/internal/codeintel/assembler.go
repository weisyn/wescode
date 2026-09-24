package codeintel

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	wesgine "github.com/weisyn/wesgine"
	"github.com/weisyn/wesgine/message"

	"github.com/weisyn/wescode/internal/codeintel/constraints"
)

// CodeAssembler implements wesgine.ContextAssembler for programming scenarios.
// It orchestrates a pipeline of ContextOverlay instances, each responsible for
// a distinct code-intelligence signal (CKG retrieval, type context, CSE
// constraints, project mindmap). Stage 3 (agentic) is handled by the
// search_symbols tool that the model can call directly.
type CodeAssembler struct {
	inner          wesgine.ContextAssembler // delegate to wesgine default for Skills/compression
	retriever      *Retriever
	mu             sync.RWMutex
	state          EditorState
	cachedTaskType TaskType              // cached from first non-general classification within a Run
	onSnapshot     SnapshotCallback      // optional: called after Assemble with context summary
	metrics        *RetrievalMetrics     // retrieval quality metrics
	gitProvider    *GitContextProvider   // optional: git diff context injection
	typeCtx        *TypeContextProvider  // optional: LSP type-aware context injection
	constraintReg  *constraints.Registry // optional: CSE constraint injection (INV-P2-02)

	overlays          []ContextOverlay     // pipeline of context overlays, executed in order
	workspaceStatusFn func() string        // optional: returns workspace status line for AI context
	getCellFn         func() *wesgine.Cell // optional: getter for KB overlay (Cell created after Build)
}

var _ wesgine.ContextAssembler = (*CodeAssembler)(nil)

// NewCodeAssembler creates an assembler that wraps the default assembler
// and enriches context with code intelligence.
func NewCodeAssembler(retriever *Retriever) *CodeAssembler {
	return &CodeAssembler{
		retriever: retriever,
		metrics:   NewRetrievalMetrics(),
	}
}

// Metrics returns the retrieval metrics collector for external inspection.
func (ca *CodeAssembler) Metrics() *RetrievalMetrics {
	return ca.metrics
}

// SetRetriever sets the retriever (allows circular init with metrics).
func (ca *CodeAssembler) SetRetriever(r *Retriever) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.retriever = r
}

// SetSnapshotCallback registers a callback invoked after each Assemble
// with a summary of the injected code context (for transparency/debugging).
func (ca *CodeAssembler) SetSnapshotCallback(fn SnapshotCallback) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.onSnapshot = fn
}

// SetGitProvider injects a GitContextProvider for working tree diff injection.
func (ca *CodeAssembler) SetGitProvider(gp *GitContextProvider) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.gitProvider = gp
}

// SetTypeContextProvider injects a TypeContextProvider for LSP type-aware injection.
func (ca *CodeAssembler) SetTypeContextProvider(tcp *TypeContextProvider) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.typeCtx = tcp
}

// SetConstraintRegistry injects a CSE Registry for context-time constraint injection.
func (ca *CodeAssembler) SetConstraintRegistry(reg *constraints.Registry) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.constraintReg = reg
}

// AddOverlay appends an overlay to the pipeline. Order matters: overlays
// execute sequentially and earlier overlays consume budget first.
func (ca *CodeAssembler) AddOverlay(o ContextOverlay) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.overlays = append(ca.overlays, o)
}

// BuildDefaultOverlays constructs and registers the standard overlay pipeline.
// Call after all Set* providers are configured. The CSE overlay uses a getter
// because the constraint registry is created asynchronously in postInitialize.
func (ca *CodeAssembler) BuildDefaultOverlays(index *CodeIndex) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.overlays = []ContextOverlay{
		NewCKGOverlay(ca.retriever, ca),
		NewTypeOverlay(ca.typeCtx),
		NewCSEOverlay(func() *constraints.Registry {
			ca.mu.RLock()
			defer ca.mu.RUnlock()
			return ca.constraintReg
		}),
		NewKBOverlay(func() *wesgine.Cell {
			ca.mu.RLock()
			defer ca.mu.RUnlock()
			if ca.getCellFn == nil {
				return nil
			}
			return ca.getCellFn()
		}),
		NewMindMapOverlay(index),
	}
}

// TypeContextFor resolves type bindings for the given file range.
// Returns nil if no TypeContextProvider is configured or LSP is unavailable.
func (ca *CodeAssembler) TypeContextFor(ctx context.Context, path string, fromLine, toLine int) *TypeContext {
	ca.mu.RLock()
	tcp := ca.typeCtx
	ca.mu.RUnlock()
	if tcp == nil {
		return nil
	}
	return tcp.Resolve(ctx, path, fromLine, toLine)
}

// ResetRunState clears per-Run cached state. Must be called at the start of
// each RunChat to prevent stale task classification from leaking across runs.
func (ca *CodeAssembler) ResetRunState() {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.cachedTaskType = ""
	if ca.gitProvider != nil {
		ca.gitProvider.InvalidateCache()
	}
	// INV-CI-23: clear LSP result cache at Run boundary.
	if ca.retriever != nil {
		ca.retriever.LSPCache().Reset()
	}
}

// SetCellGetter registers a getter for the wesgine Cell, used by KBOverlay.
// The Cell is created after BuildDefaultOverlays, so a getter is needed.
func (ca *CodeAssembler) SetCellGetter(fn func() *wesgine.Cell) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.getCellFn = fn
}

// SetWorkspaceStatusFn registers a callback that returns a one-line workspace
// status summary injected into every Run's system context. This lets the AI
// know whether CKG/KB are available, indexing, or unavailable.
func (ca *CodeAssembler) SetWorkspaceStatusFn(fn func() string) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.workspaceStatusFn = fn
}

// WrapperFn returns a function suitable for wesgine.WithContextAssemblerWrapper.
// It captures this CodeAssembler instance and wires the inner assembler at boot time.
func (ca *CodeAssembler) WrapperFn() wesgine.ContextAssemblerWrapperFn {
	return func(inner wesgine.ContextAssembler) wesgine.ContextAssembler {
		ca.inner = inner
		return ca
	}
}

// UpdateEditorState updates the current editor state (called by the RPC layer
// when the extension reports cursor/file changes).
func (ca *CodeAssembler) UpdateEditorState(state EditorState) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.state = state
}

// EditorState returns the current editor state snapshot for diagnostics.
func (ca *CodeAssembler) EditorState() EditorState {
	ca.mu.RLock()
	defer ca.mu.RUnlock()
	return ca.state
}

// Assemble implements wesgine.ContextAssembler.
// It delegates to the inner assembler first (Skills/compression), then runs
// the overlay pipeline to inject code-intelligence fragments.
func (ca *CodeAssembler) Assemble(ctx context.Context, params *wesgine.AssembleParams) (*wesgine.AssembleResult, error) {
	assembleStart := time.Now()
	result, err := ca.inner.Assemble(ctx, params)
	if err != nil {
		return nil, err
	}

	ca.mu.RLock()
	state := ca.state
	overlays := ca.overlays
	wsFn := ca.workspaceStatusFn
	ca.mu.RUnlock()

	if wsFn != nil {
		if status := wsFn(); status != "" {
			for i := range result.Messages {
				if result.Messages[i].Role == message.RoleSystem {
					blocks := result.Messages[i].Content
					blocks = append(blocks, message.NewTextBlock("\n"+status))
					result.Messages[i].Content = blocks
					break
				}
			}
		}
	}

	userMessage := extractUserMessage(params.Messages)
	need := ca.resolveContextNeed(userMessage)
	// RemainingInput is wallet − CompactBuffer − assembled occupancy. Product
	// share is 40% of that remainder, capped at 30k. RemainingInput≤0 means
	// the wallet is exhausted — do not fall back to the pre-buffer estimate
	// (INV-CTX-76).
	codeBudget := result.RemainingInput * 40 / 100
	if codeBudget > 30000 {
		codeBudget = 30000
	}
	slog.Info("[codeintel] Assemble",
		"focusFile", state.FocusFile,
		"cursorLine", state.CursorLine,
		"codeBudget", codeBudget,
		"taskClassification", string(need.TaskType),
		"scope", need.Scope,
		"overlayCount", len(overlays),
	)

	if codeBudget <= 0 {
		return result, nil
	}

	// Run overlay pipeline: each overlay appends fragments within budget.
	overlayParams := OverlayParams{
		State:       state,
		UserMessage: userMessage,
		Need:        need,
		Budget:      codeBudget,
	}

	var fragments []CodeFragment
	for _, o := range overlays {
		if !o.Enabled() {
			continue
		}
		if err := o.Inject(ctx, overlayParams, &fragments); err != nil {
			slog.Warn("[codeintel] overlay error", "overlay", o.Name(), "error", err)
		}
	}

	// Legacy fallback: if no overlays configured, use inline retrieval.
	if len(overlays) == 0 {
		fragments = ca.legacyRetrieve(ctx, state, userMessage, codeBudget, need)
	}

	// Final budget pack — all overlays compete via ValueScore.
	allCandidates := fragments
	fragments = budgetPack(fragments, codeBudget)

	if len(fragments) == 0 {
		slog.Debug("[codeintel] Assemble: no fragments retrieved")
		return result, nil
	}

	for _, f := range fragments {
		slog.Info("[codeintel] fragment",
			"kind", string(f.Kind),
			"path", f.Path,
			"symbol", f.Symbol,
			"tokens", f.TokenCost,
			"reason", f.Reason,
		)
	}

	// MetaCognition hint (CE-INV-03: < 200 tokens).
	var readiness ReadinessReport
	if ca.retriever != nil && ca.retriever.index != nil {
		readiness = ca.retriever.index.Readiness()
	}
	hint := BuildMetaCognitionHint(fragments, allCandidates, state, readiness)

	contextMsg := fragmentsToMessageWithHint(fragments, hint)
	if contextMsg == nil {
		return result, nil
	}

	result.Messages = injectCodeContext(result.Messages, *contextMsg)

	totalCodeTokens := 0
	for _, f := range fragments {
		totalCodeTokens += f.TokenCost
	}
	if hint != "" {
		totalCodeTokens += estimateTokens(hint)
	}
	result.TokenEstimate += totalCodeTokens

	slog.Info("[codeintel] Assemble complete",
		"fragmentCount", len(fragments),
		"codeTokens", totalCodeTokens,
		"totalTokenEstimate", result.TokenEstimate,
		"hasMindMap", hasMindMapFragment(fragments),
		"truncatedCount", len(allCandidates)-len(fragments),
	)

	if ca.metrics != nil {
		ca.metrics.RecordAssemble(need.TaskType, len(fragments), totalCodeTokens, codeBudget, time.Since(assembleStart))
	}

	ca.mu.RLock()
	snapshotFn := ca.onSnapshot
	ca.mu.RUnlock()
	if snapshotFn != nil {
		summaries := make([]FragmentSummary, len(fragments))
		for i, f := range fragments {
			summaries[i] = FragmentSummary{
				Path:       f.Path,
				Symbol:     f.Symbol,
				Kind:       string(f.Kind),
				Reason:     f.Reason,
				TokenCost:  f.TokenCost,
				ValueScore: f.ValueScore,
			}
		}
		snapshotFn(ContextSnapshot{
			Fragments:        summaries,
			TokenBudget:      codeBudget,
			TokenUsed:        totalCodeTokens,
			TaskType:         string(need.TaskType),
			HasMindMap:       hasMindMapFragment(fragments),
			TruncatedCount:   len(allCandidates) - len(fragments),
			ContextNeedScope: need.Scope,
		})
	}

	return result, nil
}

// legacyRetrieve preserves the pre-pipeline inline retrieval path for backward
// compatibility when no overlays are registered.
func (ca *CodeAssembler) legacyRetrieve(ctx context.Context, state EditorState, userMessage string, codeBudget int, need ContextNeed) []CodeFragment {
	if ca.retriever == nil {
		return nil
	}

	ca.mu.RLock()
	gp := ca.gitProvider
	ca.mu.RUnlock()
	if gp != nil && state.FocusFile != "" {
		workDir := dirOf(state.FocusFile)
		if len(state.WorkspaceRoots) > 0 {
			workDir = state.WorkspaceRoots[0]
		}
		diff := gp.DiffSummary(ctx, workDir)
		ca.retriever.SetGitDiff(diff)
	}

	var fragments []CodeFragment
	if state.FocusFile == "" {
		fragments = ca.retriever.noFocusFallback(ctx, state, userMessage, codeBudget)
	} else {
		fragments = ca.retriever.RetrieveV2(ctx, state, userMessage, codeBudget, need)
	}

	if mm := ca.retriever.index.GetMindMap(ctx); mm != nil {
		usedTokens := 0
		for _, f := range fragments {
			usedTokens += f.TokenCost
		}
		remaining := codeBudget - usedTokens
		if remaining > 200 {
			maxMM := remaining / 3
			if maxMM > 500 {
				maxMM = 500
			}
			content := mm.ToContextString(maxMM)
			if content != "" {
				fragments = append(fragments, CodeFragment{
					Content:    content,
					Kind:       FragmentProjectMap,
					Reason:     "project structure overview",
					TokenCost:  estimateTokens(content),
					ValueScore: 0.15,
				})
			}
		}
	}

	ca.mu.RLock()
	tcp := ca.typeCtx
	ca.mu.RUnlock()
	if tcp != nil && state.FocusFile != "" {
		fromLine, toLine := state.CursorLine-10, state.CursorLine+10
		if fromLine < 0 {
			fromLine = 0
		}
		if state.VisibleRange != nil {
			fromLine = state.VisibleRange.Start
			toLine = state.VisibleRange.End
		}
		if tc := tcp.Resolve(ctx, state.FocusFile, fromLine, toLine); tc != nil {
			typeStr := tc.FormatForPrompt()
			if typeStr != "" && estimateTokens(typeStr) <= 300 {
				fragments = append(fragments, CodeFragment{
					Content:    typeStr,
					Kind:       FragmentTypeContext,
					Path:       state.FocusFile,
					Reason:     "LSP type bindings for visible code",
					TokenCost:  estimateTokens(typeStr),
					ValueScore: 0.7,
				})
			}
		}
	}

	ca.mu.RLock()
	cseReg := ca.constraintReg
	ca.mu.RUnlock()
	if cseReg != nil {
		ca.injectCSEConstraints(state, &fragments)
	}

	return fragments
}

// resolveContextNeed performs multi-signal classification using ClassifyTaskV2
// and caches the first non-general result for the duration of the Run
// (CI-11: task classification is stable within a Run).
func (ca *CodeAssembler) resolveContextNeed(userMessage string) ContextNeed {
	ca.mu.RLock()
	state := ca.state
	ca.mu.RUnlock()

	need := ClassifyTaskV2(userMessage, state)

	if need.TaskType != TaskGeneral {
		ca.mu.Lock()
		ca.cachedTaskType = need.TaskType
		ca.mu.Unlock()
	} else {
		ca.mu.RLock()
		cached := ca.cachedTaskType
		ca.mu.RUnlock()
		if cached != "" {
			need.TaskType = cached
		}
	}
	return need
}

// extractUserMessage returns the text of the last user message.
func extractUserMessage(msgs []message.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == message.RoleUser {
			var sb strings.Builder
			for _, block := range msgs[i].Content {
				if block.Type == "text" {
					sb.WriteString(block.Text)
				}
			}
			return sb.String()
		}
	}
	return ""
}

// injectCodeContext inserts the code context message before the last user message.
func injectCodeContext(msgs []message.Message, contextMsg message.Message) []message.Message {
	lastUserIdx := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == message.RoleUser {
			lastUserIdx = i
			break
		}
	}

	if lastUserIdx < 0 {
		return append(msgs, contextMsg)
	}

	result := make([]message.Message, 0, len(msgs)+1)
	result = append(result, msgs[:lastUserIdx]...)
	result = append(result, contextMsg)
	result = append(result, msgs[lastUserIdx:]...)
	return result
}

// injectCSEConstraints adds the constraints that match the focus file. Shares
// cseConstraintFragment with CSEOverlay: same wording, same caps, same
// focus-is-the-scope rule (INV-CSE-16).
func (ca *CodeAssembler) injectCSEConstraints(state EditorState, frags *[]CodeFragment) {
	ca.mu.RLock()
	reg := ca.constraintReg
	ca.mu.RUnlock()

	frag, ok := cseConstraintFragment(reg, state.FocusFile)
	if !ok {
		return
	}
	*frags = append(*frags, frag)

	slog.Info("[codeintel] CSE constraints injected",
		"tokens", frag.TokenCost,
		"focusFile", state.FocusFile,
	)
}
