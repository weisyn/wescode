package codeintel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weisyn/wescode/internal/codeintel/constraints"
	"github.com/weisyn/wescode/internal/treesitter"
	"github.com/weisyn/wesgine/tool"
)

// Multi-root runtime proof (INV-CSE-10).
//
// The unit tests elsewhere assert root isolation one predicate at a time. This
// file drives the real lifecycle instead: seven workspace roots on disk, the
// same inference → reconcile → seed sequence that engine.go boot and
// background.go postIndexWork run, then an actual write through the real
// PreWriteCheck hooks and the real CSE overlay builder.
//
// The failure it guards against is not hypothetical. Inferrer IDs are derived
// from content — `inferID("pattern", "no-panic-production")` is one fixed hash
// regardless of root — and the Registry is keyed by ID alone. Seven roots
// therefore minted seven constraints with one identical ID, so `Add` kept only
// the last root's copy and the other six repos ran uncovered while their files
// got advisories naming a constraint rooted somewhere else.

// wesRoots mirrors the real workspace: seven Go repos plus one (wesui) that has
// a module file but no auth surface, which is what makes cold-start seeding
// observable.
var wesRoots = []struct {
	name    string
	hasAuth bool
}{
	{"weisyn", true},
	{"wesgine", true},
	{"wesui", false},
	{"wesapp", true},
	{"wesclaw", true},
	{"wescode", true},
	{"wescraft", true},
}

// buildWesWorkspace lays down seven sibling repos under one parent, the way a
// multi-root VS Code window sees them.
func buildWesWorkspace(t *testing.T) map[string]string {
	t.Helper()
	parent := t.TempDir()
	roots := make(map[string]string, len(wesRoots))

	for _, r := range wesRoots {
		root := filepath.Join(parent, r.name+".git")
		writeFileT(t, filepath.Join(root, "go.mod"), "module github.com/weisyn/"+r.name+"\n\ngo 1.22\n")
		writeFileT(t, filepath.Join(root, "internal", "svc", "svc.go"), `package svc

// Serve runs the service.
func Serve() error { return nil }
`)
		if r.hasAuth {
			// inferSecurity keys off the directory's existence, so the same
			// CheckPlaintextSecret constraint is inferred in every repo that
			// has one — the exact ID-collision shape.
			writeFileT(t, filepath.Join(root, "internal", "auth", "auth.go"), `package auth

// Login authenticates a user.
func Login(user, pass string) error { return nil }
`)
		}
		roots[r.name] = root
	}
	return roots
}

// runInferencePass is postIndexWork's per-root body, verbatim minus the CKG
// calls that need a populated code.db. Order matters: each root's reconcile
// runs while the previous roots' constraints are already in the registry.
//
// The `since` stamp precedes inference, mirroring background.go: rows this pass
// re-derives get a newer LastSeen and survive the sweep, everything else from
// an earlier pass falls behind the cutoff and retires.
func runInferencePass(reg *constraints.Registry, roots []string, readiness float64) {
	for _, root := range roots {
		since := time.Now()
		for _, c := range constraints.InferFromProject(root, readiness) {
			reg.Add(c)
		}
		// Only the structure scan runs here, so it is the one source whose
		// silence is evidence that its constraints no longer hold.
		reg.ReconcileRoot(root, []string{constraints.SourceInferred}, since)
		if reg.CountForRoot(root) == 0 {
			constraints.SeedGoConstraints(reg, root)
		}
	}
}

func orderedRoots(roots map[string]string) []string {
	out := make([]string, 0, len(roots))
	for _, r := range wesRoots {
		out = append(out, roots[r.name])
	}
	return out
}

// TestMultiRoot_EveryRootKeepsItsOwnCoverage is the collision regression. Seven
// roots, one shared logical rule set, and afterwards every root must still own
// a full copy — not just whichever one happened to be indexed last.
func TestMultiRoot_EveryRootKeepsItsOwnCoverage(t *testing.T) {
	roots := buildWesWorkspace(t)
	ordered := orderedRoots(roots)
	reg := constraints.NewRegistry()

	// Cold pass: boot, CKG empty. Only filesystem-visible categories fire, so
	// wesui (no auth dir) infers nothing and falls back to the Go seed set.
	runInferencePass(reg, ordered, 0.1)
	if got := reg.CountForRoot(roots["wesui"]); got == 0 {
		t.Error("wesui has no auth surface, so the cold pass must seed it")
	}

	// Warm pass: indexing done, every category eligible.
	runInferencePass(reg, ordered, 1.0)

	for _, r := range wesRoots {
		if got := reg.CountForRoot(roots[r.name]); got == 0 {
			t.Errorf("%s ended with zero constraints — its entries were overwritten by a sibling root", r.name)
		}
	}

	// The six auth-bearing roots infer an identical rule set, so their counts
	// must match. A lower count on any one of them means Add dropped an entry.
	want := reg.CountForRoot(roots["weisyn"])
	for _, r := range wesRoots {
		if !r.hasAuth {
			continue
		}
		if got := reg.CountForRoot(roots[r.name]); got != want {
			t.Errorf("%s has %d constraints, weisyn has %d — same project shape must yield the same coverage",
				r.name, got, want)
		}
	}

	// Every active constraint is owned by exactly one of the seven roots, and
	// no ID is shared between two of them.
	owners := map[string]string{}
	for _, c := range reg.Active() {
		if prev, dup := owners[c.ID]; dup {
			t.Errorf("ID %q is claimed by both %s and %s", c.ID, prev, c.Root)
		}
		owners[c.ID] = c.Root
	}
}

// TestMultiRoot_WriteAdvisoryStaysInsideItsRoot is the user-visible assertion:
// a write into wesclaw is judged by wesclaw's constraints. Sibling repos'
// rules — including the INV-* prose that used to travel across roots — must not
// appear in the tool result.
func TestMultiRoot_WriteAdvisoryStaysInsideItsRoot(t *testing.T) {
	roots := buildWesWorkspace(t)
	ordered := orderedRoots(roots)
	reg := constraints.NewRegistry()
	runInferencePass(reg, ordered, 1.0)

	// Plant engine-specific invariant prose in wesgine, carrying the *same*
	// predicate wesclaw's own inferred security constraint uses. Same checker,
	// same file content, two roots: the only thing that can keep the wesgine
	// copy silent is root scoping, so a single FAIL is a real isolation result
	// rather than an artifact of the two rules disagreeing.
	const wesgineInvariant = "INV-CELL-08: Cell.Start requires bound process hooks; 禁止在 GetOrCreate 前调用 ActivatePersisted."
	reg.Add(constraints.Constraint{
		ID:         "inv-cell-08",
		Rule:       wesgineInvariant,
		Kind:       "architecture",
		Priority:   constraints.PriorityArchitecture,
		Status:     constraints.StatusActive,
		Confidence: 0.9,
		Source:     "seed",
	}.AtTreeGo(roots["wesgine"], constraints.CheckPlaintextSecret))

	wesclaw := roots["wesclaw"]
	result := driveSecretWrite(t, reg, wesclaw)

	fails := constraintFailLines(result)
	if len(fails) == 0 {
		t.Fatal("wesclaw owns a plaintext-secret constraint, so the hardcoded password must produce an advisory")
	}
	if len(fails) != 1 {
		t.Errorf("expected exactly 1 advisory (wesclaw's own); wesgine carries the same predicate and must stay silent, got %d:\n%s",
			len(fails), strings.Join(fails, "\n"))
	}

	// Resolve each advisory back to its constraint and check the owning root.
	byID := map[string]constraints.Constraint{}
	for _, c := range reg.Active() {
		byID[c.ID] = c
	}
	for _, line := range fails {
		id := failLineID(line)
		if id == "" {
			t.Errorf("advisory %q has no parseable constraint ID", line)
			continue
		}
		c, ok := byID[id]
		if !ok {
			t.Errorf("advisory names unknown constraint %q", id)
			continue
		}
		if c.Root != filepath.Clean(wesclaw) {
			t.Errorf("advisory %q comes from root %s, but the write targets wesclaw", id, c.Root)
		}
	}

	// The prose itself, and the invariant vocabulary generally, stay out.
	if strings.Contains(result.Content, wesgineInvariant) {
		t.Errorf("wesgine invariant prose reached the tool result:\n%s", result.Content)
	}
	for _, needle := range []string{"INV-", "禁止", "inv-cell-08"} {
		if strings.Contains(result.Content, needle) {
			t.Errorf("tool result contains cross-root marker %q:\n%s", needle, result.Content)
		}
	}
}

// TestMultiRoot_OverlayStaysInsideItsRoot covers the other injection channel:
// the focus-scoped CSE overlay. Same workspace, focus in wesclaw, so the
// fragment may only describe wesclaw's constraints.
func TestMultiRoot_OverlayStaysInsideItsRoot(t *testing.T) {
	roots := buildWesWorkspace(t)
	ordered := orderedRoots(roots)
	reg := constraints.NewRegistry()
	runInferencePass(reg, ordered, 1.0)

	const wesuiRule = "INV-CHAT-05: 附件芯片必须走 ChatInputCard.topSlot，禁止渲染在卡片外。"
	reg.Add(constraints.Constraint{
		ID:         "inv-chat-05",
		Rule:       wesuiRule,
		Kind:       "consistency",
		Priority:   constraints.PriorityConsistency,
		Status:     constraints.StatusActive,
		Confidence: 0.9,
		Source:     "seed",
	}.AtTreeGo(roots["wesui"], constraints.CheckNoPanic))

	focus := filepath.Join(roots["wesclaw"], "internal", "svc", "svc.go")
	frag, ok := cseConstraintFragment(reg, focus)
	if !ok {
		t.Fatal("wesclaw owns active tree-scoped constraints, so the overlay must render")
	}

	if strings.Contains(frag.Content, wesuiRule) || strings.Contains(frag.Content, "INV-CHAT-05") {
		t.Errorf("wesui rule leaked into a wesclaw overlay:\n%s", frag.Content)
	}

	// Ownership has to be asserted through constraint identity, not prose.
	// inferSecurity writes one fixed sentence into every auth-bearing root, so
	// six roots hold byte-identical Rule text; a substring scan would flag
	// wesclaw's own rule as a wesgine leak. The selection set is the real
	// contract: every constraint the overlay is built from must be wesclaw's,
	// and the rendered body must not contain more rules than that set.
	wesclaw := filepath.Clean(roots["wesclaw"])
	selected := reg.MatchingFile(focus)
	if len(selected) == 0 {
		t.Fatal("MatchingFile returned nothing for a focus file that rendered an overlay")
	}
	for _, c := range selected {
		if c.Root != wesclaw {
			t.Errorf("overlay selection includes %s owned by %s, focus is in wesclaw", c.ID, c.Root)
		}
	}
	if rendered := strings.Count(frag.Content, "\n- "); rendered > len(selected) {
		t.Errorf("overlay rendered %d rules but only %d were selected:\n%s",
			rendered, len(selected), frag.Content)
	}

	if frag.TokenCost > 500 {
		t.Errorf("overlay is ~%d tokens, budget is 500", frag.TokenCost)
	}
}

// TestMultiRoot_ReconcileSweepIsRootLocal replays the incremental case: one root
// gets re-indexed after its auth package is deleted. Its stale constraint
// retires; the other six repos, which were not re-inferred at all, keep theirs.
func TestMultiRoot_ReconcileSweepIsRootLocal(t *testing.T) {
	roots := buildWesWorkspace(t)
	ordered := orderedRoots(roots)
	reg := constraints.NewRegistry()
	runInferencePass(reg, ordered, 1.0)

	before := map[string]int{}
	for _, r := range wesRoots {
		before[r.name] = reg.CountForRoot(roots[r.name])
	}

	// weisyn drops internal/auth, then only weisyn is re-indexed.
	if err := os.RemoveAll(filepath.Join(roots["weisyn"], "internal", "auth")); err != nil {
		t.Fatal(err)
	}
	runInferencePass(reg, []string{roots["weisyn"]}, 1.0)

	if got := reg.CountForRoot(roots["weisyn"]); got >= before["weisyn"] {
		t.Errorf("weisyn lost its auth package but kept %d of %d constraints", got, before["weisyn"])
	}
	for _, r := range wesRoots {
		if r.name == "weisyn" {
			continue
		}
		if got := reg.CountForRoot(roots[r.name]); got != before[r.name] {
			t.Errorf("%s dropped from %d to %d constraints during weisyn's reconcile pass",
				r.name, before[r.name], got)
		}
	}
}

// TestMultiRoot_SurvivesRestart is the restart boundary. Root, TargetPath and
// Checker live in the `data_json` blob, not in dedicated columns, so a drift in
// that encoding fails in one of two silent ways: LoadFromSQLite's Validate()
// gate drops every row and the workspace reboots with no constraints at all, or
// the rows come back with an empty Root and every constraint matches every repo
// again — the pre-refactor cross-root leak, reintroduced by a reboot.
//
// The proof has to run the real write path after the reload, not just compare
// counts: a Checker that round-trips as its zero value still counts.
func TestMultiRoot_SurvivesRestart(t *testing.T) {
	roots := buildWesWorkspace(t)
	reg := constraints.NewRegistry()
	runInferencePass(reg, orderedRoots(roots), 1.0)

	// Production schema, not a hand-written DDL: a column the registry writes
	// but schema.go does not declare has to fail here. Save goes through the
	// writer handle and load through the reader handle, mirroring
	// engine.persistConstraints / LoadPersistedConstraints — so this also
	// covers the reader seeing the writer's commit.
	ci, _ := newTestIndex(t)
	writer, reader := ci.WriterDB(), ci.DB()
	if writer == nil || reader == nil {
		t.Fatal("test index has no database")
	}

	before := map[string]int{}
	for _, r := range wesRoots {
		before[r.name] = reg.CountForRoot(roots[r.name])
		// Without this, the post-restart comparison would pass vacuously on a
		// root that never had anything to lose.
		if before[r.name] == 0 {
			t.Fatalf("precondition: %s has no constraints to persist", r.name)
		}
	}
	beforeFails := constraintFailLines(driveSecretWrite(t, reg, roots["wesclaw"]))
	if len(beforeFails) != 1 {
		t.Fatalf("precondition: pre-restart advisory count = %d, want 1", len(beforeFails))
	}

	if err := reg.SaveToSQLite(writer); err != nil {
		t.Fatalf("SaveToSQLite: %v", err)
	}

	// Reboot: a fresh registry reading the same code.db, the way engine.go
	// hydrates a workspace that was indexed in an earlier session.
	reloaded := constraints.NewRegistry()
	if err := reloaded.LoadFromSQLite(reader); err != nil {
		t.Fatalf("LoadFromSQLite: %v", err)
	}

	for _, r := range wesRoots {
		got := reloaded.CountForRoot(roots[r.name])
		if got == 0 && before[r.name] > 0 {
			t.Errorf("%s came back from disk with no constraints (had %d): Root or Checker did not round-trip",
				r.name, before[r.name])
			continue
		}
		if got != before[r.name] {
			t.Errorf("%s has %d constraints after restart, had %d", r.name, got, before[r.name])
		}
	}

	// Same write, same file, reloaded registry: still exactly one advisory, and
	// still wesclaw's. More than one means a sibling root's copy came back
	// unscoped; zero means the checker did not survive.
	afterFails := constraintFailLines(driveSecretWrite(t, reloaded, roots["wesclaw"]))
	if len(afterFails) != 1 {
		t.Errorf("post-restart advisory count = %d, want 1:\n%s", len(afterFails), strings.Join(afterFails, "\n"))
	}
	wesclaw := filepath.Clean(roots["wesclaw"])
	for _, line := range afterFails {
		c := reloaded.Get(failLineID(line))
		if c == nil {
			t.Errorf("post-restart advisory names unknown constraint: %q", line)
			continue
		}
		if c.Root != wesclaw {
			t.Errorf("post-restart advisory %q is rooted at %s, the write targets wesclaw", c.ID, c.Root)
		}
	}
}

// driveSecretWrite runs one `write` of a file with a hardcoded password through
// the real PreWriteCheck hooks, against the given registry and root. The content
// trips CheckPlaintextSecret, which every auth-bearing root infers — so the
// advisory count is a direct measure of root scoping.
func driveSecretWrite(t *testing.T, reg *constraints.Registry, root string) *tool.ToolResult {
	t.Helper()
	ci, _ := newTestIndex(t)
	ts := treesitter.NewParserPool()
	t.Cleanup(ts.Close)
	// PreWriteCheck skips its constraint pass on an unindexed workspace; one
	// file is enough to clear that gate.
	atomic.StoreInt32(&ci.totalFiles, 1)

	pre, post := NewPreWriteCheck(ci, ts, PreWriteCheckOpts{Constraints: reg})
	input, err := json.Marshal(map[string]string{
		"path": filepath.Join(root, "internal", "auth", "session.go"),
		"content": `package auth

// NewSession opens a session.
func NewSession(user string) error {
	password := "hunter2-plaintext"
	_ = password
	return nil
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	call := tool.ToolCall{ID: "multiroot-write", Name: "write", Input: input}

	if r, err := pre(context.Background(), call, &tool.ToolContext{WorkDir: root}); err != nil || r != nil {
		t.Fatalf("PreWrite must never block: result=%v err=%v", r, err)
	}
	result := &tool.ToolResult{Content: "written"}
	post(context.Background(), call, result, nil)
	return result
}

// failLineID pulls the constraint ID out of a `[FAIL <id>] <msg>` advisory.
func failLineID(line string) string {
	i := strings.Index(line, "[FAIL ")
	if i < 0 {
		return ""
	}
	rest := line[i+len("[FAIL "):]
	j := strings.Index(rest, "]")
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:j])
}

func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
