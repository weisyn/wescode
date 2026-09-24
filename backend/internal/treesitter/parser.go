// Package treesitter provides a wescode-specific facade over the official
// tree-sitter Go bindings. It manages per-language parsers, incremental
// re-parsing, and query helpers for function/type/import extraction.
//
// The package owns all CGo interaction so that consumers (codeintel,
// editengine, verification) never import tree-sitter directly.
package treesitter

import (
	"fmt"
	"path/filepath"
	"strings"

	tree_sitter_kotlin "github.com/fwcd/tree-sitter-kotlin/bindings/go"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_c_sharp "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"
	tree_sitter_c "github.com/tree-sitter/tree-sitter-c/bindings/go"
	tree_sitter_cpp "github.com/tree-sitter/tree-sitter-cpp/bindings/go"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_php "github.com/tree-sitter/tree-sitter-php/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tree_sitter_ruby "github.com/tree-sitter/tree-sitter-ruby/bindings/go"
	tree_sitter_rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// Lang identifies a supported programming language.
type Lang string

const (
	LangGo         Lang = "go"
	LangTypeScript Lang = "typescript"
	LangTSX        Lang = "tsx"
	LangJavaScript Lang = "javascript"
	LangPython     Lang = "python"
	LangRust       Lang = "rust"
	LangJava       Lang = "java"
	LangCPP        Lang = "cpp"
	LangC          Lang = "c"
	LangCSharp     Lang = "c_sharp"
	LangKotlin     Lang = "kotlin"
	LangPHP        Lang = "php"
	LangRuby       Lang = "ruby"
)

// DetectLang infers the language from a file extension.
// Returns ("", false) for unsupported extensions.
func DetectLang(path string) (Lang, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return LangGo, true
	case ".ts":
		return LangTypeScript, true
	case ".tsx":
		return LangTSX, true
	case ".js", ".mjs", ".cjs":
		return LangJavaScript, true
	case ".py":
		return LangPython, true
	case ".rs":
		return LangRust, true
	case ".java":
		return LangJava, true
	case ".cpp", ".cc", ".cxx", ".hpp", ".hxx", ".hh":
		return LangCPP, true
	case ".c":
		return LangC, true
	case ".h":
		return detectHeaderLang(path)
	case ".cs":
		return LangCSharp, true
	case ".kt", ".kts":
		return LangKotlin, true
	case ".php":
		return LangPHP, true
	case ".rb":
		return LangRuby, true
	default:
		return "", false
	}
}

// detectHeaderLang resolves .h ambiguity: if sibling .cpp/.cc/.cxx files exist
// in the same directory, treat as C++; otherwise default to C.
func detectHeaderLang(path string) (Lang, bool) {
	dir := filepath.Dir(path)
	cppExts := []string{".cpp", ".cc", ".cxx", ".hpp", ".hxx"}
	entries, err := filepath.Glob(filepath.Join(dir, "*"))
	if err == nil {
		for _, e := range entries {
			ext := strings.ToLower(filepath.Ext(e))
			for _, ce := range cppExts {
				if ext == ce {
					return LangCPP, true
				}
			}
		}
	}
	return LangC, true
}

func tsLanguage(lang Lang) (*tree_sitter.Language, error) {
	switch lang {
	case LangGo:
		return tree_sitter.NewLanguage(tree_sitter_go.Language()), nil
	case LangTypeScript:
		return tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript()), nil
	case LangTSX:
		return tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTSX()), nil
	case LangJavaScript:
		return tree_sitter.NewLanguage(tree_sitter_javascript.Language()), nil
	case LangPython:
		return tree_sitter.NewLanguage(tree_sitter_python.Language()), nil
	case LangRust:
		return tree_sitter.NewLanguage(tree_sitter_rust.Language()), nil
	case LangJava:
		return tree_sitter.NewLanguage(tree_sitter_java.Language()), nil
	case LangCPP:
		return tree_sitter.NewLanguage(tree_sitter_cpp.Language()), nil
	case LangC:
		return tree_sitter.NewLanguage(tree_sitter_c.Language()), nil
	case LangCSharp:
		return tree_sitter.NewLanguage(tree_sitter_c_sharp.Language()), nil
	case LangKotlin:
		return tree_sitter.NewLanguage(tree_sitter_kotlin.Language()), nil
	case LangPHP:
		return tree_sitter.NewLanguage(tree_sitter_php.LanguagePHP()), nil
	case LangRuby:
		return tree_sitter.NewLanguage(tree_sitter_ruby.Language()), nil
	default:
		return nil, fmt.Errorf("unsupported language: %s", lang)
	}
}

// ParserPool manages reusable parsers per language. Each slot holds an
// independent set of per-language parsers so multiple goroutines can
// parse concurrently (tree-sitter parsers are not goroutine-safe).
// Slot acquisition is channel-based to bound concurrency to pool size.
type ParserPool struct {
	slots chan *parserSlot
	size  int
}

type parserSlot struct {
	parsers map[Lang]*tree_sitter.Parser
}

// NewParserPool creates a pool with one slot (original behavior).
func NewParserPool() *ParserPool {
	return NewParserPoolN(1)
}

// NewParserPoolN creates a pool with n independent parser slots.
// Each slot can parse in parallel with other slots.
func NewParserPoolN(n int) *ParserPool {
	if n < 1 {
		n = 1
	}
	ch := make(chan *parserSlot, n)
	for i := 0; i < n; i++ {
		ch <- &parserSlot{parsers: make(map[Lang]*tree_sitter.Parser)}
	}
	return &ParserPool{slots: ch, size: n}
}

func (slot *parserSlot) getParser(lang Lang) (*tree_sitter.Parser, error) {
	if p, ok := slot.parsers[lang]; ok {
		return p, nil
	}
	tsLang, err := tsLanguage(lang)
	if err != nil {
		return nil, err
	}
	p := tree_sitter.NewParser()
	if err := p.SetLanguage(tsLang); err != nil {
		p.Close()
		return nil, fmt.Errorf("set language %s: %w", lang, err)
	}
	slot.parsers[lang] = p
	return p, nil
}

// Parse parses source content for the given language. Acquires a pool slot,
// parses, then returns the slot. Multiple goroutines can parse concurrently
// when pool size > 1.
//
// The returned *Tree must be closed by the caller when no longer needed.
func (pp *ParserPool) Parse(lang Lang, content []byte, oldTree *tree_sitter.Tree) (*tree_sitter.Tree, error) {
	slot := <-pp.slots
	defer func() { pp.slots <- slot }()

	p, err := slot.getParser(lang)
	if err != nil {
		return nil, err
	}
	tree := p.Parse(content, oldTree)
	if tree == nil {
		return nil, fmt.Errorf("tree-sitter parse returned nil for %s", lang)
	}
	return tree, nil
}

// Size returns the number of parser slots (concurrency level).
func (pp *ParserPool) Size() int { return pp.size }

// Close releases all pooled parsers.
func (pp *ParserPool) Close() {
	for i := 0; i < pp.size; i++ {
		slot := <-pp.slots
		for _, p := range slot.parsers {
			p.Close()
		}
	}
}

// HasErrors reports whether the tree contains any ERROR or MISSING nodes.
func HasErrors(tree *tree_sitter.Tree) bool {
	root := tree.RootNode()
	return root.HasError()
}

// LangExtensions returns the file extensions associated with a language.
func LangExtensions(lang Lang) []string {
	switch lang {
	case LangGo:
		return []string{".go"}
	case LangTypeScript:
		return []string{".ts", ".tsx"}
	case LangTSX:
		return []string{".ts", ".tsx"}
	case LangJavaScript:
		return []string{".js", ".jsx"}
	case LangPython:
		return []string{".py"}
	case LangRust:
		return []string{".rs"}
	case LangJava:
		return []string{".java"}
	case LangCPP:
		return []string{".cpp", ".cc", ".cxx", ".hpp", ".hxx", ".hh", ".h"}
	case LangC:
		return []string{".c", ".h"}
	case LangCSharp:
		return []string{".cs"}
	case LangKotlin:
		return []string{".kt", ".kts"}
	case LangPHP:
		return []string{".php"}
	case LangRuby:
		return []string{".rb"}
	default:
		return nil
	}
}
