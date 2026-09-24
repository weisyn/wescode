package codeintel

import (
	"context"
	"log/slog"
)

// TypeOverlay injects LSP type bindings for the visible code range.
// It resolves hover/definition information through the IDE LSP bridge
// and formats type signatures for the model.
type TypeOverlay struct {
	provider *TypeContextProvider
}

func NewTypeOverlay(provider *TypeContextProvider) *TypeOverlay {
	return &TypeOverlay{provider: provider}
}

func (o *TypeOverlay) Name() string  { return "type" }
func (o *TypeOverlay) Enabled() bool { return o.provider != nil }

func (o *TypeOverlay) Inject(ctx context.Context, params OverlayParams, fragments *[]CodeFragment) error {
	if o.provider == nil || params.State.FocusFile == "" {
		return nil
	}

	fromLine, toLine := params.State.CursorLine-10, params.State.CursorLine+10
	if fromLine < 0 {
		fromLine = 0
	}
	if params.State.VisibleRange != nil {
		fromLine = params.State.VisibleRange.Start
		toLine = params.State.VisibleRange.End
	}

	tc := o.provider.Resolve(ctx, params.State.FocusFile, fromLine, toLine)
	if tc == nil {
		return nil
	}

	typeStr := tc.FormatForPrompt()
	if typeStr == "" {
		return nil
	}

	tokens := estimateTokens(typeStr)
	if tokens > 300 {
		slog.Debug("[overlay/type] skipped: exceeds 300 token cap", "tokens", tokens)
		return nil
	}

	*fragments = append(*fragments, CodeFragment{
		Content:    typeStr,
		Kind:       FragmentTypeContext,
		Path:       params.State.FocusFile,
		Reason:     "LSP type bindings for visible code",
		TokenCost:  tokens,
		ValueScore: 0.7,
	})

	slog.Debug("[overlay/type] injected", "tokens", tokens, "file", params.State.FocusFile)
	return nil
}
