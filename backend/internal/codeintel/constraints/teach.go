package constraints

import (
	"fmt"
	"path/filepath"
	"strings"
)

// EditRejection captures the context when a user rejects an AI-generated edit.
type EditRejection struct {
	FilePath     string // absolute path of the rejected file
	OriginalCode string // what AI tried to write
	Symbol       string // function/type being edited
	Reason       string // user's rejection reason (if provided)
}

// EditModification captures the context when a user accepts but modifies an AI edit.
type EditModification struct {
	FilePath string // absolute path of the modified file
	AICode   string // what AI generated
	UserCode string // what user modified it to
	Symbol   string
}

// TeachFromEditRejection registers a constraint when a user rejects an AI edit.
//
// Only one rejection class survives the CSE rewrite: discarded errors, because
// that is the only class with an executable predicate (err_discard). The old
// classifier also emitted type_mismatch / dependency_violation / api_change /
// convention constraints whose Rule was pure prose with nothing to evaluate —
// those are Knowledge, not constraints (INV-CSE-17), and a rejection reason
// mentioning "type" is far too weak a signal to pin a checker to anyway.
//
// Confidence starts at 0.55 — a candidate, well under the 0.8 activate
// threshold. One rejection is not a rule: it could be this line, this moment,
// or a changed mind. Three repeats on the same file+predicate cross into
// active via Promote. Humans wanting immediate effect use Confirm instead.
func TeachFromEditRejection(reg *Registry, roots []string, rejection EditRejection) *Constraint {
	root, rel := resolveTaughtFile(roots, rejection.FilePath)
	if reg == nil || root == "" {
		return nil
	}
	if !rejectedForErrorHandling(rejection) {
		return nil
	}

	// Build first, look up second: AtFile binds the constraint and root-scopes
	// its ID, and the scoped form is what the registry keys on. Probing with the
	// raw LearnedID would never hit, so every repeat rejection would re-enter at
	// the 0.55 floor instead of climbing.
	c := Constraint{
		ID:         LearnedID("reject", root+"/"+rel+":"+rejection.Symbol+":err_discard"),
		Rule:       fmt.Sprintf("Handle error returns in %s — the user rejected an edit that discarded them.", rel),
		Kind:       "quality",
		Priority:   PriorityQuality,
		Status:     StatusCandidate,
		Confidence: 0.55,
		Source:     SourceLearned,
		TTL:        90,
	}.AtFile(root, rel, CheckErrDiscard)

	if boosted := reg.PromoteByID(c.ID, 0.1); boosted != nil {
		return boosted
	}
	return reg.Add(c)
}

// TeachFromEditModification registers a constraint when a user accepts but
// modifies an AI edit. As with rejection, the only modification pattern that
// maps to a predicate is "AI dropped the error check, user added it back".
// Confidence starts at 0.50 and climbs by 0.05 — lower and slower than
// rejection because the edit was partially acceptable, so the signal is weaker.
func TeachFromEditModification(reg *Registry, roots []string, modification EditModification) *Constraint {
	root, rel := resolveTaughtFile(roots, modification.FilePath)
	if reg == nil || root == "" {
		return nil
	}
	if !addedErrorHandling(modification.AICode, modification.UserCode) {
		return nil
	}

	c := Constraint{
		ID:         LearnedID("modify", root+"/"+rel+":"+modification.Symbol+":err_discard"),
		Rule:       fmt.Sprintf("Handle error returns in %s — the user added the error check back after an AI edit.", rel),
		Kind:       "quality",
		Priority:   PriorityQuality,
		Status:     StatusCandidate,
		Confidence: 0.50,
		Source:     SourceLearned,
		TTL:        90,
	}.AtFile(root, rel, CheckErrDiscard)

	if boosted := reg.PromoteByID(c.ID, 0.05); boosted != nil {
		return boosted
	}
	return reg.Add(c)
}

// resolveTaughtFile picks the owning workspace root for an edited Go file and
// returns (root, pathRelativeToRoot). A file outside every root, or a non-Go
// file, yields ("", "") — no checker can evaluate it, so nothing is taught.
func resolveTaughtFile(roots []string, absPath string) (string, string) {
	if !strings.HasSuffix(absPath, ".go") {
		return "", ""
	}
	root := RootFor(roots, absPath)
	if root == "" {
		return "", ""
	}
	rel := RelToRoot(root, absPath)
	if rel == "" {
		return "", ""
	}
	return root, rel
}

func rejectedForErrorHandling(r EditRejection) bool {
	reason := strings.ToLower(r.Reason)
	if containsAny(reason, "error", "err", "错误", "handle", "处理") {
		return true
	}
	// No reason given: fall back to the code itself. Reuse the checker's own
	// predicate so what we teach is what PreWrite will later evaluate.
	if r.Reason == "" && r.OriginalCode != "" {
		failed, _ := checkErrDiscard(r.FilePath, r.OriginalCode)
		return failed
	}
	return false
}

func addedErrorHandling(aiCode, userCode string) bool {
	if aiCode == "" || userCode == "" {
		return false
	}
	return !strings.Contains(aiCode, "if err != nil") && strings.Contains(userCode, "if err != nil")
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// RootFor returns the longest workspace root that contains absPath, or "" when
// the path is relative or lives outside every root. Longest-match matters for
// nested roots: a file under both /repo and /repo/sub belongs to /repo/sub.
func RootFor(roots []string, absPath string) string {
	if absPath == "" || !filepath.IsAbs(absPath) {
		return ""
	}
	absPath = filepath.Clean(absPath)
	best := ""
	for _, root := range roots {
		if root == "" || !filepath.IsAbs(root) {
			continue
		}
		root = filepath.Clean(root)
		rel, err := filepath.Rel(root, absPath)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if len(root) > len(best) {
			best = root
		}
	}
	return best
}
