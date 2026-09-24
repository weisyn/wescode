package codeintel

import (
	"bufio"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/weisyn/wescode/internal/treesitter"
)

// TestCKG_FalsePositiveRate_WescodeBackend indexes the wescode backend/ directory
// and measures FindOrphans false positive rate against a gold list of symbols
// known to be used via paths invisible to tree-sitter (JSON-RPC, interface
// dispatch, reflection, build tags).
//
// This is the CSP Phase 3 validation gate:
//   - FP rate < 12% = PASS (regression gate)
//   - Outputs precision/recall metrics for tracking improvement over time
func TestCKG_FalsePositiveRate_WescodeBackend(t *testing.T) {
	// Locate backend/ root relative to this test file.
	backendRoot := locateBackendRoot(t)
	goldPath := filepath.Join(backendRoot, "tests", "bench", "dataset", "ckg_gold_known_used.txt")

	goldSet := loadGoldList(t, goldPath)
	t.Logf("Gold list: %d known-used symbols", len(goldSet))

	// Create a fresh index and index all Go files in backend/.
	ts := treesitter.NewParserPool()
	t.Cleanup(ts.Close)
	dbPath := filepath.Join(t.TempDir(), "bench_ckg.db")
	ci, err := NewCodeIndex(dbPath, ts)
	if err != nil {
		t.Fatalf("NewCodeIndex: %v", err)
	}
	// TempDir's cleanup was registered above and therefore runs last (LIFO), so
	// the DB is closed before the directory is removed. Required on Windows,
	// which refuses to delete a file that still has an open handle — POSIX
	// unlinks it regardless, which is why this leak stayed invisible.
	t.Cleanup(func() { ci.Close() })

	ctx := context.Background()
	files := discoverGoFiles(t, backendRoot)
	t.Logf("Indexing %d Go files...", len(files))

	ci.SetTotalFiles(len(files))
	for _, f := range files {
		if err := ci.IndexFile(ctx, f); err != nil {
			t.Logf("IndexFile %s: %v", f, err)
		}
	}

	r := ci.Readiness()
	t.Logf("Index readiness: %.0f%% (%d/%d files)", r.Completeness*100, r.IndexedFiles, r.TotalFiles)

	// Declare external entry points (same as engine boot). These are called by
	// the LLM through the tool layer, so the CKG has no inbound edge for them
	// and FindOrphans would otherwise count every one as a false positive.
	ci.SetExternalEntryPoints([]string{
		"SearchSymbols", "FindReferences", "GetDiagnostics",
		"FindOrphans", "ImpactAnalysis", "FindSimilar",
		"SearchFiles", "ListFileSymbols", "ListImports", "ReverseImports",
		"PackageOfFile", "SymbolCount", "SymbolIDs",
		"PromoteByID", "DemoteByID",
		"FindSymbolInLang", "WrapperFn",
	})

	// Run FindOrphans.
	orphans, _, err := ci.FindOrphans(ctx, FindOrphanOpts{
		ExcludeMain:      true,
		ExcludeTests:     true,
		ExcludeBuildTags: true,
		Limit:            500,
	})
	if err != nil {
		t.Fatalf("FindOrphans: %v", err)
	}

	// Compute FP rate: only count orphans with certainty >= 0.5 (the ones
	// actually shown to the AI in detail). Low-certainty orphans are folded
	// into "Uncertain" group and don't consume AI tokens (CI-23).
	falsePositives := 0
	highCertOrphans := 0
	var fpNames []string
	for _, o := range orphans {
		if o.Certainty < 0.5 {
			continue
		}
		highCertOrphans++
		if goldSet[o.Symbol.Name] {
			falsePositives++
			fpNames = append(fpNames, o.Symbol.Name)
		}
	}

	totalOrphans := len(orphans)
	fpRate := 0.0
	if highCertOrphans > 0 {
		fpRate = float64(falsePositives) / float64(highCertOrphans)
	}

	// Edge coverage metric.
	var totalEdges, resolvedEdges int
	ci.DB().QueryRow("SELECT COUNT(*) FROM edges").Scan(&totalEdges)
	ci.DB().QueryRow("SELECT COUNT(*) FROM edges WHERE target_id IS NOT NULL").Scan(&resolvedEdges)
	edgeCoverage := 0.0
	if totalEdges > 0 {
		edgeCoverage = float64(resolvedEdges) / float64(totalEdges)
	}

	// Report.
	t.Logf("=== CKG Bench Results ===")
	t.Logf("Total orphan candidates: %d (all certainties)", totalOrphans)
	t.Logf("High-certainty orphans (>= 0.5): %d", highCertOrphans)
	t.Logf("False positives (in gold list, high-cert only): %d", falsePositives)
	t.Logf("FP rate (high-cert): %.1f%%", fpRate*100)
	t.Logf("Edge coverage (resolved/total): %.1f%% (%d/%d)", edgeCoverage*100, resolvedEdges, totalEdges)
	if len(fpNames) > 0 {
		t.Logf("FP symbols: %s", strings.Join(fpNames, ", "))
	}

	// Regression gate: FP rate among high-certainty orphans must be below 12%.
	if fpRate > 0.12 {
		t.Errorf("FP rate %.1f%% exceeds 12%% regression gate", fpRate*100)
	}
}

func locateBackendRoot(t *testing.T) string {
	t.Helper()
	// Walk up from current test file to find backend/ root.
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find backend/ root (no go.mod found)")
		}
		dir = parent
	}
}

func loadGoldList(t *testing.T, path string) map[string]bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open gold list: %v", err)
	}
	defer f.Close()

	gold := map[string]bool{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		gold[line] = true
	}
	return gold
}

func discoverGoFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if name == "vendor" || name == "node_modules" || name == ".git" ||
				name == "testdata" || name == ".build" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return files
}

// TestGoldList_NoStaleEntries fails when a gold-list name no longer names a
// declared function. A stale name registers no edge, so it silently inflates
// the denominator of the FP-rate metric instead of erroring — which is how
// renamed methods (RetireByID → DismissByID) survived here unnoticed.
func TestGoldList_NoStaleEntries(t *testing.T) {
	backendRoot := locateBackendRoot(t)
	gold := loadGoldList(t, filepath.Join(backendRoot, "tests/bench/dataset/ckg_gold_known_used.txt"))

	declared := map[string]bool{}
	fset := token.NewFileSet()
	for _, path := range discoverGoFiles(t, backendRoot) {
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			continue // unparseable file is not this test's concern
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				declared[fn.Name.Name] = true
			}
		}
	}

	var stale []string
	for name := range gold {
		if !declared[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("gold list names %d symbol(s) that no longer exist: %v\n"+
			"Remove or rename them in tests/bench/dataset/ckg_gold_known_used.txt.", len(stale), stale)
	}
}

// BenchmarkCKG_IndexBackend measures indexing throughput on wescode backend.
func BenchmarkCKG_IndexBackend(b *testing.B) {
	backendRoot := "."
	if _, err := os.Stat(filepath.Join(backendRoot, "go.mod")); err != nil {
		dir, _ := os.Getwd()
		for {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				backendRoot = dir
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				b.Skip("cannot find backend root")
			}
			dir = parent
		}
	}

	files := []string{}
	filepath.Walk(backendRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() && (info.Name() == "vendor" || info.Name() == ".git") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ts := treesitter.NewParserPool()
		dbPath := filepath.Join(b.TempDir(), fmt.Sprintf("bench_%d.db", i))
		ci, _ := NewCodeIndex(dbPath, ts)
		ci.SetTotalFiles(len(files))
		ctx := context.Background()
		for _, f := range files {
			ci.IndexFile(ctx, f)
		}
	}
}
