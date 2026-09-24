package codeintel

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/weisyn/wescode/internal/treesitter"
	"github.com/weisyn/wesgine/tool"
)

// TypeBinding represents a resolved type for a local identifier.
type TypeBinding struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Line int    `json:"line"`
}

// TypeContext is the aggregated type information for a code range.
type TypeContext struct {
	Bindings []TypeBinding
	Path     string
	FromLine int
	ToLine   int
}

// FormatForPrompt produces a compact string suitable for injection into LLM context.
func (tc *TypeContext) FormatForPrompt() string {
	if len(tc.Bindings) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("[TYPE_CONTEXT] Types in scope:\n")
	seen := make(map[string]bool)
	for _, b := range tc.Bindings {
		key := b.Name + ":" + b.Type
		if seen[key] {
			continue
		}
		seen[key] = true
		fmt.Fprintf(&sb, "  %s: %s\n", b.Name, b.Type)
	}
	return sb.String()
}

// TypeContextProvider resolves type information for identifiers in a given
// code range using LSP hover. It uses tree-sitter to find identifier positions
// then batches hover requests to the language server.
type TypeContextProvider struct {
	lsp    LSPBridge
	tsPool *treesitter.ParserPool
	files  tool.FileProvider

	mu    sync.RWMutex
	cache map[typeCacheKey]*typeCacheEntry
}

type typeCacheKey struct {
	Path     string
	FromLine int
	ToLine   int
}

type typeCacheEntry struct {
	Result   *TypeContext
	CachedAt time.Time
}

const typeCacheTTL = 30 * time.Second
const maxHoverBatch = 50

func NewTypeContextProvider(lsp LSPBridge, tsPool *treesitter.ParserPool, files tool.FileProvider) *TypeContextProvider {
	return &TypeContextProvider{
		lsp:    lsp,
		tsPool: tsPool,
		files:  files,
		cache:  make(map[typeCacheKey]*typeCacheEntry),
	}
}

// Resolve returns type bindings for identifiers in the given line range.
func (p *TypeContextProvider) Resolve(ctx context.Context, path string, fromLine, toLine int) *TypeContext {
	if p.lsp == nil || p.tsPool == nil || p.files == nil {
		return nil
	}
	if _, isNoop := p.lsp.(NoopLSP); isNoop {
		return nil
	}

	// Check cache
	key := typeCacheKey{Path: path, FromLine: fromLine, ToLine: toLine}
	p.mu.RLock()
	if entry, ok := p.cache[key]; ok && time.Since(entry.CachedAt) < typeCacheTTL {
		p.mu.RUnlock()
		return entry.Result
	}
	p.mu.RUnlock()

	// Read file and parse with tree-sitter
	content, err := p.files.ReadFile(ctx, path)
	if err != nil || len(content) == 0 {
		return nil
	}

	lang, ok := treesitter.DetectLang(path)
	if !ok {
		return nil
	}

	// Extract identifier positions in range
	positions := p.extractIdentifierPositions(lang, content, fromLine, toLine)
	if len(positions) == 0 {
		return nil
	}

	// Limit batch size
	if len(positions) > maxHoverBatch {
		positions = positions[:maxHoverBatch]
	}

	// Batch hover with timeout
	hoverCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	bindings := p.batchHover(hoverCtx, path, positions)

	result := &TypeContext{
		Bindings: bindings,
		Path:     path,
		FromLine: fromLine,
		ToLine:   toLine,
	}

	// Cache result
	p.mu.Lock()
	p.cache[key] = &typeCacheEntry{Result: result, CachedAt: time.Now()}
	// Prune old entries
	if len(p.cache) > 200 {
		now := time.Now()
		for k, v := range p.cache {
			if now.Sub(v.CachedAt) > typeCacheTTL {
				delete(p.cache, k)
			}
		}
	}
	p.mu.Unlock()

	slog.Debug("[type_context] resolved", "path", path, "range", fmt.Sprintf("%d-%d", fromLine, toLine), "bindings", len(bindings))
	return result
}

// InvalidateFile removes all cache entries for a given file.
func (p *TypeContextProvider) InvalidateFile(path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k := range p.cache {
		if k.Path == path {
			delete(p.cache, k)
		}
	}
}

type identPos struct {
	Name string
	Line int
	Col  int
}

func (p *TypeContextProvider) extractIdentifierPositions(lang treesitter.Lang, content []byte, fromLine, toLine int) []identPos {
	tree, err := p.tsPool.Parse(lang, content, nil)
	if err != nil || tree == nil {
		return nil
	}
	defer tree.Close()

	root := tree.RootNode()
	var positions []identPos
	seen := make(map[string]bool)

	var walk func(node *tree_sitter.Node)
	walk = func(node *tree_sitter.Node) {
		if node == nil {
			return
		}
		startLine := int(node.StartPosition().Row)
		endLine := int(node.EndPosition().Row)

		// Skip nodes entirely outside range
		if endLine < fromLine || startLine > toLine {
			return
		}

		nodeType := node.GrammarName()
		// Identifier-like nodes (language-agnostic heuristic)
		if isIdentifierNode(nodeType) && startLine >= fromLine && startLine <= toLine {
			name := string(content[node.StartByte():node.EndByte()])
			// Skip keywords, single-char identifiers, and duplicates
			if len(name) >= 2 && !isKeyword(name) {
				key := fmt.Sprintf("%s:%d", name, startLine)
				if !seen[key] {
					seen[key] = true
					positions = append(positions, identPos{
						Name: name,
						Line: startLine,
						Col:  int(node.StartPosition().Column),
					})
				}
			}
		}

		for i := uint(0); i < node.ChildCount(); i++ {
			walk(node.Child(i))
		}
	}
	walk(root)
	return positions
}

func (p *TypeContextProvider) batchHover(ctx context.Context, path string, positions []identPos) []TypeBinding {
	var bindings []TypeBinding

	for _, pos := range positions {
		select {
		case <-ctx.Done():
			return bindings
		default:
		}

		hover, err := p.lsp.Hover(ctx, path, pos.Line, pos.Col)
		if err != nil || hover == "" {
			continue
		}

		// Extract type from hover (usually first line is type signature)
		typeStr := extractTypeFromHover(hover)
		if typeStr == "" {
			continue
		}

		bindings = append(bindings, TypeBinding{
			Name: pos.Name,
			Type: typeStr,
			Line: pos.Line,
		})
	}
	return bindings
}

func extractTypeFromHover(hover string) string {
	// Hover typically returns markdown code block with type signature
	// e.g. "```go\nvar ctx context.Context\n```"
	// or "func (s *Service) Foo(ctx context.Context) error"
	lines := strings.Split(strings.TrimSpace(hover), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || line == "```" || strings.HasPrefix(line, "```") {
			continue
		}
		// Skip doc comments
		if strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") {
			continue
		}
		// Truncate long signatures
		if len(line) > 120 {
			line = line[:120] + "..."
		}
		return line
	}
	return ""
}

func isIdentifierNode(nodeType string) bool {
	switch nodeType {
	case "identifier", "type_identifier", "field_identifier",
		"property_identifier", "shorthand_property_identifier",
		"method_name", "variable_name", "name",
		"simple_identifier", "value_identifier":
		return true
	}
	return false
}

func isKeyword(s string) bool {
	switch s {
	case "if", "else", "for", "while", "return", "func", "var", "let", "const",
		"type", "struct", "interface", "import", "package", "class", "def",
		"true", "false", "nil", "null", "undefined", "this", "self",
		"break", "continue", "switch", "case", "default", "range", "defer",
		"go", "select", "chan", "map", "make", "new", "append", "len", "cap",
		"fn", "pub", "mod", "use", "impl", "trait", "enum", "match",
		"async", "await", "try", "catch", "throw", "finally":
		return true
	}
	return false
}
