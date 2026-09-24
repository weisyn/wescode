package generic_queries

import (
	"embed"
	"strings"
	"sync"
)

//go:embed *.scm
var queryFiles embed.FS

// QueryKind identifies a category of tree-sitter query pattern.
type QueryKind string

const (
	QueryFunctions QueryKind = "functions"
	QueryClasses   QueryKind = "classes"
	QueryImports   QueryKind = "imports"
	QueryCalls     QueryKind = "calls"
)

var (
	loaded     map[QueryKind]string
	loadedOnce sync.Once
)

// Get returns the raw S-expression query content for a given kind.
// Returns empty string if the kind is not found.
func Get(kind QueryKind) string {
	loadedOnce.Do(func() {
		loaded = make(map[QueryKind]string, 4)
		for _, k := range []QueryKind{QueryFunctions, QueryClasses, QueryImports, QueryCalls} {
			data, err := queryFiles.ReadFile(string(k) + ".scm")
			if err == nil {
				loaded[k] = strings.TrimSpace(string(data))
			}
		}
	})
	return loaded[kind]
}

// All returns all loaded query patterns.
func All() map[QueryKind]string {
	_ = Get(QueryFunctions) // ensure loaded
	result := make(map[QueryKind]string, len(loaded))
	for k, v := range loaded {
		result[k] = v
	}
	return result
}

// GrammarRegistry tracks available tree-sitter grammars for Tier B languages.
// Grammars can be registered at build time (compiled-in) or at runtime
// (external plugins providing grammar bindings).
type GrammarRegistry struct {
	mu       sync.RWMutex
	grammars map[string]*GrammarInfo
}

// GrammarInfo describes an available tree-sitter grammar.
type GrammarInfo struct {
	Language   string   // canonical language name (e.g. "elixir")
	Extensions []string // file extensions (e.g. [".ex", ".exs"])
	Tier       string   // "A" (deep, langs/*.json) | "B" (generic queries) | "C" (FTS5 only)
	Available  bool     // true if grammar binary is loadable
}

// NewGrammarRegistry creates a registry pre-populated with Tier A languages.
func NewGrammarRegistry() *GrammarRegistry {
	r := &GrammarRegistry{
		grammars: make(map[string]*GrammarInfo, 64),
	}
	for _, g := range tierAGrammars {
		r.grammars[g.Language] = &g
	}
	for _, g := range tierBGrammars {
		r.grammars[g.Language] = &g
	}
	return r
}

// Register adds or updates a grammar in the registry.
// Used by external plugins to provide additional language support.
func (r *GrammarRegistry) Register(info GrammarInfo) {
	r.mu.Lock()
	r.grammars[info.Language] = &info
	r.mu.Unlock()
}

// Lookup returns grammar info for a file extension.
func (r *GrammarRegistry) Lookup(ext string) *GrammarInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, g := range r.grammars {
		for _, e := range g.Extensions {
			if e == ext {
				return g
			}
		}
	}
	return nil
}

// TierFor returns the support tier for a given language.
func (r *GrammarRegistry) TierFor(language string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if g, ok := r.grammars[language]; ok {
		return g.Tier
	}
	return "C"
}

var tierAGrammars = []GrammarInfo{
	{Language: "go", Extensions: []string{".go"}, Tier: "A", Available: true},
	{Language: "typescript", Extensions: []string{".ts", ".tsx"}, Tier: "A", Available: true},
	{Language: "javascript", Extensions: []string{".js", ".jsx", ".mjs", ".cjs"}, Tier: "A", Available: true},
	{Language: "python", Extensions: []string{".py"}, Tier: "A", Available: true},
	{Language: "rust", Extensions: []string{".rs"}, Tier: "A", Available: true},
	{Language: "java", Extensions: []string{".java"}, Tier: "A", Available: true},
	{Language: "csharp", Extensions: []string{".cs"}, Tier: "A", Available: true},
	{Language: "cpp", Extensions: []string{".cpp", ".cc", ".cxx", ".hpp"}, Tier: "A", Available: true},
	{Language: "c", Extensions: []string{".c", ".h"}, Tier: "A", Available: true},
	{Language: "kotlin", Extensions: []string{".kt", ".kts"}, Tier: "A", Available: true},
	{Language: "swift", Extensions: []string{".swift"}, Tier: "A", Available: true},
	{Language: "php", Extensions: []string{".php"}, Tier: "A", Available: true},
	{Language: "ruby", Extensions: []string{".rb"}, Tier: "A", Available: true},
}

var tierBGrammars = []GrammarInfo{
	{Language: "elixir", Extensions: []string{".ex", ".exs"}, Tier: "B", Available: false},
	{Language: "haskell", Extensions: []string{".hs"}, Tier: "B", Available: false},
	{Language: "scala", Extensions: []string{".scala"}, Tier: "B", Available: false},
	{Language: "lua", Extensions: []string{".lua"}, Tier: "B", Available: false},
	{Language: "dart", Extensions: []string{".dart"}, Tier: "B", Available: false},
	{Language: "julia", Extensions: []string{".jl"}, Tier: "B", Available: false},
	{Language: "r", Extensions: []string{".r", ".R"}, Tier: "B", Available: false},
	{Language: "zig", Extensions: []string{".zig"}, Tier: "B", Available: false},
	{Language: "nim", Extensions: []string{".nim"}, Tier: "B", Available: false},
	{Language: "ocaml", Extensions: []string{".ml", ".mli"}, Tier: "B", Available: false},
	{Language: "fsharp", Extensions: []string{".fs", ".fsi"}, Tier: "B", Available: false},
	{Language: "clojure", Extensions: []string{".clj", ".cljs"}, Tier: "B", Available: false},
	{Language: "erlang", Extensions: []string{".erl"}, Tier: "B", Available: false},
	{Language: "perl", Extensions: []string{".pl", ".pm"}, Tier: "B", Available: false},
	{Language: "bash", Extensions: []string{".sh", ".bash"}, Tier: "B", Available: false},
	{Language: "sql", Extensions: []string{".sql"}, Tier: "B", Available: false},
	{Language: "graphql", Extensions: []string{".graphql", ".gql"}, Tier: "B", Available: false},
	{Language: "hcl", Extensions: []string{".hcl", ".tf"}, Tier: "B", Available: false},
	{Language: "protobuf", Extensions: []string{".proto"}, Tier: "B", Available: false},
	{Language: "svelte", Extensions: []string{".svelte"}, Tier: "B", Available: false},
	{Language: "vue", Extensions: []string{".vue"}, Tier: "B", Available: false},
}
