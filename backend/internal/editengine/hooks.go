package editengine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/weisyn/wesgine/tool"
)

// resolveToolPath joins a relative path with the project root from ToolContext.
// PrimaryRoot (user's project) is preferred over WorkDir (engine artifact dir).
func resolveToolPath(path string, tc *tool.ToolContext) string {
	if tc == nil || filepath.IsAbs(path) {
		return path
	}
	if tc.PrimaryRoot != "" {
		return filepath.Join(tc.PrimaryRoot, path)
	}
	if tc.WorkDir != "" {
		return filepath.Join(tc.WorkDir, path)
	}
	return path
}

// PreCallHook returns a tool.PreCallHook that runs before every edit/write/apply_patch
// tool call. It performs:
//   - EditPatrol safety checks (binary file rejection, sensitive path warning)
//   - In non-git repos, memory backup fallback (INV-EDIT-21)
func (e *EditEngine) PreCallHook() tool.PreCallHook {
	return func(_ context.Context, call tool.ToolCall, tc *tool.ToolContext) (*tool.ToolResult, error) {
		if call.Name != "edit" && call.Name != "write" && call.Name != "apply_patch" {
			return nil, nil
		}

		var targetPaths []string
		if call.Name == "apply_patch" {
			var params struct {
				Patch string `json:"patch"`
			}
			if err := json.Unmarshal(call.Input, &params); err != nil {
				slog.Warn("[editengine] unmarshal apply_patch input failed", "error", err)
			}
			targetPaths = PatchFilePaths(params.Patch)
		} else {
			var params struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(call.Input, &params); err != nil {
				slog.Warn("[editengine] unmarshal edit tool input failed", "tool", call.Name, "error", err)
			}
			if params.Path != "" {
				targetPaths = []string{params.Path}
			}
		}

		// EditPatrol: binary file detection — reject edits to binary files.
		for _, path := range targetPaths {
			absPath := resolveToolPath(path, tc)
			if isBinaryFile(absPath, e.readFile) {
				return &tool.ToolResult{
					Content: fmt.Sprintf("Error: %s is a binary file — editing binary files is not supported.", path),
					IsError: true,
				}, nil
			}
		}

		// EditPatrol: sensitive path warning — warn but allow.
		for _, path := range targetPaths {
			if isSensitivePath(path) {
				// Warning only — does not block. Agent sees the warning in result.
				// The actual edit proceeds; wesgine Governance DenyPaths handles hard blocks.
				break
			}
		}

		// 不做逐文件备份：恢复点是 BeginRun 时对整棵工作树的快照（EE-15），
		// 覆盖本轮会碰到的每个文件，包括 PreCall 阶段还不知道的那些
		// （apply_patch 的隐含目标、工具内部决定要改的相邻文件）。
		//
		// 此前这里按 `cm.IsGitRepo()` 分岔：git 跳过、非 git 存内存。那个分岔
		// 是 EE-11 文档与代码脱节的来源——文档说"PreCallHook 在 edit 前创建
		// 快照"，而 git 仓库下这个函数什么都没做。
		return nil, nil
	}
}

// isBinaryFile checks if the file contains null bytes (indicating binary content).
// Reads up to 8KB. Returns false if file cannot be read (fail-open).
func isBinaryFile(path string, readFn func(string) ([]byte, error)) bool {
	data, err := readFn(path)
	if err != nil {
		return false
	}
	checkLen := len(data)
	if checkLen > 8192 {
		checkLen = 8192
	}
	for i := 0; i < checkLen; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

// sensitivePatterns are file name patterns that trigger a warning before edit.
var sensitivePatterns = []string{
	".env", ".env.local", ".env.production",
	"config.yaml", "config.yml", "config.json",
	"credentials", "secrets",
	".htpasswd", ".pgpass",
}

// isSensitivePath returns true if the path matches known sensitive file patterns.
func isSensitivePath(path string) bool {
	base := filepath.Base(path)
	for _, pattern := range sensitivePatterns {
		if base == pattern || strings.HasSuffix(base, pattern) {
			return true
		}
	}
	return false
}

// PostCallHook returns a tool.PostCallHook that runs after every edit/write/apply_patch
// tool call. It performs three functions:
//
//  1. Tracks modified files (for QualityGate to know what changed)
//  2. Runs L0 syntax check and appends warnings to tool_result
//  3. Records evidence metadata (before_hash / after_hash) for audit trail
//
// The hook only activates for successful filesystem-modifying tool calls.
func (e *EditEngine) PostCallHook() tool.PostCallHook {
	return func(_ context.Context, call tool.ToolCall, result *tool.ToolResult, tc *tool.ToolContext) {
		if call.Name != "edit" && call.Name != "write" && call.Name != "apply_patch" {
			return
		}
		e.Metrics.RecordTool(call.Name)
		if result.IsError {
			return
		}

		paths := OutputFilePaths(result)
		if len(paths) == 0 {
			if call.Name == "apply_patch" {
				var params struct {
					Patch string `json:"patch"`
				}
				if err := json.Unmarshal(call.Input, &params); err != nil {
					slog.Warn("[editengine] unmarshal apply_patch input in post-hook failed", "error", err)
				}
				paths = PatchFilePaths(params.Patch)
			} else {
				var params struct {
					Path string `json:"path"`
				}
				if err := json.Unmarshal(call.Input, &params); err != nil {
					slog.Warn("[editengine] unmarshal edit tool input in post-hook failed", "tool", call.Name, "error", err)
				}
				if params.Path != "" {
					paths = []string{params.Path}
				}
			}
		}

		action := "modified"

		// G2: sensitive path warning — inform agent after successful write.
		for _, path := range paths {
			if isSensitivePath(path) {
				result.Content += fmt.Sprintf(
					"\n⚠ Sensitive file modified: %s — verify this change is intentional and does not expose secrets.", path)
			}
		}

		// G3: mass deletion detection — warn when edit removes >200 lines without adding
		if call.Name == "edit" {
			var editParams struct {
				OldString string `json:"old_string"`
				NewString string `json:"new_string"`
			}
			if json.Unmarshal(call.Input, &editParams) == nil {
				oldLines := strings.Count(editParams.OldString, "\n") + 1
				newLines := strings.Count(editParams.NewString, "\n") + 1
				if oldLines > 200 && newLines < oldLines/2 {
					result.Content += fmt.Sprintf(
						"\n⚠ Mass deletion: this edit removes %d lines and adds only %d. Verify this is intentional.",
						oldLines, newLines)
				}
			}
		}

		var evidenceFiles []map[string]any

		for _, path := range paths {
			// Capture before_hash from snapshot (taken at Run start or last UpdateSnapshot).
			e.mu.Lock()
			beforeHash := e.fileHashes[path]
			e.mu.Unlock()

			if e.CheckConflict(path) {
				result.Content += "\n⚠ File conflict: " + path +
					" was modified externally since session start. The edit was applied but may overwrite external changes."
			}
			e.TrackModified(path)
			e.UpdateSnapshot(path)
			e.RecordFileChange(path, action, "")

			// Multi-agent conflict detection: record this edit's byte range and
			// check for overlap with edits from other agents in the same Cell.
			if tc != nil && tc.AgentID != "" {
				matchStart, matchEnd := e.Metrics.LastMatchRange()
				if matchEnd > matchStart {
					if conflict := e.conflict.RecordEdit(tc.AgentID, tc.SessionID, path, matchStart, matchEnd); conflict != nil {
						result.Content += "\n" + FormatConflict(conflict)
					}
				}
			}

			// Re-index the edited file synchronously, before the tool result
			// returns. This is what makes "edit then query" coherent within one
			// turn — there is no query-time overlay to fall back on, so the
			// production DB has to be current by the time the model sees the
			// result. Making this async reintroduces the read-your-own-write gap
			// that the (removed) Shadow Graph overlay existed to paper over.
			e.mu.Lock()
			notifier := e.ckgNotifier
			e.mu.Unlock()
			if notifier != nil {
				notifier(path)
			}

			// Capture after_hash from newly updated snapshot.
			e.mu.Lock()
			afterHash := e.fileHashes[path]
			e.mu.Unlock()

			evidenceFiles = append(evidenceFiles, map[string]any{
				"path":        path,
				"before_hash": beforeHash,
				"after_hash":  afterHash,
				"match_level": e.Metrics.LastTier(),
			})

			if e.checker == nil {
				continue
			}
			content, err := e.readFile(path)
			if err != nil {
				continue
			}
			warnings := e.checker(path, content)
			if len(warnings) > 0 {
				var msgs []string
				for _, w := range warnings {
					msgs = append(msgs, fmt.Sprintf("  line %d:%d: %s", w.Line+1, w.Column, w.Message))
				}
				result.Content += "\n⚠ Syntax warnings (" + path + "):\n" + strings.Join(msgs, "\n")
			}
		}

		if len(evidenceFiles) > 0 {
			if result.Metadata == nil {
				result.Metadata = make(map[string]any)
			}
			result.Metadata["edit_evidence"] = evidenceFiles
		}
	}
}
