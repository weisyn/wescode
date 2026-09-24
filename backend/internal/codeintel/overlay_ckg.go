package codeintel

import (
	"context"
	"log/slog"
)

// CKGOverlay retrieves code fragments from the Code Knowledge Graph via
// the Retriever. This is the primary context source — it provides
// callers, callees, type definitions, and related symbols based on the
// editor focus and user message.
type CKGOverlay struct {
	retriever *Retriever
	assembler *CodeAssembler // for EditorState + GitDiff injection
}

func NewCKGOverlay(retriever *Retriever, assembler *CodeAssembler) *CKGOverlay {
	return &CKGOverlay{retriever: retriever, assembler: assembler}
}

func (o *CKGOverlay) Name() string  { return "ckg" }
func (o *CKGOverlay) Enabled() bool { return o.retriever != nil }

func (o *CKGOverlay) Inject(ctx context.Context, params OverlayParams, fragments *[]CodeFragment) error {
	if o.retriever == nil || params.Budget <= 0 {
		return nil
	}

	// Inject git diff into retriever before retrieval (CE-12).
	o.assembler.mu.RLock()
	gp := o.assembler.gitProvider
	o.assembler.mu.RUnlock()
	if gp != nil && params.State.FocusFile != "" {
		workDir := dirOf(params.State.FocusFile)
		if len(params.State.WorkspaceRoots) > 0 {
			workDir = params.State.WorkspaceRoots[0]
		}
		diff := gp.DiffSummary(ctx, workDir)
		o.retriever.SetGitDiff(diff)
	}

	o.retriever.SetStrategy(params.Need.TaskType)

	var frags []CodeFragment
	if params.State.FocusFile == "" {
		frags = o.retriever.noFocusFallback(ctx, params.State, params.UserMessage, params.Budget)
	} else {
		frags = o.retriever.RetrieveV2(ctx, params.State, params.UserMessage, params.Budget, params.Need)
	}

	slog.Debug("[overlay/ckg] retrieved", "count", len(frags), "budget", params.Budget)
	*fragments = append(*fragments, frags...)
	return nil
}
