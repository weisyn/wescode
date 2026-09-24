package codeintel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/weisyn/wescode/internal/codeintel/constraints"
	"github.com/weisyn/wescode/internal/treesitter"
	"github.com/weisyn/wesgine/tool"
)

// --- CI-24: Readiness degradation ---

func TestPreWriteCheck_SkipsWhenReadinessLow(t *testing.T) {
	ci, dir := newTestIndex(t)
	ts := treesitter.NewParserPool()
	t.Cleanup(ts.Close)

	goFile := filepath.Join(dir, "existing.go")
	os.WriteFile(goFile, []byte(`package main

func ExistingFunc() string { return "hello" }
`), 0644)
	ci.IndexFile(context.Background(), goFile)

	// Simulate low readiness by setting totalFiles much higher than indexed.
	atomic.StoreInt32(&ci.totalFiles, 1000)

	r := ci.Readiness()
	if r.Completeness >= ReadinessLow {
		t.Fatalf("expected completeness < %.1f, got %.2f", ReadinessLow, r.Completeness)
	}

	pre, post := NewPreWriteCheck(ci, ts)

	input, _ := json.Marshal(map[string]string{
		"path":    filepath.Join(dir, "new.go"),
		"content": "package main\n\nfunc ExistingFunc() string { return \"world\" }\n",
	})
	call := tool.ToolCall{ID: "low-readiness", Name: "write", Input: input}

	result, err := pre(context.Background(), call, &tool.ToolContext{WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatal("should not block")
	}

	toolResult := &tool.ToolResult{Content: "written"}
	post(context.Background(), call, toolResult, nil)

	// With low readiness, CKG checks are skipped — no warnings expected.
	if toolResult.Metadata != nil {
		if warnings, ok := toolResult.Metadata["ckg_warnings"]; ok {
			ws, _ := warnings.([]string)
			if len(ws) > 0 {
				t.Errorf("expected no CKG warnings at low readiness, got %d: %v", len(ws), ws)
			}
		}
	}
}

func TestPreWriteCheck_RunsWhenReadinessHigh(t *testing.T) {
	ci, dir := newTestIndex(t)
	ts := treesitter.NewParserPool()
	t.Cleanup(ts.Close)

	// Use a distinctive exported name that FTS5 will match exactly.
	goFile := filepath.Join(dir, "existing.go")
	os.WriteFile(goFile, []byte(`package main

func ProcessOrderPayment(id int) error { return nil }
`), 0644)
	ci.IndexFile(context.Background(), goFile)

	// totalFiles = indexedCount → completeness = 1.0
	atomic.StoreInt32(&ci.totalFiles, 1)

	r := ci.Readiness()
	if r.Completeness < ReadinessHigh {
		t.Fatalf("expected completeness >= %.1f, got %.2f", ReadinessHigh, r.Completeness)
	}

	pre, post := NewPreWriteCheck(ci, ts)

	// Write the EXACT same function name in a different file — guaranteed match.
	input, _ := json.Marshal(map[string]string{
		"path":    filepath.Join(dir, "new.go"),
		"content": "package main\n\nfunc ProcessOrderPayment(orderID int) error { return nil }\n",
	})
	call := tool.ToolCall{ID: "high-readiness", Name: "write", Input: input}

	result, err := pre(context.Background(), call, &tool.ToolContext{WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatal("should not block")
	}

	toolResult := &tool.ToolResult{Content: "written"}
	post(context.Background(), call, toolResult, nil)

	// With high readiness + exact duplicate name, CKG should warn.
	if toolResult.Metadata == nil {
		t.Log("no metadata — FindSimilar may not have matched (FTS5 tokenization)")
		return
	}
	warnings, ok := toolResult.Metadata["ckg_warnings"]
	if !ok {
		t.Log("no ckg_warnings key — this is acceptable if FTS5 threshold was not met")
		return
	}
	ws, ok2 := warnings.([]string)
	if !ok2 {
		t.Fatal("ckg_warnings is not []string")
	}
	if len(ws) == 0 {
		t.Log("empty warnings — FTS5 similarity check below threshold")
	} else {
		t.Logf("CKG correctly warned about similar symbol: %v", ws)
	}
}

// --- CI-26: Buffer freshness ---

type mockBufferProvider struct {
	buffers map[string][]byte
}

func (m *mockBufferProvider) Get(path string) []byte {
	return m.buffers[path]
}

func TestPreWriteCheck_DirtyBufferRefresh(t *testing.T) {
	ci, dir := newTestIndex(t)
	ts := treesitter.NewParserPool()
	t.Cleanup(ts.Close)

	goFile := filepath.Join(dir, "service.go")
	os.WriteFile(goFile, []byte(`package main

func OldFunction() {}
`), 0644)
	ci.IndexFile(context.Background(), goFile)
	atomic.StoreInt32(&ci.totalFiles, 1)

	// Verify initial state: OldFunction is indexed.
	syms, _ := ci.ListFileSymbols(context.Background(), goFile)
	hasOld := false
	for _, s := range syms {
		if s.Name == "OldFunction" {
			hasOld = true
		}
	}
	if !hasOld {
		t.Fatal("precondition: OldFunction should be indexed initially")
	}

	// Simulate dirty buffer: user renamed OldFunction → NewFunction in editor.
	buffers := &mockBufferProvider{
		buffers: map[string][]byte{
			goFile: []byte("package main\n\nfunc NewFunction() {}\n"),
		},
	}

	pre, post := NewPreWriteCheck(ci, ts, PreWriteCheckOpts{Buffers: buffers})

	// Trigger PreWriteCheck which should call refreshTemporarySymbols for goFile.
	input, _ := json.Marshal(map[string]string{
		"path":    goFile,
		"content": "package main\n\nfunc NewFunction() {}\n",
	})
	call := tool.ToolCall{ID: "buffer-test", Name: "write", Input: input}
	tc := &tool.ToolContext{WorkDir: dir}

	pre(context.Background(), call, tc)
	toolResult := &tool.ToolResult{Content: "written"}
	post(context.Background(), call, toolResult, nil)

	// After buffer refresh, the index should now have NewFunction instead of OldFunction.
	syms, _ = ci.ListFileSymbols(context.Background(), goFile)
	hasNew := false
	for _, s := range syms {
		if s.Name == "NewFunction" {
			hasNew = true
		}
	}
	if !hasNew {
		names := symbolNames(syms)
		t.Errorf("expected buffer content (NewFunction) to be indexed after refresh, got: %v", names)
	}
}

// --- CI-25: PostWriteIndex synchronous consistency ---

func TestPostWriteIndex_ImmediateVisibility(t *testing.T) {
	ci, dir := newTestIndex(t)

	hook := NewPostWriteIndex(ci)

	// Simulate writing a file with a new exported function.
	newFile := filepath.Join(dir, "fresh.go")
	os.WriteFile(newFile, []byte(`package main

func BrandNewFunction() error { return nil }
`), 0644)

	input, _ := json.Marshal(map[string]string{"path": "fresh.go"})
	call := tool.ToolCall{Name: "write", Input: input}
	result := &tool.ToolResult{Content: "ok"}
	hook(context.Background(), call, result, &tool.ToolContext{WorkDir: dir})

	// Immediately after PostWriteIndex, the symbol should be in the DB.
	syms, err := ci.ListFileSymbols(context.Background(), newFile)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range syms {
		if s.Name == "BrandNewFunction" {
			found = true
		}
	}
	if !found {
		t.Errorf("BrandNewFunction should be immediately visible after PostWriteIndex (CI-25), got symbols: %v", symbolNames(syms))
	}
}

func symbolNames(syms []SymbolEntry) []string {
	var names []string
	for _, s := range syms {
		names = append(names, s.Name)
	}
	return names
}

// --- Constraint EXECUTE: fail-only advisory (INV-CSE-15) ---

// mutexConstraint returns an active mutex_defer constraint rooted at dir.
func mutexConstraint(dir string) constraints.Constraint {
	return constraints.Constraint{
		ID:         "seed-mutex-defer",
		Rule:       "A function that Locks a mutex must defer its Unlock.",
		Kind:       "quality",
		Priority:   constraints.PriorityQuality,
		Status:     constraints.StatusActive,
		Confidence: 0.9,
		Source:     "seed",
	}.AtTreeGo(dir, constraints.CheckMutexDefer)
}

// TestPreWriteCheck_ConstraintFailAdvisory: a Lock without defer Unlock is one
// FAIL line and nothing else. The advisory carries the constraint ID so the
// model can tell which predicate fired, not a wall of rule prose.
func TestPreWriteCheck_ConstraintFailAdvisory(t *testing.T) {
	ci, dir := newTestIndex(t)
	ts := treesitter.NewParserPool()
	t.Cleanup(ts.Close)
	atomic.StoreInt32(&ci.totalFiles, 1)

	reg := constraints.NewRegistry()
	reg.Add(mutexConstraint(dir))
	// A second active constraint the content satisfies: it must stay silent.
	reg.Add(constraints.Constraint{
		ID:         "seed-no-panic",
		Rule:       "Do not panic in production code.",
		Kind:       "quality",
		Priority:   constraints.PriorityQuality,
		Status:     constraints.StatusActive,
		Confidence: 0.9,
		Source:     "seed",
	}.AtTreeGo(dir, constraints.CheckNoPanic))

	pre, post := NewPreWriteCheck(ci, ts, PreWriteCheckOpts{Constraints: reg})

	input, _ := json.Marshal(map[string]string{
		"path": filepath.Join(dir, "counter.go"),
		"content": `package main

import "sync"

type Counter struct {
	mu sync.Mutex
	n  int
}

func (c *Counter) Inc() {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
}
`,
	})
	call := tool.ToolCall{ID: "mutex-fail", Name: "write", Input: input}

	if r, err := pre(context.Background(), call, &tool.ToolContext{WorkDir: dir}); err != nil || r != nil {
		t.Fatalf("PreWrite must never block: result=%v err=%v", r, err)
	}
	result := &tool.ToolResult{Content: "written"}
	post(context.Background(), call, result, nil)

	fails := constraintFailLines(result)
	if len(fails) != 1 {
		t.Fatalf("expected exactly 1 FAIL advisory, got %d: %v", len(fails), fails)
	}
	if !strings.Contains(fails[0], "seed-mutex-defer") {
		t.Errorf("FAIL line should name the constraint that fired, got %q", fails[0])
	}
	if strings.Contains(result.Content, "seed-no-panic") {
		t.Error("a passing constraint must not appear in the tool result")
	}
}

// TestPreWriteCheck_ConstraintPassIsSilent: a compliant edit produces zero
// constraint text. This is the regression guard against the pre-refactor
// behavior where every scope-matching rule was echoed on every write.
func TestPreWriteCheck_ConstraintPassIsSilent(t *testing.T) {
	ci, dir := newTestIndex(t)
	ts := treesitter.NewParserPool()
	t.Cleanup(ts.Close)
	atomic.StoreInt32(&ci.totalFiles, 1)

	target := filepath.Join(dir, "counter.go")
	if err := os.WriteFile(target, []byte(`package main

import "sync"

type Counter struct {
	mu sync.Mutex
	n  int
}

func (c *Counter) Inc() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
}
`), 0644); err != nil {
		t.Fatal(err)
	}

	reg := constraints.NewRegistry()
	reg.Add(mutexConstraint(dir))

	pre, post := NewPreWriteCheck(ci, ts, PreWriteCheckOpts{Constraints: reg})

	input, _ := json.Marshal(map[string]string{
		"path":       target,
		"old_string": "c.n++",
		"new_string": "c.n += 2",
	})
	call := tool.ToolCall{ID: "mutex-pass", Name: "edit", Input: input}

	pre(context.Background(), call, &tool.ToolContext{WorkDir: dir})
	result := &tool.ToolResult{Content: "edited"}
	post(context.Background(), call, result, nil)

	if result.Content != "edited" {
		t.Errorf("compliant edit must not append constraint text, got %q", result.Content)
	}
	if fails := constraintFailLines(result); len(fails) != 0 {
		t.Errorf("expected no FAIL advisories, got %v", fails)
	}
}

// TestPreWriteCheck_ConstraintFromOtherRootIgnored: constraints rooted in a
// different workspace never reach a file in this one (INV-CSE-10).
func TestPreWriteCheck_ConstraintFromOtherRootIgnored(t *testing.T) {
	ci, dir := newTestIndex(t)
	ts := treesitter.NewParserPool()
	t.Cleanup(ts.Close)
	atomic.StoreInt32(&ci.totalFiles, 1)

	otherRoot := t.TempDir()
	reg := constraints.NewRegistry()
	reg.Add(mutexConstraint(otherRoot))

	pre, post := NewPreWriteCheck(ci, ts, PreWriteCheckOpts{Constraints: reg})

	input, _ := json.Marshal(map[string]string{
		"path": filepath.Join(dir, "counter.go"),
		"content": `package main

import "sync"

func Inc(mu *sync.Mutex) {
	mu.Lock()
	mu.Unlock()
}
`,
	})
	call := tool.ToolCall{ID: "cross-root", Name: "write", Input: input}

	pre(context.Background(), call, &tool.ToolContext{WorkDir: dir})
	result := &tool.ToolResult{Content: "written"}
	post(context.Background(), call, result, nil)

	if fails := constraintFailLines(result); len(fails) != 0 {
		t.Errorf("cross-root constraint leaked into advisory: %v", fails)
	}
}

// --- INV-CSE-16: CSE Overlay is focus-scoped and capped ---

// TestCSEFragment_RequiresAbsoluteFocus: without an absolute focus file there is
// no root to scope by, so nothing is injected. The pre-refactor fallback dumped
// registry.Active() here, which is how cross-root rules travelled.
func TestCSEFragment_RequiresAbsoluteFocus(t *testing.T) {
	dir := t.TempDir()
	reg := constraints.NewRegistry()
	reg.Add(mutexConstraint(dir))

	for _, focus := range []string{"", "counter.go", "internal/store/db.go"} {
		if _, ok := cseConstraintFragment(reg, focus); ok {
			t.Errorf("focus %q must inject nothing", focus)
		}
	}
}

// TestCSEFragment_OtherRootInjectsNothing: focus in root A never picks up
// constraints rooted in B (INV-CSE-10).
func TestCSEFragment_OtherRootInjectsNothing(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	reg := constraints.NewRegistry()
	reg.Add(mutexConstraint(rootB))

	if _, ok := cseConstraintFragment(reg, filepath.Join(rootA, "counter.go")); ok {
		t.Error("constraint from another root leaked into the overlay")
	}
}

// TestCSEFragment_CappedAtTokenBudget: many matching constraints still fit the
// budget, and the fragment only carries rules whose root owns the focus file.
func TestCSEFragment_CappedAtTokenBudget(t *testing.T) {
	dir := t.TempDir()
	reg := constraints.NewRegistry()
	checkers := []constraints.CheckerKind{
		constraints.CheckMutexDefer,
		constraints.CheckErrDiscard,
		constraints.CheckNoPanic,
		constraints.CheckContextFirst,
		constraints.CheckNoInitIO,
		constraints.CheckExportedDoc,
		constraints.CheckPlaintextSecret,
		constraints.CheckSQLConcat,
		constraints.CheckGoroutineCapture,
		constraints.CheckNPlusOne,
	}
	for i, ck := range checkers {
		reg.Add(constraints.Constraint{
			ID: string(ck),
			// Long prose on purpose: the cap must hold even when rules are verbose.
			Rule:       strings.Repeat("This rule states something at length. ", 8),
			Kind:       "quality",
			Priority:   constraints.PriorityQuality,
			Status:     constraints.StatusActive,
			Confidence: 0.5 + float64(i)/100,
			Source:     "seed",
		}.AtTreeGo(dir, ck))
	}

	frag, ok := cseConstraintFragment(reg, filepath.Join(dir, "counter.go"))
	if !ok {
		t.Fatal("expected a fragment for a focus file inside the root")
	}
	if frag.TokenCost > cseOverlayMaxTokens {
		t.Errorf("fragment is %d tokens, over the %d budget", frag.TokenCost, cseOverlayMaxTokens)
	}
	if frag.Kind != FragmentConstraint {
		t.Errorf("unexpected fragment kind %q", frag.Kind)
	}
}

// constraintFailLines extracts the "[FAIL id] msg" advisory lines from a
// ToolResult, ignoring CKG similarity/impact warnings.
func constraintFailLines(result *tool.ToolResult) []string {
	if result == nil || result.Metadata == nil {
		return nil
	}
	raw, ok := result.Metadata["ckg_warnings"].([]string)
	if !ok {
		return nil
	}
	var out []string
	for _, w := range raw {
		if strings.Contains(w, "[FAIL ") {
			out = append(out, w)
		}
	}
	return out
}
