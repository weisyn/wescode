package editengine

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ── NormalizedMatch (Tier 2 fallback for wesgine edit) ───────────────────────

func TestNormalizedMatch_ExactContentReturnsTrue(t *testing.T) {
	content := "func hello() {\n\treturn\n}\n"
	old := "func hello() {\n\treturn\n}"
	start, end, found := NormalizedMatch(content, old)
	if !found {
		t.Fatal("expected match for exact content")
	}
	if content[start:end] != old {
		t.Errorf("matched range %q doesn't equal old %q", content[start:end], old)
	}
}

func TestNormalizedMatch_TrailingWhitespace(t *testing.T) {
	content := "if err != nil {   \n\treturn err   \n}"
	old := "if err != nil {\n\treturn err\n}"

	if strings.Count(content, old) != 0 {
		t.Skip("exact match unexpectedly succeeded")
	}

	start, end, found := NormalizedMatch(content, old)
	if !found {
		t.Fatal("NormalizedMatch should recover from trailing whitespace")
	}
	if start < 0 || end > len(content) {
		t.Fatalf("invalid range: [%d, %d)", start, end)
	}
}

func TestNormalizedMatch_CRLFMismatch(t *testing.T) {
	content := "line1\r\nline2\r\nline3\r\n"
	old := "line1\nline2\nline3\n"

	start, end, found := NormalizedMatch(content, old)
	if !found {
		t.Fatal("NormalizedMatch should recover from CRLF mismatch")
	}
	if start != 0 {
		t.Errorf("expected start=0, got %d", start)
	}
	_ = end
}

func TestNormalizedMatch_ExtraBlankLines(t *testing.T) {
	content := "func a() {}\n\n\n\nfunc b() {}\n"
	old := "func a() {}\n\nfunc b() {}\n"

	_, _, found := NormalizedMatch(content, old)
	if !found {
		t.Fatal("NormalizedMatch should recover from extra blank lines")
	}
}

func TestNormalizedMatch_NonUnique_ReturnsFalse(t *testing.T) {
	content := "x := 1\ny := 1\n"
	old := "1"

	_, _, found := NormalizedMatch(content, old)
	if found {
		t.Fatal("should reject ambiguous match in normalized mode")
	}
}

func TestNormalizedMatch_NotFound_ReturnsFalse(t *testing.T) {
	content := "func hello() {}\n"
	old := "func goodbye() {}"

	_, _, found := NormalizedMatch(content, old)
	if found {
		t.Fatal("should return false for non-matching content")
	}
}

func TestNormalize(t *testing.T) {
	input := "hello   \r\n\r\nworld\n\n\n\nend"
	got := normalize(input)
	want := "hello\n\nworld\n\nend"
	if got != want {
		t.Errorf("normalize:\n got: %q\nwant: %q", got, want)
	}
}

func TestNormalize_TabToSpace(t *testing.T) {
	input := "\tfunc hello() {\n\t\treturn\n\t}\n"
	got := normalize(input)
	want := "    func hello() {\n        return\n    }\n"
	if got != want {
		t.Errorf("normalize tab expansion:\n got: %q\nwant: %q", got, want)
	}
}

func TestNormalizedMatch_TabVsSpace(t *testing.T) {
	content := "\tfunc hello() {\n\t\treturn\n\t}\n"
	old := "    func hello() {\n        return\n    }"

	start, end, found := NormalizedMatch(content, old)
	if !found {
		t.Fatal("NormalizedMatch should recover from tab vs space mismatch")
	}
	if start < 0 || end > len(content) {
		t.Fatalf("invalid range: [%d, %d)", start, end)
	}
}

func TestDetectIndentWidth(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"  a\n  b\n  c\n", 2},
		{"    a\n    b\n", 4},
		{"\ta\n\tb\n", 4},
		{"no indent\n", 4},
	}
	for _, tt := range tests {
		got := detectIndentWidth(tt.input)
		if got != tt.want {
			t.Errorf("detectIndentWidth(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

// ── Observer tracking ────────────────────────────────────────────────────────

func TestTrackModified(t *testing.T) {
	ee := New(nil)

	ee.TrackModified("/tmp/a.go")
	ee.TrackModified("/tmp/b.go")
	ee.TrackModified("/tmp/a.go") // duplicate

	files := ee.ModifiedFiles()
	sort.Strings(files)
	if len(files) != 2 {
		t.Fatalf("want 2 files, got %d: %v", len(files), files)
	}
	if files[0] != "/tmp/a.go" || files[1] != "/tmp/b.go" {
		t.Errorf("unexpected files: %v", files)
	}
}

func TestResetTracking(t *testing.T) {
	ee := New(nil)
	ee.TrackModified("/tmp/a.go")
	ee.ResetTracking()

	files := ee.ModifiedFiles()
	if len(files) != 0 {
		t.Errorf("want 0 files after reset, got %d", len(files))
	}
}

func TestCheckSyntax_NilChecker(t *testing.T) {
	ee := New(nil)
	warnings := ee.CheckSyntax("test.go", []byte("package main"))
	if warnings != nil {
		t.Errorf("expected nil warnings with nil checker, got %v", warnings)
	}
}

func TestCheckSyntax_WithChecker(t *testing.T) {
	checker := func(path string, content []byte) []SyntaxWarning {
		if strings.Contains(string(content), "BROKEN") {
			return []SyntaxWarning{{Line: 2, Column: 0, Message: "syntax error"}}
		}
		return nil
	}

	ee := New(checker)
	warnings := ee.CheckSyntax("test.go", []byte("func BROKEN {{"))
	if len(warnings) == 0 {
		t.Error("expected syntax warnings")
	}
}

// ── Conflict detection ───────────────────────────────────────────────────────

func TestConflictDetection_NoConflict(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/test.go"
	os.WriteFile(file, []byte("package main\n"), 0644)

	ee := New(nil)
	ee.SnapshotFiles([]string{file})

	if ee.CheckConflict(file) {
		t.Error("should not detect conflict when file is unchanged")
	}
}

func TestConflictDetection_ExternalModification(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/test.go"
	os.WriteFile(file, []byte("package main\n"), 0644)

	ee := New(nil)
	ee.SnapshotFiles([]string{file})

	os.WriteFile(file, []byte("package main\n\nfunc New() {}\n"), 0644)

	if !ee.CheckConflict(file) {
		t.Error("should detect conflict after external modification")
	}
}

func TestConflictDetection_UnsnapshotedFile(t *testing.T) {
	ee := New(nil)

	if ee.CheckConflict("/nonexistent/file.go") {
		t.Error("should not detect conflict for unsnapshoted file")
	}
}

func TestConflictDetection_UpdateSnapshot(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/test.go"
	os.WriteFile(file, []byte("package main\n"), 0644)

	ee := New(nil)
	ee.SnapshotFiles([]string{file})

	os.WriteFile(file, []byte("package main\n\nfunc New() {}\n"), 0644)
	ee.UpdateSnapshot(file)

	if ee.CheckConflict(file) {
		t.Error("should not detect conflict after UpdateSnapshot")
	}
}

func TestConflictDetection_ResetClearsHashes(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/test.go"
	os.WriteFile(file, []byte("package main\n"), 0644)

	ee := New(nil)
	ee.SnapshotFiles([]string{file})
	ee.ResetTracking()

	os.WriteFile(file, []byte("changed\n"), 0644)

	if ee.CheckConflict(file) {
		t.Error("should not detect conflict after reset (hashes cleared)")
	}
}

// ── Phase 2: Backup / Accept / Reject ────────────────────────────────────────

// 随 mem/git 双路径删除而删掉的测试（断言的是内部备份槽，不是用户可见行为）：
//
//   - TestBackupBeforeEdit_FirstEditOnly     内存单槽"只记首次"语义
//   - TestBackupBeforeEdit_NonexistentFile   同上，文件不存在时记 nil
//   - TestAcceptEdits_ClearsBackup           断言 Accept 清空备份
//   - TestAcceptEdits_Nil_ClearsAll          同上
//   - TestResetTracking_ClearsBackups        断言 Reset 清空备份
//
// 后三个不只是过时——它们在**守护 EE-14/EE-16 要废除的那个缺陷**：Accept 销毁
// 恢复点、Run 边界清空账本。它们绿着，而 reversibility_test.go 里的 EE-14/EE-16
// 红着，同一个包里两组测试在互相矛盾地作证。

// newTestEditEngineWithCheckpoint 建一个挂了真实恢复点管理器的 EditEngine。
//
// 此前这些测试用 `New(nil)` 加 `BackupBeforeEdit`，走的是 `ensureFallbackCheckpoint`
// 造的内存 manager——那条路径整条删除了，因为它让"有没有退路"取决于项目有没有
// .git（EE-15）。
func newTestEditEngineWithCheckpoint(t *testing.T) (*EditEngine, string) {
	t.Helper()
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	ee := New(nil)
	ee.SetCheckpointManager(NewCheckpointManager(filepath.Join(root, "shadow"), proj, nil))
	return ee, proj
}

func TestRejectEdits_RestoresOriginal(t *testing.T) {
	ee, proj := newTestEditEngineWithCheckpoint(t)
	file := filepath.Join(proj, "test.go")
	original := []byte("package main\n\nfunc Old() {}\n")
	os.WriteFile(file, original, 0644)

	ee.SnapshotFiles([]string{file})
	if err := ee.BeginRun("run-1"); err != nil {
		t.Fatalf("BeginRun: %v", err)
	}

	// Simulate agent edit.
	os.WriteFile(file, []byte("package main\n\nfunc New() {}\n"), 0644)
	ee.UpdateSnapshot(file)

	if err := ee.RejectEdits([]string{file}); err != nil {
		t.Fatalf("reject failed: %v", err)
	}

	content, _ := os.ReadFile(file)
	if string(content) != string(original) {
		t.Errorf("file not restored:\n got: %q\nwant: %q", content, original)
	}
}

func TestRejectEdits_ConflictDetection(t *testing.T) {
	ee, proj := newTestEditEngineWithCheckpoint(t)
	file := filepath.Join(proj, "test.go")
	os.WriteFile(file, []byte("package main\n"), 0644)

	ee.SnapshotFiles([]string{file})
	if err := ee.BeginRun("run-1"); err != nil {
		t.Fatalf("BeginRun: %v", err)
	}

	// Simulate agent edit.
	os.WriteFile(file, []byte("package main\n\nfunc AgentEdit() {}\n"), 0644)
	ee.UpdateSnapshot(file)

	// Simulate user manual edit AFTER agent edit.
	os.WriteFile(file, []byte("package main\n\nfunc UserEdit() {}\n"), 0644)

	err := ee.RejectEdits([]string{file})
	if err == nil {
		t.Fatal("expected ErrFileModifiedByUser, got nil")
	}
	if !strings.Contains(err.Error(), "modified by user") {
		t.Errorf("unexpected error: %v", err)
	}

	// File should NOT be overwritten.
	content, _ := os.ReadFile(file)
	if string(content) != "package main\n\nfunc UserEdit() {}\n" {
		t.Error("file should remain unchanged when conflict detected")
	}
}

func TestRejectEdits_NewFileDeleted(t *testing.T) {
	ee, proj := newTestEditEngineWithCheckpoint(t)
	// 快照需要一个非空工作树：write-tree 对完全空的目录产出空树，
	// 而"快照里没有这个路径"与"快照本身是空的"在断言上不可区分。
	os.WriteFile(filepath.Join(proj, "seed.go"), []byte("package main\n"), 0644)

	if err := ee.BeginRun("run-1"); err != nil {
		t.Fatalf("BeginRun: %v", err)
	}

	// Simulate agent creating the file.
	file := filepath.Join(proj, "new_file.go")
	os.WriteFile(file, []byte("package main\n"), 0644)

	// Reject should delete the file (restore "did not exist" state).
	if err := ee.RejectEdits([]string{file}); err != nil {
		t.Fatalf("reject failed: %v", err)
	}

	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("file should not exist after rejecting a new-file write")
	}
}
