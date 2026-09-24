package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/weisyn/wescode/internal/codeintel"
)

// symbolContextSource provides symbol search via CKG index.
type symbolContextSource struct {
	getIndex func() *codeintel.CodeIndex
}

func (s *symbolContextSource) ID() string { return "symbol" }

func (s *symbolContextSource) Info() ContextSourceInfo {
	return ContextSourceInfo{
		ID: "symbol", Label: "符号", Icon: "symbol-method",
		Searchable: true, Available: s.getIndex() != nil,
	}
}

func (s *symbolContextSource) Search(ctx context.Context, query string, limit int) ([]ContextSearchItem, error) {
	idx := s.getIndex()
	if idx == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	query = strings.TrimSpace(query)
	var syms []codeintel.SymbolEntry
	var err error
	if query == "" || query == "*" {
		syms, err = idx.ListSymbols(ctx, limit)
	} else {
		syms, err = idx.SearchSymbols(ctx, strings.ToLower(query), limit)
	}
	if err != nil {
		return nil, err
	}
	results := make([]ContextSearchItem, 0, len(syms))
	for _, sym := range syms {
		results = append(results, ContextSearchItem{
			ID:       fmt.Sprintf("symbol:%s:%d", sym.FilePath, sym.LineStart),
			SourceID: "symbol",
			Label:    sym.Name,
			Detail:   fmt.Sprintf("%s:%d (%s)", sym.FilePath, sym.LineStart, sym.Kind),
			Icon:     symbolIcon(sym.Kind),
			Data: map[string]any{
				"kind":    sym.Kind,
				"path":    sym.FilePath,
				"line":    sym.LineStart,
				"lineEnd": sym.LineEnd,
			},
		})
	}
	return results, nil
}

func (s *symbolContextSource) Resolve(_ context.Context, itemID string) (*ContextResolved, error) {
	parts := strings.SplitN(strings.TrimPrefix(itemID, "symbol:"), ":", 2)
	if len(parts) < 2 {
		return &ContextResolved{ID: itemID}, nil
	}
	filePath := parts[0]
	var startLine int
	fmt.Sscanf(parts[1], "%d", &startLine)

	content, lang := readSourceLines(filePath, startLine, startLine+50)
	if lang == "" {
		lang = langFromExt(filePath)
	}

	return &ContextResolved{
		ID: itemID,
		CodeSnippet: &CodeSnippetRef{
			Path:      filePath,
			StartLine: startLine,
			EndLine:   startLine + strings.Count(content, "\n"),
			Language:  lang,
			Content:   content,
		},
	}, nil
}

func symbolIcon(kind string) string {
	switch kind {
	case "function", "method":
		return "symbol-method"
	case "class", "struct":
		return "symbol-class"
	case "interface":
		return "symbol-interface"
	case "variable", "constant":
		return "symbol-variable"
	default:
		return "symbol-misc"
	}
}

func readSourceLines(path string, start, end int) (string, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	lines := strings.Split(string(data), "\n")
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start > len(lines) {
		return "", ""
	}
	selected := lines[start-1 : end]
	return strings.Join(selected, "\n"), langFromExt(path)
}

func langFromExt(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx":
		return "javascript"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cc", ".cxx", ".hpp":
		return "cpp"
	case ".rb":
		return "ruby"
	case ".swift":
		return "swift"
	case ".kt":
		return "kotlin"
	case ".cs":
		return "csharp"
	case ".sh", ".bash":
		return "bash"
	default:
		return ""
	}
}
