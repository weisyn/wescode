package codeintel

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	wesgine "github.com/weisyn/wesgine"
	"github.com/weisyn/wesgine/knowledge"
)

// kbOverlayMaxTokens bounds the KB Overlay injection. Documents compete for
// the remaining budget via ValueScore; the cap prevents a single long match
// from crowding out code fragments.
const kbOverlayMaxTokens = 800

// KBOverlay injects relevant project documentation fragments into the LLM
// context. It queries the Cell's Knowledge subsystem (populated by DocSync)
// with the user's message, surfacing design docs, AGENTS.md sections, and
// other indexed documentation alongside code context.
//
// The Cell is accessed via a getter because it is created asynchronously —
// BuildDefaultOverlays runs before Cell assignment (same pattern as CSEOverlay).
type KBOverlay struct {
	getCell func() *wesgine.Cell
}

// NewKBOverlay creates a KBOverlay that retrieves the Cell via the given getter.
func NewKBOverlay(getter func() *wesgine.Cell) *KBOverlay {
	return &KBOverlay{getCell: getter}
}

func (o *KBOverlay) Name() string { return "kb" }

func (o *KBOverlay) Enabled() bool {
	if o.getCell == nil {
		return false
	}
	cell := o.getCell()
	return cell != nil && cell.Knowledge() != nil
}

func (o *KBOverlay) Inject(ctx context.Context, params OverlayParams, fragments *[]CodeFragment) error {
	cell := o.getCell()
	if cell == nil {
		return nil
	}
	kb := cell.Knowledge()
	if kb == nil {
		return nil
	}

	query := params.UserMessage
	if query == "" {
		return nil
	}

	// Remaining budget after prior overlays.
	usedTokens := 0
	for _, f := range *fragments {
		usedTokens += f.TokenCost
	}
	remaining := params.Budget - usedTokens
	if remaining < 200 {
		return nil
	}

	results, err := kb.Search(ctx, query, knowledge.SearchOptions{Limit: 3})
	if err != nil {
		slog.Debug("[overlay/kb] search failed", "error", err)
		return nil
	}
	if len(results) == 0 {
		return nil
	}

	var sb strings.Builder
	sb.WriteString("Relevant project documentation:\n")
	tokensUsed := estimateTokens(sb.String())

	for _, sr := range results {
		for _, chunk := range sr.Chunks {
			// ChunkMatch has Content and Highlight; use Content for injection.
			line := fmt.Sprintf("\n--- %s ---\n%s\n", sr.FileName, chunk.Content)
			lineCost := estimateTokens(line)
			if tokensUsed+lineCost > kbOverlayMaxTokens {
				goto done
			}
			sb.WriteString(line)
			tokensUsed += lineCost
		}
	}
done:

	content := sb.String()
	if tokensUsed <= estimateTokens("Relevant project documentation:\n") {
		return nil
	}

	*fragments = append(*fragments, CodeFragment{
		Content:    content,
		Kind:       FragmentDocContext,
		Reason:     "project documentation matching user query",
		TokenCost:  estimateTokens(content),
		ValueScore: 0.6,
	})

	slog.Info("[overlay/kb] documentation injected",
		"tokens", estimateTokens(content),
		"results", len(results),
	)
	return nil
}
