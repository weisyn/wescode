package verification

import (
	"fmt"

	"github.com/weisyn/wescode/internal/codeintel/constraints"
)

// EditInfo describes the edit that caused a regression.
type EditInfo struct {
	FilePath      string // absolute path of the edited file
	Symbol        string // function being edited
	ChangeType    string // "signature_change" | "body_change" | "deletion" | "addition"
	ReturnChanged bool
	ParamsChanged bool
}

// RegressionContext pairs a regression with its causal edit context.
type RegressionContext struct {
	Regression RegressionInfo
	EditInfo   EditInfo
}

// LearnFromRegression turns a behavioral regression into a constraint that
// prevents the same class of error — but only when the regression class has an
// executable predicate.
//
// Exactly one class qualifies: a signature change that broke callers pins the
// symbol's current arity via signature_stable. The pre-CSE learner also emitted
// "boundary" (nil deref), "state", "api", "performance", "consistency" and
// "behavior" constraints; every one of those was a prose sentence with nothing
// to evaluate against an edited file, so they are Knowledge, not constraints
// (INV-CSE-17). A nil-safety or performance regression is real, but the place to
// record it is the test that caught it, not a rule injected into the prompt.
//
// Confidence starts at 0.7 (first occurrence). A repeat regression on the same
// symbol boosts it (capped +0.2 per INV-CSE-07).
func LearnFromRegression(reg *constraints.Registry, roots []string, br RegressionContext) *constraints.Constraint {
	if reg == nil || !isSignatureRegression(br.EditInfo) {
		return nil
	}

	root := constraints.RootFor(roots, br.EditInfo.FilePath)
	if root == "" {
		return nil
	}
	rel := constraints.RelToRoot(root, br.EditInfo.FilePath)
	if rel == "" {
		return nil
	}
	// Pin the arity the symbol has *now*, after the regression was surfaced and
	// the edit reverted. Without a concrete signature the checker is a no-op.
	signature, ok := constraints.SignatureFromFile(br.EditInfo.FilePath, br.EditInfo.Symbol)
	if !ok {
		return nil
	}

	// Build first, look up second: Bind root-scopes the ID, and the scoped form
	// is what the registry keys on. Probing with the raw LearnedID would never
	// hit, so a repeat regression would never boost.
	c := constraints.Constraint{
		ID: constraints.LearnedID("regression-signature", root+"/"+rel+":"+br.EditInfo.Symbol),
		Rule: fmt.Sprintf(
			"Do not change the signature of %s — callers depend on its current arity (test %s/%s regressed after a signature change).",
			br.EditInfo.Symbol, br.Regression.Package, br.Regression.TestName,
		),
		Kind:       "architecture",
		Priority:   constraints.PriorityArchitecture,
		Status:     constraints.StatusCandidate,
		Confidence: 0.7,
		Source:     constraints.SourceLearned,
		TTL:        120,
	}.Bind(root, constraints.TargetFunction, rel, constraints.CheckerSpec{
		Kind:      constraints.CheckSignatureStable,
		Symbol:    br.EditInfo.Symbol,
		Signature: signature,
	})

	if boosted := constraints.BoostFromRegression(reg, c.ID); boosted != nil {
		return boosted
	}
	return reg.Add(c)
}

// LearnFromRegressions processes a batch of regressions and returns
// the number of constraints created or boosted.
func LearnFromRegressions(reg *constraints.Registry, roots []string, regressions []RegressionContext) int {
	count := 0
	for _, br := range regressions {
		if c := LearnFromRegression(reg, roots, br); c != nil {
			count++
		}
	}
	return count
}

func isSignatureRegression(edit EditInfo) bool {
	return edit.Symbol != "" &&
		(edit.ChangeType == "signature_change" || edit.ReturnChanged || edit.ParamsChanged)
}
