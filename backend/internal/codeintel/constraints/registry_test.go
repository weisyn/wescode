package constraints

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// validC builds a constraint that passes Validate(): a root, a target, and a
// checker. Every test that wants a constraint to actually land in the registry
// must go through here — Add is fail-closed, so a literal missing any of the
// three is silently dropped (INV-CSE-15).
func validC(root, id, rule string, mut ...func(*Constraint)) Constraint {
	c := Constraint{
		ID:         id,
		Rule:       rule,
		Kind:       "quality",
		Status:     StatusActive,
		Confidence: 1.0,
	}.AtTreeGo(root, CheckNoPanic)
	for _, m := range mut {
		m(&c)
	}
	return c
}

func TestRegistry_AddAndGet(t *testing.T) {
	root := t.TempDir()
	r := NewRegistry()
	r.Add(validC(root, "test-1", "no plaintext passwords", func(c *Constraint) {
		c.Kind = "security"
		c.Confidence = 0.7
		c.Status = StatusCandidate
		c.Priority = 0 // let Add derive it from Kind
	}))

	c := r.Get(scopeID("test-1", root))
	if c == nil {
		t.Fatal("expected to find constraint test-1")
	}
	if c.Rule != "no plaintext passwords" {
		t.Errorf("rule = %q", c.Rule)
	}
	if c.Priority != PrioritySecurity {
		t.Errorf("priority = %d, want %d", c.Priority, PrioritySecurity)
	}
	if c.Root != filepath.Clean(root) {
		t.Errorf("root = %q, want %q", c.Root, root)
	}
}

// TestAdd_FailClosed is the load-bearing gate: a constraint without an
// executable checker, without a root, or without a target is not a constraint.
// Before the CSE refactor these all landed in the registry and were injected as
// prose, which is exactly the second-.cursorrules failure mode.
func TestAdd_FailClosed(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name string
		c    Constraint
	}{
		{
			name: "no checker",
			c: Constraint{ID: "x1", Rule: "be careful", Status: StatusActive,
				Root: root, TargetKind: TargetTree, TargetPath: "**/*.go"},
		},
		{
			name: "no root",
			c: Constraint{ID: "x2", Rule: "be careful", Status: StatusActive,
				TargetKind: TargetTree, TargetPath: "**/*.go",
				Checker: CheckerSpec{Kind: CheckNoPanic}},
		},
		{
			name: "no target path",
			c: Constraint{ID: "x3", Rule: "be careful", Status: StatusActive,
				Root: root, TargetKind: TargetTree,
				Checker: CheckerSpec{Kind: CheckNoPanic}},
		},
		{
			name: "no target kind",
			c: Constraint{ID: "x4", Rule: "be careful", Status: StatusActive,
				Root: root, TargetPath: "**/*.go",
				Checker: CheckerSpec{Kind: CheckNoPanic}},
		},
		{
			name: "bogus target kind",
			c: Constraint{ID: "x5", Rule: "be careful", Status: StatusActive,
				Root: root, TargetKind: TargetKind("everything"), TargetPath: "**/*.go",
				Checker: CheckerSpec{Kind: CheckNoPanic}},
		},
		{
			name: "no rule text",
			c: Constraint{ID: "x6", Status: StatusActive,
				Root: root, TargetKind: TargetTree, TargetPath: "**/*.go",
				Checker: CheckerSpec{Kind: CheckNoPanic}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			r.Add(tt.c)
			if got := r.Count(); got != 0 {
				t.Errorf("registry accepted an invalid constraint: count = %d", got)
			}
			if r.Get(tt.c.ID) != nil {
				t.Error("invalid constraint is retrievable")
			}
		})
	}
}

func TestRegistry_Active(t *testing.T) {
	root := t.TempDir()
	r := NewRegistry()
	r.Add(validC(root, "a1", "active"))
	r.Add(validC(root, "c1", "candidate", func(c *Constraint) {
		c.Status = StatusCandidate
		c.Confidence = 0.5
	}))
	r.Add(validC(root, "r1", "retired", func(c *Constraint) {
		c.Status = StatusRetired
		c.Confidence = 0.1
	}))

	active := r.Active()
	if len(active) != 1 {
		t.Fatalf("expected 1 active, got %d", len(active))
	}
	if active[0].ID != scopeID("a1", root) {
		t.Errorf("active constraint ID = %q", active[0].ID)
	}
}

// TestRegistry_MatchingFile pins the multi-root behavior. A constraint mined in
// root A must never fire on a file in root B, and a relative path must never
// match at all — a bare "internal/auth/handler.go" is ambiguous across the
// seven repos a user can have open (INV-CSE-05/10).
func TestRegistry_MatchingFile(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()

	r := NewRegistry()
	r.Add(validC(rootA, "tree-a", "no panic in A"))
	r.Add(validC(rootA, "auth-a", "auth rule", func(c *Constraint) {
		*c = c.Bind(rootA, TargetPackage, "internal/auth/*", CheckerSpec{Kind: CheckPlaintextSecret})
	}))
	r.Add(validC(rootA, "inactive-a", "inactive", func(c *Constraint) {
		c.Status = StatusCandidate
		*c = c.Bind(rootA, TargetPackage, "internal/auth/*", CheckerSpec{Kind: CheckPlaintextSecret})
	}))
	r.Add(validC(rootB, "tree-b", "no panic in B"))

	authFile := filepath.Join(rootA, "internal", "auth", "handler.go")
	matches := r.MatchingFile(authFile)
	if len(matches) != 2 {
		t.Fatalf("auth file in root A: expected 2 matches (tree-a, auth-a), got %d: %v", len(matches), ids(matches))
	}

	userFile := filepath.Join(rootA, "internal", "user", "service.go")
	matches = r.MatchingFile(userFile)
	if len(matches) != 1 || matches[0].ID != scopeID("tree-a", rootA) {
		t.Errorf("non-auth file in root A should only match tree-a, got %v", ids(matches))
	}

	bFile := filepath.Join(rootB, "internal", "auth", "handler.go")
	matches = r.MatchingFile(bFile)
	if len(matches) != 1 || matches[0].ID != scopeID("tree-b", rootB) {
		t.Errorf("file in root B must not match root A constraints, got %v", ids(matches))
	}

	if matches := r.MatchingFile("internal/auth/handler.go"); len(matches) != 0 {
		t.Errorf("relative path must never match, got %v", ids(matches))
	}
	if matches := r.MatchingFile(""); len(matches) != 0 {
		t.Errorf("empty path must never match, got %v", ids(matches))
	}
}

// TestBind_NamespacesIDUnderRoot is the identity half of INV-CSE-10. The
// registry is keyed by ID alone, while every inferrer derives IDs from content
// (a directory name, a rule string, a path relative to root). Seven workspace
// roots that each contain internal/auth/ therefore mint seven constraints with
// one identical logical ID, and without namespacing the last Add silently
// overwrites the previous six — six roots lose coverage with no error anywhere.
func TestBind_NamespacesIDUnderRoot(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	r := NewRegistry()

	a := validC(rootA, "sec-auth", "no plaintext secrets")
	b := validC(rootB, "sec-auth", "no plaintext secrets")
	if a.ID == b.ID {
		t.Fatalf("two roots minted the same ID %q", a.ID)
	}
	r.Add(a)
	r.Add(b)

	if got := r.Count(); got != 2 {
		t.Fatalf("expected 2 entries for one logical rule across 2 roots, got %d", got)
	}
	for _, c := range []Constraint{a, b} {
		got := r.Get(c.ID)
		if got == nil {
			t.Fatalf("constraint %s (root %s) is missing", c.ID, c.Root)
		}
		if got.Root != c.Root {
			t.Errorf("%s root = %q, want %q", c.ID, got.Root, c.Root)
		}
	}

	// Re-binding must replace the tag, not stack a second one: an inferrer that
	// rebinds a constraint it just loaded from SQLite would otherwise grow the
	// ID on every pass and orphan the previous entry.
	rebound := a.Bind(rootB, TargetTree, "**/*.go", a.Checker)
	if rebound.ID != b.ID {
		t.Errorf("rebinding to rootB gave %q, want %q", rebound.ID, b.ID)
	}
	if strings.Count(rebound.ID, "@") != 1 {
		t.Errorf("rebinding stacked tags: %q", rebound.ID)
	}
}

// TestSeedGoConstraints_PerRoot pairs with the namespacing test: the Go seed set
// uses fixed IDs, so it is the path most exposed to collision. Two roots must
// end up with two full baselines, not one.
func TestSeedGoConstraints_PerRoot(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	for _, root := range []string{rootA, rootB} {
		mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/test\ngo 1.22\n")
	}
	r := NewRegistry()

	nA := SeedGoConstraints(r, rootA)
	if nA == 0 {
		t.Fatal("expected the Go seed set to be non-empty")
	}
	nB := SeedGoConstraints(r, rootB)
	if nB != nA {
		t.Errorf("rootB seeded %d constraints, rootA seeded %d", nB, nA)
	}
	if got := r.Count(); got != nA+nB {
		t.Errorf("registry holds %d constraints, want %d", got, nA+nB)
	}
	if got := r.CountForRoot(rootA); got != nA {
		t.Errorf("CountForRoot(rootA) = %d, want %d", got, nA)
	}
	if got := r.CountForRoot(rootB); got != nB {
		t.Errorf("CountForRoot(rootB) = %d, want %d", got, nB)
	}

	// Re-seeding an already-covered root is a no-op, not a duplicate set.
	if again := SeedGoConstraints(r, rootA); again != 0 {
		t.Errorf("re-seeding rootA added %d constraints, want 0", again)
	}
}

// TestMatchesFile_NonGoTargetPath checks that a file-scoped target only fires
// on that file, which is what the teach/L2.5 paths rely on.
func TestMatchesFile_FileScope(t *testing.T) {
	root := t.TempDir()
	c := validC(root, "f1", "keep error handling", func(c *Constraint) {
		*c = c.AtFile(root, "internal/store/db.go", CheckErrDiscard)
	})

	if !c.MatchesFile(filepath.Join(root, "internal", "store", "db.go")) {
		t.Error("should match its own file")
	}
	if c.MatchesFile(filepath.Join(root, "internal", "store", "other.go")) {
		t.Error("should not match a sibling file")
	}
}

func TestConstraint_Promote(t *testing.T) {
	c := Constraint{ID: "test", Status: StatusCandidate, Confidence: 0.75}
	c.Promote(0.1)
	if c.Confidence < 0.84 {
		t.Errorf("confidence = %f, want >= 0.85", c.Confidence)
	}
	if c.Status != StatusActive {
		t.Errorf("status = %s, want active (threshold crossed)", c.Status)
	}
	if c.UsageCount != 1 {
		t.Errorf("usage count = %d", c.UsageCount)
	}
}

func TestConstraint_Demote(t *testing.T) {
	c := Constraint{ID: "test", Confidence: 0.3}
	c.Demote(0.5)
	if c.Confidence != 0.0 {
		t.Errorf("confidence = %f, want 0.0 (clamped)", c.Confidence)
	}
}

func TestConstraint_Confirm(t *testing.T) {
	c := Constraint{ID: "test", Status: StatusCandidate, Confidence: 0.5, TTL: 90}
	c.Confirm()
	if c.Confidence != 1.0 || c.Status != StatusActive || c.TTL != 0 {
		t.Errorf("confirm: confidence=%f status=%s ttl=%d", c.Confidence, c.Status, c.TTL)
	}
}

func TestConstraint_ShouldRetire(t *testing.T) {
	now := time.Now()
	c := Constraint{Confidence: 0.5, TTL: 1, LastUsed: now.Add(-48 * time.Hour)}
	if !c.ShouldRetire() {
		t.Error("should retire after TTL exceeded")
	}

	c.Confidence = 1.0
	if c.ShouldRetire() {
		t.Error("human-confirmed should never retire")
	}

	c.Confidence = 0.5
	c.TTL = 0
	if c.ShouldRetire() {
		t.Error("TTL=0 means never expires")
	}
}

func TestRegistry_RunDecay(t *testing.T) {
	root := t.TempDir()
	r := NewRegistry()
	r.Add(validC(root, "old", "old rule", func(c *Constraint) {
		c.Status = StatusCandidate
		c.Confidence = 0.5
		c.TTL = 1
		c.LastUsed = time.Now().Add(-48 * time.Hour)
	}))
	r.Add(validC(root, "fresh", "fresh rule", func(c *Constraint) {
		c.Confidence = 0.9
		c.TTL = 90
		c.LastUsed = time.Now()
	}))

	retired := r.RunDecay()
	if retired != 1 {
		t.Errorf("expected 1 retired, got %d", retired)
	}
	if c := r.Get(scopeID("old", root)); c.Status != StatusRetired {
		t.Errorf("old constraint status = %s", c.Status)
	}
}

// TestRegistry_DetectConflicts requires the two constraints to share a root:
// contradictory rules mined from two different repos are not in conflict, they
// are simply two different projects' rules.
func TestRegistry_DetectConflicts(t *testing.T) {
	root := t.TempDir()
	r := NewRegistry()
	r.Add(validC(root, "c1", "handlers must not access DB directly", func(c *Constraint) {
		c.Kind = "architecture"
		c.Priority = PriorityArchitecture
	}))
	r.Add(validC(root, "c2", "handlers must access DB for performance", func(c *Constraint) {
		c.Kind = "quality"
		c.Priority = PriorityQuality
	}))

	conflicts := r.DetectConflicts()
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].Higher.Priority >= conflicts[0].Lower.Priority {
		t.Error("higher should have lower priority number")
	}
}

func TestRegistry_DetectConflicts_DifferentRoots(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	r := NewRegistry()
	r.Add(validC(rootA, "c1", "handlers must not access DB directly", func(c *Constraint) {
		c.Kind = "architecture"
		c.Priority = PriorityArchitecture
	}))
	r.Add(validC(rootB, "c2", "handlers must access DB for performance", func(c *Constraint) {
		c.Kind = "quality"
		c.Priority = PriorityQuality
	}))

	if conflicts := r.DetectConflicts(); len(conflicts) != 0 {
		t.Errorf("constraints from different roots must not conflict, got %d", len(conflicts))
	}
}

// TestInferFromProject_AllExecutable is the INV-CSE-15 regression test: every
// inferred constraint carries a checker and a root, so none of them can reach
// the model as bare prose.
func TestInferFromProject_AllExecutable(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "internal", "auth"))
	mustWrite(t, filepath.Join(dir, "go.mod"), "module example.com/test\ngo 1.22\n")

	inferred := InferFromProject(dir, 1.0)
	if len(inferred) == 0 {
		t.Fatal("expected at least one inferred constraint from go.mod + internal/auth")
	}

	for _, c := range inferred {
		if err := c.Validate(); err != nil {
			t.Errorf("inferred %s is not executable: %v", c.ID, err)
		}
		if c.Root != filepath.Clean(dir) {
			t.Errorf("inferred %s root = %q, want %q", c.ID, c.Root, dir)
		}
		t.Logf("inferred: [%s] checker=%s target=%s %s (confidence=%.2f status=%s)",
			c.Kind, c.Checker.Kind, c.TargetKind, c.TargetPath, c.Confidence, c.Status)
	}
}

// TestInferFromProject_IgnoresAgentsMD documents the deletion of the AGENTS.md
// reader. A markdown table of prohibitions is documentation, not a constraint:
// nothing in it can be evaluated against a file, so it belongs in Knowledge
// (INV-CSE-17). Leaving it in was how wesgine's INV-* lines ended up advising
// edits to wesclaw.
func TestInferFromProject_IgnoresAgentsMD(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "go.mod"), "module example.com/test\ngo 1.22\n")
	mustWrite(t, filepath.Join(dir, "AGENTS.md"), `# Project

## 禁止

| 禁止 | 正确做法 |
|------|---------|
| import github.com/weisyn/weisyn/pkg/... | use wesapp |

- **INV-CELL-08**: Cell.Start requires a bound process hook.
`)

	for _, c := range InferFromProject(dir, 1.0) {
		if c.Source == "agents_md" {
			t.Errorf("AGENTS.md must not produce constraints, got %s: %s", c.ID, c.Rule)
		}
		if strings.Contains(c.Rule, "INV-CELL-08") || strings.Contains(c.Rule, "wesapp") {
			t.Errorf("constraint text leaked from AGENTS.md: %s", c.Rule)
		}
	}
}

func TestAutoPromote(t *testing.T) {
	tests := []struct {
		name       string
		constraint Constraint
		wantStatus Status
	}{
		{
			name:       "high confidence >= 0.8 auto-activates",
			constraint: Constraint{Confidence: 0.8, Status: StatusCandidate, Priority: PriorityConsistency},
			wantStatus: StatusActive,
		},
		{
			name:       "security with confidence >= 0.7 auto-activates",
			constraint: Constraint{Confidence: 0.7, Status: StatusCandidate, Priority: PrioritySecurity},
			wantStatus: StatusActive,
		},
		{
			name:       "data with confidence >= 0.7 auto-activates",
			constraint: Constraint{Confidence: 0.7, Status: StatusCandidate, Priority: PriorityData},
			wantStatus: StatusActive,
		},
		{
			name:       "architecture with confidence 0.6 stays candidate",
			constraint: Constraint{Confidence: 0.6, Status: StatusCandidate, Priority: PriorityArchitecture},
			wantStatus: StatusCandidate,
		},
		{
			name:       "quality with confidence 0.7 stays candidate",
			constraint: Constraint{Confidence: 0.7, Status: StatusCandidate, Priority: PriorityQuality},
			wantStatus: StatusCandidate,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.constraint
			autoPromote(&c)
			if c.Status != tt.wantStatus {
				t.Errorf("got status %s, want %s", c.Status, tt.wantStatus)
			}
		})
	}
}

func TestInferFromProject_EmptyDir(t *testing.T) {
	inferred := InferFromProject(t.TempDir(), 1.0)
	if len(inferred) != 0 {
		t.Errorf("expected 0 inferred from empty dir, got %d", len(inferred))
	}
}

func TestPriorityFromKind(t *testing.T) {
	if PriorityFromKind("security") != PrioritySecurity {
		t.Error("security should be PrioritySecurity")
	}
	if PriorityFromKind("unknown") != PriorityQuality {
		t.Error("unknown should default to PriorityQuality")
	}
}

func ids(cs []Constraint) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
