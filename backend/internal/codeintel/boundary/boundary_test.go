package boundary

import (
	"os"
	"path/filepath"
	"testing"
)

// ── Bug 1: Symlink detection must work via Lstat ──

func TestClassifyFile_SymlinkExcluded(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.go")
	link := filepath.Join(dir, "link.go")

	os.WriteFile(real, []byte("package main\n"), 0644)
	os.Symlink(real, link)

	info, _ := os.Stat(link) // follows symlink — ModeSymlink NOT set
	c := NewClassifier(ModeModerate, nil)
	v := c.ClassifyFile(link, info, nil)

	if v.Pipeline != PipelineExcluded || v.Reason != ReasonSymlink {
		t.Errorf("symlink should be excluded, got Pipeline=%s Reason=%s", v.Pipeline, v.Reason)
	}
}

// ── Bug 2: Dot-prefix directories must be skipped ──

func TestShouldSkipDir_DotPrefixCatchall(t *testing.T) {
	c := NewClassifier(ModeModerate, nil)

	tests := []struct {
		name   string
		expect bool
	}{
		{".env_backup", true},
		{".custom_cache", true},
		{".internal", true},
		{".hidden", true},
		{".", false},  // current dir — never skip
		{"..", false}, // parent dir — never skip
		{"src", false},
		{"internal", false},
	}

	for _, tt := range tests {
		skip, _ := c.ShouldSkipDir(tt.name, tt.name)
		if skip != tt.expect {
			t.Errorf("ShouldSkipDir(%q) = %v, want %v", tt.name, skip, tt.expect)
		}
	}
}

// ── Bug 3: .wescodeignore paths must be relative to workspace root ──

func TestClassifyFile_WescodeignoreRelativePath(t *testing.T) {
	dir := t.TempDir()
	wsRoot := dir
	subDir := filepath.Join(dir, "editor", "src")
	os.MkdirAll(subDir, 0755)
	target := filepath.Join(subDir, "main.ts")
	os.WriteFile(target, []byte("console.log('hi');\n"), 0644)

	rules := parseIgnoreFile("editor/")
	c := NewClassifier(ModeModerate, nil)
	c.SetWorkDir(wsRoot)
	c.SetWescodeignore(rules)

	info, _ := os.Stat(target)
	v := c.ClassifyFile(target, info, nil)

	if v.Pipeline != PipelineExcluded || v.Reason != ReasonWescodeignore {
		t.Errorf("file under editor/ should be excluded by .wescodeignore, got Pipeline=%s Reason=%s", v.Pipeline, v.Reason)
	}
}

// ── Bug 4: Fast-only patterns must not apply in Moderate/Full ──

func TestClassifyFile_MockFilesIndexedInModerate(t *testing.T) {
	dir := t.TempDir()
	mockFile := filepath.Join(dir, "mock_service.go")
	os.WriteFile(mockFile, []byte("package mock\n"), 0644)

	info, _ := os.Stat(mockFile)
	detectGo := func(p string) (string, bool) {
		if filepath.Ext(p) == ".go" {
			return "go", true
		}
		return "", false
	}

	// Fast mode: mock should be excluded
	fast := NewClassifier(ModeFast, nil)
	vFast := fast.ClassifyFile(mockFile, info, detectGo)
	if vFast.Pipeline != PipelineExcluded {
		t.Errorf("fast mode: mock file should be excluded, got Pipeline=%s", vFast.Pipeline)
	}

	// Moderate mode: mock should be indexed (CKG needs TESTS edges)
	moderate := NewClassifier(ModeModerate, nil)
	vMod := moderate.ClassifyFile(mockFile, info, detectGo)
	if vMod.Pipeline != PipelineCKG {
		t.Errorf("moderate mode: mock file should be in CKG, got Pipeline=%s Reason=%s", vMod.Pipeline, vMod.Reason)
	}
}

// ── Bug 5: LICENSE files should use ReasonLayer1Metadata, not Generated ──

func TestClassifyFile_LicenseUsesMetadataReason(t *testing.T) {
	dir := t.TempDir()
	lic := filepath.Join(dir, "LICENSE")
	os.WriteFile(lic, []byte("MIT License\n"), 0644)

	info, _ := os.Stat(lic)
	c := NewClassifier(ModeModerate, nil)
	v := c.ClassifyFile(lic, info, nil)

	if v.Reason != ReasonLayer1Metadata {
		t.Errorf("LICENSE should have reason %q, got %q", ReasonLayer1Metadata, v.Reason)
	}
}

// ── Safety Core: must be unskippable even with .wescodeignore ──

func TestShouldSkipDir_SafetyCoreCannotBeOverridden(t *testing.T) {
	c := NewClassifier(ModeFull, nil) // Full mode = least restrictive
	c.SetWescodeignore(EmptyIgnoreRules())

	for _, dir := range []string{".git", "node_modules", ".worktrees"} {
		skip, reason := c.ShouldSkipDir(dir, dir)
		if !skip {
			t.Errorf("safety core dir %q should always be skipped", dir)
		}
		if reason != ReasonSafetyCore {
			t.Errorf("safety core dir %q reason = %q, want %q", dir, reason, ReasonSafetyCore)
		}
	}
}

// ── Three-mode behavior: fast skips more than moderate ──

func TestShouldSkipDir_ModeGradient(t *testing.T) {
	dirs := map[string]struct {
		fastSkip     bool
		moderateSkip bool
		fullSkip     bool
	}{
		"node_modules": {true, true, true},    // safety core — always
		"vendor":       {true, true, true},    // alwaysSkip
		"third_party":  {true, true, false},   // moderateSkip
		"docs":         {true, false, false},  // fastSkip only
		"src":          {false, false, false}, // never skip
	}

	for name, expect := range dirs {
		fast := NewClassifier(ModeFast, nil)
		mod := NewClassifier(ModeModerate, nil)
		full := NewClassifier(ModeFull, nil)

		skipFast, _ := fast.ShouldSkipDir(name, name)
		skipMod, _ := mod.ShouldSkipDir(name, name)
		skipFull, _ := full.ShouldSkipDir(name, name)

		if skipFast != expect.fastSkip {
			t.Errorf("Fast  ShouldSkipDir(%q) = %v, want %v", name, skipFast, expect.fastSkip)
		}
		if skipMod != expect.moderateSkip {
			t.Errorf("Moderate ShouldSkipDir(%q) = %v, want %v", name, skipMod, expect.moderateSkip)
		}
		if skipFull != expect.fullSkip {
			t.Errorf("Full ShouldSkipDir(%q) = %v, want %v", name, skipFull, expect.fullSkip)
		}
	}
}

// ── Routing: code → CKG, prose → Knowledge, config → L3 ──

func TestClassifyFile_RoutingPipeline(t *testing.T) {
	dir := t.TempDir()

	files := map[string]struct {
		content  string
		pipeline Pipeline
	}{
		"main.go":     {"package main\n", PipelineCKG},
		"README.md":   {"# Hello\n", PipelineKnowledge},
		"config.yaml": {"key: val\n", PipelineL3},
		"data.json":   {`{"a":1}`, PipelineL3},
		"script.sh":   {"#!/bin/bash\n", PipelineL3},
	}

	detectLang := func(p string) (string, bool) {
		if filepath.Ext(p) == ".go" {
			return "go", true
		}
		return "", false
	}

	c := NewClassifier(ModeModerate, nil)

	for name, tt := range files {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte(tt.content), 0644)
		info, _ := os.Stat(path)
		v := c.ClassifyFile(path, info, detectLang)
		if v.Pipeline != tt.pipeline {
			t.Errorf("ClassifyFile(%q) pipeline = %s, want %s (reason=%s)",
				name, v.Pipeline, tt.pipeline, v.Reason)
		}
	}
}

// ── Content heuristics: minified detection ──

func TestClassifyFileContent_Minified(t *testing.T) {
	c := NewClassifier(ModeModerate, nil)

	// 15K chars on a single line = minified
	bigLine := make([]byte, 15000)
	for i := range bigLine {
		bigLine[i] = 'x'
	}
	r := c.ClassifyFileContent("bundle.js", bigLine)
	if r == nil || *r != ReasonMinified {
		t.Error("15K single-line content should be detected as minified")
	}

	// Normal multiline code = not minified
	normal := []byte("func main() {\n\tfmt.Println(\"hello\")\n}\n")
	r = c.ClassifyFileContent("main.go", normal)
	if r != nil {
		t.Errorf("normal code should not be minified, got reason=%s", *r)
	}
}

// ── JSON blacklist ──

func TestClassifyFile_JSONBlacklist(t *testing.T) {
	dir := t.TempDir()

	blacklisted := []string{"package.json", "tsconfig.json", "openapi.json"}
	allowed := []string{"custom.json", "data.json"}

	c := NewClassifier(ModeModerate, nil)

	for _, name := range blacklisted {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte(`{"key":"val"}`), 0644)
		info, _ := os.Stat(path)
		v := c.ClassifyFile(path, info, nil)
		if v.Pipeline != PipelineExcluded {
			t.Errorf("blacklisted JSON %q should be excluded, got %s", name, v.Pipeline)
		}
	}

	for _, name := range allowed {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte(`{"key":"val"}`), 0644)
		info, _ := os.Stat(path)
		v := c.ClassifyFile(path, info, nil)
		if v.Pipeline == PipelineExcluded {
			t.Errorf("non-blacklisted JSON %q should NOT be excluded", name)
		}
	}
}
