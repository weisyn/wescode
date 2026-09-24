package verification

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/weisyn/wescode/internal/codeintel/langs"
	"github.com/weisyn/wescode/internal/editengine"
)

func ExtractModifiedFiles(toolCallInputs []json.RawMessage, toolNames []string) []string {
	seen := make(map[string]struct{})
	var files []string

	for i, name := range toolNames {
		if name != "edit" && name != "write" && name != "apply_patch" {
			continue
		}
		if i >= len(toolCallInputs) {
			continue
		}
		if name == "apply_patch" {
			// apply_patch input is {"patch": "...", "base_dir": "..."} without a
			// single path field. Parse file paths from the unified diff headers
			// and resolve them exactly like the engine's apply_patch tool: against
			// base_dir when present, else keep the header-relative path.
			var params struct {
				Patch   string `json:"patch"`
				BaseDir string `json:"base_dir"`
			}
			if err := json.Unmarshal(toolCallInputs[i], &params); err != nil {
				continue
			}
			for _, raw := range editengine.PatchFilePaths(params.Patch) {
				path := editengine.ResolvePatchPath(raw, params.BaseDir, "")
				if _, ok := seen[path]; ok {
					continue
				}
				seen[path] = struct{}{}
				files = append(files, path)
			}
			continue
		}
		var params struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(toolCallInputs[i], &params); err != nil {
			continue
		}
		path := strings.TrimSpace(params.Path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		files = append(files, path)
	}
	return files
}

// HasTestFile returns true if any of the modified files is itself a test file.
func HasTestFile(modifiedFiles []string) bool {
	for _, f := range modifiedFiles {
		if isTestFile(f) {
			return true
		}
	}
	return false
}

// HasCorrespondingTestFile checks whether any modified source file has a
// corresponding test file on disk (by language convention). This triggers L2
// even when the test file itself wasn't modified.
//
// Supported conventions:
//   - Go:         foo.go        → foo_test.go
//   - TypeScript: foo.ts        → foo.test.ts, foo.spec.ts
//   - JavaScript: foo.js        → foo.test.js, foo.spec.js
//   - Python:     foo.py        → test_foo.py, foo_test.py
//   - Rust:       (inline tests, no separate file — always returns false)
func HasCorrespondingTestFile(modifiedFiles []string) bool {
	for _, f := range modifiedFiles {
		if isTestFile(f) {
			continue
		}
		for _, candidate := range testFileCandidates(f) {
			if _, err := os.Stat(candidate); err == nil {
				return true
			}
		}
	}
	return false
}

func testFileCandidates(path string) []string {
	dir := path[:strings.LastIndex(path, "/")+1]
	base := path[strings.LastIndex(path, "/")+1:]

	switch {
	case strings.HasSuffix(base, ".go"):
		stem := strings.TrimSuffix(base, ".go")
		return []string{dir + stem + "_test.go"}

	case strings.HasSuffix(base, ".ts") && !strings.HasSuffix(base, ".test.ts") && !strings.HasSuffix(base, ".spec.ts"):
		stem := strings.TrimSuffix(base, ".ts")
		return []string{
			dir + stem + ".test.ts",
			dir + stem + ".spec.ts",
		}

	case strings.HasSuffix(base, ".tsx") && !strings.HasSuffix(base, ".test.tsx") && !strings.HasSuffix(base, ".spec.tsx"):
		stem := strings.TrimSuffix(base, ".tsx")
		return []string{
			dir + stem + ".test.tsx",
			dir + stem + ".spec.tsx",
		}

	case strings.HasSuffix(base, ".js") && !strings.HasSuffix(base, ".test.js") && !strings.HasSuffix(base, ".spec.js"):
		stem := strings.TrimSuffix(base, ".js")
		return []string{
			dir + stem + ".test.js",
			dir + stem + ".spec.js",
		}

	case strings.HasSuffix(base, ".py") && !strings.HasPrefix(base, "test_") && !strings.HasSuffix(base, "_test.py"):
		stem := strings.TrimSuffix(base, ".py")
		return []string{
			dir + "test_" + base,
			dir + stem + "_test.py",
		}
	}
	return nil
}

func isTestFile(path string) bool {
	base := path[strings.LastIndex(path, "/")+1:]

	// Try registry-driven test file detection
	reg := langs.Default()
	if reg != nil {
		ext := strings.ToLower(filepath.Ext(path))
		if cfg := reg.ByExtension(ext); cfg != nil {
			for _, pattern := range cfg.Language.TestFilePatterns {
				if matched, _ := filepath.Match(pattern, base); matched {
					return true
				}
			}
			return false
		}
	}

	// Hardcoded fallback for unregistered languages
	switch {
	case strings.HasSuffix(base, "_test.go"):
		return true
	case strings.HasSuffix(base, ".test.ts"), strings.HasSuffix(base, ".spec.ts"):
		return true
	case strings.HasSuffix(base, ".test.tsx"), strings.HasSuffix(base, ".spec.tsx"):
		return true
	case strings.HasSuffix(base, ".test.js"), strings.HasSuffix(base, ".spec.js"):
		return true
	case strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py"):
		return true
	case strings.HasSuffix(base, "_test.py"):
		return true
	}
	return false
}
