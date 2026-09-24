package codeintel

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"

	"github.com/weisyn/wescode/internal/editengine"
	"github.com/weisyn/wesgine/tool"
)

// NewPostWriteIndex returns a PostCallHook that immediately indexes files
// after write/edit/apply_patch, eliminating the FileWatcher 2s polling delay.
// The next PreWriteCheck call sees up-to-date CKG data (INV-HOOK-02).
// WorkDir is read from ToolContext at call time (not captured at boot)
// so it correctly resolves paths when the workspace changes per-run.
func NewPostWriteIndex(ci *CodeIndex) tool.PostCallHook {
	return func(ctx context.Context, call tool.ToolCall, result *tool.ToolResult, tc *tool.ToolContext) {
		if result == nil || result.IsError {
			return
		}
		if ci == nil {
			return
		}
		if call.Name != "write" && call.Name != "edit" && call.Name != "apply_patch" {
			return
		}

		workDir := ""
		if tc != nil {
			workDir = tc.PrimaryRoot
			if workDir == "" {
				workDir = tc.WorkDir
			}
		}
		paths := extractWrittenPaths(call, result, workDir)
		for _, p := range paths {
			if err := ci.IndexFile(ctx, p); err != nil {
				slog.Debug("[postwrite] index failed", "path", p, "error", err)
			}
		}
	}
}

// extractWrittenPaths pulls the file paths a tool call actually wrote.
// The engine's canonical resolved paths (result.Metadata["output_files"]) are
// the single authority: write/edit/apply_patch tools emit the exact paths they
// operated on, including apply_patch's base_dir resolution. Params are only
// parsed as a fallback when the result carries no metadata — and even then,
// apply_patch paths must resolve against base_dir (when present), never a
// naive workspace-root join, which produced phantom paths like
// wesclaw.git/wescodeServerChannel.ts for patches whose base_dir pointed into
// wescode.git.
func extractWrittenPaths(call tool.ToolCall, result *tool.ToolResult, workDir string) []string {
	if paths := editengine.OutputFilePaths(result); len(paths) > 0 {
		return paths
	}
	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		if workDir != "" {
			return filepath.Join(workDir, p)
		}
		return p
	}

	switch call.Name {
	case "write", "edit":
		var params struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(call.Input, &params) == nil && params.Path != "" {
			return []string{resolve(params.Path)}
		}
	case "apply_patch":
		var params struct {
			Patch   string `json:"patch"`
			BaseDir string `json:"base_dir"`
		}
		if json.Unmarshal(call.Input, &params) == nil && params.Patch != "" {
			raw := editengine.PatchFilePaths(params.Patch)
			resolved := make([]string, len(raw))
			for i, p := range raw {
				resolved[i] = editengine.ResolvePatchPath(p, params.BaseDir, workDir)
			}
			return resolved
		}
	}
	return nil
}
