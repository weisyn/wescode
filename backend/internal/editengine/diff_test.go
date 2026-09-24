package editengine

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPatchFilePaths_HeaderForms(t *testing.T) {
	patch := `--- a/wescodeBackendService.ts
+++ b/wescodeServerChannel.ts
@@ -7,7 +7,6 @@
 import * as fs from 'fs';
--- a/handler.go
+++ handler.go
@@ -1 +1 @@
-old
+new
--- a/x.go
+++ b/x.go	 (newline metadata)
@@ -1 +1 @@
-o
+n`
	paths := PatchFilePaths(patch)
	if len(paths) != 3 {
		t.Fatalf("expected 3 paths, got %d: %v", len(paths), paths)
	}
	if paths[0] != "wescodeServerChannel.ts" || paths[1] != "handler.go" || paths[2] != "x.go" {
		t.Errorf("unexpected paths: %v", paths)
	}
}

func TestPatchFilePaths_SkipsDevNullAndDedupes(t *testing.T) {
	patch := `+++ b/a.go
+++ b/a.go
+++ /dev/null`
	paths := PatchFilePaths(patch)
	if len(paths) != 1 || paths[0] != "a.go" {
		t.Fatalf("expected only deduped a.go, got %v", paths)
	}
}

// absTestPath builds a path filepath.IsAbs accepts on both platforms.
//
// A leading separator is not sufficient: on Windows `\abs\x.go` is rooted but
// drive-relative, so IsAbs reports false. Fixtures built that way silently test
// the wrong branch — the absolute-passthrough assertion below ends up measuring
// the base_dir join instead, which is what the previous "leading-separator
// roots are absolute on both platforms" comment asserted and got wrong.
func absTestPath(elem ...string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(append([]string{`C:\`}, elem...)...)
	}
	return filepath.Join(append([]string{string(filepath.Separator)}, elem...)...)
}

func TestResolvePatchPath_HonorsBaseDir(t *testing.T) {
	// Regression: the engine's apply_patch resolves headers against the
	// call's base_dir. Consumers that joined against the workspace root alone
	// produced phantom paths (wesclaw.git/wescodeServerChannel.ts).
	base := absTestPath("src", "editor", "vs", "platform", "wescode", "electron-main")
	work := absTestPath("ws", "wesclaw.git")
	got := ResolvePatchPath("wescodeServerChannel.ts", base, work)
	want := filepath.Join(base, "wescodeServerChannel.ts")
	if got != want {
		t.Fatalf("ResolvePatchPath = %q, want %q", got, want)
	}
}

func TestResolvePatchPath_Priority(t *testing.T) {
	absFile := absTestPath("abs", "x.go")
	absBase := absTestPath("base")
	absWork := absTestPath("work")

	// Vacuity guard: if the fixture is not absolute, the assertion below passes
	// through the join branch and proves nothing about passthrough.
	if !filepath.IsAbs(absFile) {
		t.Fatalf("fixture %q is not absolute: the passthrough branch is not exercised", absFile)
	}

	// absolute passthrough
	if got := ResolvePatchPath(absFile, "base", "work"); got != absFile {
		t.Errorf("abs passthrough: got %q", got)
	}
	// base_dir beats workDir
	if got := ResolvePatchPath("x.go", absBase, absWork); got != filepath.Join(absBase, "x.go") {
		t.Errorf("base_dir priority: got %q", got)
	}
	// no base_dir → workDir
	if got := ResolvePatchPath("x.go", "", absWork); got != filepath.Join(absWork, "x.go") {
		t.Errorf("workDir fallback: got %q", got)
	}
	// neither → relative passthrough
	if got := ResolvePatchPath("x.go", "", ""); got != "x.go" {
		t.Errorf("relative passthrough: got %q", got)
	}
}

func TestComputePathsFromPatchParams_HonorsBaseDir(t *testing.T) {
	params, _ := json.Marshal(map[string]any{
		"base_dir": "/proj/editor/src",
		"patch":    "--- a/wescodeServerChannel.ts\n+++ b/wescodeServerChannel.ts\n@@ -1 +1 @@\n-o\n+n",
	})
	paths, ok := ComputePathsFromPatchParams(params, "/proj")
	if !ok {
		t.Fatal("expected ok")
	}
	if len(paths) != 1 || paths[0] != filepath.Clean("/proj/editor/src/wescodeServerChannel.ts") {
		t.Fatalf("unexpected paths: %v", paths)
	}
}
