package codeintel

import (
	"fmt"
	"path/filepath"
	"strings"
)

// BuildMetaCognitionHint generates a brief text hint appended to the code context
// message, telling the AI about context limitations and suggesting tool usage.
// CE-INV-03: output is hard-capped at 200 tokens (~800 chars).
func BuildMetaCognitionHint(injected, allCandidates []CodeFragment, state EditorState, readiness ReadinessReport) string {
	truncated := diffFragments(allCandidates, injected)
	if len(truncated) == 0 && readiness.Completeness >= ReadinessHigh {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n[Context Notes]\n")

	if len(truncated) > 0 {
		sb.WriteString("Potentially relevant but excluded (token budget):\n")
		limit := 5
		if len(truncated) < limit {
			limit = len(truncated)
		}
		for i := 0; i < limit; i++ {
			f := truncated[i]
			label := f.Symbol
			if label == "" {
				label = filepath.Base(f.Path)
			}
			if label == "" || label == "." {
				label = string(f.Kind)
			}
			sb.WriteString(fmt.Sprintf("  - %s: %s\n", label, f.Reason))
		}
		if len(truncated) > 5 {
			sb.WriteString(fmt.Sprintf("  ... and %d more\n", len(truncated)-5))
		}
		sb.WriteString("Use read/search_symbols tools to inspect if needed.\n")
	}

	if readiness.Completeness < ReadinessHigh && readiness.TotalFiles > 0 {
		sb.WriteString(fmt.Sprintf("Index: %.0f%% complete (%d/%d files). Some symbols may be missing.\n",
			readiness.Completeness*100, readiness.IndexedFiles, readiness.TotalFiles))
	}

	result := sb.String()
	if len(result) > 800 {
		result = result[:800] + "\n"
	}
	return result
}

// diffFragments returns fragments in candidates that are not in injected.
func diffFragments(candidates, injected []CodeFragment) []CodeFragment {
	injectedSet := make(map[string]bool, len(injected))
	for _, f := range injected {
		key := fmt.Sprintf("%s:%d:%s", f.Path, f.StartLine, f.Symbol)
		injectedSet[key] = true
	}

	var diff []CodeFragment
	for _, f := range candidates {
		key := fmt.Sprintf("%s:%d:%s", f.Path, f.StartLine, f.Symbol)
		if !injectedSet[key] {
			diff = append(diff, f)
		}
	}
	return diff
}
