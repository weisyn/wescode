package constraints

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// checkerCase is one predicate's FAIL fixture plus a near-miss that must PASS.
// The pair is what makes a Checker a Checker: without the PASS half a predicate
// that always fires would look healthy here.
type checkerCase struct {
	spec CheckerSpec
	fail string
	pass string
}

func checkerCases() map[CheckerKind]checkerCase {
	return map[CheckerKind]checkerCase{
		CheckMutexDefer: {
			spec: CheckerSpec{Kind: CheckMutexDefer},
			fail: `package p
import "sync"
var mu sync.Mutex
func Touch() {
	mu.Lock()
	_ = 1
}
`,
			pass: `package p
import "sync"
var mu sync.Mutex
func Touch() {
	mu.Lock()
	defer mu.Unlock()
	_ = 1
}
`,
		},
		CheckErrDiscard: {
			spec: CheckerSpec{Kind: CheckErrDiscard},
			fail: `package p
func do() error { return nil }
func Run() {
	_ = do()
}
`,
			pass: `package p
func do() error { return nil }
func Run() error {
	if err := do(); err != nil {
		return err
	}
	return nil
}
`,
		},
		CheckContextFirst: {
			spec: CheckerSpec{Kind: CheckContextFirst},
			fail: `package p
import "context"
func Fetch(id string, ctx context.Context) error { return nil }
`,
			pass: `package p
import "context"
func Fetch(ctx context.Context, id string) error { return nil }
`,
		},
		CheckNoInitIO: {
			spec: CheckerSpec{Kind: CheckNoInitIO},
			fail: `package p
import "os"
var f *os.File
func init() {
	f, _ = os.Open("/etc/hosts")
}
`,
			pass: `package p
var cache = map[string]string{}
func init() {
	cache["k"] = "v"
}
`,
		},
		CheckExportedDoc: {
			spec: CheckerSpec{Kind: CheckExportedDoc},
			fail: `package p
func Exported() {}
`,
			pass: `package p

// Exported does a thing.
func Exported() {}
`,
		},
		CheckPlaintextSecret: {
			spec: CheckerSpec{Kind: CheckPlaintextSecret},
			fail: `package p
func Connect() {
	apiKey := "sk-live-abcdef"
	_ = apiKey
}
`,
			pass: `package p
import "os"
func Connect() {
	apiKey := os.Getenv("API_KEY")
	_ = apiKey
}
`,
		},
		CheckNoPanic: {
			spec: CheckerSpec{Kind: CheckNoPanic},
			fail: `package p
func Must(ok bool) {
	if !ok {
		panic("nope")
	}
}
`,
			pass: `package p
import "errors"
func Must(ok bool) error {
	if !ok {
		return errors.New("nope")
	}
	return nil
}
`,
		},
		CheckGoroutineCapture: {
			spec: CheckerSpec{Kind: CheckGoroutineCapture},
			fail: `package p
func Run(items []int) {
	total := 0
	for _, it := range items {
		go func() {
			total += it
		}()
	}
}
`,
			pass: `package p
import "sync"
func Run(items []int) {
	var mu sync.Mutex
	total := 0
	for _, it := range items {
		go func() {
			mu.Lock()
			total += it
			mu.Unlock()
		}()
	}
}
`,
		},
		CheckSQLConcat: {
			spec: CheckerSpec{Kind: CheckSQLConcat},
			fail: `package p
func Load(id string) string {
	return "SELECT * FROM users WHERE id = " + id
}
`,
			pass: `package p
const q = "SELECT * FROM users WHERE id = ?"
func Load(id string) (string, string) {
	return q, id
}
`,
		},
		CheckSignatureStable: {
			spec: CheckerSpec{Kind: CheckSignatureStable, Symbol: "Fetch", Signature: "func Fetch(_, _)"},
			fail: `package p
func Fetch(id string) error { return nil }
`,
			pass: `package p
import "context"
func Fetch(ctx context.Context, id string) error { return nil }
`,
		},
		CheckImportCycle: {
			spec: CheckerSpec{Kind: CheckImportCycle, Forbidden: []string{"alpha", "beta"}},
			fail: `package alpha
import "example.com/x/beta"
var _ = beta.Name
`,
			pass: `package alpha
import "fmt"
var _ = fmt.Sprint
`,
		},
		CheckNPlusOne: {
			spec: CheckerSpec{Kind: CheckNPlusOne},
			fail: `package p
type db interface{ Query(string) error }
func Load(d db, ids []string) {
	for _, id := range ids {
		_ = d.Query(id)
	}
}
`,
			pass: `package p
type db interface{ Query(string) error }
func Load(d db, ids []string) {
	_ = ids
	_ = d.Query("in (...)")
}
`,
		},
		CheckStateMachine: {
			spec: CheckerSpec{
				Kind:      CheckStateMachine,
				Symbol:    "State",
				Forbidden: []string{"StateIdle", "StateRunning", "StateDone"},
			},
			fail: `package p
type State int
const (
	StateIdle State = iota
	StateRunning
	StateDone
)
func Label(s State) string {
	switch s {
	case StateIdle:
		return "idle"
	case StateRunning:
		return "running"
	}
	return ""
}
`,
			pass: `package p
type State int
const (
	StateIdle State = iota
	StateRunning
	StateDone
)
func Label(s State) string {
	switch s {
	case StateIdle:
		return "idle"
	case StateRunning:
		return "running"
	case StateDone:
		return "done"
	}
	return ""
}
`,
		},
	}
}

func checkerConstraint(t *testing.T, root string, spec CheckerSpec) Constraint {
	t.Helper()
	c := Constraint{ID: "t-" + string(spec.Kind), Rule: "test fixture"}.
		Bind(root, TargetTree, "**/*.go", spec)
	if err := c.Validate(); err != nil {
		t.Fatalf("fixture constraint invalid: %v", err)
	}
	return c
}

func TestCheckFile_EachPredicateFailsAndPasses(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "sample.go")

	for kind, tc := range checkerCases() {
		t.Run(string(kind), func(t *testing.T) {
			c := checkerConstraint(t, root, tc.spec)

			failed, msg := CheckFile(c, abs, tc.fail)
			if !failed {
				t.Errorf("%s: expected FAIL on violating fixture, got PASS", kind)
			}
			if failed && strings.TrimSpace(msg) == "" {
				t.Errorf("%s: FAIL must carry a one-line reason", kind)
			}

			if failed, msg := CheckFile(c, abs, tc.pass); failed {
				t.Errorf("%s: expected PASS on compliant fixture, got FAIL: %s", kind, msg)
			}
		})
	}
}

// TestCheckerKinds_AllCovered keeps the predicate set, its dispatch and this
// test file in lockstep: a new CheckerKind constant without a fixture (or a
// fixture the CheckFile switch silently drops) fails here rather than shipping
// as a constraint that can never FAIL.
func TestCheckerKinds_AllCovered(t *testing.T) {
	declared := declaredCheckerKinds(t)
	if len(declared) == 0 {
		t.Fatal("no CheckerKind constants found in checker.go")
	}
	cases := checkerCases()
	for _, kind := range declared {
		if _, ok := cases[kind]; !ok {
			t.Errorf("CheckerKind %q has no FAIL/PASS fixture", kind)
		}
	}
	for kind := range cases {
		found := false
		for _, d := range declared {
			if d == kind {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("fixture %q is not a declared CheckerKind", kind)
		}
	}
}

func declaredCheckerKinds(t *testing.T) []CheckerKind {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "checker.go", nil, 0)
	if err != nil {
		t.Fatalf("parse checker.go: %v", err)
	}
	var kinds []CheckerKind
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) != 1 {
				continue
			}
			ident, ok := vs.Type.(*ast.Ident)
			if !ok || ident.Name != "CheckerKind" {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			kinds = append(kinds, CheckerKind(strings.Trim(lit.Value, `"`)))
		}
	}
	return kinds
}

// A relative edit path must never match: multi-root isolation depends on
// absolute containment (INV-CSE-05).
func TestCheckFile_RelativePathNeverMatches(t *testing.T) {
	root := t.TempDir()
	c := checkerConstraint(t, root, CheckerSpec{Kind: CheckNoPanic})
	if failed, _ := CheckFile(c, "sample.go", "package p\nfunc F() { panic(1) }\n"); failed {
		t.Fatal("relative path matched a rooted constraint")
	}
}

// A constraint from another workspace root must stay silent on this file even
// when the file violates the predicate.
func TestCheckFile_OtherRootNeverMatches(t *testing.T) {
	mine := t.TempDir()
	other := t.TempDir()
	c := checkerConstraint(t, other, CheckerSpec{Kind: CheckNoPanic})
	abs := filepath.Join(mine, "sample.go")
	if failed, msg := CheckFile(c, abs, "package p\nfunc F() { panic(1) }\n"); failed {
		t.Fatalf("cross-root constraint fired: %s", msg)
	}
}

// Unparsable content is a PASS, not a FAIL: CSE would otherwise flood every
// mid-edit buffer with syntax noise (INV-CSE-01 宁可漏报).
func TestCheckFile_ParseErrorPasses(t *testing.T) {
	root := t.TempDir()
	c := checkerConstraint(t, root, CheckerSpec{Kind: CheckNoPanic})
	abs := filepath.Join(root, "sample.go")
	if failed, msg := CheckFile(c, abs, "package p\nfunc F() { panic(1"); failed {
		t.Fatalf("parse error produced FAIL: %s", msg)
	}
}
