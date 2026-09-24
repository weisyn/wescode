package editengine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/weisyn/wesgine/tool"
)

// DiffHunk represents a single contiguous change region within a file.
type DiffHunk struct {
	OldStart int    `json:"oldStart"`
	OldEnd   int    `json:"oldEnd"`
	OldText  string `json:"oldText"`
	NewText  string `json:"newText"`
}

// ComputeHunksFromEditParams extracts diff hunks from an edit tool's parameters.
// The edit tool uses old_string/new_string semantics; we read the file to locate
// the exact line number of old_string for accurate inline diff positioning.
// fp is optional; if nil, os.ReadFile is used.
func ComputeHunksFromEditParams(params json.RawMessage, workDir string, fp ...tool.FileProvider) (path string, hunks []DiffHunk, ok bool) {
	var p struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Path == "" {
		return "", nil, false
	}
	if workDir != "" && !filepath.IsAbs(p.Path) {
		p.Path = filepath.Join(workDir, p.Path)
	}
	if p.OldString == "" && p.NewString == "" {
		return p.Path, nil, false
	}

	var fileProv tool.FileProvider
	if len(fp) > 0 {
		fileProv = fp[0]
	}
	startLine := findOldStringLine(p.Path, p.OldString, fileProv)
	oldLines := countLines(p.OldString)
	hunks = []DiffHunk{{
		OldStart: startLine,
		OldEnd:   startLine + oldLines,
		OldText:  p.OldString,
		NewText:  p.NewString,
	}}
	return p.Path, hunks, true
}

// findOldStringLine reads the file and returns the 1-based line number where
// oldString starts. Returns 1 as fallback if the file cannot be read or the
// string is not found.
func findOldStringLine(path, oldString string, fp tool.FileProvider) int {
	var raw []byte
	var err error
	if fp != nil {
		raw, err = fp.ReadFile(context.Background(), path)
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return 1
	}
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	needle := strings.ReplaceAll(oldString, "\r\n", "\n")
	idx := strings.Index(content, needle)
	if idx < 0 {
		return 1
	}
	return strings.Count(content[:idx], "\n") + 1
}

// ComputeHunksFromWriteParamsWithDiff extracts file path, diff hunks, and new-file flag from a write tool's parameters.
// also returns diff hunks by comparing the existing file content with the new content.
func ComputeHunksFromWriteParamsWithDiff(params json.RawMessage, workDir string, fp ...tool.FileProvider) (path string, hunks []DiffHunk, isNew bool, ok bool) {
	var p struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Path == "" {
		return "", nil, false, false
	}
	if workDir != "" && !filepath.IsAbs(p.Path) {
		p.Path = filepath.Join(workDir, p.Path)
	}

	var oldContent string
	var readErr error
	if len(fp) > 0 && fp[0] != nil {
		rc, err := fp[0].Open(context.Background(), p.Path)
		if err == nil {
			raw, _ := io.ReadAll(rc)
			rc.Close()
			oldContent = string(raw)
		}
		readErr = err
	} else {
		raw, err := os.ReadFile(p.Path)
		if err == nil {
			oldContent = string(raw)
		}
		readErr = err
	}

	if os.IsNotExist(readErr) {
		// New file — single hunk with all content as addition
		hunks = []DiffHunk{{
			OldStart: 1,
			OldEnd:   1,
			OldText:  "",
			NewText:  p.Content,
		}}
		return p.Path, hunks, true, true
	}

	if readErr != nil {
		return p.Path, nil, false, true
	}

	// Existing file — produce hunks only for reasonably sized files (≤200 lines)
	// to avoid massive diff payloads for full-file writes.
	oldContent = strings.ReplaceAll(oldContent, "\r\n", "\n")
	newContent := strings.ReplaceAll(p.Content, "\r\n", "\n")
	if oldContent == newContent {
		return p.Path, nil, false, true
	}
	oldLineCount := countLines(oldContent)
	newLineCount := countLines(newContent)
	if oldLineCount > 200 || newLineCount > 200 {
		return p.Path, nil, false, true
	}
	hunks = []DiffHunk{{
		OldStart: 1,
		OldEnd:   oldLineCount + 1,
		OldText:  oldContent,
		NewText:  newContent,
	}}
	return p.Path, hunks, false, true
}

// PatchFilePaths extracts candidate file paths from a unified diff's +++ headers.
// Handles the "+++ b/path", "+++ a/path" and bare "+++ path" header forms,
// strips trailing tab metadata, skips /dev/null, and de-duplicates. Returns nil
// when the patch contains no usable headers.
//
// This is the single shared implementation of patch-header path extraction.
// Every consumer (preview builder, postwrite index, prewrite constraint check,
// verification affected-file analysis) must use it — duplicated per-package
// parsers drifted apart and produced phantom paths for apply_patch calls.
func PatchFilePaths(patch string) []string {
	var paths []string
	seen := make(map[string]struct{})
	for _, line := range strings.Split(patch, "\n") {
		if !strings.HasPrefix(line, "+++ ") {
			continue
		}
		path := strings.TrimPrefix(line, "+++ ")
		path = strings.TrimSpace(path)
		if idx := strings.Index(path, "\t"); idx >= 0 {
			path = path[:idx]
		}
		path = strings.TrimPrefix(path, "b/")
		if path == "" || path == "/dev/null" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

// ResolvePatchPath resolves an apply_patch header path using the SAME rules the
// engine's apply_patch tool applies at execution time: absolute paths pass
// through unchanged; relative paths join against the tool call's base_dir
// parameter when present, falling back to the caller-supplied workDir.
//
// The engine honors apply_patch's "base_dir" parameter; consumers that joined
// against the workspace root alone derived paths that never existed on disk
// (e.g. wesclaw.git/wescodeServerChannel.ts when base_dir pointed at
// wescode.git/editor/...), which surfaced as the editor's "file not found"
// popup and failed CKG index stats.
func ResolvePatchPath(path, baseDir, workDir string) string {
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	base := baseDir
	if base == "" {
		base = workDir
	}
	if base != "" {
		return filepath.Clean(filepath.Join(base, path))
	}
	return filepath.Clean(path)
}

// OutputFilePaths returns the absolute file paths a tool result reports as
// written/modified, read from result.Metadata["output_files"]. These are the
// engine's canonical, post-execution resolved paths (write/edit/apply_patch
// tools emit them with the exact paths they operated on, including apply_patch
// base_dir resolution). Returns nil when absent or malformed.
//
// Consumers must prefer these paths over re-deriving from tool params: the
// params only carry the model's raw input (relative paths + optional base_dir),
// which downstream layers historically re-joined with their own root and
// produced phantom paths.
func OutputFilePaths(result *tool.ToolResult) []string {
	if result == nil || result.Metadata == nil {
		return nil
	}
	files, ok := result.Metadata["output_files"].([]map[string]any)
	if !ok {
		return nil
	}
	var paths []string
	for _, f := range files {
		if p, ok := f["path"].(string); ok && p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// ComputePathsFromPatchParams extracts the list of file paths from apply_patch
// tool parameters. The patch parameter is a unified diff; we extract paths from
// --- a/ and +++ b/ headers.
func ComputePathsFromPatchParams(params json.RawMessage, workDir string) (paths []string, ok bool) {
	var p struct {
		Patch   string `json:"patch"`
		BaseDir string `json:"base_dir"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Patch == "" {
		return nil, false
	}
	seen := make(map[string]bool)
	// apply_patch honors the base_dir parameter; the preview must resolve
	// against it (fallback: workDir) exactly like the engine's tool does,
	// otherwise multi-root / base_dir patches produce phantom paths.
	for _, raw := range PatchFilePaths(p.Patch) {
		path := ResolvePatchPath(raw, p.BaseDir, workDir)
		if !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	return paths, len(paths) > 0
}

// NewTxID generates a short random transaction identifier.
func NewTxID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}
