package verification

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestExtractModifiedFiles(t *testing.T) {
	inputs := []json.RawMessage{
		json.RawMessage(`{"path": "/tmp/a.go", "old_string": "x", "new_string": "y"}`),
		json.RawMessage(`{"path": "/tmp/b.go", "content": "package b"}`),
		json.RawMessage(`{"command": "ls"}`),
	}
	names := []string{"edit", "write", "exec"}

	files := ExtractModifiedFiles(inputs, names)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(files), files)
	}
	found := map[string]bool{}
	for _, f := range files {
		found[f] = true
	}
	if !found["/tmp/a.go"] || !found["/tmp/b.go"] {
		t.Errorf("unexpected files: %v", files)
	}
}

func TestExtractModifiedFiles_Dedup(t *testing.T) {
	inputs := []json.RawMessage{
		json.RawMessage(`{"path": "/tmp/a.go", "old_string": "x", "new_string": "y"}`),
		json.RawMessage(`{"path": "/tmp/a.go", "old_string": "p", "new_string": "q"}`),
	}
	names := []string{"edit", "edit"}

	files := ExtractModifiedFiles(inputs, names)
	if len(files) != 1 {
		t.Fatalf("expected 1 file after dedup, got %d", len(files))
	}
}

func TestAffectedPackages(t *testing.T) {
	dir := "/work"
	files := []string{
		"/work/internal/auth/auth.go",
		"/work/internal/auth/token.go",
		"/work/cmd/main.go",
	}

	pkgs := AffectedPackages(files, dir)
	if len(pkgs) != 2 {
		t.Fatalf("expected 2 packages, got %d: %v", len(pkgs), pkgs)
	}
}

func TestHasTestFile(t *testing.T) {
	if !HasTestFile([]string{"foo.go", "foo_test.go"}) {
		t.Error("expected true with _test.go present")
	}
	if HasTestFile([]string{"foo.go", "bar.go"}) {
		t.Error("expected false without _test.go")
	}
}

func TestFormatCompileErrors(t *testing.T) {
	errs := []CompileError{
		{Path: "main.go", Line: 10, Column: 5, Message: "undefined: foo"},
	}
	msg := FormatCompileErrors(errs)
	if msg.Role != "user" {
		t.Error("expected user role")
	}
	text := msg.Content[0].Text
	if !strings.Contains(text, "undefined: foo") {
		t.Error("expected error message in output")
	}
}

func TestFormatHint(t *testing.T) {
	errs := []CompileError{
		{Path: "main.go", Line: 5, Column: 1, Message: "undefined: FooBar"},
		{Path: "main.go", Line: 8, Column: 1, Message: `"fmt" imported and not used`},
	}
	hint := FormatHint(errs)
	if !strings.Contains(hint, "undefined") {
		t.Error("expected hint about undefined symbol")
	}
}

func TestExtractModifiedFiles_ApplyPatch(t *testing.T) {
	patch := "--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new\n--- a/handler.go\n+++ b/handler.go\n@@ -1 +1 @@\n-old\n+new"
	inputs := []json.RawMessage{
		json.RawMessage(`{"patch": "` + strings.ReplaceAll(patch, "\n", "\\n") + `"}`),
	}
	names := []string{"apply_patch"}

	files := ExtractModifiedFiles(inputs, names)
	if len(files) != 2 {
		t.Fatalf("expected 2 files from apply_patch, got %d: %v", len(files), files)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.L0Syntax {
		t.Error("L0Syntax should default to true")
	}
	if !cfg.L1Compile {
		t.Error("L1Compile should default to true")
	}
	if cfg.L2Test != "auto" {
		t.Errorf("L2Test should default to auto, got %q", cfg.L2Test)
	}
	if cfg.L2ExpandDeps {
		t.Error("L2ExpandDeps should default to false")
	}
	if cfg.MaxRevisions.Compile != 3 {
		t.Errorf("MaxRevisions.Compile should be 3, got %d", cfg.MaxRevisions.Compile)
	}
}

func TestHasCorrespondingTestFile(t *testing.T) {
	dir := t.TempDir()
	src := dir + "/service.go"
	test := dir + "/service_test.go"
	os.WriteFile(src, []byte("package x"), 0644)
	os.WriteFile(test, []byte("package x"), 0644)

	if !HasCorrespondingTestFile([]string{src}) {
		t.Error("expected true when corresponding _test.go exists")
	}
	if HasCorrespondingTestFile([]string{dir + "/other.go"}) {
		t.Error("expected false when no corresponding test file")
	}
}

func TestHasCorrespondingTestFile_TypeScript(t *testing.T) {
	dir := t.TempDir()
	src := dir + "/utils.ts"
	test := dir + "/utils.test.ts"
	os.WriteFile(src, []byte("export {}"), 0644)
	os.WriteFile(test, []byte("test()"), 0644)

	if !HasCorrespondingTestFile([]string{src}) {
		t.Error("expected true when .test.ts exists")
	}
}

func TestHasCorrespondingTestFile_Python(t *testing.T) {
	dir := t.TempDir()
	src := dir + "/handler.py"
	test := dir + "/test_handler.py"
	os.WriteFile(src, []byte("pass"), 0644)
	os.WriteFile(test, []byte("pass"), 0644)

	if !HasCorrespondingTestFile([]string{src}) {
		t.Error("expected true when test_*.py exists")
	}
}

func TestIsTestFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"foo_test.go", true},
		{"foo.go", false},
		{"foo.test.ts", true},
		{"foo.spec.ts", true},
		{"foo.ts", false},
		{"test_foo.py", true},
		{"foo_test.py", true},
		{"foo.py", false},
	}
	for _, tt := range tests {
		if got := isTestFile(tt.path); got != tt.want {
			t.Errorf("isTestFile(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestShouldRunTests_ForceL2(t *testing.T) {
	cfg := DefaultConfig()
	gate := NewCodeQualityGate(cfg, t.TempDir(), nil)
	gate.SetForceL2(true)

	if !gate.shouldRunTests([]string{"/tmp/main.go"}) {
		t.Error("expected true when forceL2 is set")
	}
}

func TestShouldRunTests_FixBug(t *testing.T) {
	cfg := DefaultConfig()
	gate := NewCodeQualityGate(cfg, t.TempDir(), nil)
	gate.SetTaskType("fix_bug")

	if !gate.shouldRunTests([]string{"/tmp/main.go"}) {
		t.Error("expected true when taskType is fix_bug")
	}
}

func TestFormatHintWithIndex_FindsSymbol(t *testing.T) {
	finder := &mockFinder{results: []SymbolResult{
		{FilePath: "/project/internal/model/user.go", Name: "UserService", LineStart: 12},
	}}
	errors := []CompileError{
		{Path: "main.go", Line: 5, Column: 1, Message: "undefined: UserService"},
	}
	hint := FormatHintWithIndex(context.Background(), errors, finder)
	if !strings.Contains(hint, "user.go") || !strings.Contains(hint, "line 12") {
		t.Errorf("expected index-driven hint, got: %s", hint)
	}
}

func TestFormatHintWithIndex_NilFinder(t *testing.T) {
	errors := []CompileError{
		{Path: "main.go", Line: 5, Column: 1, Message: "undefined: Foo"},
	}
	hint := FormatHintWithIndex(context.Background(), errors, nil)
	if !strings.Contains(hint, "undefined") {
		t.Errorf("expected fallback hint, got: %s", hint)
	}
}

func TestRelatedChangeHint(t *testing.T) {
	failures := []TestFailure{
		{Package: "internal/auth", TestName: "TestCreate"},
	}
	modified := []string{"/work/internal/auth/service.go"}
	hint := RelatedChangeHint(failures, modified, "/work")
	if !strings.Contains(hint, "service.go") {
		t.Errorf("expected related hint, got: %s", hint)
	}
}

type mockFinder struct {
	results []SymbolResult
}

func (m *mockFinder) FindSymbol(_ context.Context, _ string) ([]SymbolResult, error) {
	return m.results, nil
}

// ── Phase 2: Behavioral Baseline Tests ─────────────────────────────────────

func newTestBaselineStore(t *testing.T) *BaselineStore {
	t.Helper()
	dir := t.TempDir()
	db, err := openTestDB(dir)
	if err != nil {
		t.Fatalf("openTestDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	bs, err := NewBaselineStore(db)
	if err != nil {
		t.Fatalf("NewBaselineStore: %v", err)
	}
	return bs
}

func openTestDB(dir string) (*sql.DB, error) {
	path := dir + "/test-app.db"
	return sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
}

func TestBaselineCapture(t *testing.T) {
	bs := newTestBaselineStore(t)
	ctx := context.Background()

	outcomes := []TestOutcome{
		{Package: "pkg/a", TestName: "TestA1", Status: "pass", Duration: 100},
		{Package: "pkg/a", TestName: "TestA2", Status: "pass", Duration: 200},
		{Package: "pkg/b", TestName: "TestB1", Status: "skip"},
	}

	hash := WorkDirHash("/tmp/project")
	runID, err := bs.SaveRun(ctx, hash, "/tmp/project", []string{"./pkg/a/...", "./pkg/b/..."}, "abc123", "full", outcomes)
	if err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	if runID == 0 {
		t.Fatal("expected non-zero runID")
	}

	latest, err := bs.LatestRun(ctx, hash)
	if err != nil || latest == nil {
		t.Fatalf("LatestRun: %v, %v", latest, err)
	}
	if latest.TotalPass != 2 {
		t.Errorf("TotalPass = %d, want 2", latest.TotalPass)
	}
	if latest.TotalSkip != 1 {
		t.Errorf("TotalSkip = %d, want 1", latest.TotalSkip)
	}
	if latest.GitCommit != "abc123" {
		t.Errorf("GitCommit = %q, want abc123", latest.GitCommit)
	}

	results, err := bs.LoadResults(ctx, runID)
	if err != nil {
		t.Fatalf("LoadResults: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("LoadResults count = %d, want 3", len(results))
	}
}

func TestRegressionDetection(t *testing.T) {
	bs := newTestBaselineStore(t)
	ctx := context.Background()
	hash := WorkDirHash("/tmp/project")

	// Save a passing baseline.
	outcomes := []TestOutcome{
		{Package: "pkg/a", TestName: "TestA1", Status: "pass"},
		{Package: "pkg/a", TestName: "TestA2", Status: "pass"},
		{Package: "pkg/b", TestName: "TestB1", Status: "pass"},
	}
	bs.SaveRun(ctx, hash, "/tmp/project", []string{"./..."}, "", "full", outcomes)

	// Simulate: TestA2 now fails → regression.
	failures := []TestFailure{
		{Package: "pkg/a", TestName: "TestA2", Output: "expected X got Y"},
	}
	regressions, err := DetectRegressions(ctx, bs, hash, failures)
	if err != nil {
		t.Fatalf("DetectRegressions: %v", err)
	}
	if len(regressions) != 1 {
		t.Fatalf("expected 1 regression, got %d", len(regressions))
	}
	if regressions[0].TestName != "TestA2" {
		t.Errorf("regression test = %q, want TestA2", regressions[0].TestName)
	}
	if regressions[0].BaselineStatus != "pass" {
		t.Errorf("baseline status = %q, want pass", regressions[0].BaselineStatus)
	}
}

func TestPreExistingFailure(t *testing.T) {
	bs := newTestBaselineStore(t)
	ctx := context.Background()
	hash := WorkDirHash("/tmp/project")

	// Baseline only has TestA1 passing; TestNewFunc doesn't exist in baseline.
	outcomes := []TestOutcome{
		{Package: "pkg/a", TestName: "TestA1", Status: "pass"},
	}
	bs.SaveRun(ctx, hash, "/tmp/project", []string{"./..."}, "", "full", outcomes)

	// TestNewFunc fails — not in baseline, so not a regression.
	failures := []TestFailure{
		{Package: "pkg/a", TestName: "TestNewFunc", Output: "fail"},
	}
	regressions, err := DetectRegressions(ctx, bs, hash, failures)
	if err != nil {
		t.Fatalf("DetectRegressions: %v", err)
	}
	if len(regressions) != 0 {
		t.Errorf("expected 0 regressions for pre-existing failure, got %d", len(regressions))
	}
}

func TestFlakyExclusion(t *testing.T) {
	bs := newTestBaselineStore(t)
	ctx := context.Background()
	hash := WorkDirHash("/tmp/project")

	// Record 3 flips for TestFlaky.
	for i := 0; i < 3; i++ {
		bs.RecordFlip(ctx, hash, "pkg/a", "TestFlaky")
	}

	if !bs.IsFlaky(ctx, hash, "pkg/a", "TestFlaky") {
		t.Error("expected TestFlaky to be flaky after 3 flips")
	}
	if bs.IsFlaky(ctx, hash, "pkg/a", "TestStable") {
		t.Error("expected TestStable to NOT be flaky")
	}

	// Regression with flaky test should be filtered out.
	regressions := []RegressionInfo{
		{Package: "pkg/a", TestName: "TestFlaky", IsFlaky: true},
		{Package: "pkg/a", TestName: "TestReal", IsFlaky: false},
	}
	filtered := FilterNonFlaky(regressions)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 non-flaky regression, got %d", len(filtered))
	}
	if filtered[0].TestName != "TestReal" {
		t.Errorf("expected TestReal, got %s", filtered[0].TestName)
	}
}

func TestBaselineCleanup(t *testing.T) {
	bs := newTestBaselineStore(t)
	ctx := context.Background()
	hash := WorkDirHash("/tmp/project")

	// Save 15 baseline runs.
	for i := 0; i < 15; i++ {
		outcomes := []TestOutcome{
			{Package: "pkg/a", TestName: "TestA1", Status: "pass"},
		}
		bs.SaveRun(ctx, hash, "/tmp/project", []string{"./..."}, "", "full", outcomes)
	}

	// Cleanup keeps only 10.
	bs.Cleanup(ctx, hash, 10)

	var count int
	bs.db.QueryRowContext(ctx, "SELECT count(*) FROM wc_baseline_runs WHERE work_dir_hash = ?", hash).Scan(&count)
	if count != 10 {
		t.Errorf("expected 10 runs after cleanup, got %d", count)
	}
}

func TestParseJSONTestOutput(t *testing.T) {
	jsonOutput := strings.Join([]string{
		`{"Action":"run","Package":"pkg/a","Test":"TestA1"}`,
		`{"Action":"output","Package":"pkg/a","Test":"TestA1","Output":"=== RUN TestA1\n"}`,
		`{"Action":"pass","Package":"pkg/a","Test":"TestA1","Elapsed":0.1}`,
		`{"Action":"run","Package":"pkg/a","Test":"TestA2"}`,
		`{"Action":"output","Package":"pkg/a","Test":"TestA2","Output":"--- FAIL: TestA2\n"}`,
		`{"Action":"fail","Package":"pkg/a","Test":"TestA2","Elapsed":0.05}`,
		`{"Action":"run","Package":"pkg/b","Test":"TestB1"}`,
		`{"Action":"skip","Package":"pkg/b","Test":"TestB1","Elapsed":0}`,
		`{"Action":"pass","Package":"pkg/a","Elapsed":0.15}`,
	}, "\n")

	outcomes, failures := parseJSONTestOutput(jsonOutput)

	passCount := 0
	failCount := 0
	skipCount := 0
	for _, o := range outcomes {
		switch o.Status {
		case "pass":
			passCount++
		case "fail":
			failCount++
		case "skip":
			skipCount++
		}
	}

	if passCount != 1 {
		t.Errorf("pass count = %d, want 1", passCount)
	}
	if failCount != 1 {
		t.Errorf("fail count = %d, want 1", failCount)
	}
	if skipCount != 1 {
		t.Errorf("skip count = %d, want 1", skipCount)
	}
	if len(failures) != 1 {
		t.Errorf("failures count = %d, want 1", len(failures))
	}
	if len(failures) > 0 && failures[0].TestName != "TestA2" {
		t.Errorf("failure test = %q, want TestA2", failures[0].TestName)
	}
}
