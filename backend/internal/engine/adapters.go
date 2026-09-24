package engine

import (
	"context"

	"github.com/weisyn/wescode/internal/codeintel"
	"github.com/weisyn/wescode/internal/verification"
)

// codeIndexSymbolAdapter adapts CodeIndex.FindSymbol to verification.SymbolFinder.
type codeIndexSymbolAdapter struct {
	idx *codeintel.CodeIndex
}

func (a *codeIndexSymbolAdapter) FindSymbol(ctx context.Context, name string) ([]verification.SymbolResult, error) {
	entries, err := a.idx.FindSymbol(ctx, name)
	if err != nil {
		return nil, err
	}
	results := make([]verification.SymbolResult, len(entries))
	for i, e := range entries {
		results[i] = verification.SymbolResult{
			FilePath:  e.FilePath,
			Name:      e.Name,
			LineStart: e.LineStart,
		}
	}
	return results, nil
}

// codeIndexSymbolLister adapts CodeIndex.ListFileSymbols to verification.SymbolLister.
func codeIndexSymbolLister(idx *codeintel.CodeIndex) verification.SymbolLister {
	return func(ctx context.Context, path string) ([]string, error) {
		entries, err := idx.ListFileSymbols(ctx, path)
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.Kind == "function" || e.Kind == "method" {
				names = append(names, e.Name)
			}
		}
		return names, nil
	}
}
