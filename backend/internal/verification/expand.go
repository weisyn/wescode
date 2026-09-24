package verification

import (
	"context"
	"path/filepath"
)

const maxExpandedPackages = 10

// ExpandByImportGraph expands the test scope from directly modified packages
// to packages that import them (one level only). Language-agnostic: relies on
// the code index having recorded import relationships for the project's language.
//
// Expansion is bounded: if the expanded set exceeds maxExpandedPackages,
// it falls back to only the original packages (prevents runaway test times).
func ExpandByImportGraph(
	ctx context.Context,
	modifiedFiles []string,
	workDir string,
	querier ImportGraphQuerier,
) []string {
	if querier == nil {
		return AffectedPackages(modifiedFiles, workDir)
	}

	original := AffectedPackages(modifiedFiles, workDir)
	expanded := make(map[string]struct{})
	for _, pkg := range original {
		expanded[pkg] = struct{}{}
	}

	for _, f := range modifiedFiles {
		pkg, err := querier.PackageOfFile(ctx, f)
		if err != nil || pkg == "" {
			continue
		}
		importers, err := querier.ReverseImports(ctx, pkg)
		if err != nil {
			continue
		}
		for _, imp := range importers {
			rel, err := filepath.Rel(workDir, imp)
			if err != nil {
				continue
			}
			epkg := "./" + filepath.Dir(rel) + "/..."
			expanded[epkg] = struct{}{}
		}
	}

	if len(expanded) > maxExpandedPackages {
		return original
	}

	result := make([]string, 0, len(expanded))
	for pkg := range expanded {
		result = append(result, pkg)
	}
	return result
}
