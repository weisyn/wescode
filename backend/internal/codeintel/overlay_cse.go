package codeintel

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/weisyn/wescode/internal/codeintel/constraints"
)

// cseOverlayMaxConstraints and cseOverlayMaxTokens bound the CSE Overlay
// (INV-CSE-16). Both the standalone overlay and the assembler's inline path go
// through cseConstraintFragment so the wording and the caps stay identical.
const (
	cseOverlayMaxConstraints = 10
	cseOverlayMaxTokens      = 500
)

// cseConstraintFragment renders the constraints that match focusFile, or false
// when nothing should be injected.
//
// An empty or relative focusFile yields nothing. Focus is the scope: without it
// there is no way to tell which root the model is editing, and the old
// registry.Active() fallback answered that by injecting every root's rules —
// which is how an edit in wesclaw arrived carrying wesgine invariants
// (INV-CSE-10). MatchesFile also needs an absolute path to test root
// containment at all.
func cseConstraintFragment(reg *constraints.Registry, focusFile string) (CodeFragment, bool) {
	if reg == nil || !filepath.IsAbs(focusFile) {
		return CodeFragment{}, false
	}
	active := reg.MatchingFile(focusFile)
	if len(active) == 0 {
		return CodeFragment{}, false
	}

	var sb strings.Builder
	sb.WriteString("Active constraints for this file:\n")
	for i, c := range active {
		if i >= cseOverlayMaxConstraints {
			break
		}
		line := fmt.Sprintf("- [%s] %s (confidence: %.0f%%)\n", c.Kind, c.Rule, c.Confidence*100)
		if estimateTokens(sb.String()+line) > cseOverlayMaxTokens {
			break
		}
		sb.WriteString(line)
	}
	text := sb.String()

	return CodeFragment{
		Kind:       FragmentConstraint,
		Content:    text,
		Reason:     "active constraints for focus file",
		TokenCost:  estimateTokens(text),
		ValueScore: 0.8,
	}, true
}

// CSEOverlay injects the constraints that apply to the current focus file as a
// user-message fragment. It is the only channel that carries constraint prose
// to the model ahead of an edit; PreWrite carries FAIL advisories after one.
//
// The registry is accessed via a getter function because it's created
// asynchronously in postInitialize (30s+ after boot).
type CSEOverlay struct {
	getRegistry func() *constraints.Registry
}

func NewCSEOverlay(getter func() *constraints.Registry) *CSEOverlay {
	return &CSEOverlay{getRegistry: getter}
}

func (o *CSEOverlay) Name() string { return "cse" }
func (o *CSEOverlay) Enabled() bool {
	return o.getRegistry != nil && o.getRegistry() != nil
}

func (o *CSEOverlay) Inject(_ context.Context, params OverlayParams, fragments *[]CodeFragment) error {
	frag, ok := cseConstraintFragment(o.getRegistry(), params.State.FocusFile)
	if !ok {
		return nil
	}
	*fragments = append(*fragments, frag)

	slog.Info("[overlay/cse] constraints injected",
		"tokens", frag.TokenCost,
		"focusFile", params.State.FocusFile,
	)
	return nil
}
