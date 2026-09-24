package editengine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weisyn/wesgine/tool"
)

// ── NormalizedMatch wrapped as tool.MatchFallback ─────────────────────────────

func TestNormalizedMatch_ImplementsMatchFallback(t *testing.T) {
	var fn tool.MatchFallback = func(_, content, old string) (int, int, bool) {
		return NormalizedMatch(content, old)
	}
	content := "func foo() {}   \n"
	old := "func foo() {}\n"

	start, end, found := fn("test.go", content, old)
	if !found {
		t.Fatal("NormalizedMatch should recover trailing whitespace diff")
	}
	if start < 0 || end > len(content) {
		t.Fatalf("invalid range: [%d, %d)", start, end)
	}
}

// ── PostCallHook integration ─────────────────────────────────────────────────

func TestPostCallHook_TracksEditFiles(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "test.go")
	os.WriteFile(file, []byte("package main\n"), 0644)

	ee := New(nil)
	hook := ee.PostCallHook()

	input, _ := json.Marshal(map[string]string{"path": file})
	call := tool.ToolCall{Name: "edit", Input: input}
	result := &tool.ToolResult{Content: "Modified test.go"}

	hook(context.Background(), call, result, nil)

	files := ee.ModifiedFiles()
	if len(files) != 1 || files[0] != file {
		t.Errorf("expected tracked file %s, got %v", file, files)
	}
}

func TestPostCallHook_IgnoresNonEditTools(t *testing.T) {
	ee := New(nil)
	hook := ee.PostCallHook()

	input, _ := json.Marshal(map[string]string{"path": "/tmp/test.go"})
	call := tool.ToolCall{Name: "read", Input: input}
	result := &tool.ToolResult{Content: "file content"}

	hook(context.Background(), call, result, nil)

	if len(ee.ModifiedFiles()) != 0 {
		t.Error("read tool should not be tracked")
	}
}

func TestPostCallHook_IgnoresErrors(t *testing.T) {
	ee := New(nil)
	hook := ee.PostCallHook()

	input, _ := json.Marshal(map[string]string{"path": "/tmp/test.go"})
	call := tool.ToolCall{Name: "edit", Input: input}
	result := &tool.ToolResult{Content: "edit failed", IsError: true}

	hook(context.Background(), call, result, nil)

	if len(ee.ModifiedFiles()) != 0 {
		t.Error("failed edit should not be tracked")
	}
}

func TestPostCallHook_AppendsSyntaxWarnings(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "test.go")
	os.WriteFile(file, []byte("func BROKEN {{\n"), 0644)

	checker := func(path string, content []byte) []SyntaxWarning {
		if strings.Contains(string(content), "BROKEN") {
			return []SyntaxWarning{{Line: 0, Column: 5, Message: "expected '}'"}}
		}
		return nil
	}

	ee := New(checker)
	hook := ee.PostCallHook()

	input, _ := json.Marshal(map[string]string{"path": file})
	call := tool.ToolCall{Name: "edit", Input: input}
	result := &tool.ToolResult{Content: "Modified test.go"}

	hook(context.Background(), call, result, nil)

	if !strings.Contains(result.Content, "Syntax warnings") {
		t.Errorf("expected syntax warnings appended, got: %s", result.Content)
	}
}

func TestPostCallHook_TracksWriteTool(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "new.go")
	os.WriteFile(file, []byte("package main\n"), 0644)

	ee := New(nil)
	hook := ee.PostCallHook()

	input, _ := json.Marshal(map[string]string{"path": file})
	call := tool.ToolCall{Name: "write", Input: input}
	result := &tool.ToolResult{Content: "Created new.go"}

	hook(context.Background(), call, result, nil)

	files := ee.ModifiedFiles()
	if len(files) != 1 {
		t.Errorf("expected 1 tracked file, got %d", len(files))
	}
}

func TestPostCallHook_TracksApplyPatch(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "test.go")
	os.WriteFile(file, []byte("package main\n"), 0644)

	ee := New(nil)
	hook := ee.PostCallHook()

	// Real apply_patch schema uses "patch" field with unified diff, not "path".
	patch := "--- a/test.go\n+++ b/" + file + "\n@@ -1 +1 @@\n-old\n+new\n"
	input, _ := json.Marshal(map[string]string{"patch": patch})
	call := tool.ToolCall{Name: "apply_patch", Input: input}
	result := &tool.ToolResult{Content: "Applied 1 file(s)"}

	hook(context.Background(), call, result, nil)

	files := ee.ModifiedFiles()
	if len(files) != 1 || files[0] != file {
		t.Errorf("expected tracked file %s, got %v", file, files)
	}
}
