package codeintel

import (
	"strings"

	"github.com/weisyn/wesgine/message"
)

// BudgetAllocator implements the waterfall budget allocation strategy:
// P0 (mandatory) → P1 (high) → P2 (medium) → P3 (low).
type BudgetAllocator struct {
	totalBudget int
	used        int
}

// NewBudgetAllocator creates an allocator with the given total token budget.
func NewBudgetAllocator(totalBudget int) *BudgetAllocator {
	return &BudgetAllocator{totalBudget: totalBudget}
}

// Remaining returns the unused token budget.
func (ba *BudgetAllocator) Remaining() int {
	r := ba.totalBudget - ba.used
	if r < 0 {
		return 0
	}
	return r
}

// Consume deducts tokens from the budget.
func (ba *BudgetAllocator) Consume(tokens int) {
	ba.used += tokens
}

// CanFit checks if a fragment fits in the remaining budget.
func (ba *BudgetAllocator) CanFit(tokens int) bool {
	return ba.used+tokens <= ba.totalBudget
}

// Used returns the number of tokens consumed so far.
func (ba *BudgetAllocator) Used() int {
	return ba.used
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + itoa(-n)
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// fragmentsToMessageWithHint is like fragmentsToMessage but appends a
// metacognition hint at the end of the context message (if non-empty).
func fragmentsToMessageWithHint(fragments []CodeFragment, hint string) *message.Message {
	if len(fragments) == 0 {
		return nil
	}

	var sb strings.Builder
	sb.WriteString("[Code Context — Workspace Snapshot]\n")
	sb.WriteString("Below is code from the active editor, open tabs, recent edits, and git working tree. ")
	sb.WriteString("It may NOT be the file the user is asking about. ")
	sb.WriteString("When the user names a specific file, locate that file with tools before acting.\n\n")

	for _, f := range fragments {
		if f.Path != "" {
			sb.WriteString("--- ")
			sb.WriteString(f.Path)
			if f.StartLine > 0 {
				sb.WriteString(" (lines ")
				sb.WriteString(itoa(f.StartLine))
				sb.WriteString("-")
				sb.WriteString(itoa(f.EndLine))
				sb.WriteString(")")
			}
			sb.WriteString(" ---\n")
		}
		if f.Symbol != "" {
			sb.WriteString("// ")
			sb.WriteString(f.Symbol)
			sb.WriteString("\n")
		}
		sb.WriteString(f.Content)
		if !strings.HasSuffix(f.Content, "\n") {
			sb.WriteByte('\n')
		}
		sb.WriteByte('\n')
	}

	if hint != "" {
		sb.WriteString(hint)
	}

	content := sb.String()
	msg := message.Message{
		Role:    message.RoleUser,
		Content: []message.ContentBlock{message.NewTextBlock(content)},
	}
	return &msg
}

// hasMindMapFragment checks if any fragment is a project mind map.
func hasMindMapFragment(fragments []CodeFragment) bool {
	for _, f := range fragments {
		if f.Kind == FragmentProjectMap {
			return true
		}
	}
	return false
}
