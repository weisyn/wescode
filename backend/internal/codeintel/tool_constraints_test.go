package codeintel

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weisyn/wescode/internal/codeintel/constraints"
	"github.com/weisyn/wesgine/tool"
)

const lockRuleProse = "A function that Locks a mutex must defer its Unlock."

// TestCheckConstraintsTool_FailCarriesVerdictNotProse: the failure line names
// the constraint and the checker's finding. The rule sentence stays out — a
// model-facing tool that echoed it would be rule injection by pull instead of
// push, same bytes in the same context window (INV-CSE-16).
func TestCheckConstraintsTool_FailCarriesVerdictNotProse(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "counter.go")
	if err := os.WriteFile(target, []byte(`package main

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
`), 0644); err != nil {
		t.Fatal(err)
	}

	reg := constraints.NewRegistry()
	// Bind namespaces the ID under its root, so the expected line has to be
	// derived from the registered constraint rather than the literal seed name.
	c := mutexConstraint(dir)
	reg.Add(c)

	out := callCheckConstraints(t, reg, dir, target)

	if !strings.Contains(out, "[FAIL "+c.ID+"]") {
		t.Errorf("expected a FAIL line naming the constraint, got:\n%s", out)
	}
	if strings.Contains(out, lockRuleProse) {
		t.Errorf("rule prose leaked into the tool result:\n%s", out)
	}
}

// TestCheckConstraintsTool_PassSaysNothingAboutTheRule: a compliant file gets a
// count, never the rules it happened to satisfy.
func TestCheckConstraintsTool_PassSaysNothingAboutTheRule(t *testing.T) {
	dir := t.TempDir()
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

	out := callCheckConstraints(t, reg, dir, target)

	if strings.Contains(out, "[FAIL") {
		t.Errorf("compliant file must not produce a FAIL line, got:\n%s", out)
	}
	if strings.Contains(out, lockRuleProse) || strings.Contains(out, "seed-mutex-defer") {
		t.Errorf("passing constraint must stay anonymous, got:\n%s", out)
	}
}

// TestCheckConstraintsTool_OtherRootIsNotApplicable: a constraint rooted
// elsewhere is not "passing" for this file, it simply does not apply.
func TestCheckConstraintsTool_OtherRootIsNotApplicable(t *testing.T) {
	dir, otherRoot := t.TempDir(), t.TempDir()
	target := filepath.Join(dir, "counter.go")
	if err := os.WriteFile(target, []byte(`package main

import "sync"

func Inc(mu *sync.Mutex) {
	mu.Lock()
	mu.Unlock()
}
`), 0644); err != nil {
		t.Fatal(err)
	}

	reg := constraints.NewRegistry()
	reg.Add(mutexConstraint(otherRoot))

	out := callCheckConstraints(t, reg, dir, target)

	if !strings.Contains(out, "No constraints apply") {
		t.Errorf("cross-root constraint must not match, got:\n%s", out)
	}
}

// --- Source-level locks on the model-facing tool surface ---

// TestModelFacingTools_NeverReadConstraintRule walks every tool_*.go in this
// package and fails on a `.Rule` selector. The two behavioral tests above only
// cover check_constraints; this one catches the next tool someone writes.
func TestModelFacingTools_NeverReadConstraintRule(t *testing.T) {
	for _, path := range toolSourceFiles(t) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if ok && sel.Sel.Name == "Rule" {
				t.Errorf("%s references .Rule at %s — rule prose must not reach the model (INV-CSE-16)",
					filepath.Base(path), fset.Position(sel.Pos()))
			}
			return true
		})
	}
}

// TestModelFacingSurfaces_NeverReadEvidence guards every path that writes into
// the context window — tools, the CSE overlay, the pre/post-write advisories.
// Evidence is the inferrer's trail (the packages in the cycle, the call sites
// missing a guard); it exists so a human reviewer can judge confirm-or-dismiss
// without re-deriving the finding. Rendering it for the model would put a
// several-hundred-token narrative next to a one-line verdict the model can
// already act on, and it would do it on every matching edit.
func TestModelFacingSurfaces_NeverReadEvidence(t *testing.T) {
	for _, path := range modelFacingSourceFiles(t) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if ok && sel.Sel.Name == "Evidence" {
				t.Errorf("%s references .Evidence at %s — findings are for the human panel, not the context window (INV-CSE-13)",
					filepath.Base(path), fset.Position(sel.Pos()))
			}
			return true
		})
	}
}

// TestNoBulkConstraintLister locks the deletion of list_constraints. Humans
// browse the full registry over the constraint/list RPC; the model gets the
// focus-scoped overlay and FAIL advisories.
func TestNoBulkConstraintLister(t *testing.T) {
	for _, path := range toolSourceFiles(t) {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), "list_constraints") {
			t.Errorf("%s reintroduces a bulk constraint lister", filepath.Base(path))
		}
	}
}

// modelFacingSourceFiles is every file in this package that can put bytes in
// front of the model: the tools it calls, the overlay injected into the user
// message, and the pre/post-write hooks that append advisories.
func modelFacingSourceFiles(t *testing.T) []string {
	t.Helper()
	out := toolSourceFiles(t)
	for _, name := range []string{"overlay_cse.go", "prewrite.go", "postwrite.go"} {
		if _, err := os.Stat(name); err != nil {
			t.Fatalf("%s is gone — the guard would silently stop covering it: %v", name, err)
		}
		out = append(out, name)
	}
	return out
}

func toolSourceFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "tool_") || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, name)
	}
	if len(out) == 0 {
		t.Fatal("no tool_*.go sources found — the lock would silently pass")
	}
	return out
}

func callCheckConstraints(t *testing.T, reg *constraints.Registry, workDir, path string) string {
	t.Helper()
	input, err := json.Marshal(map[string]string{"file_path": path})
	if err != nil {
		t.Fatal(err)
	}
	res, err := NewCheckConstraintsTool(reg).(interface {
		Call(context.Context, json.RawMessage, *tool.ToolContext) (*tool.ToolResult, error)
	}).Call(context.Background(), input, &tool.ToolContext{WorkDir: workDir})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool errored: %s", res.Content)
	}
	return res.Content
}
