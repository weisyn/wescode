package constraints

import (
	"path/filepath"
	"testing"
	"time"
)

// inferred builds an inferred tree-scoped constraint. Bind namespaces the ID
// under root, so tests must read the ID back rather than assume the literal.
func inferred(root, id string, confidence float64, kind CheckerKind, mut ...func(*Constraint)) Constraint {
	c := Constraint{
		ID:         id,
		Rule:       "rule for " + id,
		Kind:       "quality",
		Status:     StatusActive,
		Confidence: confidence,
		Source:     SourceInferred,
		TTL:        90,
		LastUsed:   time.Now(),
	}.AtTreeGo(root, kind)
	for _, m := range mut {
		m(&c)
	}
	return c
}

// passStart returns the timestamp an inference pass records as its start.
//
// The sleep is load-bearing, not padding: Add stamps LastSeen with time.Now(),
// so a `since` read in the same clock tick would make already-stored rows look
// fresh (LastSeen.Before(since) is false on equality) and the sweep would
// retire nothing. Real passes take milliseconds of I/O; tests do not.
func passStart() time.Time {
	time.Sleep(2 * time.Millisecond)
	return time.Now()
}

func TestReconcileRoot_RetiresStaleInferred(t *testing.T) {
	root := t.TempDir()
	r := NewRegistry()

	cycle := Constraint{
		ID:         "cycle-detected",
		Rule:       "Circular dependency between a and b",
		Kind:       "architecture",
		Status:     StatusActive,
		Confidence: 0.6,
		Source:     SourceInferred,
		TTL:        90,
		LastUsed:   time.Now(),
	}.Bind(root, TargetTree, "**/*.go", CheckerSpec{
		Kind:      CheckImportCycle,
		Forbidden: []string{"example.com/a", "example.com/b"},
	})
	sec := inferred(root, "sec-password", 0.7, CheckPlaintextSecret)
	learned := Constraint{
		ID:         "learned-regression",
		Rule:       "Keep the error return on store.Save",
		Kind:       "quality",
		Status:     StatusActive,
		Confidence: 0.9,
		Source:     SourceLearned,
		TTL:        90,
		LastUsed:   time.Now(),
	}.AtFile(root, "internal/store/db.go", CheckErrDiscard)
	r.Add(cycle)
	r.Add(sec)
	r.Add(learned)

	// A fresh pass re-derives sec-password only (the cycle was broken), so its
	// LastSeen moves past `since` while cycle-detected's stays behind.
	since := passStart()
	r.Add(sec)

	reconciled := r.ReconcileRoot(root, []string{SourceInferred}, since)
	if reconciled != 1 {
		t.Errorf("expected 1 reconciled, got %d", reconciled)
	}

	if c := r.Get(cycle.ID); c == nil || c.Status != StatusRetired {
		t.Error("cycle-detected should be retired after reconcile")
	} else if c.RetiredBy != RetiredByReconcile {
		t.Errorf("retired_by = %q, want %q", c.RetiredBy, RetiredByReconcile)
	}
	if c := r.Get(sec.ID); c == nil || c.Status != StatusActive {
		t.Error("sec-password should remain active")
	}
	// learned constraint untouched (source is not machine-derived).
	if c := r.Get(learned.ID); c == nil || c.Status != StatusActive {
		t.Error("learned constraint should not be reconciled")
	}
}

// TestReconcileRoot_OnlySweepsSourcesThatRan is why freshness is a timestamp
// plus a source list instead of an ID set. Inference is seven independent
// passes; when one crashes its constraints must survive untouched rather than
// look like "this source produced nothing" and get wiped.
func TestReconcileRoot_OnlySweepsSourcesThatRan(t *testing.T) {
	root := t.TempDir()
	r := NewRegistry()

	structural := inferred(root, "sec-password", 0.7, CheckPlaintextSecret)
	ckg := inferred(root, "ckg-nopanic", 0.7, CheckNoPanic, func(c *Constraint) {
		c.Source = SourceInferredCKG
	})
	r.Add(structural)
	r.Add(ckg)

	// The CKG pass ran and produced nothing; the structure scan errored out and
	// is not in the source list.
	since := passStart()
	if got := r.ReconcileRoot(root, []string{SourceInferredCKG}, since); got != 1 {
		t.Fatalf("expected 1 reconciled, got %d", got)
	}
	if c := r.Get(ckg.ID); c == nil || c.Status != StatusRetired {
		t.Error("ckg constraint should be retired: its pass ran and re-derived nothing")
	}
	if c := r.Get(structural.ID); c == nil || c.Status != StatusActive {
		t.Error("a source whose pass never ran must not be swept")
	}
}

// TestReconcileRoot_NonMachineSourcesAreNoOp: seed and human constraints are not
// re-derived by inference, so "the pass did not produce it" says nothing about
// whether it still holds.
func TestReconcileRoot_NonMachineSourcesAreNoOp(t *testing.T) {
	root := t.TempDir()
	for _, src := range []string{SourceSeed, SourceLearned, SourceDeclared} {
		t.Run(src, func(t *testing.T) {
			r := NewRegistry()
			c := inferred(root, "c-"+src, 0.7, CheckNoPanic, func(c *Constraint) {
				c.Source = src
			})
			r.Add(c)

			since := passStart()
			if got := r.ReconcileRoot(root, []string{src}, since); got != 0 {
				t.Errorf("ReconcileRoot swept %d %s constraints; want 0", got, src)
			}
			if got := r.Get(c.ID); got == nil || got.Status != StatusActive {
				t.Errorf("%s constraint must survive reconcile", src)
			}
		})
	}
}

// TestReconcileRoot_NoSourcesIsNoOp: an empty source list means no inferrer
// reported success, which must not be read as "nothing was inferred anywhere".
func TestReconcileRoot_NoSourcesIsNoOp(t *testing.T) {
	root := t.TempDir()
	r := NewRegistry()
	c := inferred(root, "sec-password", 0.7, CheckPlaintextSecret)
	r.Add(c)

	since := passStart()
	for _, sources := range [][]string{nil, {}} {
		if got := r.ReconcileRoot(root, sources, since); got != 0 {
			t.Errorf("ReconcileRoot(sources=%v) retired %d; want 0", sources, got)
		}
	}
	if got := r.Get(c.ID); got == nil || got.Status != StatusActive {
		t.Error("constraint should survive a sourceless reconcile")
	}
}

// TestReconcileRoot_LeavesOtherRootsAlone is the multi-root invariant: a
// workspace re-infers one root at a time, so a reconcile pass that swept the
// whole registry would let each root retire every other root's constraints and
// leave only the last-indexed root covered.
func TestReconcileRoot_LeavesOtherRootsAlone(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	r := NewRegistry()

	a := inferred(rootA, "sec-password", 0.7, CheckPlaintextSecret)
	b := inferred(rootB, "sec-password", 0.7, CheckPlaintextSecret)
	r.Add(a)
	r.Add(b)

	// Same logical rule, two roots: distinct registry entries, not one
	// overwriting the other.
	if a.ID == b.ID {
		t.Fatalf("two roots produced the same constraint ID %q", a.ID)
	}
	if r.Count() != 2 {
		t.Fatalf("expected 2 constraints across 2 roots, got %d", r.Count())
	}

	// rootA no longer yields anything; rootB was not re-inferred at all.
	since := passStart()
	if got := r.ReconcileRoot(rootA, []string{SourceInferred}, since); got != 1 {
		t.Errorf("expected 1 reconciled in rootA, got %d", got)
	}
	if c := r.Get(a.ID); c == nil || c.Status != StatusRetired {
		t.Error("rootA constraint should be retired")
	}
	if c := r.Get(b.ID); c == nil || c.Status != StatusActive {
		t.Error("rootB constraint must survive rootA's reconcile pass")
	}
}

func TestReconcileRoot_NeverTouchesHumanConfirmed(t *testing.T) {
	root := t.TempDir()
	r := NewRegistry()
	c := inferred(root, "human-nopanic", 1.0, CheckNoPanic) // confidence 1.0 = human confirmed
	c.TTL = 0
	r.Add(c)

	// Fresh inference produces nothing (directory changed).
	reconciled := r.ReconcileRoot(root, []string{SourceInferred}, passStart())
	if reconciled != 0 {
		t.Errorf("human-confirmed constraints should never be reconciled, got %d", reconciled)
	}
	if got := r.Get(c.ID); got == nil || got.Status != StatusActive {
		t.Error("human-confirmed should remain active")
	}
}

func TestReconcileRoot_IgnoresRetired(t *testing.T) {
	root := t.TempDir()
	r := NewRegistry()
	c := inferred(root, "already-retired", 0.3, CheckNoPanic)
	c.Status = StatusRetired
	c.LastUsed = time.Now().Add(-200 * 24 * time.Hour)
	r.Add(c)

	if reconciled := r.ReconcileRoot(root, []string{SourceInferred}, passStart()); reconciled != 0 {
		t.Errorf("already-retired should not be counted, got %d", reconciled)
	}
}

// TestReconcileRoot_EmptyRootIsNoOp keeps an unset root from being read as a
// wildcard that retires everything.
func TestReconcileRoot_EmptyRootIsNoOp(t *testing.T) {
	root := t.TempDir()
	r := NewRegistry()
	c := inferred(root, "sec-password", 0.7, CheckPlaintextSecret)
	r.Add(c)

	since := passStart()
	for _, bad := range []string{"", ".", "  "} {
		if got := r.ReconcileRoot(bad, []string{SourceInferred}, since); got != 0 {
			t.Errorf("ReconcileRoot(%q) retired %d constraints; want 0", bad, got)
		}
	}
	if got := r.Get(c.ID); got == nil || got.Status != StatusActive {
		t.Error("constraint should survive a no-op reconcile")
	}
}

// TestLearningLoop_LearnedConstraintHitsNextRun walks the learn → match →
// promote cycle. The learned constraint carries CheckErrDiscard and a file
// target, so the next write to that file re-evaluates the predicate instead of
// re-reading the sentence the user's rejection produced.
func TestLearningLoop_LearnedConstraintHitsNextRun(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistry()
	if reg.Count() != 0 {
		t.Fatal("expected empty registry")
	}

	rule := "Do not discard the error returned by store.Save."
	reg.Add(Constraint{
		ID:         LearnedID("reject", "internal/store/db.go"),
		Rule:       rule,
		Kind:       "quality",
		Priority:   PriorityQuality,
		Status:     StatusActive,
		Confidence: 0.9,
		Source:     "learned",
		TTL:        90,
		LastUsed:   time.Now(),
	}.AtFile(root, "internal/store/db.go", CheckErrDiscard))

	if reg.Count() != 1 {
		t.Fatalf("expected 1 constraint after learning, got %d", reg.Count())
	}

	target := filepath.Join(root, "internal", "store", "db.go")
	matching := reg.MatchingFile(target)
	if len(matching) != 1 {
		t.Fatalf("expected 1 matching constraint for %s, got %d", target, len(matching))
	}
	if matching[0].Rule != rule {
		t.Errorf("unexpected rule: %s", matching[0].Rule)
	}
	if matching[0].Checker.Kind != CheckErrDiscard {
		t.Errorf("learned constraint must carry a checker, got %q", matching[0].Checker.Kind)
	}

	// A sibling file in the same package is out of scope.
	if got := reg.MatchingFile(filepath.Join(root, "internal", "store", "tx.go")); len(got) != 0 {
		t.Errorf("file-scoped constraint leaked to a sibling file: %v", ids(got))
	}

	// AI obeys, tests pass → promote.
	reg.PromoteByID(matching[0].ID, 0.05)
	c := reg.Get(matching[0].ID)
	if c.Confidence < 0.94 || c.Confidence > 0.96 {
		t.Errorf("expected confidence ~0.95 after promote, got %.4f", c.Confidence)
	}
}
