package treesitter

import (
	"strings"
	"unicode"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

// ExtractSymbols extracts top-level code symbols from a parsed tree.
// The returned list is ordered by source position. Each symbol is annotated
// with Exported and Visibility based on language-specific conventions.
func ExtractSymbols(lang Lang, tree *tree_sitter.Tree, content []byte) []Symbol {
	root := tree.RootNode()
	var syms []Symbol
	switch lang {
	case LangGo:
		syms = extractGoSymbols(root, content)
	case LangTypeScript, LangTSX, LangJavaScript:
		syms = extractTSSymbols(root, content)
	case LangPython:
		syms = extractPythonSymbols(root, content)
	case LangRust:
		syms = extractRustSymbols(root, content)
	case LangJava:
		syms = extractJavaSymbols(root, content)
	case LangCPP, LangC:
		syms = extractCCppSymbols(root, content)
	case LangCSharp:
		syms = extractCSharpSymbols(root, content)
	case LangKotlin:
		syms = extractKotlinSymbols(root, content)
	case LangPHP:
		syms = extractPHPSymbols(root, content)
	case LangRuby:
		syms = extractRubySymbols(root, content)
	default:
		return nil
	}
	annotateVisibility(lang, syms)
	return syms
}

// annotateVisibility fills Exported and Visibility on symbols that were not
// already annotated during extraction (e.g. TS export_statement symbols are
// pre-annotated). Language-specific rules:
//   - Go: uppercase first letter = public
//   - Python: leading underscore = private (convention)
//   - Rust: presence of "pub " in signature
//   - Java/C#/Kotlin/PHP: presence of "public " in signature
//   - C/C++: all symbols treated as potentially public (header detection not done here)
//   - Ruby: default public unless "private" or "protected" prefix
func annotateVisibility(lang Lang, syms []Symbol) {
	for i := range syms {
		s := &syms[i]
		if s.Visibility != "" {
			continue // already annotated (e.g. TS export)
		}
		if s.Kind == KindImport {
			continue
		}
		switch lang {
		case LangGo:
			if len(s.Name) > 0 && unicode.IsUpper(rune(s.Name[0])) {
				s.Exported = true
				s.Visibility = "public"
			} else {
				s.Visibility = "private"
			}
		case LangTypeScript, LangTSX, LangJavaScript:
			// non-export symbols default to module-private
			s.Visibility = "private"
		case LangPython:
			if len(s.Name) > 0 && s.Name[0] == '_' {
				s.Visibility = "private"
			} else {
				s.Exported = true
				s.Visibility = "public"
			}
		case LangRust:
			if strings.HasPrefix(s.Signature, "pub ") || strings.Contains(s.Signature, " pub ") {
				s.Exported = true
				s.Visibility = "public"
			} else {
				s.Visibility = "private"
			}
		case LangJava, LangCSharp, LangKotlin, LangPHP:
			sig := s.Signature
			if strings.Contains(sig, "public ") {
				s.Exported = true
				s.Visibility = "public"
			} else if strings.Contains(sig, "protected ") {
				s.Visibility = "protected"
			} else if strings.Contains(sig, "private ") {
				s.Visibility = "private"
			} else if strings.Contains(sig, "internal ") {
				s.Visibility = "internal"
			} else {
				// Java: package-private; C#: internal; Kotlin: public by default
				if lang == LangKotlin {
					s.Exported = true
					s.Visibility = "public"
				} else {
					s.Visibility = "internal"
				}
			}
		case LangCPP, LangC:
			// C/C++ visibility is header-based; treat all as potentially public
			s.Exported = true
			s.Visibility = "public"
		case LangRuby:
			// Ruby defaults to public; private/protected would need method-level analysis
			s.Exported = true
			s.Visibility = "public"
		default:
			s.Exported = true
			s.Visibility = "public"
		}
	}
}

// ExtractImports returns all import entries from the file.
func ExtractImports(lang Lang, tree *tree_sitter.Tree, content []byte) []ImportEntry {
	root := tree.RootNode()
	switch lang {
	case LangGo:
		return extractGoImports(root, content)
	case LangTypeScript, LangTSX, LangJavaScript:
		return extractTSImports(root, content)
	case LangPython:
		return extractPythonImports(root, content)
	case LangRust:
		return extractRustImports(root, content)
	case LangJava:
		return extractJavaImports(root, content)
	case LangCPP, LangC:
		return extractCCppIncludes(root, content)
	case LangCSharp:
		return extractCSharpImports(root, content)
	case LangKotlin:
		return extractKotlinImports(root, content)
	case LangPHP:
		return extractPHPImports(root, content)
	case LangRuby:
		return extractRubyImports(root, content)
	default:
		return nil
	}
}

// FindFunctionAt returns the innermost function/method symbol that encloses
// the given 0-based line. Returns nil if no enclosing function is found.
func FindFunctionAt(symbols []Symbol, line int) *Symbol {
	for i := range symbols {
		s := &symbols[i]
		if (s.Kind == KindFunction || s.Kind == KindMethod) &&
			line >= s.StartLine && line <= s.EndLine {
			return s
		}
	}
	return nil
}

// ── Go ─────────────────────────────────────────────────────────────────────

func extractGoSymbols(root *tree_sitter.Node, content []byte) []Symbol {
	var syms []Symbol
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(uint(i))
		if child == nil {
			continue
		}
		kind := child.GrammarName()
		switch kind {
		case "function_declaration":
			syms = append(syms, goFuncSymbol(child, content, ""))
		case "method_declaration":
			receiver := ""
			if recv := child.ChildByFieldName("receiver"); recv != nil {
				receiver = nodeText(recv, content)
			}
			syms = append(syms, goFuncSymbol(child, content, receiver))
		case "type_declaration":
			for j := 0; j < int(child.ChildCount()); j++ {
				spec := child.Child(uint(j))
				if spec != nil && spec.GrammarName() == "type_spec" {
					ts := goTypeSymbol(spec, content)
					syms = append(syms, ts)
					if ts.Kind == KindInterface {
						syms = append(syms, goInterfaceMethods(spec, content, ts.Name)...)
					}
				}
			}
		}
	}
	return syms
}

func goFuncSymbol(node *tree_sitter.Node, content []byte, receiver string) Symbol {
	name := ""
	if n := node.ChildByFieldName("name"); n != nil {
		name = nodeText(n, content)
	}
	kind := KindFunction
	if receiver != "" {
		kind = KindMethod
	}
	return Symbol{
		Kind:      kind,
		Name:      name,
		Signature: firstLine(nodeText(node, content)),
		Parent:    cleanReceiver(receiver),
		StartLine: int(node.StartPosition().Row),
		EndLine:   int(node.EndPosition().Row),
		StartByte: uint(node.StartByte()),
		EndByte:   uint(node.EndByte()),
	}
}

func goTypeSymbol(node *tree_sitter.Node, content []byte) Symbol {
	name := ""
	if n := node.ChildByFieldName("name"); n != nil {
		name = nodeText(n, content)
	}
	k := KindType
	if typeNode := node.ChildByFieldName("type"); typeNode != nil {
		if typeNode.GrammarName() == "interface_type" {
			k = KindInterface
		}
	}
	return Symbol{
		Kind:      k,
		Name:      name,
		Signature: firstLine(nodeText(node, content)),
		StartLine: int(node.StartPosition().Row),
		EndLine:   int(node.EndPosition().Row),
		StartByte: uint(node.StartByte()),
		EndByte:   uint(node.EndByte()),
	}
}

// goInterfaceMethods extracts method signatures declared inside a Go interface_type node.
// Each method becomes a Symbol with Kind=KindMethod and Parent=interfaceName.
func goInterfaceMethods(typeSpec *tree_sitter.Node, content []byte, interfaceName string) []Symbol {
	typeNode := typeSpec.ChildByFieldName("type")
	if typeNode == nil || typeNode.GrammarName() != "interface_type" {
		return nil
	}

	var methods []Symbol
	for i := 0; i < int(typeNode.ChildCount()); i++ {
		child := typeNode.Child(uint(i))
		if child == nil {
			continue
		}
		switch child.GrammarName() {
		case "method_elem", "method_spec":
			mName := ""
			if n := child.ChildByFieldName("name"); n != nil {
				mName = nodeText(n, content)
			}
			if mName == "" {
				for j := 0; j < int(child.ChildCount()); j++ {
					c := child.Child(uint(j))
					if c != nil && c.GrammarName() == "field_identifier" {
						mName = nodeText(c, content)
						break
					}
				}
			}
			if mName != "" {
				methods = append(methods, Symbol{
					Kind:      KindMethod,
					Name:      mName,
					Parent:    interfaceName,
					Signature: strings.TrimSpace(nodeText(child, content)),
					StartLine: int(child.StartPosition().Row),
					EndLine:   int(child.EndPosition().Row),
					StartByte: uint(child.StartByte()),
					EndByte:   uint(child.EndByte()),
					Exported:  len(mName) > 0 && unicode.IsUpper(rune(mName[0])),
				})
			}
		}
	}
	return methods
}

func extractGoImports(root *tree_sitter.Node, content []byte) []ImportEntry {
	var imports []ImportEntry
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(uint(i))
		if child == nil {
			continue
		}
		if child.GrammarName() == "import_declaration" {
			for j := 0; j < int(child.ChildCount()); j++ {
				spec := child.Child(uint(j))
				if spec == nil {
					continue
				}
				if spec.GrammarName() == "import_spec" {
					imports = append(imports, goImportSpec(spec, content))
				} else if spec.GrammarName() == "import_spec_list" {
					for k := 0; k < int(spec.ChildCount()); k++ {
						s := spec.Child(uint(k))
						if s != nil && s.GrammarName() == "import_spec" {
							imports = append(imports, goImportSpec(s, content))
						}
					}
				}
			}
		}
	}
	return imports
}

func goImportSpec(node *tree_sitter.Node, content []byte) ImportEntry {
	entry := ImportEntry{
		StartLine: int(node.StartPosition().Row),
		EndLine:   int(node.EndPosition().Row),
	}
	if name := node.ChildByFieldName("name"); name != nil {
		entry.Alias = nodeText(name, content)
	}
	if path := node.ChildByFieldName("path"); path != nil {
		entry.Path = strings.Trim(nodeText(path, content), "\"")
	}
	return entry
}

// ── TypeScript / JavaScript ─────────────────────────────────────────────────

func extractTSSymbols(root *tree_sitter.Node, content []byte) []Symbol {
	var syms []Symbol
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(uint(i))
		if child == nil {
			continue
		}
		kind := child.GrammarName()
		switch kind {
		case "function_declaration":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			syms = append(syms, Symbol{
				Kind:      KindFunction,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "class_declaration":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			syms = append(syms, Symbol{
				Kind:      KindClass,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "interface_declaration":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			syms = append(syms, Symbol{
				Kind:      KindInterface,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "lexical_declaration":
			extractTSLexicalDecl(child, content, &syms)
		case "export_statement":
			before := len(syms)
			extractTSExport(child, content, &syms)
			for j := before; j < len(syms); j++ {
				syms[j].Exported = true
				syms[j].Visibility = "public"
			}
		}
	}
	return syms
}

func extractTSLexicalDecl(node *tree_sitter.Node, content []byte, syms *[]Symbol) {
	for i := 0; i < int(node.ChildCount()); i++ {
		decl := node.Child(uint(i))
		if decl == nil || decl.GrammarName() != "variable_declarator" {
			continue
		}
		name := ""
		if n := decl.ChildByFieldName("name"); n != nil {
			name = nodeText(n, content)
		}
		k := KindVar
		if nodeText(node.Child(0), content) == "const" {
			k = KindConst
		}
		isFunc := false
		if val := decl.ChildByFieldName("value"); val != nil {
			vk := val.GrammarName()
			if vk == "arrow_function" || vk == "function_expression" || vk == "function" {
				isFunc = true
			}
		}
		if isFunc {
			k = KindFunction
		}
		*syms = append(*syms, Symbol{
			Kind:      k,
			Name:      name,
			Signature: firstLine(nodeText(node, content)),
			StartLine: int(node.StartPosition().Row),
			EndLine:   int(node.EndPosition().Row),
			StartByte: uint(node.StartByte()),
			EndByte:   uint(node.EndByte()),
		})
	}
}

func extractTSExport(node *tree_sitter.Node, content []byte, syms *[]Symbol) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child == nil {
			continue
		}
		switch child.GrammarName() {
		case "function_declaration":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			*syms = append(*syms, Symbol{
				Kind:      KindFunction,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "class_declaration":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			*syms = append(*syms, Symbol{
				Kind:      KindClass,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "interface_declaration":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			*syms = append(*syms, Symbol{
				Kind:      KindInterface,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "lexical_declaration":
			extractTSLexicalDecl(child, content, syms)
		}
	}
}

func extractTSImports(root *tree_sitter.Node, content []byte) []ImportEntry {
	var imports []ImportEntry
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(uint(i))
		if child == nil || child.GrammarName() != "import_statement" {
			continue
		}
		path := ""
		if src := child.ChildByFieldName("source"); src != nil {
			path = strings.Trim(nodeText(src, content), "\"'`")
		}
		imports = append(imports, ImportEntry{
			Path:      path,
			StartLine: int(child.StartPosition().Row),
			EndLine:   int(child.EndPosition().Row),
		})
	}
	return imports
}

// ── Python ──────────────────────────────────────────────────────────────────

func extractPythonSymbols(root *tree_sitter.Node, content []byte) []Symbol {
	var syms []Symbol
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(uint(i))
		if child == nil {
			continue
		}
		kind := child.GrammarName()
		switch kind {
		case "function_definition":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			syms = append(syms, Symbol{
				Kind:      KindFunction,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "class_definition":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			syms = append(syms, Symbol{
				Kind:      KindClass,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		}
	}
	return syms
}

func extractPythonImports(root *tree_sitter.Node, content []byte) []ImportEntry {
	var imports []ImportEntry
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(uint(i))
		if child == nil {
			continue
		}
		gn := child.GrammarName()
		if gn == "import_statement" || gn == "import_from_statement" {
			imports = append(imports, ImportEntry{
				Path:      strings.TrimSpace(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
			})
		}
	}
	return imports
}

// ExtractCallNames extracts function/method names called within a code fragment.
// Returns a deduplicated list of callee names (minimum 2 chars).
func ExtractCallNames(lang Lang, ts *ParserPool, content []byte) []string {
	return ExtractCallNamesWithConfig(lang, ts, content, nil)
}

// ExtractCallNamesWithConfig extracts call names using a language-specific
// configuration from the langs registry. When cfg is nil, falls back to
// hardcoded AST node matching (WIRE-06).
func ExtractCallNamesWithConfig(lang Lang, ts *ParserPool, content []byte, cfg *CallNodeConfig) []string {
	if ts == nil {
		return nil
	}
	tree, err := ts.Parse(lang, content, nil)
	if err != nil {
		return nil
	}
	defer tree.Close()

	seen := make(map[string]struct{})
	var names []string

	WalkCallExpressionsWithConfig(tree.RootNode(), content, cfg, func(name string) {
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	})

	return names
}

// CallNodeConfig describes how to extract calls from AST nodes for a language.
// Passed from the langs registry to avoid hardcoding node types.
type CallNodeConfig struct {
	FuncCallNodeType string // e.g. "call_expression", "call", "method_invocation"
	FuncField        string // e.g. "function", "name"
	MethodNodeType   string // may be same as FuncCallNodeType
	ReceiverField    string // e.g. "operand", "object", "value"
	MethodField      string // e.g. "field", "property", "attribute", "name"
}

// WalkCallExpressionsWithConfig uses registry-driven config for call extraction.
// Falls back to hardcoded logic for nil config.
func WalkCallExpressionsWithConfig(node *tree_sitter.Node, content []byte, cfg *CallNodeConfig, emit func(string)) {
	if cfg == nil {
		walkCallExpressions(node, content, emit)
		return
	}
	walkCallExpressionsConfig(node, content, cfg, emit)
}

func walkCallExpressionsConfig(node *tree_sitter.Node, content []byte, cfg *CallNodeConfig, emit func(string)) {
	if node == nil {
		return
	}

	grammar := node.GrammarName()
	if grammar == cfg.FuncCallNodeType || (cfg.MethodNodeType != "" && grammar == cfg.MethodNodeType) {
		name := extractCallNameConfig(node, content, cfg)
		if name != "" && len(name) >= 2 {
			emit(name)
		}
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child != nil {
			walkCallExpressionsConfig(child, content, cfg, emit)
		}
	}
}

func extractCallNameConfig(node *tree_sitter.Node, content []byte, cfg *CallNodeConfig) string {
	// Try the function/name field first
	if cfg.FuncField != "" {
		if fn := node.ChildByFieldName(cfg.FuncField); fn != nil {
			fnGrammar := fn.GrammarName()
			// If the function node is a simple identifier, use it directly
			if fnGrammar == "identifier" {
				return nodeText(fn, content)
			}
			// If it's a member/selector/attribute expression, extract the method name
			if cfg.MethodField != "" {
				if method := fn.ChildByFieldName(cfg.MethodField); method != nil {
					return nodeText(method, content)
				}
			}
			// Fallback: use the full hardcoded extractCallName
			return extractCallName(fn, content)
		}
	}
	return ""
}

func walkCallExpressions(node *tree_sitter.Node, content []byte, emit func(string)) {
	if node == nil {
		return
	}

	grammar := node.GrammarName()
	switch grammar {
	case "call_expression":
		if fn := node.ChildByFieldName("function"); fn != nil {
			name := extractCallName(fn, content)
			if name != "" && len(name) >= 2 {
				emit(name)
			}
		}
	case "call":
		if fn := node.ChildByFieldName("function"); fn != nil {
			name := extractCallName(fn, content)
			if name != "" && len(name) >= 2 {
				emit(name)
			}
		}
	case "method_invocation":
		if nameNode := node.ChildByFieldName("name"); nameNode != nil {
			name := nodeText(nameNode, content)
			if name != "" && len(name) >= 2 {
				emit(name)
			}
		}
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child != nil {
			walkCallExpressions(child, content, emit)
		}
	}
}

// ExtractCallSites extracts function/method calls with full position info,
// preserving receiver expressions for LSP-based type disambiguation.
func ExtractCallSites(lang Lang, ts *ParserPool, content []byte) []CallSite {
	if ts == nil {
		return nil
	}
	tree, err := ts.Parse(lang, content, nil)
	if err != nil {
		return nil
	}
	defer tree.Close()

	seen := make(map[string]struct{})
	var sites []CallSite

	walkCallSites(tree.RootNode(), content, func(cs CallSite) {
		key := cs.Receiver + "." + cs.Name
		if cs.Receiver == "" {
			key = cs.Name
		}
		if _, ok := seen[key]; ok {
			return
		}
		if len(cs.Name) < 2 {
			return
		}
		seen[key] = struct{}{}
		sites = append(sites, cs)
	})

	return sites
}

func walkCallSites(node *tree_sitter.Node, content []byte, emit func(CallSite)) {
	if node == nil {
		return
	}

	grammar := node.GrammarName()
	switch grammar {
	case "call_expression":
		if fn := node.ChildByFieldName("function"); fn != nil {
			cs := extractCallSite(fn, content)
			if cs.Name != "" {
				emit(cs)
			}
		}
	case "call":
		// Python
		if fn := node.ChildByFieldName("function"); fn != nil {
			cs := extractCallSite(fn, content)
			if cs.Name != "" {
				emit(cs)
			}
		}
	case "method_invocation":
		// Java: object.method(args)
		nameNode := node.ChildByFieldName("name")
		objNode := node.ChildByFieldName("object")
		if nameNode != nil {
			receiver := ""
			if objNode != nil {
				receiver = nodeText(objNode, content)
			}
			emit(CallSite{
				Name:     nodeText(nameNode, content),
				Receiver: receiver,
				Line:     int(nameNode.StartPosition().Row),
				Col:      int(nameNode.StartPosition().Column),
				IsMethod: true,
			})
		}
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child != nil {
			walkCallSites(child, content, emit)
		}
	}
}

func extractCallSite(node *tree_sitter.Node, content []byte) CallSite {
	switch node.GrammarName() {
	case "identifier":
		return CallSite{
			Name: nodeText(node, content),
			Line: int(node.StartPosition().Row),
			Col:  int(node.StartPosition().Column),
		}
	case "selector_expression":
		// Go: pkg.Func() or obj.Method()
		field := node.ChildByFieldName("field")
		operand := node.ChildByFieldName("operand")
		if field == nil {
			return CallSite{}
		}
		receiver := ""
		if operand != nil {
			receiver = nodeText(operand, content)
		}
		return CallSite{
			Name:     nodeText(field, content),
			Receiver: receiver,
			Line:     int(field.StartPosition().Row),
			Col:      int(field.StartPosition().Column),
			IsMethod: true,
		}
	case "member_expression":
		// TypeScript/JavaScript: obj.method()
		prop := node.ChildByFieldName("property")
		obj := node.ChildByFieldName("object")
		if prop == nil {
			return CallSite{}
		}
		receiver := ""
		if obj != nil {
			receiver = nodeText(obj, content)
		}
		return CallSite{
			Name:     nodeText(prop, content),
			Receiver: receiver,
			Line:     int(prop.StartPosition().Row),
			Col:      int(prop.StartPosition().Column),
			IsMethod: true,
		}
	case "attribute":
		// Python: obj.method()
		attr := node.ChildByFieldName("attribute")
		obj := node.ChildByFieldName("object")
		if attr == nil {
			return CallSite{}
		}
		receiver := ""
		if obj != nil {
			receiver = nodeText(obj, content)
		}
		return CallSite{
			Name:     nodeText(attr, content),
			Receiver: receiver,
			Line:     int(attr.StartPosition().Row),
			Col:      int(attr.StartPosition().Column),
			IsMethod: true,
		}
	case "field_expression":
		// Rust: obj.method()
		field := node.ChildByFieldName("field")
		value := node.ChildByFieldName("value")
		if field == nil {
			return CallSite{}
		}
		receiver := ""
		if value != nil {
			receiver = nodeText(value, content)
		}
		return CallSite{
			Name:     nodeText(field, content),
			Receiver: receiver,
			Line:     int(field.StartPosition().Row),
			Col:      int(field.StartPosition().Column),
			IsMethod: true,
		}
	case "field_identifier":
		return CallSite{
			Name: nodeText(node, content),
			Line: int(node.StartPosition().Row),
			Col:  int(node.StartPosition().Column),
		}
	}
	return CallSite{}
}

func extractCallName(node *tree_sitter.Node, content []byte) string {
	switch node.GrammarName() {
	case "identifier":
		return nodeText(node, content)
	case "selector_expression":
		// Go: obj.Method or pkg.Func → "Receiver.Method" for type disambiguation.
		if field := node.ChildByFieldName("field"); field != nil {
			methodName := nodeText(field, content)
			if operand := node.ChildByFieldName("operand"); operand != nil {
				recvName := extractLastSelector(operand, content)
				if qualifiedMethodName := qualifyWithReceiver(recvName, methodName); qualifiedMethodName != "" {
					return qualifiedMethodName
				}
			}
			return methodName
		}
	case "member_expression":
		// TypeScript/JavaScript: obj.method → "Type.method"
		if prop := node.ChildByFieldName("property"); prop != nil {
			methodName := nodeText(prop, content)
			if obj := node.ChildByFieldName("object"); obj != nil {
				recvName := extractLastSelector(obj, content)
				if qualifiedMethodName := qualifyWithReceiver(recvName, methodName); qualifiedMethodName != "" {
					return qualifiedMethodName
				}
			}
			return methodName
		}
	case "attribute":
		// Python: obj.method → "Type.method"
		if attr := node.ChildByFieldName("attribute"); attr != nil {
			methodName := nodeText(attr, content)
			if obj := node.ChildByFieldName("object"); obj != nil {
				recvName := extractLastSelector(obj, content)
				if qualifiedMethodName := qualifyWithReceiver(recvName, methodName); qualifiedMethodName != "" {
					return qualifiedMethodName
				}
			}
			return methodName
		}
	case "field_expression":
		// Rust: obj.method → "Type.method"
		if field := node.ChildByFieldName("field"); field != nil {
			methodName := nodeText(field, content)
			if value := node.ChildByFieldName("value"); value != nil {
				recvName := extractLastSelector(value, content)
				if qualifiedMethodName := qualifyWithReceiver(recvName, methodName); qualifiedMethodName != "" {
					return qualifiedMethodName
				}
			}
			return methodName
		}
	case "method_invocation":
		// Java: obj.method(args) → "Type.method"
		if nameNode := node.ChildByFieldName("name"); nameNode != nil {
			methodName := nodeText(nameNode, content)
			if obj := node.ChildByFieldName("object"); obj != nil {
				recvName := extractLastSelector(obj, content)
				if qualifiedMethodName := qualifyWithReceiver(recvName, methodName); qualifiedMethodName != "" {
					return qualifiedMethodName
				}
			}
			return methodName
		}
	case "field_identifier":
		return nodeText(node, content)
	}
	return ""
}

// qualifyWithReceiver produces "Receiver.method" from a receiver variable name and method name.
// Returns empty string if receiver should not be used for qualification.
// Rules:
//   - Skip language keywords: this, self, super, cls, mSelf
//   - Skip single-char variables (too ambiguous)
//   - Preserve the ORIGINAL receiver spelling. No capitalization guessing:
//     a lowercase receiver is a local variable (`svc.CreateUser`) or a
//     package name (`fmt.Errorf`) whose owning type cannot be statically
//     resolved here — inventing "Svc.CreateUser" produces a phantom
//     qualified name that matches nothing and silently breaks the call
//     edge. The resolver side falls back from qualified → package-path →
//     bare-name matching instead (INV-CKG-EDGE-01).
func qualifyWithReceiver(recvName, methodName string) string {
	if recvName == "" || len(recvName) < 2 {
		return ""
	}
	switch recvName {
	case "this", "self", "super", "cls", "mSelf", "it":
		return ""
	}
	return recvName + "." + methodName
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func nodeText(n *tree_sitter.Node, content []byte) string {
	if n == nil {
		return ""
	}
	start := n.StartByte()
	end := n.EndByte()
	if int(end) > len(content) {
		end = uint(len(content))
	}
	return string(content[start:end])
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}

// extractLastSelector extracts the last identifier from a chain expression.
// Language-agnostic: supports Go selector_expression, TS member_expression,
// Python attribute, Rust field_expression, Java method_invocation receiver.
func extractLastSelector(node *tree_sitter.Node, content []byte) string {
	switch node.GrammarName() {
	case "identifier", "type_identifier", "constant":
		return nodeText(node, content)
	case "selector_expression":
		if field := node.ChildByFieldName("field"); field != nil {
			return nodeText(field, content)
		}
	case "member_expression":
		if prop := node.ChildByFieldName("property"); prop != nil {
			return nodeText(prop, content)
		}
	case "attribute":
		if attr := node.ChildByFieldName("attribute"); attr != nil {
			return nodeText(attr, content)
		}
	case "field_expression":
		if field := node.ChildByFieldName("field"); field != nil {
			return nodeText(field, content)
		}
	case "field_access":
		if field := node.ChildByFieldName("field"); field != nil {
			return nodeText(field, content)
		}
	}
	return ""
}

// ── Rust ────────────────────────────────────────────────────────────────────

func extractRustSymbols(root *tree_sitter.Node, content []byte) []Symbol {
	var syms []Symbol
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(uint(i))
		if child == nil {
			continue
		}
		kind := child.GrammarName()
		switch kind {
		case "function_item":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			syms = append(syms, Symbol{
				Kind:      KindFunction,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "struct_item":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			syms = append(syms, Symbol{
				Kind:      KindType,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "trait_item":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			syms = append(syms, Symbol{
				Kind:      KindInterface,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "impl_item":
			name := ""
			if n := child.ChildByFieldName("type"); n != nil {
				name = nodeText(n, content)
			}
			// Extract methods inside impl blocks
			if body := child.ChildByFieldName("body"); body != nil {
				for j := 0; j < int(body.ChildCount()); j++ {
					member := body.Child(uint(j))
					if member != nil && member.GrammarName() == "function_item" {
						mName := ""
						if n := member.ChildByFieldName("name"); n != nil {
							mName = nodeText(n, content)
						}
						syms = append(syms, Symbol{
							Kind:      KindMethod,
							Name:      mName,
							Parent:    name,
							Signature: firstLine(nodeText(member, content)),
							StartLine: int(member.StartPosition().Row),
							EndLine:   int(member.EndPosition().Row),
							StartByte: uint(member.StartByte()),
							EndByte:   uint(member.EndByte()),
						})
					}
				}
			}
		case "enum_item":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			syms = append(syms, Symbol{
				Kind:      KindType,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		}
	}
	return syms
}

func extractRustImports(root *tree_sitter.Node, content []byte) []ImportEntry {
	var imports []ImportEntry
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(uint(i))
		if child == nil || child.GrammarName() != "use_declaration" {
			continue
		}
		imports = append(imports, ImportEntry{
			Path:      strings.TrimSuffix(strings.TrimSpace(nodeText(child, content)), ";"),
			StartLine: int(child.StartPosition().Row),
			EndLine:   int(child.EndPosition().Row),
		})
	}
	return imports
}

// ── Java ────────────────────────────────────────────────────────────────────

func extractJavaSymbols(root *tree_sitter.Node, content []byte) []Symbol {
	var syms []Symbol
	// Java top level is typically program > class_declaration
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(uint(i))
		if child == nil {
			continue
		}
		kind := child.GrammarName()
		switch kind {
		case "class_declaration":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			syms = append(syms, Symbol{
				Kind:      KindClass,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
			// Extract methods inside class body
			if body := child.ChildByFieldName("body"); body != nil {
				extractJavaClassBody(body, content, name, &syms)
			}
		case "interface_declaration":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			syms = append(syms, Symbol{
				Kind:      KindInterface,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
			if body := child.ChildByFieldName("body"); body != nil {
				extractJavaClassBody(body, content, name, &syms)
			}
		}
	}
	return syms
}

func extractJavaClassBody(body *tree_sitter.Node, content []byte, parent string, syms *[]Symbol) {
	for j := 0; j < int(body.ChildCount()); j++ {
		member := body.Child(uint(j))
		if member == nil {
			continue
		}
		if member.GrammarName() == "method_declaration" || member.GrammarName() == "constructor_declaration" {
			mName := ""
			if n := member.ChildByFieldName("name"); n != nil {
				mName = nodeText(n, content)
			}
			*syms = append(*syms, Symbol{
				Kind:      KindMethod,
				Name:      mName,
				Parent:    parent,
				Signature: firstLine(nodeText(member, content)),
				StartLine: int(member.StartPosition().Row),
				EndLine:   int(member.EndPosition().Row),
				StartByte: uint(member.StartByte()),
				EndByte:   uint(member.EndByte()),
			})
		}
	}
}

func extractJavaImports(root *tree_sitter.Node, content []byte) []ImportEntry {
	var imports []ImportEntry
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(uint(i))
		if child == nil || child.GrammarName() != "import_declaration" {
			continue
		}
		imports = append(imports, ImportEntry{
			Path:      strings.TrimSuffix(strings.TrimSpace(nodeText(child, content)), ";"),
			StartLine: int(child.StartPosition().Row),
			EndLine:   int(child.EndPosition().Row),
		})
	}
	return imports
}

// ExtractVarFlow walks a function body AST and extracts variable definition,
// usage, and return points for data flow analysis (INV-DF-01: function scope only).
func ExtractVarFlow(lang Lang, ts *ParserPool, content []byte) []VarFlowEntry {
	if ts == nil || len(content) == 0 {
		return nil
	}
	tree, err := ts.Parse(lang, content, nil)
	if err != nil {
		return nil
	}
	defer tree.Close()

	var entries []VarFlowEntry
	walkVarFlow(tree.RootNode(), content, func(e VarFlowEntry) {
		entries = append(entries, e)
	})
	return entries
}

func walkVarFlow(node *tree_sitter.Node, content []byte, emit func(VarFlowEntry)) {
	if node == nil {
		return
	}
	grammar := node.GrammarName()

	switch grammar {
	// Go: short variable declaration — result := expr
	case "short_var_declaration":
		if left := node.ChildByFieldName("left"); left != nil {
			rhs := ""
			if right := node.ChildByFieldName("right"); right != nil {
				rhs = truncExpr(nodeText(right, content), 80)
			}
			for i := 0; i < int(left.ChildCount()); i++ {
				if id := left.Child(uint(i)); id != nil && id.GrammarName() == "identifier" {
					emit(VarFlowEntry{
						Name:    nodeText(id, content),
						Line:    int(id.StartPosition().Row),
						Col:     int(id.StartPosition().Column),
						Kind:    VarFlowDef,
						RHSExpr: rhs,
					})
				}
			}
		}

	// Go/Python: assignment_statement — x = expr
	case "assignment_statement", "assignment_expression":
		if left := node.ChildByFieldName("left"); left != nil {
			rhs := ""
			if right := node.ChildByFieldName("right"); right != nil {
				rhs = truncExpr(nodeText(right, content), 80)
			}
			name := nodeText(left, content)
			if name != "" && len(name) < 60 {
				emit(VarFlowEntry{
					Name:    name,
					Line:    int(left.StartPosition().Row),
					Col:     int(left.StartPosition().Column),
					Kind:    VarFlowDefUse,
					RHSExpr: rhs,
				})
			}
		}

	// TS/JS: variable_declarator within lexical_declaration/variable_declaration
	case "variable_declarator":
		if nameNode := node.ChildByFieldName("name"); nameNode != nil {
			rhs := ""
			if val := node.ChildByFieldName("value"); val != nil {
				rhs = truncExpr(nodeText(val, content), 80)
			}
			emit(VarFlowEntry{
				Name:    nodeText(nameNode, content),
				Line:    int(nameNode.StartPosition().Row),
				Col:     int(nameNode.StartPosition().Column),
				Kind:    VarFlowDef,
				RHSExpr: rhs,
			})
		}

	// return_statement — return expr
	case "return_statement":
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(uint(i))
			if child == nil {
				continue
			}
			cg := child.GrammarName()
			if cg == "identifier" {
				emit(VarFlowEntry{
					Name: nodeText(child, content),
					Line: int(child.StartPosition().Row),
					Col:  int(child.StartPosition().Column),
					Kind: VarFlowReturn,
				})
			} else if cg == "expression_list" {
				for j := 0; j < int(child.ChildCount()); j++ {
					expr := child.Child(uint(j))
					if expr != nil && expr.GrammarName() == "identifier" {
						emit(VarFlowEntry{
							Name: nodeText(expr, content),
							Line: int(expr.StartPosition().Row),
							Col:  int(expr.StartPosition().Column),
							Kind: VarFlowReturn,
						})
					}
				}
			}
		}

	// parameter_list / formal_parameters — function parameters
	case "parameter_list", "formal_parameters", "parameters":
		for i := 0; i < int(node.ChildCount()); i++ {
			param := node.Child(uint(i))
			if param == nil {
				continue
			}
			pg := param.GrammarName()
			if pg == "parameter_declaration" || pg == "required_parameter" || pg == "typed_parameter" {
				if nameNode := param.ChildByFieldName("name"); nameNode != nil {
					emit(VarFlowEntry{
						Name: nodeText(nameNode, content),
						Line: int(nameNode.StartPosition().Row),
						Col:  int(nameNode.StartPosition().Column),
						Kind: VarFlowParam,
					})
				}
			} else if pg == "identifier" {
				emit(VarFlowEntry{
					Name: nodeText(param, content),
					Line: int(param.StartPosition().Row),
					Col:  int(param.StartPosition().Column),
					Kind: VarFlowParam,
				})
			}
		}
	}

	// Recurse into children
	for i := 0; i < int(node.ChildCount()); i++ {
		if child := node.Child(uint(i)); child != nil {
			walkVarFlow(child, content, emit)
		}
	}
}

// ExtractControlFlow walks a function body AST and extracts control flow nodes
// (if/for/switch/return/defer/panic/try/catch) with nesting depth and conditions.
func ExtractControlFlow(lang Lang, ts *ParserPool, content []byte) []ControlFlowEntry {
	if ts == nil || len(content) == 0 {
		return nil
	}
	tree, err := ts.Parse(lang, content, nil)
	if err != nil {
		return nil
	}
	defer tree.Close()

	var entries []ControlFlowEntry
	walkControlFlow(tree.RootNode(), content, 0, func(e ControlFlowEntry) {
		entries = append(entries, e)
	})
	return entries
}

func walkControlFlow(node *tree_sitter.Node, content []byte, depth int, emit func(ControlFlowEntry)) {
	if node == nil {
		return
	}
	grammar := node.GrammarName()
	emitted := false

	switch grammar {
	case "if_statement", "if_expression":
		cond := ""
		if c := node.ChildByFieldName("condition"); c != nil {
			cond = truncExpr(nodeText(c, content), 80)
		}
		isErr := isErrorCheck(cond)
		emit(ControlFlowEntry{Kind: CFNodeIf, Line: int(node.StartPosition().Row), Col: int(node.StartPosition().Column), Depth: depth, Condition: cond, IsError: isErr})
		emitted = true

	case "else_clause":
		first := firstNamedChild(node)
		if first != nil && (first.GrammarName() == "if_statement" || first.GrammarName() == "if_expression") {
			emit(ControlFlowEntry{Kind: CFNodeElseIf, Line: int(node.StartPosition().Row), Col: int(node.StartPosition().Column), Depth: depth})
		} else {
			emit(ControlFlowEntry{Kind: CFNodeElse, Line: int(node.StartPosition().Row), Col: int(node.StartPosition().Column), Depth: depth})
		}
		emitted = true

	case "for_statement", "for_expression", "while_statement", "for_in_statement", "for_of_statement":
		cond := ""
		if c := node.ChildByFieldName("condition"); c != nil {
			cond = truncExpr(nodeText(c, content), 80)
		} else if c := node.ChildByFieldName("value"); c != nil {
			cond = "range " + truncExpr(nodeText(c, content), 60)
		}
		emit(ControlFlowEntry{Kind: CFNodeFor, Line: int(node.StartPosition().Row), Col: int(node.StartPosition().Column), Depth: depth, Condition: cond})
		emitted = true

	case "expression_switch_statement", "type_switch_statement", "switch_statement", "match_expression", "match_statement":
		cond := ""
		if c := node.ChildByFieldName("value"); c != nil {
			cond = truncExpr(nodeText(c, content), 80)
		}
		emit(ControlFlowEntry{Kind: CFNodeSwitch, Line: int(node.StartPosition().Row), Col: int(node.StartPosition().Column), Depth: depth, Condition: cond})
		emitted = true

	case "expression_case", "type_case", "case_clause", "match_arm":
		emit(ControlFlowEntry{Kind: CFNodeCase, Line: int(node.StartPosition().Row), Col: int(node.StartPosition().Column), Depth: depth})

	case "default_case":
		emit(ControlFlowEntry{Kind: CFNodeDefault, Line: int(node.StartPosition().Row), Col: int(node.StartPosition().Column), Depth: depth})

	case "return_statement":
		emit(ControlFlowEntry{Kind: CFNodeReturn, Line: int(node.StartPosition().Row), Col: int(node.StartPosition().Column), Depth: depth})

	case "defer_statement":
		emit(ControlFlowEntry{Kind: CFNodeDefer, Line: int(node.StartPosition().Row), Col: int(node.StartPosition().Column), Depth: depth})

	case "go_statement":
		// Go goroutine launch — not a traditional CFG node but useful context
	case "select_statement":
		emit(ControlFlowEntry{Kind: CFNodeSelect, Line: int(node.StartPosition().Row), Col: int(node.StartPosition().Column), Depth: depth})
		emitted = true

	case "try_statement":
		emit(ControlFlowEntry{Kind: CFNodeTry, Line: int(node.StartPosition().Row), Col: int(node.StartPosition().Column), Depth: depth})
		emitted = true

	case "catch_clause", "except_clause", "rescue":
		emit(ControlFlowEntry{Kind: CFNodeCatch, Line: int(node.StartPosition().Row), Col: int(node.StartPosition().Column), Depth: depth})

	case "finally_clause":
		emit(ControlFlowEntry{Kind: CFNodeFinally, Line: int(node.StartPosition().Row), Col: int(node.StartPosition().Column), Depth: depth})

	case "break_statement":
		emit(ControlFlowEntry{Kind: CFNodeBreak, Line: int(node.StartPosition().Row), Col: int(node.StartPosition().Column), Depth: depth})

	case "continue_statement":
		emit(ControlFlowEntry{Kind: CFNodeContinue, Line: int(node.StartPosition().Row), Col: int(node.StartPosition().Column), Depth: depth})
	}

	// Detect panic() calls
	if grammar == "call_expression" {
		if fn := node.ChildByFieldName("function"); fn != nil {
			name := nodeText(fn, content)
			if name == "panic" || name == "os.Exit" || name == "log.Fatal" || name == "log.Fatalf" {
				emit(ControlFlowEntry{Kind: CFNodePanic, Line: int(node.StartPosition().Row), Col: int(node.StartPosition().Column), Depth: depth})
			}
		}
	}

	childDepth := depth
	if emitted {
		childDepth = depth + 1
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		if child := node.Child(uint(i)); child != nil {
			walkControlFlow(child, content, childDepth, emit)
		}
	}
}

func firstNamedChild(n *tree_sitter.Node) *tree_sitter.Node {
	for i := 0; i < int(n.ChildCount()); i++ {
		if c := n.Child(uint(i)); c != nil && c.IsNamed() {
			return c
		}
	}
	return nil
}

func isErrorCheck(cond string) bool {
	return strings.Contains(cond, "err != nil") ||
		strings.Contains(cond, "err !=nil") ||
		strings.Contains(cond, "error") ||
		strings.Contains(cond, "Error") ||
		strings.Contains(cond, "catch") ||
		strings.Contains(cond, "except")
}

// ExtractRoutes extracts HTTP route registrations from backend code.
// Supports Go (net/http, gin, chi, echo), Express.js, Flask, and FastAPI patterns.
func ExtractRoutes(lang Lang, ts *ParserPool, content []byte, filePath string) []RouteEntry {
	if ts == nil || len(content) == 0 {
		return nil
	}
	tree, err := ts.Parse(lang, content, nil)
	if err != nil {
		return nil
	}
	defer tree.Close()

	var routes []RouteEntry
	walkRoutes(tree.RootNode(), content, filePath, func(r RouteEntry) {
		routes = append(routes, r)
	})
	return routes
}

// httpMethods maps common method names (lowercase) to HTTP methods.
var httpMethods = map[string]string{
	"get": "GET", "post": "POST", "put": "PUT", "delete": "DELETE",
	"patch": "PATCH", "head": "HEAD", "options": "OPTIONS",
	"handlefunc": "*", "handle": "*", "use": "*",
	"route": "*", "any": "*",
}

func walkRoutes(node *tree_sitter.Node, content []byte, filePath string, emit func(RouteEntry)) {
	if node == nil {
		return
	}
	grammar := node.GrammarName()

	// Go/TS/JS: method call pattern — r.GET("/path", handler) or app.get("/path", handler)
	if grammar == "call_expression" {
		if fn := node.ChildByFieldName("function"); fn != nil {
			fnText := nodeText(fn, content)
			// Check for selector pattern: receiver.Method
			if fn.GrammarName() == "selector_expression" || fn.GrammarName() == "member_expression" {
				if field := fn.ChildByFieldName("field"); field == nil {
					if field = fn.ChildByFieldName("property"); field != nil {
						methodName := strings.ToLower(nodeText(field, content))
						if httpMethod, ok := httpMethods[methodName]; ok {
							if route := extractRouteFromCallArgs(node, content, httpMethod, filePath); route != nil {
								emit(*route)
							}
						}
					}
				} else {
					methodName := strings.ToLower(nodeText(field, content))
					if httpMethod, ok := httpMethods[methodName]; ok {
						if route := extractRouteFromCallArgs(node, content, httpMethod, filePath); route != nil {
							emit(*route)
						}
					}
				}
			}
			// Plain function: http.HandleFunc("/path", handler)
			if strings.HasSuffix(fnText, "HandleFunc") || strings.HasSuffix(fnText, "Handle") {
				if route := extractRouteFromCallArgs(node, content, "*", filePath); route != nil {
					emit(*route)
				}
			}
		}
	}

	// Python: decorator pattern — @app.route("/path") or @app.get("/path")
	if grammar == "decorator" {
		decText := nodeText(node, content)
		for method, httpMethod := range httpMethods {
			pattern := "." + method + "("
			if strings.Contains(decText, pattern) || strings.Contains(decText, ".route(") {
				path := extractStringFromDecorator(decText)
				if path != "" {
					if httpMethod == "*" || strings.Contains(decText, ".route(") {
						httpMethod = "*"
					}
					emit(RouteEntry{
						Method:   httpMethod,
						Path:     path,
						Handler:  "", // will be the next function_definition
						FilePath: filePath,
						Line:     int(node.StartPosition().Row),
					})
				}
				break
			}
		}
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		if child := node.Child(uint(i)); child != nil {
			walkRoutes(child, content, filePath, emit)
		}
	}
}

func extractRouteFromCallArgs(callNode *tree_sitter.Node, content []byte, method, filePath string) *RouteEntry {
	args := callNode.ChildByFieldName("arguments")
	if args == nil {
		return nil
	}
	// First string argument = path; second argument = handler
	var path, handler string
	argIdx := 0
	for i := 0; i < int(args.ChildCount()); i++ {
		arg := args.Child(uint(i))
		if arg == nil || !arg.IsNamed() {
			continue
		}
		g := arg.GrammarName()
		if argIdx == 0 && (g == "interpreted_string_literal" || g == "raw_string_literal" || g == "string" || g == "template_string") {
			path = unquote(nodeText(arg, content))
		} else if argIdx == 1 {
			handler = nodeText(arg, content)
		}
		argIdx++
	}
	if path == "" || !strings.HasPrefix(path, "/") {
		return nil
	}
	return &RouteEntry{
		Method:   method,
		Path:     path,
		Handler:  handler,
		FilePath: filePath,
		Line:     int(callNode.StartPosition().Row),
	}
}

func extractStringFromDecorator(text string) string {
	// Extract first quoted string from decorator text
	for _, q := range []byte{'"', '\''} {
		start := strings.IndexByte(text, q)
		if start < 0 {
			continue
		}
		end := strings.IndexByte(text[start+1:], q)
		if end < 0 {
			continue
		}
		return text[start+1 : start+1+end]
	}
	return ""
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '`' && s[len(s)-1] == '`') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// ExtractAPICalls extracts HTTP API calls from frontend/client code.
// Supports fetch(), axios, ky, and Go http.Get/Post patterns.
func ExtractAPICalls(lang Lang, ts *ParserPool, content []byte, filePath string) []APICallEntry {
	if ts == nil || len(content) == 0 {
		return nil
	}
	tree, err := ts.Parse(lang, content, nil)
	if err != nil {
		return nil
	}
	defer tree.Close()

	var calls []APICallEntry
	walkAPICalls(tree.RootNode(), content, filePath, func(c APICallEntry) {
		calls = append(calls, c)
	})
	return calls
}

// apiCallerPatterns maps function/method names to HTTP methods for API call detection.
var apiCallerPatterns = map[string]string{
	"fetch":     "GET", // default; actual method from options
	"axios.get": "GET", "axios.post": "POST", "axios.put": "PUT", "axios.delete": "DELETE", "axios.patch": "PATCH",
	"ky.get": "GET", "ky.post": "POST", "ky.put": "PUT", "ky.delete": "DELETE",
	"http.Get": "GET", "http.Post": "POST", "http.Head": "HEAD",
	"requests.get": "GET", "requests.post": "POST", "requests.put": "PUT", "requests.delete": "DELETE",
}

func walkAPICalls(node *tree_sitter.Node, content []byte, filePath string, emit func(APICallEntry)) {
	if node == nil {
		return
	}

	if node.GrammarName() == "call_expression" || node.GrammarName() == "call" {
		if fn := node.ChildByFieldName("function"); fn != nil {
			fnText := nodeText(fn, content)
			if method, ok := apiCallerPatterns[fnText]; ok {
				url := extractFirstStringArg(node, content)
				if url != "" && (strings.HasPrefix(url, "/") || strings.HasPrefix(url, "http")) {
					emit(APICallEntry{
						Method:   method,
						URL:      normalizeAPIURL(url),
						FilePath: filePath,
						Line:     int(node.StartPosition().Row),
						Col:      int(node.StartPosition().Column),
					})
				}
			}
		}
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		if child := node.Child(uint(i)); child != nil {
			walkAPICalls(child, content, filePath, emit)
		}
	}
}

func extractFirstStringArg(callNode *tree_sitter.Node, content []byte) string {
	args := callNode.ChildByFieldName("arguments")
	if args == nil {
		return ""
	}
	for i := 0; i < int(args.ChildCount()); i++ {
		arg := args.Child(uint(i))
		if arg == nil || !arg.IsNamed() {
			continue
		}
		g := arg.GrammarName()
		if g == "interpreted_string_literal" || g == "raw_string_literal" || g == "string" || g == "template_string" {
			return unquote(nodeText(arg, content))
		}
	}
	return ""
}

func normalizeAPIURL(url string) string {
	// Strip host prefix for matching: "http://localhost:3000/api/users" → "/api/users"
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		idx := strings.Index(url[8:], "/")
		if idx >= 0 {
			return url[8+idx:]
		}
	}
	// Strip query string
	if idx := strings.IndexByte(url, '?'); idx >= 0 {
		url = url[:idx]
	}
	return url
}

// ── C / C++ ─────────────────────────────────────────────────────────────────

func extractCCppSymbols(root *tree_sitter.Node, content []byte) []Symbol {
	var syms []Symbol
	walkCCppSymbols(root, content, "", &syms)
	return syms
}

func walkCCppSymbols(node *tree_sitter.Node, content []byte, parent string, syms *[]Symbol) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child == nil {
			continue
		}
		kind := child.GrammarName()
		switch kind {
		case "function_definition":
			name := ""
			if n := child.ChildByFieldName("declarator"); n != nil {
				name = extractDeclaratorName(n, content)
			}
			k := KindFunction
			if parent != "" {
				k = KindMethod
			}
			*syms = append(*syms, Symbol{
				Kind:      k,
				Name:      name,
				Parent:    parent,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "declaration":
			// Could be a function declaration (prototype) or variable
			if decl := child.ChildByFieldName("declarator"); decl != nil {
				if decl.GrammarName() == "function_declarator" {
					name := extractDeclaratorName(decl, content)
					*syms = append(*syms, Symbol{
						Kind:      KindFunction,
						Name:      name,
						Signature: firstLine(nodeText(child, content)),
						StartLine: int(child.StartPosition().Row),
						EndLine:   int(child.EndPosition().Row),
						StartByte: uint(child.StartByte()),
						EndByte:   uint(child.EndByte()),
					})
				}
			}
		case "struct_specifier":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			*syms = append(*syms, Symbol{
				Kind:      KindType,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "class_specifier":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			*syms = append(*syms, Symbol{
				Kind:      KindClass,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
			if body := child.ChildByFieldName("body"); body != nil {
				walkCCppSymbols(body, content, name, syms)
			}
		case "enum_specifier":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			*syms = append(*syms, Symbol{
				Kind:      KindType,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "namespace_definition":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			if body := child.ChildByFieldName("body"); body != nil {
				walkCCppSymbols(body, content, name, syms)
			}
		case "template_declaration":
			walkCCppSymbols(child, content, parent, syms)
		}
	}
}

func extractDeclaratorName(node *tree_sitter.Node, content []byte) string {
	switch node.GrammarName() {
	case "identifier", "field_identifier", "destructor_name":
		return nodeText(node, content)
	case "qualified_identifier":
		if n := node.ChildByFieldName("name"); n != nil {
			return nodeText(n, content)
		}
	case "function_declarator":
		if n := node.ChildByFieldName("declarator"); n != nil {
			return extractDeclaratorName(n, content)
		}
	case "pointer_declarator", "reference_declarator":
		if n := node.ChildByFieldName("declarator"); n != nil {
			return extractDeclaratorName(n, content)
		}
	}
	return nodeText(node, content)
}

func extractCCppIncludes(root *tree_sitter.Node, content []byte) []ImportEntry {
	var imports []ImportEntry
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(uint(i))
		if child == nil {
			continue
		}
		if child.GrammarName() == "preproc_include" {
			path := ""
			if p := child.ChildByFieldName("path"); p != nil {
				path = strings.Trim(nodeText(p, content), "\"<>")
			}
			imports = append(imports, ImportEntry{
				Path:      path,
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
			})
		}
	}
	return imports
}

// ── C# ──────────────────────────────────────────────────────────────────────

func extractCSharpSymbols(root *tree_sitter.Node, content []byte) []Symbol {
	var syms []Symbol
	walkCSharpSymbols(root, content, "", &syms)
	return syms
}

func walkCSharpSymbols(node *tree_sitter.Node, content []byte, parent string, syms *[]Symbol) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child == nil {
			continue
		}
		kind := child.GrammarName()
		switch kind {
		case "class_declaration":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			*syms = append(*syms, Symbol{
				Kind:      KindClass,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
			if body := child.ChildByFieldName("body"); body != nil {
				walkCSharpSymbols(body, content, name, syms)
			}
		case "interface_declaration":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			*syms = append(*syms, Symbol{
				Kind:      KindInterface,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "struct_declaration":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			*syms = append(*syms, Symbol{
				Kind:      KindType,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "enum_declaration":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			*syms = append(*syms, Symbol{
				Kind:      KindType,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "method_declaration", "constructor_declaration":
			mName := ""
			if n := child.ChildByFieldName("name"); n != nil {
				mName = nodeText(n, content)
			}
			*syms = append(*syms, Symbol{
				Kind:      KindMethod,
				Name:      mName,
				Parent:    parent,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "namespace_declaration", "file_scoped_namespace_declaration":
			if body := child.ChildByFieldName("body"); body != nil {
				walkCSharpSymbols(body, content, parent, syms)
			} else {
				walkCSharpSymbols(child, content, parent, syms)
			}
		case "record_declaration":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			*syms = append(*syms, Symbol{
				Kind:      KindClass,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		}
	}
}

func extractCSharpImports(root *tree_sitter.Node, content []byte) []ImportEntry {
	var imports []ImportEntry
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(uint(i))
		if child == nil {
			continue
		}
		if child.GrammarName() == "using_directive" {
			imports = append(imports, ImportEntry{
				Path:      strings.TrimSuffix(strings.TrimSpace(nodeText(child, content)), ";"),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
			})
		}
	}
	return imports
}

// ── Kotlin ──────────────────────────────────────────────────────────────────

func extractKotlinSymbols(root *tree_sitter.Node, content []byte) []Symbol {
	var syms []Symbol
	walkKotlinSymbols(root, content, "", &syms)
	return syms
}

func walkKotlinSymbols(node *tree_sitter.Node, content []byte, parent string, syms *[]Symbol) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child == nil {
			continue
		}
		kind := child.GrammarName()
		switch kind {
		case "function_declaration":
			name := ""
			if n := child.ChildByFieldName("name"); n == nil {
				for j := 0; j < int(child.ChildCount()); j++ {
					c := child.Child(uint(j))
					if c != nil && c.GrammarName() == "simple_identifier" {
						name = nodeText(c, content)
						break
					}
				}
			} else {
				name = nodeText(n, content)
			}
			k := KindFunction
			if parent != "" {
				k = KindMethod
			}
			*syms = append(*syms, Symbol{
				Kind:      k,
				Name:      name,
				Parent:    parent,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "class_declaration":
			name := ""
			if n := child.ChildByFieldName("name"); n == nil {
				for j := 0; j < int(child.ChildCount()); j++ {
					c := child.Child(uint(j))
					if c != nil && c.GrammarName() == "type_identifier" {
						name = nodeText(c, content)
						break
					}
				}
			} else {
				name = nodeText(n, content)
			}
			isInterface := false
			for j := 0; j < int(child.ChildCount()); j++ {
				c := child.Child(uint(j))
				if c != nil && nodeText(c, content) == "interface" {
					isInterface = true
					break
				}
			}
			k := KindClass
			if isInterface {
				k = KindInterface
			}
			*syms = append(*syms, Symbol{
				Kind:      k,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
			if body := child.ChildByFieldName("body"); body == nil {
				for j := 0; j < int(child.ChildCount()); j++ {
					c := child.Child(uint(j))
					if c != nil && c.GrammarName() == "class_body" {
						walkKotlinSymbols(c, content, name, syms)
						break
					}
				}
			} else {
				walkKotlinSymbols(body, content, name, syms)
			}
		case "object_declaration":
			name := ""
			for j := 0; j < int(child.ChildCount()); j++ {
				c := child.Child(uint(j))
				if c != nil && c.GrammarName() == "type_identifier" {
					name = nodeText(c, content)
					break
				}
			}
			*syms = append(*syms, Symbol{
				Kind:      KindClass,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		}
	}
}

func extractKotlinImports(root *tree_sitter.Node, content []byte) []ImportEntry {
	var imports []ImportEntry
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(uint(i))
		if child == nil {
			continue
		}
		gn := child.GrammarName()
		if gn == "import_header" || gn == "import_list" {
			if gn == "import_list" {
				for j := 0; j < int(child.ChildCount()); j++ {
					imp := child.Child(uint(j))
					if imp != nil && imp.GrammarName() == "import_header" {
						imports = append(imports, ImportEntry{
							Path:      strings.TrimSpace(nodeText(imp, content)),
							StartLine: int(imp.StartPosition().Row),
							EndLine:   int(imp.EndPosition().Row),
						})
					}
				}
			} else {
				imports = append(imports, ImportEntry{
					Path:      strings.TrimSpace(nodeText(child, content)),
					StartLine: int(child.StartPosition().Row),
					EndLine:   int(child.EndPosition().Row),
				})
			}
		}
	}
	return imports
}

// ── PHP ─────────────────────────────────────────────────────────────────────

func extractPHPSymbols(root *tree_sitter.Node, content []byte) []Symbol {
	var syms []Symbol
	walkPHPSymbols(root, content, "", &syms)
	return syms
}

func walkPHPSymbols(node *tree_sitter.Node, content []byte, parent string, syms *[]Symbol) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child == nil {
			continue
		}
		kind := child.GrammarName()
		switch kind {
		case "function_definition":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			k := KindFunction
			if parent != "" {
				k = KindMethod
			}
			*syms = append(*syms, Symbol{
				Kind:      k,
				Name:      name,
				Parent:    parent,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "method_declaration":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			*syms = append(*syms, Symbol{
				Kind:      KindMethod,
				Name:      name,
				Parent:    parent,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "class_declaration":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			*syms = append(*syms, Symbol{
				Kind:      KindClass,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
			if body := child.ChildByFieldName("body"); body != nil {
				walkPHPSymbols(body, content, name, syms)
			}
		case "interface_declaration":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			*syms = append(*syms, Symbol{
				Kind:      KindInterface,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "trait_declaration":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			*syms = append(*syms, Symbol{
				Kind:      KindType,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "enum_declaration":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			*syms = append(*syms, Symbol{
				Kind:      KindType,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "namespace_definition":
			walkPHPSymbols(child, content, parent, syms)
		case "program":
			walkPHPSymbols(child, content, parent, syms)
		}
	}
}

func extractPHPImports(root *tree_sitter.Node, content []byte) []ImportEntry {
	var imports []ImportEntry
	walkPHPImports(root, content, &imports)
	return imports
}

func walkPHPImports(node *tree_sitter.Node, content []byte, imports *[]ImportEntry) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child == nil {
			continue
		}
		gn := child.GrammarName()
		if gn == "namespace_use_declaration" {
			*imports = append(*imports, ImportEntry{
				Path:      strings.TrimSuffix(strings.TrimSpace(nodeText(child, content)), ";"),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
			})
		} else if gn == "program" {
			walkPHPImports(child, content, imports)
		}
	}
}

// ── Ruby ────────────────────────────────────────────────────────────────────

func extractRubySymbols(root *tree_sitter.Node, content []byte) []Symbol {
	var syms []Symbol
	walkRubySymbols(root, content, "", &syms)
	return syms
}

func walkRubySymbols(node *tree_sitter.Node, content []byte, parent string, syms *[]Symbol) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child == nil {
			continue
		}
		kind := child.GrammarName()
		switch kind {
		case "method":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			k := KindFunction
			if parent != "" {
				k = KindMethod
			}
			*syms = append(*syms, Symbol{
				Kind:      k,
				Name:      name,
				Parent:    parent,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "singleton_method":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			*syms = append(*syms, Symbol{
				Kind:      KindFunction,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
		case "class":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			*syms = append(*syms, Symbol{
				Kind:      KindClass,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
			if body := child.ChildByFieldName("body"); body != nil {
				walkRubySymbols(body, content, name, syms)
			} else {
				walkRubySymbols(child, content, name, syms)
			}
		case "module":
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			*syms = append(*syms, Symbol{
				Kind:      KindType,
				Name:      name,
				Signature: firstLine(nodeText(child, content)),
				StartLine: int(child.StartPosition().Row),
				EndLine:   int(child.EndPosition().Row),
				StartByte: uint(child.StartByte()),
				EndByte:   uint(child.EndByte()),
			})
			if body := child.ChildByFieldName("body"); body != nil {
				walkRubySymbols(body, content, name, syms)
			} else {
				walkRubySymbols(child, content, name, syms)
			}
		}
	}
}

func extractRubyImports(root *tree_sitter.Node, content []byte) []ImportEntry {
	var imports []ImportEntry
	walkRubyImports(root, content, &imports)
	return imports
}

func walkRubyImports(node *tree_sitter.Node, content []byte, imports *[]ImportEntry) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child == nil {
			continue
		}
		if child.GrammarName() == "call" {
			if fn := child.ChildByFieldName("method"); fn != nil {
				name := nodeText(fn, content)
				if name == "require" || name == "require_relative" || name == "include" {
					arg := ""
					if args := child.ChildByFieldName("arguments"); args != nil {
						arg = strings.Trim(strings.TrimSpace(nodeText(args, content)), "\"'()")
					}
					*imports = append(*imports, ImportEntry{
						Path:      arg,
						StartLine: int(child.StartPosition().Row),
						EndLine:   int(child.EndPosition().Row),
					})
				}
			}
		}
		walkRubyImports(child, content, imports)
	}
}

func truncExpr(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) > maxLen {
		runes := []rune(s)
		if len(runes) > maxLen {
			return string(runes[:maxLen]) + "..."
		}
		return s
	}
	return s
}

func cleanReceiver(r string) string {
	r = strings.TrimSpace(r)
	r = strings.TrimPrefix(r, "(")
	r = strings.TrimSuffix(r, ")")
	parts := strings.Fields(r)
	if len(parts) >= 2 {
		t := parts[1]
		t = strings.TrimPrefix(t, "*")
		return t
	}
	if len(parts) == 1 {
		return strings.TrimPrefix(parts[0], "*")
	}
	return r
}

// ── Single-Parse AST Reuse Functions ────────────────────────────────────────
// These functions accept an existing *tree_sitter.Tree to avoid re-parsing
// the same file content multiple times during indexing.

// ExtractAllCallNames extracts call names for ALL functions/methods in the file
// from a single parsed AST, returning a map keyed by "funcName\x00startLine"
// to deduplicated callee names. This replaces per-function-body re-parsing.
func ExtractAllCallNames(lang Lang, tree *tree_sitter.Tree, content []byte, symbols []Symbol, cfg *CallNodeConfig) map[string][]string {
	result := make(map[string][]string)
	root := tree.RootNode()

	for _, sym := range symbols {
		if sym.Kind != KindFunction && sym.Kind != KindMethod {
			continue
		}
		key := sym.Name + "\x00" + itoa(sym.StartLine)

		seen := make(map[string]struct{})
		var names []string
		walkCallsInRange(root, content, sym.StartByte, sym.EndByte, cfg, func(name string) {
			if _, ok := seen[name]; ok {
				return
			}
			seen[name] = struct{}{}
			names = append(names, name)
		})
		if len(names) > 0 {
			result[key] = names
		}
	}
	return result
}

// walkCallsInRange walks the AST tree finding call expressions within the
// given byte range [startByte, endByte). Emits deduplicated callee names.
func walkCallsInRange(node *tree_sitter.Node, content []byte, startByte, endByte uint, cfg *CallNodeConfig, emit func(string)) {
	if node == nil {
		return
	}
	nodeStart := uint(node.StartByte())
	nodeEnd := uint(node.EndByte())

	// Prune: skip nodes entirely outside the target range.
	if nodeEnd <= startByte || nodeStart >= endByte {
		return
	}

	if cfg == nil {
		checkCallExpressionInRange(node, content, emit)
	} else {
		checkCallExpressionConfigInRange(node, content, cfg, emit)
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child != nil {
			walkCallsInRange(child, content, startByte, endByte, cfg, emit)
		}
	}
}

func checkCallExpressionInRange(node *tree_sitter.Node, content []byte, emit func(string)) {
	grammar := node.GrammarName()
	switch grammar {
	case "call_expression":
		if fn := node.ChildByFieldName("function"); fn != nil {
			name := extractCallName(fn, content)
			if name != "" && len(name) >= 2 {
				emit(name)
			}
		}
	case "call":
		if fn := node.ChildByFieldName("function"); fn != nil {
			name := extractCallName(fn, content)
			if name != "" && len(name) >= 2 {
				emit(name)
			}
		}
	case "method_invocation":
		name := extractCallName(node, content)
		if name != "" && len(name) >= 2 {
			emit(name)
		}
	}
}

func checkCallExpressionConfigInRange(node *tree_sitter.Node, content []byte, cfg *CallNodeConfig, emit func(string)) {
	grammar := node.GrammarName()
	if grammar == cfg.FuncCallNodeType || (cfg.MethodNodeType != "" && grammar == cfg.MethodNodeType) {
		name := extractCallNameConfig(node, content, cfg)
		if name != "" && len(name) >= 2 {
			emit(name)
		}
	}
}

// ExtractAllCallNamesFromTree extracts call names for ALL function/method
// bodies from a single parsed AST, returning a map keyed by "funcName:startLine".
// This replaces per-function-body re-parsing (N+1 parses → 1 parse).
func ExtractAllCallNamesFromTree(lang Lang, tree *tree_sitter.Tree, content []byte, symbols []Symbol, cfg *CallNodeConfig) map[string][]string {
	if tree == nil || len(content) == 0 {
		return nil
	}
	result := make(map[string][]string)
	root := tree.RootNode()

	for _, sym := range symbols {
		if sym.Kind != KindFunction && sym.Kind != KindMethod {
			continue
		}
		key := sym.Name + ":" + itoa(int(sym.StartLine))
		seen := make(map[string]struct{})
		var names []string

		walkCallsInByteRange(root, content, sym.StartByte, sym.EndByte, cfg, func(name string) {
			if _, ok := seen[name]; ok {
				return
			}
			seen[name] = struct{}{}
			names = append(names, name)
		})
		if len(names) > 0 {
			result[key] = names
		}
	}
	return result
}

// walkCallsInByteRange walks AST nodes within [startByte, endByte) and emits call names.
func walkCallsInByteRange(node *tree_sitter.Node, content []byte, startByte, endByte uint, cfg *CallNodeConfig, emit func(string)) {
	if node == nil {
		return
	}
	ns := uint(node.StartByte())
	ne := uint(node.EndByte())
	if ne <= startByte || ns >= endByte {
		return
	}
	if cfg != nil {
		checkCallExpressionConfigInRange(node, content, cfg, emit)
	} else {
		checkCallExpressionInRange(node, content, emit)
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child != nil {
			walkCallsInByteRange(child, content, startByte, endByte, cfg, emit)
		}
	}
}

// ExtractRoutesFromTree extracts route definitions from an existing AST.
func ExtractRoutesFromTree(lang Lang, tree *tree_sitter.Tree, content []byte, filePath string) []RouteEntry {
	if tree == nil || len(content) == 0 {
		return nil
	}
	var routes []RouteEntry
	walkRoutes(tree.RootNode(), content, filePath, func(r RouteEntry) {
		routes = append(routes, r)
	})
	return routes
}

// ExtractAPICallsFromTree extracts API call patterns from an existing AST.
func ExtractAPICallsFromTree(lang Lang, tree *tree_sitter.Tree, content []byte, filePath string) []APICallEntry {
	if tree == nil || len(content) == 0 {
		return nil
	}
	var calls []APICallEntry
	walkAPICalls(tree.RootNode(), content, filePath, func(c APICallEntry) {
		calls = append(calls, c)
	})
	return calls
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// TypeHierarchyEdge represents an explicit implements/extends declaration found in source.
type TypeHierarchyEdge struct {
	SourceName string // the type that implements/extends
	SourceLine int    // 0-based line of the source type declaration
	TargetName string // the interface/base type being implemented/extended
}

// ExtractTypeHierarchy extracts explicit inheritance/implementation declarations from the AST.
// Returns edges representing "SourceName extends/implements TargetName" relationships.
func ExtractTypeHierarchy(lang Lang, tree *tree_sitter.Tree, content []byte) []TypeHierarchyEdge {
	if tree == nil || len(content) == 0 {
		return nil
	}
	root := tree.RootNode()
	switch lang {
	case LangGo:
		return extractGoTypeHierarchy(root, content)
	case LangJava:
		return extractJavaTypeHierarchy(root, content)
	case LangTypeScript, LangTSX:
		return extractTSTypeHierarchy(root, content)
	case LangPython:
		return extractPythonTypeHierarchy(root, content)
	case LangRust:
		return extractRustTypeHierarchy(root, content)
	case LangCPP, LangC:
		return extractCppTypeHierarchy(root, content)
	case LangCSharp:
		return extractCSharpTypeHierarchy(root, content)
	case LangKotlin:
		return extractKotlinTypeHierarchy(root, content)
	case LangPHP:
		return extractPHPTypeHierarchy(root, content)
	case LangRuby:
		return extractRubyTypeHierarchy(root, content)
	case LangJavaScript:
		return extractTSTypeHierarchy(root, content)
	default:
		return nil
	}
}

func extractGoTypeHierarchy(root *tree_sitter.Node, content []byte) []TypeHierarchyEdge {
	var edges []TypeHierarchyEdge
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(uint(i))
		if child == nil {
			continue
		}
		if child.GrammarName() != "type_declaration" {
			continue
		}
		for j := 0; j < int(child.ChildCount()); j++ {
			spec := child.Child(uint(j))
			if spec == nil || spec.GrammarName() != "type_spec" {
				continue
			}
			name := ""
			if n := spec.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			if name == "" {
				continue
			}
			typeNode := spec.ChildByFieldName("type")
			if typeNode == nil {
				continue
			}
			if typeNode.GrammarName() == "struct_type" {
				extractGoEmbeddedInterfaces(typeNode, content, name, int(child.StartPosition().Row), &edges)
			}
			if typeNode.GrammarName() == "interface_type" {
				extractGoEmbeddedInInterface(typeNode, content, name, int(child.StartPosition().Row), &edges)
			}
		}
	}
	// Also find var _ Interface = (*Type)(nil) compiler assertions
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(uint(i))
		if child == nil || child.GrammarName() != "var_declaration" {
			continue
		}
		for j := 0; j < int(child.ChildCount()); j++ {
			vs := child.Child(uint(j))
			if vs == nil || vs.GrammarName() != "var_spec" {
				continue
			}
			nameNode := vs.ChildByFieldName("name")
			if nameNode == nil || nodeText(nameNode, content) != "_" {
				continue
			}
			typeIdNode := vs.ChildByFieldName("type")
			if typeIdNode == nil {
				continue
			}
			ifaceName := nodeText(typeIdNode, content)
			valueList := vs.ChildByFieldName("value")
			if valueList == nil {
				continue
			}
			valText := nodeText(valueList, content)
			if strings.Contains(valText, "(nil)") {
				concrName := extractConcreteFromAssertion(valText)
				if concrName != "" && ifaceName != "" {
					edges = append(edges, TypeHierarchyEdge{
						SourceName: concrName,
						SourceLine: int(child.StartPosition().Row),
						TargetName: ifaceName,
					})
				}
			}
		}
	}
	return edges
}

func extractGoEmbeddedInterfaces(structNode *tree_sitter.Node, content []byte, structName string, line int, edges *[]TypeHierarchyEdge) {
	for i := 0; i < int(structNode.ChildCount()); i++ {
		child := structNode.Child(uint(i))
		if child == nil {
			continue
		}
		if child.GrammarName() == "field_declaration_list" {
			for j := 0; j < int(child.ChildCount()); j++ {
				field := child.Child(uint(j))
				if field == nil || field.GrammarName() != "field_declaration" {
					continue
				}
				if field.ChildByFieldName("name") != nil {
					continue
				}
				typeField := field.ChildByFieldName("type")
				if typeField == nil {
					continue
				}
				embeddedName := nodeText(typeField, content)
				if embeddedName != "" && embeddedName[0] >= 'A' && embeddedName[0] <= 'Z' {
					*edges = append(*edges, TypeHierarchyEdge{
						SourceName: structName,
						SourceLine: line,
						TargetName: embeddedName,
					})
				}
			}
		}
	}
}

// extractGoEmbeddedInInterface extracts embedded interfaces inside an interface_type.
// e.g. `type ReadWriter interface { Reader; Writer }` produces ReadWriter→Reader, ReadWriter→Writer.
func extractGoEmbeddedInInterface(ifaceNode *tree_sitter.Node, content []byte, ifaceName string, line int, edges *[]TypeHierarchyEdge) {
	for i := 0; i < int(ifaceNode.ChildCount()); i++ {
		child := ifaceNode.Child(uint(i))
		if child == nil {
			continue
		}
		gn := child.GrammarName()
		if gn == "type_identifier" || gn == "qualified_type" {
			embName := nodeText(child, content)
			if embName != "" {
				*edges = append(*edges, TypeHierarchyEdge{
					SourceName: ifaceName,
					SourceLine: line,
					TargetName: embName,
				})
			}
		}
	}
}

func extractConcreteFromAssertion(valText string) string {
	valText = strings.TrimSpace(valText)
	if strings.HasPrefix(valText, "(*") {
		if idx := strings.IndexByte(valText, ')'); idx > 2 {
			return valText[2:idx]
		}
	}
	if strings.HasPrefix(valText, "(") {
		if idx := strings.IndexByte(valText, ')'); idx > 1 {
			inner := valText[1:idx]
			inner = strings.TrimPrefix(inner, "*")
			return inner
		}
	}
	return ""
}

func extractJavaTypeHierarchy(root *tree_sitter.Node, content []byte) []TypeHierarchyEdge {
	var edges []TypeHierarchyEdge
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(uint(i))
		if child == nil {
			continue
		}
		gname := child.GrammarName()
		if gname != "class_declaration" && gname != "interface_declaration" {
			continue
		}
		name := ""
		if n := child.ChildByFieldName("name"); n != nil {
			name = nodeText(n, content)
		}
		if name == "" {
			continue
		}
		line := int(child.StartPosition().Row)

		if sc := child.ChildByFieldName("superclass"); sc != nil {
			baseName := extractTypeName(sc, content)
			if baseName != "" {
				edges = append(edges, TypeHierarchyEdge{SourceName: name, SourceLine: line, TargetName: baseName})
			}
		}
		if ifaces := child.ChildByFieldName("interfaces"); ifaces != nil {
			extractTypeListEdges(ifaces, content, name, line, &edges)
		}
	}
	return edges
}

func extractTSTypeHierarchy(root *tree_sitter.Node, content []byte) []TypeHierarchyEdge {
	var edges []TypeHierarchyEdge
	walkTSHierarchy(root, content, &edges)
	return edges
}

func walkTSHierarchy(node *tree_sitter.Node, content []byte, edges *[]TypeHierarchyEdge) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child == nil {
			continue
		}
		gname := child.GrammarName()
		if gname == "class_declaration" || gname == "interface_declaration" {
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			if name == "" {
				continue
			}
			line := int(child.StartPosition().Row)
			for j := 0; j < int(child.ChildCount()); j++ {
				heritage := child.Child(uint(j))
				if heritage == nil {
					continue
				}
				hname := heritage.GrammarName()
				if hname == "class_heritage" {
					for k := 0; k < int(heritage.ChildCount()); k++ {
						clause := heritage.Child(uint(k))
						if clause == nil {
							continue
						}
						cname := clause.GrammarName()
						if cname == "extends_clause" || cname == "implements_clause" || cname == "extends_type_clause" {
							for l := 0; l < int(clause.ChildCount()); l++ {
								typeRef := clause.Child(uint(l))
								if typeRef == nil {
									continue
								}
								if typeRef.GrammarName() == "type_identifier" || typeRef.GrammarName() == "identifier" {
									tn := nodeText(typeRef, content)
									if tn != "" {
										*edges = append(*edges, TypeHierarchyEdge{SourceName: name, SourceLine: line, TargetName: tn})
									}
								}
							}
						}
					}
				}
			}
		} else {
			walkTSHierarchy(child, content, edges)
		}
	}
}

func extractPythonTypeHierarchy(root *tree_sitter.Node, content []byte) []TypeHierarchyEdge {
	var edges []TypeHierarchyEdge
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(uint(i))
		if child == nil || child.GrammarName() != "class_definition" {
			continue
		}
		name := ""
		if n := child.ChildByFieldName("name"); n != nil {
			name = nodeText(n, content)
		}
		if name == "" {
			continue
		}
		line := int(child.StartPosition().Row)
		if bases := child.ChildByFieldName("superclasses"); bases != nil {
			for j := 0; j < int(bases.ChildCount()); j++ {
				base := bases.Child(uint(j))
				if base == nil {
					continue
				}
				if base.GrammarName() == "identifier" {
					bn := nodeText(base, content)
					if bn != "" && bn != "object" {
						edges = append(edges, TypeHierarchyEdge{SourceName: name, SourceLine: line, TargetName: bn})
					}
				}
			}
		}
	}
	return edges
}

func extractRustTypeHierarchy(root *tree_sitter.Node, content []byte) []TypeHierarchyEdge {
	var edges []TypeHierarchyEdge
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(uint(i))
		if child == nil || child.GrammarName() != "impl_item" {
			continue
		}
		traitNode := child.ChildByFieldName("trait")
		typeNode := child.ChildByFieldName("type")
		if traitNode == nil || typeNode == nil {
			continue
		}
		traitName := nodeText(traitNode, content)
		typeName := nodeText(typeNode, content)
		if traitName != "" && typeName != "" {
			edges = append(edges, TypeHierarchyEdge{
				SourceName: typeName,
				SourceLine: int(child.StartPosition().Row),
				TargetName: traitName,
			})
		}
	}
	return edges
}

func extractTypeName(node *tree_sitter.Node, content []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child == nil {
			continue
		}
		gn := child.GrammarName()
		if gn == "type_identifier" || gn == "identifier" {
			return nodeText(child, content)
		}
	}
	return ""
}

func extractTypeListEdges(node *tree_sitter.Node, content []byte, sourceName string, line int, edges *[]TypeHierarchyEdge) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child == nil {
			continue
		}
		gn := child.GrammarName()
		if gn == "type_list" {
			extractTypeListEdges(child, content, sourceName, line, edges)
		} else if gn == "type_identifier" || gn == "identifier" {
			tn := nodeText(child, content)
			if tn != "" {
				*edges = append(*edges, TypeHierarchyEdge{SourceName: sourceName, SourceLine: line, TargetName: tn})
			}
		}
	}
}

// ── Type Hierarchy: C++ ────────────────────────────────────────────────────

func extractCppTypeHierarchy(root *tree_sitter.Node, content []byte) []TypeHierarchyEdge {
	var edges []TypeHierarchyEdge
	walkCppHierarchy(root, content, &edges)
	return edges
}

func walkCppHierarchy(node *tree_sitter.Node, content []byte, edges *[]TypeHierarchyEdge) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child == nil {
			continue
		}
		gn := child.GrammarName()
		if gn == "class_specifier" || gn == "struct_specifier" {
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			if name == "" {
				for j := 0; j < int(child.ChildCount()); j++ {
					c := child.Child(uint(j))
					if c != nil && (c.GrammarName() == "type_identifier" || c.GrammarName() == "identifier") {
						name = nodeText(c, content)
						break
					}
				}
			}
			if name == "" {
				continue
			}
			line := int(child.StartPosition().Row)
			for j := 0; j < int(child.ChildCount()); j++ {
				base := child.Child(uint(j))
				if base == nil || base.GrammarName() != "base_class_clause" {
					continue
				}
				for k := 0; k < int(base.ChildCount()); k++ {
					spec := base.Child(uint(k))
					if spec != nil && (spec.GrammarName() == "type_identifier" || spec.GrammarName() == "identifier") {
						bn := nodeText(spec, content)
						if bn != "" && bn != "public" && bn != "protected" && bn != "private" && bn != "virtual" {
							*edges = append(*edges, TypeHierarchyEdge{SourceName: name, SourceLine: line, TargetName: bn})
						}
					}
				}
			}
		} else {
			walkCppHierarchy(child, content, edges)
		}
	}
}

// ── Type Hierarchy: C# ─────────────────────────────────────────────────────

func extractCSharpTypeHierarchy(root *tree_sitter.Node, content []byte) []TypeHierarchyEdge {
	var edges []TypeHierarchyEdge
	walkCSharpHierarchy(root, content, &edges)
	return edges
}

func walkCSharpHierarchy(node *tree_sitter.Node, content []byte, edges *[]TypeHierarchyEdge) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child == nil {
			continue
		}
		gn := child.GrammarName()
		if gn == "class_declaration" || gn == "interface_declaration" || gn == "struct_declaration" {
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			if name == "" {
				for j := 0; j < int(child.ChildCount()); j++ {
					c := child.Child(uint(j))
					if c != nil && c.GrammarName() == "identifier" {
						name = nodeText(c, content)
						break
					}
				}
			}
			if name == "" {
				continue
			}
			line := int(child.StartPosition().Row)
			for j := 0; j < int(child.ChildCount()); j++ {
				bt := child.Child(uint(j))
				if bt == nil {
					continue
				}
				bgn := bt.GrammarName()
				if bgn == "base_list" || bgn == "bases" {
					for k := 0; k < int(bt.ChildCount()); k++ {
						bn := bt.Child(uint(k))
						if bn != nil {
							ident := extractFirstIdent(bn, content)
							if ident != "" {
								*edges = append(*edges, TypeHierarchyEdge{SourceName: name, SourceLine: line, TargetName: ident})
							}
						}
					}
				}
			}
		} else if gn == "namespace_declaration" || gn == "declaration_list" {
			walkCSharpHierarchy(child, content, edges)
		}
	}
}

func extractFirstIdent(node *tree_sitter.Node, content []byte) string {
	if node.GrammarName() == "identifier" || node.GrammarName() == "type_identifier" {
		return nodeText(node, content)
	}
	if n := node.ChildByFieldName("name"); n != nil {
		return nodeText(n, content)
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		c := node.Child(uint(i))
		if c != nil && (c.GrammarName() == "identifier" || c.GrammarName() == "type_identifier") {
			return nodeText(c, content)
		}
	}
	return ""
}

// ── Type Hierarchy: Kotlin ──────────────────────────────────────────────────

func extractKotlinTypeHierarchy(root *tree_sitter.Node, content []byte) []TypeHierarchyEdge {
	var edges []TypeHierarchyEdge
	walkKotlinHierarchy(root, content, &edges)
	return edges
}

func walkKotlinHierarchy(node *tree_sitter.Node, content []byte, edges *[]TypeHierarchyEdge) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child == nil {
			continue
		}
		gn := child.GrammarName()
		if gn == "class_declaration" || gn == "object_declaration" {
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			if name == "" {
				for j := 0; j < int(child.ChildCount()); j++ {
					c := child.Child(uint(j))
					if c != nil && (c.GrammarName() == "type_identifier" || c.GrammarName() == "simple_identifier") {
						name = nodeText(c, content)
						break
					}
				}
			}
			if name == "" {
				continue
			}
			line := int(child.StartPosition().Row)
			for j := 0; j < int(child.ChildCount()); j++ {
				deleg := child.Child(uint(j))
				if deleg == nil {
					continue
				}
				dgn := deleg.GrammarName()
				if dgn == "delegation_specifiers" || dgn == "delegation_specifier" {
					if dgn == "delegation_specifier" {
						tn := extractKotlinTypeName(deleg, content)
						if tn != "" {
							*edges = append(*edges, TypeHierarchyEdge{SourceName: name, SourceLine: line, TargetName: tn})
						}
					} else {
						for k := 0; k < int(deleg.ChildCount()); k++ {
							spec := deleg.Child(uint(k))
							if spec == nil {
								continue
							}
							tn := extractKotlinTypeName(spec, content)
							if tn != "" {
								*edges = append(*edges, TypeHierarchyEdge{SourceName: name, SourceLine: line, TargetName: tn})
							}
						}
					}
				}
			}
		} else {
			walkKotlinHierarchy(child, content, edges)
		}
	}
}

func extractKotlinTypeName(node *tree_sitter.Node, content []byte) string {
	if node == nil {
		return ""
	}
	gn := node.GrammarName()
	if gn == "simple_identifier" || gn == "type_identifier" || gn == "identifier" {
		return nodeText(node, content)
	}
	if n := node.ChildByFieldName("name"); n != nil {
		return nodeText(n, content)
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		c := node.Child(uint(i))
		if c == nil {
			continue
		}
		cgn := c.GrammarName()
		if cgn == "simple_identifier" || cgn == "type_identifier" || cgn == "identifier" {
			return nodeText(c, content)
		}
		if cgn == "user_type" {
			return extractKotlinTypeName(c, content)
		}
	}
	return ""
}

// ── Type Hierarchy: PHP ─────────────────────────────────────────────────────

func extractPHPTypeHierarchy(root *tree_sitter.Node, content []byte) []TypeHierarchyEdge {
	var edges []TypeHierarchyEdge
	walkPHPHierarchy(root, content, &edges)
	return edges
}

func walkPHPHierarchy(node *tree_sitter.Node, content []byte, edges *[]TypeHierarchyEdge) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child == nil {
			continue
		}
		if child.GrammarName() == "class_declaration" {
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			if name == "" {
				continue
			}
			line := int(child.StartPosition().Row)
			for j := 0; j < int(child.ChildCount()); j++ {
				clause := child.Child(uint(j))
				if clause == nil {
					continue
				}
				cgn := clause.GrammarName()
				if cgn == "class_interface_clause" || cgn == "class_base_clause" {
					for k := 0; k < int(clause.ChildCount()); k++ {
						nn := clause.Child(uint(k))
						if nn != nil && (nn.GrammarName() == "name" || nn.GrammarName() == "qualified_name") {
							tn := nodeText(nn, content)
							if tn != "" {
								*edges = append(*edges, TypeHierarchyEdge{SourceName: name, SourceLine: line, TargetName: tn})
							}
						}
					}
				}
			}
		} else {
			walkPHPHierarchy(child, content, edges)
		}
	}
}

// ── Type Hierarchy: Ruby ────────────────────────────────────────────────────

func extractRubyTypeHierarchy(root *tree_sitter.Node, content []byte) []TypeHierarchyEdge {
	var edges []TypeHierarchyEdge
	walkRubyHierarchy(root, content, &edges)
	return edges
}

func walkRubyHierarchy(node *tree_sitter.Node, content []byte, edges *[]TypeHierarchyEdge) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child == nil {
			continue
		}
		if child.GrammarName() == "class" {
			name := ""
			if n := child.ChildByFieldName("name"); n != nil {
				name = nodeText(n, content)
			}
			if name == "" {
				continue
			}
			line := int(child.StartPosition().Row)
			if sc := child.ChildByFieldName("superclass"); sc != nil {
				for j := 0; j < int(sc.ChildCount()); j++ {
					c := sc.Child(uint(j))
					if c != nil && (c.GrammarName() == "constant" || c.GrammarName() == "scope_resolution") {
						bn := nodeText(c, content)
						if bn != "" {
							*edges = append(*edges, TypeHierarchyEdge{SourceName: name, SourceLine: line, TargetName: bn})
						}
					}
				}
			}
		} else {
			walkRubyHierarchy(child, content, edges)
		}
	}
}
