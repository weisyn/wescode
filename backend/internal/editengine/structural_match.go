package editengine

import (
	"regexp"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/weisyn/wescode/internal/treesitter"
)

var identRe = regexp.MustCompile(`[a-zA-Z_]\w{2,}`)

// reservedWords contains common keywords across Go, TypeScript, and Python
// that should be excluded from identifier coverage checks.
var reservedWords = map[string]struct{}{
	"func": {}, "return": {}, "if": {}, "else": {}, "for": {}, "range": {},
	"switch": {}, "case": {}, "default": {}, "break": {}, "continue": {},
	"var": {}, "const": {}, "type": {}, "struct": {}, "interface": {},
	"package": {}, "import": {}, "map": {}, "chan": {}, "defer": {},
	"go": {}, "select": {}, "fallthrough": {}, "goto": {},
	"nil": {}, "true": {}, "false": {}, "error": {}, "string": {},
	"int": {}, "bool": {}, "byte": {}, "float64": {}, "int64": {},
	"function": {}, "class": {}, "extends": {}, "implements": {},
	"export": {}, "async": {}, "await": {}, "from": {}, "this": {},
	"let": {}, "new": {}, "try": {}, "catch": {}, "throw": {},
	"def": {}, "self": {}, "pass": {}, "elif": {}, "None": {},
	"True": {}, "False": {}, "lambda": {}, "yield": {}, "with": {},
}

// extractIdentifiers returns unique non-keyword identifiers (len >= 3) from s.
func extractIdentifiers(s string) []string {
	matches := identRe.FindAllString(s, -1)
	seen := make(map[string]struct{}, len(matches))
	var result []string
	for _, m := range matches {
		if _, reserved := reservedWords[m]; reserved {
			continue
		}
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}
		result = append(result, m)
	}
	return result
}

// NewCombinedFallback creates a MatchFallback that chains Tier 2 (normalized)
// and Tier 3 (structural) matching. Tier 2 is tried first; if it fails,
// Tier 3 uses tree-sitter to find the target symbol by name.
//
// If metrics is non-nil, each fallback invocation records which tier matched
// (or all-miss). Tier 1 is not recorded here — it succeeds inside wesgine's
// edit tool before the fallback is invoked. Tier 1 count can be derived:
// Tier1 = TotalEdits - Tier2 - Tier3 - AllMiss.
// largeFileLineThreshold is the line count above which we attempt to narrow
// the Tier 2 search range using tree-sitter symbol lookup.
const largeFileLineThreshold = 500

func NewCombinedFallback(ts *treesitter.ParserPool, metrics *EditMetrics) func(path, content, old string) (int, int, bool) {
	return func(path, content, old string) (int, int, bool) {
		// For large files, try narrowing the search range before full Tier 2.
		// Extract symbols from old_string, locate them in the file AST, and
		// run NormalizedMatch only within the symbol's byte range (± margin).
		if ts != nil && strings.Count(content, "\n") > largeFileLineThreshold {
			if start, end, ok := narrowedNormalizedMatch(ts, path, content, old); ok {
				if metrics != nil {
					metrics.RecordTier(2)
					metrics.RecordMatchRange(start, end)
				}
				return start, end, true
			}
		}

		start, end, ok := NormalizedMatch(content, old)
		if ok {
			if metrics != nil {
				metrics.RecordTier(2)
				metrics.RecordMatchRange(start, end)
			}
			return start, end, true
		}

		if ts == nil {
			if metrics != nil {
				metrics.RecordTier(0)
			}
			return -1, -1, false
		}

		start, end, ok = structuralMatch(ts, path, content, old)
		if ok {
			if metrics != nil {
				metrics.RecordTier(3)
				metrics.RecordMatchRange(start, end)
			}
			return start, end, true
		}
		if metrics != nil {
			metrics.RecordTier(0)
		}
		return -1, -1, false
	}
}

// narrowedNormalizedMatch attempts Tier 2 matching within a tree-sitter-located
// symbol region. This avoids full-file NormalizedMatch on large files.
func narrowedNormalizedMatch(ts *treesitter.ParserPool, path, content, old string) (int, int, bool) {
	lang, ok := treesitter.DetectLang(path)
	if !ok {
		return -1, -1, false
	}
	oldTree, err := ts.Parse(lang, []byte(old), nil)
	if err != nil {
		return -1, -1, false
	}
	defer oldTree.Close()

	oldSymbols := treesitter.ExtractSymbols(lang, oldTree, []byte(old))
	if len(oldSymbols) == 0 {
		return -1, -1, false
	}
	targetName := oldSymbols[0].Name
	targetKind := oldSymbols[0].Kind
	if targetName == "" {
		return -1, -1, false
	}

	fileTree, err := ts.Parse(lang, []byte(content), nil)
	if err != nil {
		return -1, -1, false
	}
	defer fileTree.Close()

	fileSymbols := treesitter.ExtractSymbols(lang, fileTree, []byte(content))
	for _, sym := range fileSymbols {
		if sym.Name != targetName || sym.Kind != targetKind {
			continue
		}
		regionStart := int(sym.StartByte)
		regionEnd := int(sym.EndByte)
		if regionStart < 0 || regionEnd > len(content) || regionEnd <= regionStart {
			continue
		}
		region := content[regionStart:regionEnd]
		start, end, ok := NormalizedMatch(region, old)
		if ok {
			return regionStart + start, regionStart + end, true
		}
	}
	return -1, -1, false
}

// structuralMatch uses tree-sitter to find the AST subtree that old_string describes.
// Two strategies are tried in order:
//  1. AST skeleton equivalence: strip comments/whitespace from both ASTs, compare
//     node-type structure recursively. Tolerates comment, formatting, and blank-line differences.
//  2. Symbol-name fallback: extract the primary symbol name from old_text, find it in
//     the file, and validate with line-count ratio + identifier coverage.
//
// Returns the byte range of the matched region in content.
func structuralMatch(ts *treesitter.ParserPool, path, content, old string) (int, int, bool) {
	lang, ok := treesitter.DetectLang(path)
	if !ok {
		return -1, -1, false
	}

	fileTree, err := ts.Parse(lang, []byte(content), nil)
	if err != nil {
		return -1, -1, false
	}
	defer fileTree.Close()

	oldTree, err := ts.Parse(lang, []byte(old), nil)
	if err != nil {
		return -1, -1, false
	}
	defer oldTree.Close()

	// Strategy 1: AST skeleton equivalence search.
	oldRoot := oldTree.RootNode()
	fileRoot := fileTree.RootNode()
	if oldRoot != nil && fileRoot != nil {
		oldSkeleton := buildSkeleton(oldRoot)
		if start, end, ok := findSkeletonMatch(fileRoot, oldSkeleton, content); ok {
			return start, end, true
		}
	}

	// Strategy 2: symbol-name fallback (existing logic).
	fileSymbols := treesitter.ExtractSymbols(lang, fileTree, []byte(content))
	if len(fileSymbols) == 0 {
		return -1, -1, false
	}

	oldSymbols := treesitter.ExtractSymbols(lang, oldTree, []byte(old))
	if len(oldSymbols) == 0 {
		return -1, -1, false
	}

	targetName := oldSymbols[0].Name
	targetKind := oldSymbols[0].Kind
	if targetName == "" {
		return -1, -1, false
	}

	oldLines := strings.Count(old, "\n") + 1
	identifiers := extractIdentifiers(old)

	for _, sym := range fileSymbols {
		if sym.Name == targetName && sym.Kind == targetKind {
			start := int(sym.StartByte)
			end := int(sym.EndByte)
			if start >= 0 && end > start && end <= len(content) {
				matched := content[start:end]
				if !strings.Contains(matched, targetName) {
					continue
				}

				matchedLines := strings.Count(matched, "\n") + 1
				if oldLines > 0 && matchedLines > 0 {
					ratio := float64(matchedLines) / float64(oldLines)
					if ratio < 0.5 || ratio > 2.0 {
						continue
					}
				}

				if len(identifiers) > 0 {
					hits := 0
					for _, id := range identifiers {
						if strings.Contains(matched, id) {
							hits++
						}
					}
					if float64(hits)/float64(len(identifiers)) < 0.5 {
						continue
					}
				}

				return start, end, true
			}
		}
	}

	return -1, -1, false
}

// skeletonNode is a simplified AST representation that strips comments and whitespace.
type skeletonNode struct {
	nodeType string
	children []skeletonNode
}

// commentTypes are node types to ignore during structural comparison.
var commentTypes = map[string]bool{
	"comment": true, "line_comment": true, "block_comment": true,
	"multiline_comment": true, "doc_comment": true,
}

// buildSkeleton creates a comment-free skeleton from a tree-sitter node.
// Only the first significant child subtree is used (the old_text root's first declaration).
func buildSkeleton(node *tree_sitter.Node) skeletonNode {
	sk := skeletonNode{nodeType: node.GrammarName()}
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child == nil {
			continue
		}
		childType := child.GrammarName()
		if commentTypes[childType] {
			continue
		}
		sk.children = append(sk.children, buildSkeleton(child))
	}
	return sk
}

// skeletonEqual checks if two skeletons have equivalent structure
// (same node types at each level).
func skeletonEqual(a, b skeletonNode) bool {
	if a.nodeType != b.nodeType {
		return false
	}
	if len(a.children) != len(b.children) {
		return false
	}
	for i := range a.children {
		if !skeletonEqual(a.children[i], b.children[i]) {
			return false
		}
	}
	return true
}

// findSkeletonMatch searches for a node in fileRoot whose skeleton matches target.
// Returns the byte range of the first unique match. Ambiguous (>1) matches return false.
func findSkeletonMatch(fileRoot *tree_sitter.Node, target skeletonNode, content string) (int, int, bool) {
	type match struct{ start, end int }
	var matches []match

	var walk func(node *tree_sitter.Node)
	walk = func(node *tree_sitter.Node) {
		if node == nil {
			return
		}
		nodeSk := buildSkeleton(node)
		if skeletonEqual(nodeSk, target) {
			start := int(node.StartByte())
			end := int(node.EndByte())
			if start >= 0 && end <= len(content) && end > start {
				matches = append(matches, match{start, end})
			}
			return
		}
		for i := 0; i < int(node.ChildCount()); i++ {
			walk(node.Child(uint(i)))
		}
	}

	// Search among top-level children (don't match root node itself which is program/source_file).
	for i := 0; i < int(fileRoot.ChildCount()); i++ {
		child := fileRoot.Child(uint(i))
		if child == nil || commentTypes[child.GrammarName()] {
			continue
		}
		walk(child)
	}

	if len(matches) == 1 {
		return matches[0].start, matches[0].end, true
	}
	return -1, -1, false
}
