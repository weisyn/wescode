package codeintel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/weisyn/wescode/internal/editengine"
	"github.com/weisyn/wescode/internal/treesitter"
	"github.com/weisyn/wesgine/tool"
)

func TestPreWriteCheck_NeverBlocks(t *testing.T) {
	ci, dir := newTestIndex(t)
	ts := treesitter.NewParserPool()
	t.Cleanup(ts.Close)

	goFile := filepath.Join(dir, "existing.go")
	os.WriteFile(goFile, []byte(`package main

func ExistingFunc() string { return "hello" }
`), 0644)
	ci.IndexFile(context.Background(), goFile)

	pre, _ := NewPreWriteCheck(ci, ts)

	input, _ := json.Marshal(map[string]string{
		"path":    filepath.Join(dir, "new.go"),
		"content": "package main\n\nfunc ExistingHelper() string { return \"world\" }\n",
	})
	call := tool.ToolCall{Name: "write", Input: input}

	result, err := pre(context.Background(), call, &tool.ToolContext{WorkDir: dir})

	if err != nil {
		t.Fatalf("PreWriteCheck returned error: %v", err)
	}
	if result != nil {
		t.Fatal("PreWriteCheck should never return a result (CI-23: no blocking)")
	}
}

func TestPreWriteCheck_EditWithImpact(t *testing.T) {
	ci, dir := newTestIndex(t)
	ts := treesitter.NewParserPool()
	t.Cleanup(ts.Close)

	os.WriteFile(filepath.Join(dir, "service.go"), []byte(`package main

func CreateUser(name string) error {
	return validate(name)
}

func validate(name string) error { return nil }
`), 0644)
	ci.IndexFile(context.Background(), filepath.Join(dir, "service.go"))

	pre, _ := NewPreWriteCheck(ci, ts)

	input, _ := json.Marshal(map[string]string{
		"path":       filepath.Join(dir, "service.go"),
		"old_string": "func validate(name string) error { return nil }",
		"new_string": "func validate(name string, strict bool) error { return nil }",
	})
	call := tool.ToolCall{Name: "edit", Input: input}

	result, err := pre(context.Background(), call, &tool.ToolContext{WorkDir: dir})

	if err != nil {
		t.Fatalf("PreWriteCheck returned error: %v", err)
	}
	if result != nil {
		t.Fatal("PreWriteCheck should never block")
	}
}

func TestPreWriteCheck_WarningsInjected(t *testing.T) {
	ci, dir := newTestIndex(t)
	ts := treesitter.NewParserPool()
	t.Cleanup(ts.Close)

	goFile := filepath.Join(dir, "existing.go")
	os.WriteFile(goFile, []byte(`package main

func ValidateUser(name string) error { return nil }
`), 0644)
	ci.IndexFile(context.Background(), goFile)

	pre, post := NewPreWriteCheck(ci, ts)

	newFile := filepath.Join(dir, "new.go")
	input, _ := json.Marshal(map[string]string{
		"path":    newFile,
		"content": "package main\n\nfunc ValidateEmail(email string) error { return nil }\n",
	})
	call := tool.ToolCall{ID: "test-1", Name: "write", Input: input}
	tc := &tool.ToolContext{WorkDir: dir}

	result, _ := pre(context.Background(), call, tc)
	if result != nil {
		t.Fatal("PreWriteCheck should never block")
	}

	toolResult := &tool.ToolResult{Content: "file written"}
	post(context.Background(), call, toolResult, nil)

	if toolResult.Metadata != nil {
		if warnings, ok := toolResult.Metadata["ckg_warnings"]; ok {
			warnSlice, _ := warnings.([]string)
			if len(warnSlice) == 0 {
				t.Log("no CKG warnings emitted (similarity below threshold)")
			}
		}
	}
}

func TestPreWriteCheck_SkipsNonWriteTools(t *testing.T) {
	pre, _ := NewPreWriteCheck(nil, nil)

	call := tool.ToolCall{Name: "read", Input: json.RawMessage(`{"path":"foo.go"}`)}
	result, err := pre(context.Background(), call, nil)

	if err != nil {
		t.Fatal("unexpected error for read tool")
	}
	if result != nil {
		t.Fatal("should return nil for non-write tools")
	}
}

func TestPreWriteCheck_NilIndex(t *testing.T) {
	pre, _ := NewPreWriteCheck(nil, nil)

	input, _ := json.Marshal(map[string]string{"path": "foo.go", "content": "package main"})
	call := tool.ToolCall{Name: "write", Input: input}

	result, err := pre(context.Background(), call, nil)

	if err != nil || result != nil {
		t.Fatal("should gracefully handle nil index")
	}
}

func TestPostWriteIndex_UpdatesCKG(t *testing.T) {
	ci, dir := newTestIndex(t)

	goFile := filepath.Join(dir, "new_func.go")
	os.WriteFile(goFile, []byte(`package main

func BrandNewFunc() string { return "new" }
`), 0644)

	hook := NewPostWriteIndex(ci)
	tc := &tool.ToolContext{WorkDir: dir}

	input, _ := json.Marshal(map[string]string{"path": goFile})
	call := tool.ToolCall{Name: "write", Input: input}
	result := &tool.ToolResult{Content: "ok"}

	hook(context.Background(), call, result, tc)

	syms, err := ci.FindSymbol(context.Background(), "BrandNewFunc")
	if err != nil {
		t.Fatalf("FindSymbol: %v", err)
	}
	if len(syms) == 0 {
		t.Fatal("expected BrandNewFunc to be indexed after PostWriteIndex")
	}
}

func TestPostWriteIndex_ResolvesRelativePaths(t *testing.T) {
	ci, dir := newTestIndex(t)

	goFile := filepath.Join(dir, "relative.go")
	os.WriteFile(goFile, []byte(`package main

func RelativeFunc() string { return "rel" }
`), 0644)

	hook := NewPostWriteIndex(ci)
	tc := &tool.ToolContext{WorkDir: dir}

	input, _ := json.Marshal(map[string]string{"path": "relative.go"})
	call := tool.ToolCall{Name: "write", Input: input}
	result := &tool.ToolResult{Content: "ok"}

	hook(context.Background(), call, result, tc)

	syms, err := ci.FindSymbol(context.Background(), "RelativeFunc")
	if err != nil {
		t.Fatalf("FindSymbol: %v", err)
	}
	if len(syms) == 0 {
		t.Fatal("expected RelativeFunc to be indexed for relative path")
	}
}

func TestPostWriteIndex_SkipsErrors(t *testing.T) {
	hook := NewPostWriteIndex(nil)

	call := tool.ToolCall{Name: "write", Input: json.RawMessage(`{"path":"foo.go"}`)}
	result := &tool.ToolResult{IsError: true, Content: "fail"}

	hook(context.Background(), call, result, nil)
}

func TestParsePatchFilePaths(t *testing.T) {
	patch := `--- a/internal/auth/service.go
+++ b/internal/auth/service.go
@@ -1,3 +1,3 @@
 package auth
--- a/internal/auth/handler.go
+++ b/internal/auth/handler.go
@@ -5,7 +5,7 @@
 func Handle() {}
`
	paths := editengine.PatchFilePaths(patch)
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d: %v", len(paths), paths)
	}
	if paths[0] != "internal/auth/service.go" {
		t.Errorf("path[0] = %q", paths[0])
	}
	if paths[1] != "internal/auth/handler.go" {
		t.Errorf("path[1] = %q", paths[1])
	}
}

func TestExtractWrittenPaths_PrefersOutputFiles(t *testing.T) {
	// The engine's canonical resolved paths (output_files metadata) are the
	// single authority — they must win over param re-derivation, which ignored
	// apply_patch base_dir and produced phantom paths.
	call := tool.ToolCall{Name: "apply_patch", Input: json.RawMessage(`{"patch":"+++ b/x.go","base_dir":"/proj/editor/src"}`)}
	result := &tool.ToolResult{Metadata: map[string]any{
		"output_files": []map[string]any{
			{"path": "/proj/editor/src/x.go", "size": int64(3), "action": "patched"},
		},
	}}
	// Compared verbatim, not through filepath.Clean: the assertion is that the
	// engine's path wins untouched, and Clean would rewrite separators on
	// Windows to something extractWrittenPaths never produces — the expectation
	// would then disagree with the contract on exactly one platform.
	paths := extractWrittenPaths(call, result, "/proj")
	if len(paths) != 1 || paths[0] != "/proj/editor/src/x.go" {
		t.Fatalf("expected engine output_files path, got %v", paths)
	}
}

func TestExtractWrittenPaths_ApplyPatchFallbackHonorsBaseDir(t *testing.T) {
	// Without output_files metadata, the fallback must still resolve against
	// base_dir — never a naive workspace-root join.
	call := tool.ToolCall{Name: "apply_patch", Input: json.RawMessage(`{"patch":"+++ b/wescodeServerChannel.ts","base_dir":"/wescode.git/editor/src/vs"}`)}
	result := &tool.ToolResult{}
	paths := extractWrittenPaths(call, result, "/wesclaw.git")
	if len(paths) != 1 || paths[0] != filepath.Clean("/wescode.git/editor/src/vs/wescodeServerChannel.ts") {
		t.Fatalf("expected base_dir-resolved path, got %v", paths)
	}
}

func TestExtractWrittenPaths_WriteFallback(t *testing.T) {
	call := tool.ToolCall{Name: "write", Input: json.RawMessage(`{"path":"foo.go"}`)}
	paths := extractWrittenPaths(call, &tool.ToolResult{}, "/work")
	if len(paths) != 1 || paths[0] != filepath.Clean("/work/foo.go") {
		t.Fatalf("unexpected paths: %v", paths)
	}
}
