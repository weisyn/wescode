package bench

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func datasetPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// backend/internal/bench/types_test.go → backend/ → backend/tests/bench/dataset
	backendDir := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	p := filepath.Join(backendDir, "tests", "bench", "dataset")
	if info, err := os.Stat(p); err != nil || !info.IsDir() {
		t.Fatalf("dataset dir %s not found: %v", p, err)
	}
	return p
}

func TestLoadDataset_ExampleCorpus(t *testing.T) {
	dir := datasetPath(t)
	cases, err := LoadDataset(dir)
	if err != nil {
		// LoadDataset returns a soft error for skipped files; only fail
		// the test if that error came alongside zero valid cases.
		if len(cases) == 0 {
			t.Fatalf("LoadDataset: %v", err)
		}
		t.Logf("LoadDataset warning (non-fatal): %v", err)
	}
	if len(cases) < 3 {
		t.Fatalf("expected >=3 cases, got %d", len(cases))
	}

	// Verify each known type is present.
	seen := map[CaseType]bool{}
	for _, c := range cases {
		if c.ID == "" {
			t.Errorf("case with empty ID loaded: %+v", c)
		}
		seen[c.Type] = true
	}
	for _, want := range []CaseType{CaseTypeSWE, CaseTypeTerminal, CaseTypeQnA} {
		if !seen[want] {
			t.Errorf("dataset missing at least one %q case", want)
		}
	}
}

func TestFilters(t *testing.T) {
	cases := []Case{
		{ID: "a-swe", Type: CaseTypeSWE},
		{ID: "b-term", Type: CaseTypeTerminal},
		{ID: "c-qna", Type: CaseTypeQnA},
	}
	if got := FilterByID(cases, "b-term"); len(got) != 1 || got[0].ID != "b-term" {
		t.Errorf("FilterByID: got %+v", got)
	}
	if got := FilterByID(cases, "does-not-exist"); len(got) != 0 {
		t.Errorf("FilterByID(miss): got %+v", got)
	}
	if got := FilterByType(cases, CaseTypeSWE); len(got) != 1 || got[0].ID != "a-swe" {
		t.Errorf("FilterByType: got %+v", got)
	}
}

func TestReport_ComputeScores(t *testing.T) {
	r := &Report{
		Agent:       "unit-test",
		RunsPerCase: 2,
	}
	cases := []Case{
		{ID: "s1", Type: CaseTypeSWE},
		{ID: "s2", Type: CaseTypeSWE},
		{ID: "t1", Type: CaseTypeTerminal},
	}
	r.SetCaseTypes(cases)
	r.Results = []Result{
		{CaseID: "s1", RunIndex: 0, Score: 1.0},
		{CaseID: "s1", RunIndex: 1, Score: 1.0}, // avg 1.0
		{CaseID: "s2", RunIndex: 0, Score: 0.0},
		{CaseID: "s2", RunIndex: 1, Score: 0.0}, // avg 0.0
		{CaseID: "t1", RunIndex: 0, Score: 1.0},
		{CaseID: "t1", RunIndex: 1, Score: 0.0}, // avg 0.5
	}
	r.ComputeScores()

	if r.SWEScore != 0.5 {
		t.Errorf("SWEScore: want 0.5, got %v", r.SWEScore)
	}
	if r.TerminalScore != 0.5 {
		t.Errorf("TerminalScore: want 0.5, got %v", r.TerminalScore)
	}
	if r.QnAScore != -1 {
		t.Errorf("QnAScore: want -1 (N/A), got %v", r.QnAScore)
	}
	// Composite = mean(SWE, Terminal) × 100 = 50.
	if r.CompositeScore != 50 {
		t.Errorf("CompositeScore: want 50, got %v", r.CompositeScore)
	}
}

func TestJudgeByTest_ExitCode(t *testing.T) {
	ctx := context.Background()
	workdir := t.TempDir()

	if !JudgeByTest(ctx, workdir, "true") {
		t.Error("JudgeByTest(true) expected pass")
	}
	if JudgeByTest(ctx, workdir, "false") {
		t.Error("JudgeByTest(false) expected fail")
	}
	if JudgeByTest(ctx, workdir, "") {
		t.Error("JudgeByTest(empty) expected fail (no cmd)")
	}
}

func TestPrepareWorkdir_NoFixture(t *testing.T) {
	c := Case{ID: "x", Type: CaseTypeSWE}
	wd, cleanup, err := PrepareWorkdir(c)
	if err != nil {
		t.Fatalf("PrepareWorkdir: %v", err)
	}
	if cleanup {
		defer os.RemoveAll(wd)
	}
	if info, err := os.Stat(wd); err != nil || !info.IsDir() {
		t.Fatalf("workdir not created: %v", err)
	}
	if !strings.Contains(wd, "wescode-bench-x-") {
		t.Errorf("workdir name should include sanitised case id, got %q", wd)
	}
}

func TestPrepareWorkdir_DirFixture(t *testing.T) {
	// Build a source fixture tree.
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "seed.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Manufacture a fake case whose sourcePath points at a yaml next to
	// the fixture directory so PrepareWorkdir's relative-path resolution
	// finds it.
	yamlPath := filepath.Join(src, "case.yaml")
	if err := os.WriteFile(yamlPath, []byte("id: dir-fixture\ntype: swe\nprompt: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixtureDir := filepath.Join(src, "fixture")
	if err := os.MkdirAll(fixtureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDir, "hello.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := Case{
		ID:         "dir-fixture",
		Type:       CaseTypeSWE,
		Fixture:    "fixture",
		sourcePath: yamlPath,
	}
	wd, cleanup, err := PrepareWorkdir(c)
	if err != nil {
		t.Fatalf("PrepareWorkdir: %v", err)
	}
	if cleanup {
		defer os.RemoveAll(wd)
	}

	content, err := os.ReadFile(filepath.Join(wd, "hello.txt"))
	if err != nil {
		t.Fatalf("fixture not copied: %v", err)
	}
	if string(content) != "world" {
		t.Errorf("fixture content: want %q, got %q", "world", string(content))
	}
}
