package treesitter

import (
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

// SymbolKind classifies extracted code symbols.
type SymbolKind string

const (
	KindFunction  SymbolKind = "function"
	KindMethod    SymbolKind = "method"
	KindType      SymbolKind = "type"
	KindInterface SymbolKind = "interface"
	KindConst     SymbolKind = "const"
	KindVar       SymbolKind = "var"
	KindImport    SymbolKind = "import"
	KindClass     SymbolKind = "class"
)

// Symbol represents a named code symbol extracted from an AST.
type Symbol struct {
	Kind       SymbolKind
	Name       string
	Signature  string // full text of the declaration line(s)
	Parent     string // enclosing type/class (methods only)
	StartLine  int    // 0-based
	EndLine    int    // 0-based, inclusive
	StartByte  uint
	EndByte    uint
	Exported   bool   // whether the symbol is publicly visible outside its package/module
	Visibility string // "public" | "private" | "internal" | "protected" | ""
}

// ImportEntry represents a single import statement.
type ImportEntry struct {
	Path      string // e.g. "fmt" or "github.com/foo/bar"
	Alias     string // local alias if any, empty otherwise
	StartLine int
	EndLine   int
}

// CallSite describes a function/method call with source position information,
// enabling LSP-based type disambiguation (e.g. resolving `s.mu.Lock()` to
// `(*sync.Mutex).Lock` via hover at the call position).
type CallSite struct {
	Name     string // the called function/method name (e.g. "Lock")
	Receiver string // the receiver expression text (e.g. "s.mu"), empty for plain functions
	Line     int    // 0-based line within the fragment
	Col      int    // 0-based column of the callee name
	IsMethod bool   // true when called via selector (obj.Method())
}

// VarFlowKind classifies a variable flow entry.
type VarFlowKind string

const (
	VarFlowDef    VarFlowKind = "def"     // variable definition (declaration or assignment LHS)
	VarFlowUse    VarFlowKind = "use"     // variable usage (read)
	VarFlowDefUse VarFlowKind = "def+use" // reassignment (reads old value + writes new)
	VarFlowReturn VarFlowKind = "return"  // returned from function
	VarFlowParam  VarFlowKind = "param"   // function parameter
)

// VarFlowEntry describes a single point in a variable's data flow within a function.
type VarFlowEntry struct {
	Name    string      // variable name
	Line    int         // 0-based line within the fragment
	Col     int         // 0-based column
	Kind    VarFlowKind // classification
	RHSExpr string      // assignment RHS summary (truncated to 80 chars)
}

// CFNodeKind classifies a control flow node.
type CFNodeKind string

const (
	CFNodeIf       CFNodeKind = "if"
	CFNodeElse     CFNodeKind = "else"
	CFNodeElseIf   CFNodeKind = "else_if"
	CFNodeFor      CFNodeKind = "for"
	CFNodeSwitch   CFNodeKind = "switch"
	CFNodeCase     CFNodeKind = "case"
	CFNodeDefault  CFNodeKind = "default"
	CFNodeReturn   CFNodeKind = "return"
	CFNodeDefer    CFNodeKind = "defer"
	CFNodePanic    CFNodeKind = "panic"
	CFNodeTry      CFNodeKind = "try"
	CFNodeCatch    CFNodeKind = "catch"
	CFNodeFinally  CFNodeKind = "finally"
	CFNodeBreak    CFNodeKind = "break"
	CFNodeContinue CFNodeKind = "continue"
	CFNodeGoto     CFNodeKind = "goto"
	CFNodeSelect   CFNodeKind = "select" // Go select{}
)

// ControlFlowEntry describes a control flow node within a function.
type ControlFlowEntry struct {
	Kind      CFNodeKind
	Line      int    // 0-based line within the fragment
	Col       int    // 0-based column
	Depth     int    // nesting depth (0 = top level of function body)
	Condition string // condition expression (if/for/switch), truncated to 80 chars
	IsError   bool   // true if this is an error-handling path (e.g. `if err != nil`)
}

// RouteEntry describes an HTTP route registration extracted from backend code.
type RouteEntry struct {
	Method   string // "GET" | "POST" | "PUT" | "DELETE" | "PATCH" | "*"
	Path     string // "/api/users/:id"
	Handler  string // handler function/method name
	FilePath string // absolute file path
	Line     int    // 0-based line number
}

// APICallEntry describes an HTTP API call extracted from frontend/client code.
type APICallEntry struct {
	Method   string // "GET" | "POST" | inferred from function name
	URL      string // "/api/users" or full URL
	FilePath string
	Line     int
	Col      int
}

// SyntaxError describes a parse error found by tree-sitter.
type SyntaxError struct {
	Line    int // 0-based
	Column  int // 0-based
	EndLine int
	EndCol  int
	Message string
}

// CollectErrors walks the tree and returns all ERROR/MISSING nodes.
func CollectErrors(tree *tree_sitter.Tree, content []byte) []SyntaxError {
	var errors []SyntaxError
	root := tree.RootNode()
	if !root.HasError() {
		return nil
	}
	walkErrors(root, func(n *tree_sitter.Node) {
		start := n.StartPosition()
		end := n.EndPosition()
		msg := "syntax error"
		if n.IsExtra() {
			msg = "unexpected token"
		} else if n.IsMissing() {
			msg = "missing " + n.GrammarName()
		}
		errors = append(errors, SyntaxError{
			Line:    int(start.Row),
			Column:  int(start.Column),
			EndLine: int(end.Row),
			EndCol:  int(end.Column),
			Message: msg,
		})
	})
	return errors
}

func walkErrors(n *tree_sitter.Node, fn func(*tree_sitter.Node)) {
	if n.IsError() || n.IsMissing() {
		fn(n)
		return
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		child := n.Child(uint(i))
		if child != nil && child.HasError() {
			walkErrors(child, fn)
		}
	}
}
