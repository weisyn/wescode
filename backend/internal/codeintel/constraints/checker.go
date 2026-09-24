package constraints

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
)

// TargetKind is the CSE scope kind (INV-CSE-05). Empty is illegal.
type TargetKind string

const (
	TargetFunction TargetKind = "function"
	TargetModule   TargetKind = "module"
	TargetPackage  TargetKind = "package"
	TargetTree     TargetKind = "tree"
)

// CheckerKind names an executable predicate. Empty Kind is illegal (INV-CSE-15).
type CheckerKind string

const (
	CheckMutexDefer       CheckerKind = "mutex_defer"
	CheckErrDiscard       CheckerKind = "err_discard"
	CheckContextFirst     CheckerKind = "context_first"
	CheckNoInitIO         CheckerKind = "no_init_io"
	CheckExportedDoc      CheckerKind = "exported_doc"
	CheckPlaintextSecret  CheckerKind = "plaintext_secret"
	CheckNoPanic          CheckerKind = "no_panic"
	CheckGoroutineCapture CheckerKind = "goroutine_capture"
	CheckSQLConcat        CheckerKind = "sql_concat"
	CheckSignatureStable  CheckerKind = "signature_stable"
	CheckImportCycle      CheckerKind = "import_cycle"
	CheckNPlusOne         CheckerKind = "nplus1"
	CheckStateMachine     CheckerKind = "state_machine"
)

// CheckerSpec is the executable payload. Rule text is not used for judging.
//
// Forbidden is overloaded by Kind: the cycle's package members for
// import_cycle, and the complete enum value set for state_machine.
type CheckerSpec struct {
	Kind      CheckerKind `json:"kind"`
	Forbidden []string    `json:"forbidden,omitempty"`
	Symbol    string      `json:"symbol,omitempty"`
	Signature string      `json:"signature,omitempty"`
}

var (
	errNoID      = errors.New("constraint: missing id or rule")
	errNoRoot    = errors.New("constraint: empty Root")
	errNoTarget  = errors.New("constraint: empty TargetKind or TargetPath")
	errBadKind   = errors.New("constraint: TargetKind must be function|module|package|tree")
	errNoChecker = errors.New("constraint: empty Checker.Kind")
)

// Validate is the Add fail-closed gate (INV-CSE-05 / INV-CSE-15).
func (c Constraint) Validate() error {
	if c.ID == "" || c.Rule == "" {
		return errNoID
	}
	if c.Root == "" {
		return errNoRoot
	}
	if c.TargetKind == "" || c.TargetPath == "" {
		return errNoTarget
	}
	switch c.TargetKind {
	case TargetFunction, TargetModule, TargetPackage, TargetTree:
	default:
		return errBadKind
	}
	if c.Checker.Kind == "" {
		return errNoChecker
	}
	return nil
}

// Bind fills Root / TargetKind / TargetPath / Checker, and namespaces the ID
// under the owning root.
//
// The namespacing is load-bearing, not cosmetic: Registry is keyed by ID
// alone, while inferrers derive IDs from content (a dir name, a rule string, a
// path relative to root). Seven workspace roots each holding internal/auth/
// therefore produce seven constraints with one identical ID, and the last Add
// silently overwrites the previous six — every root but one loses coverage
// (INV-CSE-05). Binding is the single choke point every valid constraint must
// pass through, so identity is minted here.
func (c Constraint) Bind(root string, kind TargetKind, path string, checker CheckerSpec) Constraint {
	c.Root = filepath.Clean(root)
	c.ID = scopeID(c.ID, c.Root)
	c.TargetKind = kind
	c.TargetPath = filepath.ToSlash(path)
	c.Checker = checker
	return c
}

// rootTag is a short stable discriminator for a workspace root.
func rootTag(root string) string {
	h := sha256.Sum256([]byte(filepath.Clean(root)))
	return fmt.Sprintf("%x", h[:4])
}

// scopeID appends root's tag to a logical constraint name. Total, not just
// idempotent: re-binding an already-scoped ID to a different root replaces the
// old tag instead of stacking a second one.
func scopeID(id, root string) string {
	base := id
	if i := strings.LastIndex(base, "@"); i >= 0 && isRootTag(base[i+1:]) {
		base = base[:i]
	}
	return base + "@" + rootTag(root)
}

func isRootTag(s string) bool {
	if len(s) != 8 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// AtFile binds a constraint to one file relative to root.
func (c Constraint) AtFile(root, relPath string, kind CheckerKind) Constraint {
	return c.Bind(root, TargetFunction, filepath.ToSlash(relPath), CheckerSpec{Kind: kind})
}

// AtTreeGo binds a constraint to all Go files under root.
func (c Constraint) AtTreeGo(root string, kind CheckerKind) Constraint {
	return c.Bind(root, TargetTree, "**/*.go", CheckerSpec{Kind: kind})
}

// MatchesFile reports whether absPath falls under Root and TargetPath.
// Relative paths never match (INV-CSE-05 multi-root).
func (c Constraint) MatchesFile(absPath string) bool {
	if c.Validate() != nil {
		return false
	}
	if absPath == "" || !filepath.IsAbs(absPath) {
		return false
	}
	absPath = filepath.Clean(absPath)
	root := filepath.Clean(c.Root)
	rel, err := filepath.Rel(root, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return false
	}
	return matchTarget(c.TargetKind, c.TargetPath, rel)
}

func matchTarget(_ TargetKind, pattern, rel string) bool {
	rel = filepath.ToSlash(rel)
	pattern = filepath.ToSlash(pattern)
	switch {
	case pattern == "**" || pattern == "**/*":
		return true
	case pattern == "**/*.go":
		return strings.HasSuffix(rel, ".go")
	case strings.HasPrefix(pattern, "**/"):
		return matchGlob(strings.TrimPrefix(pattern, "**/"), rel) ||
			matchGlob(strings.TrimPrefix(pattern, "**/"), filepath.Base(rel))
	default:
		return matchGlob(pattern, rel)
	}
}

// CheckFile runs the constraint's Checker against file content.
// Parse failure or unknown checker → PASS (INV-CSE-01: 宁可漏报).
func CheckFile(c Constraint, absPath, content string) (failed bool, msg string) {
	if c.Validate() != nil || !c.MatchesFile(absPath) {
		return false, ""
	}
	if !strings.HasSuffix(absPath, ".go") {
		return false, ""
	}
	switch c.Checker.Kind {
	case CheckMutexDefer:
		return checkMutexDefer(absPath, content)
	case CheckErrDiscard:
		return checkErrDiscard(absPath, content)
	case CheckContextFirst:
		return checkContextFirst(absPath, content)
	case CheckNoInitIO:
		return checkNoInitIO(absPath, content)
	case CheckExportedDoc:
		return checkExportedDoc(absPath, content)
	case CheckImportCycle:
		return checkImportCycle(absPath, content, c.Checker)
	case CheckPlaintextSecret:
		return checkPlaintextSecret(absPath, content)
	case CheckNoPanic:
		return checkNoPanic(absPath, content)
	case CheckGoroutineCapture:
		return checkGoroutineCapture(absPath, content)
	case CheckSQLConcat:
		return checkSQLConcat(absPath, content)
	case CheckSignatureStable:
		return checkSignatureStable(absPath, content, c.Checker)
	case CheckNPlusOne:
		return checkNPlusOne(absPath, content)
	case CheckStateMachine:
		return checkStateMachine(absPath, content, c.Checker)
	default:
		return false, ""
	}
}

func absRoot(workDir string) string {
	if workDir == "" {
		return ""
	}
	if abs, err := filepath.Abs(workDir); err == nil {
		return abs
	}
	return filepath.Clean(workDir)
}

func parseGo(absPath, content string) (*token.FileSet, *ast.File, error) {
	fset := token.NewFileSet()
	var src any
	if content != "" {
		src = content
	}
	f, err := parser.ParseFile(fset, absPath, src, parser.ParseComments)
	if err != nil {
		return nil, nil, err
	}
	return fset, f, nil
}

func checkMutexDefer(absPath, content string) (bool, string) {
	_, f, err := parseGo(absPath, content)
	if err != nil {
		return false, ""
	}
	var fail string
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		hasLock, hasDeferUnlock := false, false
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if sel.Sel.Name == "Lock" || sel.Sel.Name == "RLock" {
				hasLock = true
			}
			return true
		})
		if !hasLock {
			return true
		}
		for _, stmt := range fn.Body.List {
			ds, ok := stmt.(*ast.DeferStmt)
			if !ok {
				continue
			}
			call, ok := ds.Call.Fun.(*ast.SelectorExpr)
			if ok && (call.Sel.Name == "Unlock" || call.Sel.Name == "RUnlock") {
				hasDeferUnlock = true
			}
		}
		if hasLock && !hasDeferUnlock {
			fail = fmt.Sprintf("%s: %s Lock without defer Unlock", filepath.Base(absPath), fn.Name.Name)
			return false
		}
		return true
	})
	if fail != "" {
		return true, fail
	}
	return false, ""
}

func checkErrDiscard(absPath, content string) (bool, string) {
	_, f, err := parseGo(absPath, content)
	if err != nil {
		return false, ""
	}
	var fail string
	ast.Inspect(f, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		if assign.Tok != token.ASSIGN && assign.Tok != token.DEFINE {
			return true
		}
		if len(assign.Lhs) == 0 || len(assign.Rhs) == 0 {
			return true
		}
		// `_ = f()` or `_, err :=` is explicit; `_ = err` after a call that
		// returned err is OK. Fail on `_ = someCall()` where someCall likely
		// returns error as last result — we conservatively flag `_ = Call()`.
		if ident, ok := assign.Lhs[0].(*ast.Ident); ok && ident.Name == "_" {
			if _, isCall := assign.Rhs[0].(*ast.CallExpr); isCall && len(assign.Lhs) == 1 {
				fail = fmt.Sprintf("%s: discarded call result", filepath.Base(absPath))
				return false
			}
		}
		return true
	})
	if fail != "" {
		return true, fail
	}
	return false, ""
}

func checkContextFirst(absPath, content string) (bool, string) {
	_, f, err := parseGo(absPath, content)
	if err != nil {
		return false, ""
	}
	var fail string
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Type.Params == nil || !fn.Name.IsExported() {
			return true
		}
		params := fn.Type.Params.List
		if len(params) == 0 {
			return true
		}
		if hasContextType(params[0].Type) {
			return true
		}
		for _, p := range params[1:] {
			if hasContextType(p.Type) {
				fail = fmt.Sprintf("%s: %s has context.Context not as first param", filepath.Base(absPath), fn.Name.Name)
				return false
			}
		}
		return true
	})
	if fail != "" {
		return true, fail
	}
	return false, ""
}

func hasContextType(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "context" && sel.Sel.Name == "Context"
}

func checkNoInitIO(absPath, content string) (bool, string) {
	_, f, err := parseGo(absPath, content)
	if err != nil {
		return false, ""
	}
	var fail string
	ioNames := []string{"Open", "Create", "Listen", "Dial", "Get", "Post", "Do"}
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "init" || fn.Body == nil {
			return true
		}
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := callName(call)
			for _, io := range ioNames {
				if name == io {
					fail = fmt.Sprintf("%s: init() performs I/O (%s)", filepath.Base(absPath), io)
					return false
				}
			}
			return true
		})
		return fail == ""
	})
	if fail != "" {
		return true, fail
	}
	return false, ""
}

func callName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	}
	return ""
}

func qualifiedCallName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		if x, ok := fun.X.(*ast.Ident); ok {
			return x.Name + "." + fun.Sel.Name
		}
		return fun.Sel.Name
	}
	return ""
}

func checkExportedDoc(absPath, content string) (bool, string) {
	_, f, err := parseGo(absPath, content)
	if err != nil {
		return false, ""
	}
	if strings.HasSuffix(absPath, "_test.go") {
		return false, ""
	}
	var fail string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !fn.Name.IsExported() {
			continue
		}
		if fn.Doc == nil || !strings.HasPrefix(strings.TrimSpace(fn.Doc.Text()), fn.Name.Name) {
			fail = fmt.Sprintf("%s: exported %s missing doc comment", filepath.Base(absPath), fn.Name.Name)
			return true, fail
		}
	}
	return false, ""
}

// checkImportCycle fails when a file inside the cycle imports another member of
// the same cycle. spec.Forbidden carries the whole SCC, so the file's own
// package must be excluded from the comparison (a package importing itself is
// not expressible in Go).
func checkImportCycle(absPath, content string, spec CheckerSpec) (bool, string) {
	_, f, err := parseGo(absPath, content)
	if err != nil || len(spec.Forbidden) == 0 {
		return false, ""
	}
	pkg := f.Name.Name
	inCycle := false
	for _, p := range spec.Forbidden {
		if pkg == p || strings.HasSuffix(p, "/"+pkg) {
			inCycle = true
			break
		}
	}
	if !inCycle {
		return false, ""
	}
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		for _, member := range spec.Forbidden {
			if member == pkg || strings.HasSuffix(member, "/"+pkg) {
				continue
			}
			if path == member || strings.HasSuffix(path, "/"+member) {
				return true, fmt.Sprintf("%s: import %s closes a dependency cycle", filepath.Base(absPath), path)
			}
		}
	}
	return false, ""
}

func checkPlaintextSecret(absPath, content string) (bool, string) {
	_, f, err := parseGo(absPath, content)
	if err != nil {
		return false, ""
	}
	secretNames := []string{"password", "passwd", "secret", "apikey", "api_key", "token"}
	var fail string
	ast.Inspect(f, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range assign.Lhs {
			ident, ok := lhs.(*ast.Ident)
			if !ok || i >= len(assign.Rhs) {
				continue
			}
			lit, ok := assign.Rhs[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || len(lit.Value) <= 4 {
				continue
			}
			lower := strings.ToLower(ident.Name)
			for _, s := range secretNames {
				if strings.Contains(lower, s) {
					fail = fmt.Sprintf("%s: hardcoded secret %s", filepath.Base(absPath), ident.Name)
					return false
				}
			}
		}
		return true
	})
	if fail != "" {
		return true, fail
	}
	return false, ""
}

func checkNoPanic(absPath, content string) (bool, string) {
	if strings.HasSuffix(absPath, "_test.go") {
		return false, ""
	}
	_, f, err := parseGo(absPath, content)
	if err != nil {
		return false, ""
	}
	var fail string
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name == "init" || fn.Body == nil {
			return true
		}
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if ok && ident.Name == "panic" {
				fail = fmt.Sprintf("%s: panic() in %s", filepath.Base(absPath), fn.Name.Name)
				return false
			}
			return true
		})
		return fail == ""
	})
	if fail != "" {
		return true, fail
	}
	return false, ""
}

func checkGoroutineCapture(absPath, content string) (bool, string) {
	_, f, err := parseGo(absPath, content)
	if err != nil {
		return false, ""
	}
	var fail string
	ast.Inspect(f, func(n ast.Node) bool {
		goStmt, ok := n.(*ast.GoStmt)
		if !ok {
			return true
		}
		lit, ok := goStmt.Call.Fun.(*ast.FuncLit)
		if !ok || lit.Body == nil {
			return true
		}
		// A closure that locks or talks over a channel is synchronizing on
		// purpose; capturing under that is the normal pattern, not a finding.
		if hasSyncInBody(lit.Body) {
			return true
		}
		captured := capturedVars(lit)
		if len(captured) == 0 {
			return true
		}
		fail = fmt.Sprintf("%s: goroutine captures %s without sync",
			filepath.Base(absPath), strings.Join(sortedKeys(captured), ", "))
		return false
	})
	if fail != "" {
		return true, fail
	}
	return false, ""
}

// capturedVars returns identifiers a goroutine closure reads from its enclosing
// scope: variables that are neither parameters nor declared inside the body.
func capturedVars(lit *ast.FuncLit) map[string]bool {
	locals := map[string]bool{}
	if lit.Type.Params != nil {
		for _, field := range lit.Type.Params.List {
			for _, name := range field.Names {
				locals[name.Name] = true
			}
		}
	}
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		switch decl := n.(type) {
		case *ast.AssignStmt:
			if decl.Tok == token.DEFINE {
				for _, lhs := range decl.Lhs {
					if ident, ok := lhs.(*ast.Ident); ok {
						locals[ident.Name] = true
					}
				}
			}
		case *ast.RangeStmt:
			if decl.Tok != token.DEFINE {
				return true
			}
			if key, ok := decl.Key.(*ast.Ident); ok {
				locals[key.Name] = true
			}
			if val, ok := decl.Value.(*ast.Ident); ok {
				locals[val.Name] = true
			}
		}
		return true
	})

	captured := map[string]bool{}
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok || ident.Obj == nil || ident.Obj.Kind != ast.Var {
			return true
		}
		switch ident.Name {
		case "_", "nil", "true", "false", "err":
			return true
		}
		if !locals[ident.Name] {
			captured[ident.Name] = true
		}
		return true
	})
	return captured
}

// hasSyncInBody reports whether the closure body locks a mutex or uses a
// channel — the two visible signals that the capture is deliberate.
func hasSyncInBody(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		switch node := n.(type) {
		case *ast.CallExpr:
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok {
				switch sel.Sel.Name {
				case "Lock", "Unlock", "RLock", "RUnlock":
					found = true
				}
			}
		case *ast.SendStmt:
			found = true
		case *ast.UnaryExpr:
			if node.Op == token.ARROW {
				found = true
			}
		}
		return !found
	})
	return found
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func checkSQLConcat(absPath, content string) (bool, string) {
	fset, f, err := parseGo(absPath, content)
	if err != nil {
		return false, ""
	}
	var fail string
	ast.Inspect(f, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			if isFmtSprintfSQL(node) {
				pos := fset.Position(node.Pos())
				fail = fmt.Sprintf("%s:%d SQL via fmt.Sprintf", filepath.Base(absPath), pos.Line)
				return false
			}
		case *ast.BinaryExpr:
			if node.Op == token.ADD && looksLikeSQL(node) {
				pos := fset.Position(node.Pos())
				fail = fmt.Sprintf("%s:%d SQL string concat", filepath.Base(absPath), pos.Line)
				return false
			}
		}
		return true
	})
	if fail != "" {
		return true, fail
	}
	return false, ""
}

func isFmtSprintfSQL(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Sprintf" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "fmt" || len(call.Args) == 0 {
		return false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok {
		return false
	}
	s := strings.ToUpper(lit.Value)
	return strings.Contains(s, "SELECT") || strings.Contains(s, "INSERT") ||
		strings.Contains(s, "UPDATE") || strings.Contains(s, "DELETE")
}

func looksLikeSQL(bin *ast.BinaryExpr) bool {
	lit, ok := bin.X.(*ast.BasicLit)
	if !ok {
		lit, ok = bin.Y.(*ast.BasicLit)
		if !ok {
			return false
		}
	}
	s := strings.ToUpper(lit.Value)
	return strings.Contains(s, "SELECT") || strings.Contains(s, "INSERT")
}

// checkSignatureStable compares the pinned parameter arity against the edited
// file. Only arity is compared: parameter names and formatting churn are not
// breaking changes, and a full type comparison would need the CKG's exact
// rendering to match go/ast's. Unparsable pinned signature → PASS.
func checkSignatureStable(absPath, content string, spec CheckerSpec) (bool, string) {
	if spec.Symbol == "" {
		return false, ""
	}
	want, ok := arityFromSignature(spec.Signature)
	if !ok {
		return false, ""
	}
	_, f, err := parseGo(absPath, content)
	if err != nil {
		return false, ""
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != spec.Symbol {
			continue
		}
		got := 0
		if fn.Type.Params != nil {
			got = fn.Type.Params.NumFields()
		}
		if got != want {
			return true, fmt.Sprintf("%s: %s arity changed %d → %d (callers pinned)",
				filepath.Base(absPath), spec.Symbol, want, got)
		}
		return false, ""
	}
	return false, ""
}

// arityFromSignature counts parameters in a CKG-rendered signature such as
// "func Foo(ctx context.Context, id string) error". Returns ok=false when the
// parameter list cannot be located or contains nested parens/generics that
// would make a naive split wrong.
func arityFromSignature(sig string) (int, bool) {
	open := strings.Index(sig, "(")
	if open < 0 {
		return 0, false
	}
	depth := 0
	end := -1
	for i := open; i < len(sig); i++ {
		switch sig[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return 0, false
	}
	params := strings.TrimSpace(sig[open+1 : end])
	if params == "" {
		return 0, true
	}
	// Nested parens (func-typed params) or brackets make comma splitting
	// ambiguous against go/ast field counting — refuse instead of guessing.
	if strings.ContainsAny(params, "([") {
		return 0, false
	}
	return len(strings.Split(params, ",")), true
}

// SignatureFromFile renders an arity-only signature for symbol in absPath, e.g.
// three parameters → "func Foo(_, _, _)". checkSignatureStable reads nothing but
// the parameter count, and the synthetic form sidesteps the nested-paren case
// arityFromSignature has to refuse on real types (func-typed params, generics).
//
// Callers that already hold a CKG signature should pass that instead; this is
// for pinning the *current* shape of a symbol at learn time.
func SignatureFromFile(absPath, symbol string) (string, bool) {
	if symbol == "" {
		return "", false
	}
	_, f, err := parseGo(absPath, "")
	if err != nil {
		return "", false
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != symbol {
			continue
		}
		n := 0
		if fn.Type.Params != nil {
			n = fn.Type.Params.NumFields()
		}
		placeholders := make([]string, n)
		for i := range placeholders {
			placeholders[i] = "_"
		}
		return fmt.Sprintf("func %s(%s)", symbol, strings.Join(placeholders, ", ")), true
	}
	return "", false
}

// checkStateMachine reports a switch over spec.Symbol's enum values that omits
// some of them. spec.Forbidden carries the full value set (see CheckerSpec).
//
// Two conditions keep this honest rather than noisy. A switch with a `default`
// clause is exempt — default *is* the handler for the remaining values, so
// demanding every case there would be wrong. And a switch is only treated as a
// switch over this enum when it matches at least two of its values; one shared
// identifier is a coincidence, not exhaustive dispatch.
func checkStateMachine(absPath, content string, spec CheckerSpec) (bool, string) {
	if spec.Symbol == "" || len(spec.Forbidden) < 2 {
		return false, ""
	}
	_, f, err := parseGo(absPath, content)
	if err != nil {
		return false, ""
	}
	all := make(map[string]bool, len(spec.Forbidden))
	for _, v := range spec.Forbidden {
		all[v] = true
	}

	var fail string
	ast.Inspect(f, func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok || sw.Body == nil {
			return true
		}
		covered := map[string]bool{}
		hasDefault := false
		for _, stmt := range sw.Body.List {
			cc, ok := stmt.(*ast.CaseClause)
			if !ok {
				continue
			}
			if cc.List == nil {
				hasDefault = true
				continue
			}
			for _, expr := range cc.List {
				if ident, ok := expr.(*ast.Ident); ok && all[ident.Name] {
					covered[ident.Name] = true
				}
			}
		}
		if hasDefault || len(covered) < 2 || len(covered) == len(all) {
			return true
		}
		missing := make([]string, 0, len(all)-len(covered))
		for _, v := range spec.Forbidden {
			if !covered[v] {
				missing = append(missing, v)
			}
		}
		sort.Strings(missing)
		fail = fmt.Sprintf("%s: switch on %s misses %s (no default)",
			filepath.Base(absPath), spec.Symbol, strings.Join(missing, ", "))
		return false
	})
	if fail != "" {
		return true, fail
	}
	return false, ""
}

func checkNPlusOne(absPath, content string) (bool, string) {
	_, f, err := parseGo(absPath, content)
	if err != nil {
		return false, ""
	}
	ioIndicators := []string{
		"Query", "QueryRow", "Exec", "Get", "Post", "Do", "Open", "ReadFile",
	}
	var fail string
	ast.Inspect(f, func(n ast.Node) bool {
		var body *ast.BlockStmt
		switch stmt := n.(type) {
		case *ast.ForStmt:
			body = stmt.Body
		case *ast.RangeStmt:
			body = stmt.Body
		default:
			return true
		}
		if body == nil {
			return true
		}
		ast.Inspect(body, func(inner ast.Node) bool {
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := callName(call)
			for _, io := range ioIndicators {
				if name == io {
					fail = fmt.Sprintf("%s: %s inside loop (N+1)", filepath.Base(absPath), name)
					return false
				}
			}
			return true
		})
		return fail == ""
	})
	if fail != "" {
		return true, fail
	}
	return false, ""
}
