package codeintel

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/weisyn/wesgine/tool"
)

// ckgDisclosureFloor names the tools whose silence is worst, and the bias each
// must carry. These are literal strings on purpose.
//
// The rest of this file proves the derivation rule; this table proves the rule
// still covers the traffic. A rename breaks it, and that is the point: the
// previous design named tools by hand at *runtime*, so a wrong name silently
// exempted a tool from disclosure — "callers" was listed where the registered
// name is "find_callers", and the two highest-frequency CKG tools shipped with
// no readiness footer at all while the list read as complete. The names still
// appear once, but now in a test, where being wrong is a red light instead of
// a behavior change.
var ckgDisclosureFloor = map[string]CKGBias{
	"find_callers":    BiasUnderReport,
	"find_references": BiasUnderReport,
	"search_symbols":  BiasUnderReport,
	"impact_analysis": BiasUnderReport,
	"semantic_search": BiasUnderReport,
	"find_similar":    BiasUnderReport,
	// The one tool that reads absence: a missing edge invents an orphan
	// rather than hiding one, so "results may be incomplete" would point
	// suspicion at the sound half of the answer.
	"find_orphans": BiasOverReport,
}

// ckgBackedTools builds the index-reading tools the engine registers.
//
// It is a sample weighted toward high-traffic tools, not a mirror of the
// registry — claiming exhaustiveness by hand is the failure this whole change
// removes. What the sample establishes is that the shape every CKG tool in
// this package shares (index as a direct field) is the shape DependsOnCKG
// recognizes. A new tool built the same way is covered without editing this
// list; a new tool built differently is what the floor table catches.
func ckgBackedTools(ci *CodeIndex) []tool.Tool {
	return []tool.Tool{
		NewCallersTool(ci, NoopLSP{}),
		NewFindReferencesTool(NoopLSP{}, ci),
		NewSearchSymbolsTool(ci, nil),
		NewImpactTool(ci),
		NewOrphansTool(ci),
		NewSemanticSearchTool(ci),
		NewDuplicatesTool(ci),
		NewCoChangeTool(ci),
		NewDataFlowTool(ci),
		NewTracePathTool(ci),
		NewTaintPathsTool(ci),
		NewReadSymbolsTool(ci, nil),
		NewProjectMapTool(ci, "/tmp"),
		NewImplementationsTool(ci, NoopLSP{}),
		NewHotspotTool(ci),
		NewEffectsTool(ci),
		NewCircularDepsTool(ci),
		NewAPIConsumersTool(ci),
		NewVerifyCallersTool(ci, NoopLSP{}),
		NewCrossLanguageTool(ci),
		NewSuggestConstraintsTool(ci, nil),
	}
}

func TestCKGToolSet_RecognizesRegisteredTools(t *testing.T) {
	ci, dir := newTestIndex(t)

	set := NewCKGToolSet()
	for _, tl := range ckgBackedTools(ci) {
		set.Observe(tl)
		if _, ok := set.Lookup(tl.Name()); !ok {
			t.Errorf("%s reads the code index but went unrecognized: its type no longer holds a *CodeIndex field, so it will ship results with no readiness footer", tl.Name())
		}
	}

	for name, wantBias := range ckgDisclosureFloor {
		gotBias, ok := set.Lookup(name)
		if !ok {
			t.Errorf("%s is not recognized; recognized set is %v", name, set.Names())
			continue
		}
		if gotBias != wantBias {
			t.Errorf("%s bias = %v, want %v: the disclosure sentence would point at the wrong half of the answer", name, gotBias, wantBias)
		}
	}

	// Tools that hold no index must stay out. A footer that appears on
	// unrelated output teaches the model to ignore footers.
	for _, tl := range []tool.Tool{
		NewReportFindingTool(),
		NewChangeFreqTool(dir),
		// Snapshot history, not the live index: get_trend reads node_history
		// via a func() *sql.DB closure, and its answer degrades with how many
		// snapshots exist, not with index completeness. Reflection cannot see
		// through the closure anyway, so this asserts the exclusion is a
		// decision rather than an oversight.
		NewGetTrendTool(nil),
	} {
		set.Observe(tl)
		if _, ok := set.Lookup(tl.Name()); ok {
			t.Errorf("%s does not read the code index but was recognized", tl.Name())
		}
	}
}

func TestConfidenceEnrichHook_DisclosesOnDegradedIndex(t *testing.T) {
	ci, _ := newTestIndex(t)
	// A fresh index has indexed 0 of 0 files: completeness 0.0, the worst
	// case. It used to be the quietest one — wesgine's advisory channel gates
	// on `conf > 0`, so an unavailable index produced no warning anywhere.
	if got := ci.Readiness().Completeness; got != 0 {
		t.Fatalf("fresh index completeness = %v, want 0", got)
	}

	set := NewCKGToolSet()
	set.Observe(NewCallersTool(ci, NoopLSP{}))
	set.Observe(NewOrphansTool(ci))
	hook := NewConfidenceEnrichHook(ci, set)
	ctx := context.Background()

	under := &tool.ToolResult{Content: "no callers found"}
	hook(ctx, tool.ToolCall{Name: "find_callers"}, under, nil)
	if !strings.Contains(under.Content, "matches may be missing") {
		t.Errorf("find_callers footer = %q, want an under-report disclosure", under.Content)
	}
	if under.Metadata["confidence"] != 0.0 {
		t.Errorf("confidence metadata = %v, want 0.0", under.Metadata["confidence"])
	}

	over := &tool.ToolResult{Content: "3 orphans"}
	hook(ctx, tool.ToolCall{Name: "find_orphans"}, over, nil)
	if !strings.Contains(over.Content, "false positives") {
		t.Errorf("find_orphans footer = %q, want an over-report disclosure", over.Content)
	}

	// Errors do not become more trustworthy with a fuller index.
	errored := &tool.ToolResult{Content: "invalid symbol name", IsError: true}
	hook(ctx, tool.ToolCall{Name: "find_callers"}, errored, nil)
	if errored.Content != "invalid symbol name" || errored.Metadata != nil {
		t.Errorf("errored result was enriched: %+v", errored)
	}

	unrelated := &tool.ToolResult{Content: "exit 0"}
	hook(ctx, tool.ToolCall{Name: "exec"}, unrelated, nil)
	if unrelated.Content != "exit 0" || unrelated.Metadata != nil {
		t.Errorf("non-CKG tool was enriched: %+v", unrelated)
	}
}

func TestConfidenceEnrichHook_SilentAtFullFidelity(t *testing.T) {
	ci, _ := newTestIndex(t)
	ci.SetIndexedCount(10) // no total yet → treated as complete
	if got := ci.Readiness().Completeness; got < ReadinessHigh {
		t.Fatalf("completeness = %v, want >= %v", got, ReadinessHigh)
	}

	set := NewCKGToolSet()
	set.Observe(NewCallersTool(ci, NoopLSP{}))
	hook := NewConfidenceEnrichHook(ci, set)

	result := &tool.ToolResult{Content: "2 callers"}
	hook(context.Background(), tool.ToolCall{Name: "find_callers"}, result, nil)
	if result.Content != "2 callers" || result.Metadata != nil {
		t.Errorf("full-fidelity result was annotated: %+v", result)
	}
}

// TestConfidenceDisclosure_NoSilentDegradedBand walks the completeness range and
// asserts disclosure and full fidelity partition it with no gap.
//
// The gap used to be real: EnrichResult stopped attaching a warning at 0.7
// while ReadinessHigh — "full fidelity" everywhere else in this package — is
// 0.9. Between them the index was short and the answer was degraded, but the
// result was byte-identical to a healthy one. That band is the common case for
// a repo that is still indexing.
func TestConfidenceDisclosure_NoSilentDegradedBand(t *testing.T) {
	for _, completeness := range []float64{0.0, 0.1, 0.29, 0.3, 0.49, 0.5, 0.69, 0.7, 0.75, 0.85, 0.89} {
		cr := ConfidenceResult{Confidence: completeness}
		if cr.Disclosure(BiasUnderReport) == "" {
			t.Errorf("completeness %.2f < ReadinessHigh but discloses nothing", completeness)
		}
	}
	for _, completeness := range []float64{0.9, 0.95, 1.0} {
		cr := ConfidenceResult{Confidence: completeness}
		if got := cr.Disclosure(BiasUnderReport); got != "" {
			t.Errorf("completeness %.2f is full fidelity but discloses %q", completeness, got)
		}
	}
}

// ---------------------------------------------------------------------------
// extractFilePath tests
// ---------------------------------------------------------------------------

func TestExtractFilePath(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"file_path key", `{"file_path":"/a/b.go"}`, "/a/b.go"},
		{"target_file key", `{"target_file":"c.go"}`, "c.go"},
		{"file key", `{"file":"d.go"}`, "d.go"},
		{"path key", `{"path":"e.go"}`, "e.go"},
		{"target key", `{"target":"f.go"}`, "f.go"},
		{"priority order", `{"target":"last","file_path":"first"}`, "first"},
		{"empty input", ``, ""},
		{"no matching key", `{"query":"foo"}`, ""},
		{"null value", `{"file_path":null}`, ""},
		{"empty string value", `{"file_path":""}`, ""},
		{"non-object", `"just a string"`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractFilePath([]byte(tc.input))
			if got != tc.want {
				t.Errorf("extractFilePath(%s) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// enrichFileReachability tests (Phase 3 — INV-CKG-SINGLE-CLASS)
// ---------------------------------------------------------------------------

func seedFileForReach(t *testing.T, ci *CodeIndex, filePath string, classes map[string]int, boundEdges, unboundEdges int) {
	t.Helper()
	var firstID int64
	for class, count := range classes {
		for i := 0; i < count; i++ {
			res, err := ci.writerDB.Exec(
				`INSERT INTO symbols (file_path, kind, name, line_start, line_end, reachability_class)
				 VALUES (?, 'function', ?, 1, 1, ?)`,
				filePath, fmt.Sprintf("fn_%s_%d", class, i), class)
			if err != nil {
				t.Fatalf("insert symbol: %v", err)
			}
			id, _ := res.LastInsertId()
			if firstID == 0 {
				firstID = id
			}
		}
	}
	if firstID == 0 {
		return
	}
	// Insert bound call edges (need a target_id).
	for i := 0; i < boundEdges; i++ {
		if _, err := ci.writerDB.Exec(
			`INSERT INTO edges (source_id, target_id, target_name, kind, resolution, source)
			 VALUES (?, ?, 'T', 'call', 'exact', 'tree-sitter')`,
			firstID, firstID); err != nil {
			t.Fatalf("insert bound edge: %v", err)
		}
	}
	// Insert unbound call edges (target_id NULL).
	for i := 0; i < unboundEdges; i++ {
		if _, err := ci.writerDB.Exec(
			`INSERT INTO edges (source_id, target_name, kind, resolution, source)
			 VALUES (?, 'T', 'call', 'unresolved', 'tree-sitter')`,
			firstID); err != nil {
			t.Fatalf("insert unbound edge: %v", err)
		}
	}
}

func TestEnrichFileReachability_DegradedIsolated(t *testing.T) {
	ci, _ := newTestIndex(t)
	seedFileForReach(t, ci, "pkg/bad.go", map[string]int{
		"connected": 2, "isolated": 1,
	}, 0, 0)

	result := &tool.ToolResult{Content: "original"}
	call := tool.ToolCall{Name: "read_file", Input: []byte(`{"file_path":"pkg/bad.go"}`)}
	enrichFileReachability(ci, call, result, nil)

	if !strings.Contains(result.Content, "[File CKG:") {
		t.Errorf("expected CKG warning, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "1 isolated") {
		t.Errorf("expected '1 isolated' in warning, got: %s", result.Content)
	}
	if result.Metadata == nil || result.Metadata["file_reachability"] == nil {
		t.Error("expected file_reachability metadata")
	}
}

func TestEnrichFileReachability_DegradedNameReachable(t *testing.T) {
	ci, _ := newTestIndex(t)
	seedFileForReach(t, ci, "pkg/nr.go", map[string]int{
		"connected": 3, "name_reachable": 2,
	}, 0, 0)

	result := &tool.ToolResult{Content: "ok"}
	call := tool.ToolCall{Name: "find_callers", Input: []byte(`{"file_path":"pkg/nr.go"}`)}
	enrichFileReachability(ci, call, result, nil)

	if !strings.Contains(result.Content, "2 name-only reachable") {
		t.Errorf("expected name-only reachable warning, got: %s", result.Content)
	}
}

func TestEnrichFileReachability_DegradedLowEdgeResolution(t *testing.T) {
	ci, _ := newTestIndex(t)
	// 1 bound + 3 unbound = 25% resolution rate (< 50%).
	seedFileForReach(t, ci, "pkg/lowres.go", map[string]int{
		"connected": 2,
	}, 1, 3)

	result := &tool.ToolResult{Content: "ok"}
	call := tool.ToolCall{Name: "read_file", Input: []byte(`{"file_path":"pkg/lowres.go"}`)}
	enrichFileReachability(ci, call, result, nil)

	if !strings.Contains(result.Content, "edge resolution 25%") {
		t.Errorf("expected edge resolution warning, got: %s", result.Content)
	}
}

func TestEnrichFileReachability_HealthyFile(t *testing.T) {
	ci, _ := newTestIndex(t)
	// All connected, high edge resolution (2 bound / 2 total = 100%).
	seedFileForReach(t, ci, "pkg/good.go", map[string]int{
		"connected": 3,
	}, 2, 0)

	result := &tool.ToolResult{Content: "ok"}
	call := tool.ToolCall{Name: "read_file", Input: []byte(`{"file_path":"pkg/good.go"}`)}
	enrichFileReachability(ci, call, result, nil)

	if strings.Contains(result.Content, "[File CKG:") {
		t.Errorf("healthy file should not be enriched, got: %s", result.Content)
	}
	if result.Metadata != nil {
		t.Errorf("healthy file should have no metadata, got: %v", result.Metadata)
	}
}

func TestEnrichFileReachability_NoFilePathInInput(t *testing.T) {
	ci, _ := newTestIndex(t)
	seedFileForReach(t, ci, "pkg/x.go", map[string]int{"isolated": 1}, 0, 0)

	result := &tool.ToolResult{Content: "ok"}
	call := tool.ToolCall{Name: "read_file", Input: []byte(`{"query":"something"}`)}
	enrichFileReachability(ci, call, result, nil)

	if result.Content != "ok" {
		t.Errorf("no file path → no enrichment, got: %s", result.Content)
	}
}

func TestEnrichFileReachability_ErrorResult(t *testing.T) {
	ci, _ := newTestIndex(t)
	seedFileForReach(t, ci, "pkg/e.go", map[string]int{"isolated": 1}, 0, 0)

	result := &tool.ToolResult{Content: "fail", IsError: true}
	call := tool.ToolCall{Name: "read_file", Input: []byte(`{"file_path":"pkg/e.go"}`)}
	enrichFileReachability(ci, call, result, nil)

	if result.Content != "fail" {
		t.Errorf("error result should not be enriched, got: %s", result.Content)
	}
}

func TestEnrichFileReachability_EmptyFile(t *testing.T) {
	ci, _ := newTestIndex(t)
	// No symbols inserted for this path.
	result := &tool.ToolResult{Content: "ok"}
	call := tool.ToolCall{Name: "read_file", Input: []byte(`{"file_path":"pkg/empty.go"}`)}
	enrichFileReachability(ci, call, result, nil)

	if result.Content != "ok" {
		t.Errorf("empty file should not be enriched, got: %s", result.Content)
	}
}

func TestEnrichFileReachability_AbsolutePathConversion(t *testing.T) {
	ci, _ := newTestIndex(t)
	seedFileForReach(t, ci, "pkg/abs.go", map[string]int{
		"connected": 1, "isolated": 1,
	}, 0, 0)

	result := &tool.ToolResult{Content: "ok"}
	call := tool.ToolCall{
		Name:  "read_file",
		Input: []byte(`{"file_path":"/workspace/project/pkg/abs.go"}`),
	}
	tc := &tool.ToolContext{PrimaryRoot: "/workspace/project"}
	enrichFileReachability(ci, call, result, tc)

	if !strings.Contains(result.Content, "[File CKG: pkg/abs.go") {
		t.Errorf("expected relative path in warning, got: %s", result.Content)
	}
}
