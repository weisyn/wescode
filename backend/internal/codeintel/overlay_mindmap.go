package codeintel

import (
	"context"
	"log/slog"
)

// MindMapOverlay injects the project structure overview (MindMap) as a
// low-priority fragment that competes for remaining budget via ValueScore.
type MindMapOverlay struct {
	index *CodeIndex
}

func NewMindMapOverlay(index *CodeIndex) *MindMapOverlay {
	return &MindMapOverlay{index: index}
}

func (o *MindMapOverlay) Name() string  { return "mindmap" }
func (o *MindMapOverlay) Enabled() bool { return o.index != nil }

func (o *MindMapOverlay) Inject(ctx context.Context, params OverlayParams, fragments *[]CodeFragment) error {
	if o.index == nil || params.Budget <= 200 {
		return nil
	}

	mm := o.index.GetMindMap(ctx)
	if mm == nil {
		return nil
	}

	// Use at most 1/3 of remaining budget, capped at 500 tokens.
	usedTokens := 0
	for _, f := range *fragments {
		usedTokens += f.TokenCost
	}
	remaining := params.Budget - usedTokens
	if remaining <= 200 {
		return nil
	}
	maxMM := remaining / 3
	if maxMM > 500 {
		maxMM = 500
	}

	content := mm.ToContextString(maxMM)
	if content == "" {
		return nil
	}

	*fragments = append(*fragments, CodeFragment{
		Content:    content,
		Kind:       FragmentProjectMap,
		Reason:     "project structure overview",
		TokenCost:  estimateTokens(content),
		ValueScore: 0.15,
	})

	slog.Debug("[overlay/mindmap] injected", "tokens", estimateTokens(content))
	return nil
}
