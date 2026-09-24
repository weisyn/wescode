package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/weisyn/wescode/internal/codeintel"
)

// ckgContextSource provides CKG knowledge graph queries as context.
type ckgContextSource struct {
	getIndex func() *codeintel.CodeIndex
}

func (s *ckgContextSource) ID() string { return "ckg" }

func (s *ckgContextSource) Info() ContextSourceInfo {
	return ContextSourceInfo{
		ID: "ckg", Label: "代码知识图", Icon: "graph",
		Searchable: true, Available: s.getIndex() != nil,
	}
}

func (s *ckgContextSource) Search(ctx context.Context, query string, limit int) ([]ContextSearchItem, error) {
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
			ID:       fmt.Sprintf("ckg:%s:%d", sym.FilePath, sym.LineStart),
			SourceID: "ckg",
			Label:    sym.Name,
			Detail:   fmt.Sprintf("%s — %s:%d", sym.Kind, sym.FilePath, sym.LineStart),
			Icon:     "graph",
			Data: map[string]any{
				"kind": sym.Kind,
				"path": sym.FilePath,
				"line": sym.LineStart,
			},
		})
	}
	return results, nil
}

func (s *ckgContextSource) Resolve(ctx context.Context, itemID string) (*ContextResolved, error) {
	idx := s.getIndex()
	if idx == nil {
		return &ContextResolved{ID: itemID}, nil
	}

	parts := strings.SplitN(strings.TrimPrefix(itemID, "ckg:"), ":", 2)
	if len(parts) < 2 {
		return &ContextResolved{ID: itemID}, nil
	}
	filePath := parts[0]
	var startLine int
	fmt.Sscanf(parts[1], "%d", &startLine)

	content, lang := readSourceLines(filePath, startLine, startLine+30)

	var sb strings.Builder
	fmt.Fprintf(&sb, "Symbol at %s:%d\n", filePath, startLine)
	if content != "" {
		fmt.Fprintf(&sb, "```%s\n%s\n```\n", lang, content)
	}

	syms, _ := idx.SearchSymbols(ctx, strings.ToLower(filepath.Base(filePath)), 5)
	var symbolName string
	for _, sym := range syms {
		if sym.FilePath == filePath && sym.LineStart == startLine {
			symbolName = sym.Name
			break
		}
	}

	if symbolName != "" {
		callers, err := idx.CallersOf(ctx, symbolName, 10)
		if err == nil && len(callers) > 0 {
			sb.WriteString("\nCallers:\n")
			for _, c := range callers {
				fmt.Fprintf(&sb, "  - %s (%s:%d)\n", c.Name, c.FilePath, c.LineStart)
			}
		}

		callees, err := idx.CalleesOf(ctx, filePath, symbolName, startLine)
		if err == nil && len(callees) > 0 {
			sb.WriteString("\nCallees:\n")
			for _, c := range callees {
				fmt.Fprintf(&sb, "  - %s\n", c)
			}
		}
	}

	return &ContextResolved{
		ID:      itemID,
		Content: sb.String(),
	}, nil
}
