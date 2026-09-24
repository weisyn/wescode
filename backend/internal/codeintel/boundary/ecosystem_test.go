package boundary

import (
	"os"
	"path/filepath"
	"testing"
)

// ecosystemCase defines a language ecosystem test scenario.
// Each case constructs a realistic project structure and verifies that
// the boundary classifier correctly excludes non-user files while
// allowing user source code and documents to pass through.
type ecosystemCase struct {
	name string
	// dirs to create (relative to temp root)
	dirs []string
	// files to create: path → content
	files map[string]string
	// expected directory skip results: dir name → should skip
	expectDirSkip map[string]bool
	// expected file verdicts: relative path → pipeline
	expectFile map[string]Pipeline
}

// detectMultiLang is a test detectLang callback that covers the 13 tree-sitter languages.
func detectMultiLang(path string) (string, bool) {
	ext := filepath.Ext(path)
	switch ext {
	case ".go":
		return "go", true
	case ".ts":
		return "typescript", true
	case ".tsx":
		return "tsx", true
	case ".js", ".mjs", ".cjs":
		return "javascript", true
	case ".py":
		return "python", true
	case ".rs":
		return "rust", true
	case ".java":
		return "java", true
	case ".kt", ".kts":
		return "kotlin", true
	case ".c":
		return "c", true
	case ".cpp", ".cc", ".cxx":
		return "cpp", true
	case ".cs":
		return "c_sharp", true
	case ".rb":
		return "ruby", true
	case ".php":
		return "php", true
	case ".swift":
		return "swift", true
	}
	return "", false
}

func runEcosystemTest(t *testing.T, tc ecosystemCase) {
	t.Helper()
	root := t.TempDir()

	for _, d := range tc.dirs {
		os.MkdirAll(filepath.Join(root, d), 0755)
	}
	for relPath, content := range tc.files {
		full := filepath.Join(root, relPath)
		os.MkdirAll(filepath.Dir(full), 0755)
		os.WriteFile(full, []byte(content), 0644)
	}

	c := NewClassifier(ModeModerate, nil)
	c.SetWorkDir(root)

	for dirName, expectSkip := range tc.expectDirSkip {
		skip, reason := c.ShouldSkipDir(dirName, dirName)
		if skip != expectSkip {
			t.Errorf("[%s] ShouldSkipDir(%q) = %v (reason=%s), want %v",
				tc.name, dirName, skip, reason, expectSkip)
		}
	}

	for relPath, expectPipeline := range tc.expectFile {
		full := filepath.Join(root, relPath)
		info, err := os.Stat(full)
		if err != nil {
			t.Errorf("[%s] stat %q: %v", tc.name, relPath, err)
			continue
		}
		v := c.ClassifyFile(full, info, detectMultiLang)
		if v.Pipeline != expectPipeline {
			t.Errorf("[%s] ClassifyFile(%q) = %s (reason=%s), want %s",
				tc.name, relPath, v.Pipeline, v.Reason, expectPipeline)
		}
	}
}

// ── Go Ecosystem ──

func TestEcosystem_Go(t *testing.T) {
	runEcosystemTest(t, ecosystemCase{
		name: "Go",
		dirs: []string{"vendor/github.com/pkg", "cmd/server", "internal/handler", "bin"},
		files: map[string]string{
			"go.mod":                     "module example.com/app\ngo 1.22\n",
			"go.sum":                     "github.com/pkg/errors v0.9.1 h1:abc=\n",
			"main.go":                    "package main\nfunc main() {}\n",
			"cmd/server/main.go":         "package main\n",
			"internal/handler/user.go":   "package handler\n",
			"vendor/github.com/pkg/a.go": "package pkg\n",
			"README.md":                  "# My App\n",
			"config.yaml":                "port: 8080\n",
		},
		expectDirSkip: map[string]bool{
			"vendor":   true,  // always-skip
			"cmd":      false, // user code
			"internal": false, // user code
			"bin":      false, // not in alwaysSkip (moderate mode)
		},
		expectFile: map[string]Pipeline{
			"main.go":                  PipelineCKG,
			"cmd/server/main.go":       PipelineCKG,
			"internal/handler/user.go": PipelineCKG,
			"go.sum":                   PipelineExcluded, // lock file
			"README.md":                PipelineKnowledge,
			"config.yaml":              PipelineL3,
			"go.mod":                   PipelineL3,
		},
	})
}

// ── Node / TypeScript Ecosystem ──

func TestEcosystem_NodeTS(t *testing.T) {
	runEcosystemTest(t, ecosystemCase{
		name: "NodeTS",
		dirs: []string{"src/components", "dist", "coverage"},
		files: map[string]string{
			"package.json":           `{"name":"app","dependencies":{}}`,
			"package-lock.json":      `{"lockfileVersion":3}`,
			"tsconfig.json":          `{"compilerOptions":{}}`,
			"src/index.ts":           "export const main = () => {};\n",
			"src/components/App.tsx": "export default function App() { return <div/>; }\n",
			"src/utils.js":           "module.exports = {};\n",
			"dist/bundle.js":         "!function(){}();\n",
			"README.md":              "# App\n",
			".eslintrc.json":         `{"extends":"eslint:recommended"}`,
			"jest.config.json":       `{"testEnvironment":"node"}`,
		},
		expectDirSkip: map[string]bool{
			"node_modules": true,  // safety core
			"dist":         true,  // always-skip
			"coverage":     true,  // always-skip
			"src":          false, // user code
		},
		expectFile: map[string]Pipeline{
			"src/index.ts":           PipelineCKG,
			"src/components/App.tsx": PipelineCKG,
			"src/utils.js":           PipelineCKG,
			"package.json":           PipelineExcluded, // JSON blacklist
			"package-lock.json":      PipelineExcluded, // lock file
			"tsconfig.json":          PipelineExcluded, // JSON blacklist
			".eslintrc.json":         PipelineExcluded, // JSON blacklist
			"jest.config.json":       PipelineExcluded, // JSON blacklist
			"README.md":              PipelineKnowledge,
		},
	})
}

// ── Python Ecosystem ──

func TestEcosystem_Python(t *testing.T) {
	runEcosystemTest(t, ecosystemCase{
		name: "Python",
		dirs: []string{"src/app", "tests"},
		files: map[string]string{
			"pyproject.toml":      "[project]\nname = \"app\"\n",
			"poetry.lock":         "[[package]]\nname = \"requests\"\n",
			"src/app/main.py":     "def main(): pass\n",
			"src/app/__init__.py": "",
			"tests/test_main.py":  "def test_main(): pass\n",
			"requirements.txt":    "requests>=2.28\n",
			"README.md":           "# App\n",
		},
		expectDirSkip: map[string]bool{
			"__pycache__":   true,
			".venv":         true,
			"venv":          true,
			".mypy_cache":   true,
			".pytest_cache": true,
			".tox":          true,
			".nox":          true,
			"site-packages": true,
			"src":           false,
			"tests":         false,
		},
		expectFile: map[string]Pipeline{
			"src/app/main.py":    PipelineCKG,
			"tests/test_main.py": PipelineCKG,
			"poetry.lock":        PipelineExcluded, // lock file
			"requirements.txt":   PipelineL3,       // .txt but notDocumentTxt → L3
			"pyproject.toml":     PipelineL3,
			"README.md":          PipelineKnowledge,
		},
	})
}

// ── Rust Ecosystem ──

func TestEcosystem_Rust(t *testing.T) {
	runEcosystemTest(t, ecosystemCase{
		name: "Rust",
		dirs: []string{"src"},
		files: map[string]string{
			"Cargo.toml":  "[package]\nname = \"app\"\n",
			"Cargo.lock":  "[[package]]\nname = \"serde\"\n",
			"src/main.rs": "fn main() {}\n",
			"src/lib.rs":  "pub fn add(a: i32, b: i32) -> i32 { a + b }\n",
			"README.md":   "# Rust App\n",
		},
		expectDirSkip: map[string]bool{
			"target": true, // always-skip (Rust build output)
			".cargo": true, // always-skip
			"src":    false,
		},
		expectFile: map[string]Pipeline{
			"src/main.rs": PipelineCKG,
			"src/lib.rs":  PipelineCKG,
			"Cargo.lock":  PipelineExcluded, // lock file
			"Cargo.toml":  PipelineL3,
			"README.md":   PipelineKnowledge,
		},
	})
}

// ── Java / Kotlin Ecosystem ──

func TestEcosystem_JavaKotlin(t *testing.T) {
	runEcosystemTest(t, ecosystemCase{
		name: "JavaKotlin",
		dirs: []string{"src/main/java/com/app", "src/main/kotlin"},
		files: map[string]string{
			"pom.xml":                         "<project></project>\n",
			"src/main/java/com/app/Main.java": "package com.app;\npublic class Main {}\n",
			"src/main/kotlin/App.kt":          "fun main() {}\n",
			"README.md":                       "# Java App\n",
		},
		expectDirSkip: map[string]bool{
			"target":  true, // always-skip (Maven output)
			".gradle": true, // always-skip
			".m2":     true, // always-skip
			"src":     false,
		},
		expectFile: map[string]Pipeline{
			"src/main/java/com/app/Main.java": PipelineCKG,
			"src/main/kotlin/App.kt":          PipelineCKG,
			"README.md":                       PipelineKnowledge,
			"pom.xml":                         PipelineL3,
		},
	})
}

// ── C / C++ Ecosystem ──

func TestEcosystem_CPP(t *testing.T) {
	runEcosystemTest(t, ecosystemCase{
		name: "CPP",
		dirs: []string{"src", "include"},
		files: map[string]string{
			"CMakeLists.txt":  "cmake_minimum_required(VERSION 3.20)\n",
			"src/main.cpp":    "#include <iostream>\nint main() {}\n",
			"include/utils.c": "void helper() {}\n",
			"README.md":       "# C++ Project\n",
		},
		expectDirSkip: map[string]bool{
			"build":       true, // always-skip
			"CMakeFiles":  true, // always-skip
			"obj":         true, // always-skip
			".ccls-cache": true, // always-skip (LSP cache)
			".clangd":     true, // always-skip (LSP cache)
			"src":         false,
			"include":     false,
		},
		expectFile: map[string]Pipeline{
			"src/main.cpp":    PipelineCKG,
			"include/utils.c": PipelineCKG,
			"README.md":       PipelineKnowledge,
			"CMakeLists.txt":  PipelineL3,
		},
	})
}

// ── C# / .NET Ecosystem ──

func TestEcosystem_CSharp(t *testing.T) {
	runEcosystemTest(t, ecosystemCase{
		name: "CSharp",
		dirs: []string{"src"},
		files: map[string]string{
			"App.csproj":     "<Project></Project>\n",
			"src/Program.cs": "class Program { static void Main() {} }\n",
			"README.md":      "# .NET App\n",
		},
		expectDirSkip: map[string]bool{
			"obj": true,  // always-skip (.NET build intermediates)
			"bin": false, // NOT in alwaysSkip (only in fastSkipDir)
			"src": false,
		},
		expectFile: map[string]Pipeline{
			"src/Program.cs": PipelineCKG,
			"README.md":      PipelineKnowledge,
			"App.csproj":     PipelineL3,
		},
	})
}

// ── Ruby Ecosystem ──

func TestEcosystem_Ruby(t *testing.T) {
	runEcosystemTest(t, ecosystemCase{
		name: "Ruby",
		dirs: []string{"lib", "spec"},
		files: map[string]string{
			"Gemfile":          "source 'https://rubygems.org'\n",
			"Gemfile.lock":     "GEM\n  specs:\n    rails (7.0)\n",
			"lib/app.rb":       "class App; end\n",
			"spec/app_spec.rb": "describe App do; end\n",
			"README.md":        "# Ruby App\n",
		},
		expectDirSkip: map[string]bool{
			".bundle": true, // always-skip
			"lib":     false,
			"spec":    false,
		},
		expectFile: map[string]Pipeline{
			"lib/app.rb":       PipelineCKG,
			"spec/app_spec.rb": PipelineCKG,
			"Gemfile.lock":     PipelineExcluded, // lock file
			"README.md":        PipelineKnowledge,
			"Gemfile":          PipelineL3,
		},
	})
}

// ── PHP Ecosystem ──

func TestEcosystem_PHP(t *testing.T) {
	runEcosystemTest(t, ecosystemCase{
		name: "PHP",
		dirs: []string{"src"},
		files: map[string]string{
			"composer.json": `{"require":{}}`,
			"composer.lock": `{"content-hash":"abc"}`,
			"src/index.php": "<?php echo 'hello';\n",
			"README.md":     "# PHP App\n",
		},
		expectDirSkip: map[string]bool{
			"vendor": true, // always-skip (Composer vendor)
			"src":    false,
		},
		expectFile: map[string]Pipeline{
			"src/index.php": PipelineCKG,
			"composer.json": PipelineExcluded, // JSON blacklist
			"composer.lock": PipelineExcluded, // lock file
			"README.md":     PipelineKnowledge,
		},
	})
}

// ── Swift Ecosystem ──

func TestEcosystem_Swift(t *testing.T) {
	runEcosystemTest(t, ecosystemCase{
		name: "Swift",
		dirs: []string{"Sources/App", "Tests"},
		files: map[string]string{
			"Package.swift":          "// swift-tools-version:5.9\n",
			"Package.resolved":       `{"pins":[]}`,
			"Sources/App/main.swift": "print(\"Hello\")\n",
			"README.md":              "# Swift App\n",
		},
		expectDirSkip: map[string]bool{
			".build":      true, // always-skip (Swift build)
			"DerivedData": true, // always-skip (Xcode)
			"Pods":        true, // always-skip (CocoaPods)
			"Sources":     false,
		},
		expectFile: map[string]Pipeline{
			"Sources/App/main.swift": PipelineCKG,
			"Package.resolved":       PipelineExcluded, // lock file
			"README.md":              PipelineKnowledge,
			"Package.swift":          PipelineCKG, // .swift → tree-sitter
		},
	})
}

// ── Elixir Ecosystem ──

func TestEcosystem_Elixir(t *testing.T) {
	runEcosystemTest(t, ecosystemCase{
		name: "Elixir",
		dirs: []string{"lib", "test"},
		files: map[string]string{
			"mix.exs":    "defmodule App.MixProject do; end\n",
			"mix.lock":   "%{\"jason\" => {:hex, ...}}\n",
			"lib/app.ex": "defmodule App do; end\n",
			"README.md":  "# Elixir App\n",
		},
		expectDirSkip: map[string]bool{
			"deps":   true, // always-skip (Elixir deps)
			"_build": true, // always-skip (Elixir build)
			"lib":    false,
		},
		expectFile: map[string]Pipeline{
			"mix.lock":  PipelineExcluded, // lock file
			"README.md": PipelineKnowledge,
			"mix.exs":   PipelineL3,
		},
	})
}

// ── Dart / Flutter Ecosystem ──

func TestEcosystem_Dart(t *testing.T) {
	runEcosystemTest(t, ecosystemCase{
		name: "Dart",
		dirs: []string{"lib", "test"},
		files: map[string]string{
			"pubspec.yaml": "name: app\n",
			"pubspec.lock": "packages:\n  http:\n",
			"README.md":    "# Dart App\n",
		},
		expectDirSkip: map[string]bool{
			".dart_tool": true, // always-skip
			".packages":  true, // always-skip
			".pub-cache": true, // always-skip
			"build":      true, // always-skip
			"lib":        false,
		},
		expectFile: map[string]Pipeline{
			"pubspec.lock": PipelineExcluded, // lock file
			"pubspec.yaml": PipelineL3,
			"README.md":    PipelineKnowledge,
		},
	})
}

// ── Generated Code Patterns (cross-language) ──

func TestEcosystem_GeneratedCode(t *testing.T) {
	runEcosystemTest(t, ecosystemCase{
		name: "GeneratedCode",
		dirs: []string{"proto"},
		files: map[string]string{
			// Go protobuf
			"proto/user.pb.go":      "package proto\n// Code generated\n",
			"proto/user_grpc.pb.go": "package proto\n// Code generated\n",
			// Python protobuf
			"proto/user_pb2.py":      "# Generated by protoc\n",
			"proto/user_pb2_grpc.py": "# Generated by protoc\n",
			// TypeScript declarations
			"types/index.d.ts": "declare module 'app';\n",
			// Go stringer
			"internal/status_string.go": "// Code generated by stringer\n",
			// Minified assets
			"static/app.min.js":    "!function(e){e()}(window);\n",
			"static/style.min.css": "body{margin:0}\n",
			// User code (should NOT be excluded)
			"proto/user.proto":   "syntax = \"proto3\";\n",
			"internal/status.go": "package internal\ntype Status int\n",
		},
		expectFile: map[string]Pipeline{
			"proto/user.pb.go":          PipelineExcluded, // generated pattern
			"proto/user_grpc.pb.go":     PipelineExcluded,
			"proto/user_pb2.py":         PipelineExcluded,
			"proto/user_pb2_grpc.py":    PipelineExcluded,
			"types/index.d.ts":          PipelineExcluded, // .d.ts
			"internal/status_string.go": PipelineExcluded, // _string.go
			"static/app.min.js":         PipelineExcluded, // .min.js
			"static/style.min.css":      PipelineExcluded, // .min.css
			"proto/user.proto":          PipelineL3,       // .proto → not tree-sitter
			"internal/status.go":        PipelineCKG,      // regular Go
		},
	})
}

// ── Binary Files (cross-language) ──

func TestEcosystem_BinaryFiles(t *testing.T) {
	dir := t.TempDir()
	c := NewClassifier(ModeModerate, nil)

	binaries := []struct {
		name string
		ext  string
	}{
		{"app.exe", ".exe"}, {"lib.so", ".so"}, {"lib.dylib", ".dylib"},
		{"lib.dll", ".dll"}, {"module.wasm", ".wasm"}, {"App.class", ".class"},
		{"app.jar", ".jar"}, {"compiled.pyc", ".pyc"}, {"image.png", ".png"},
		{"photo.jpg", ".jpg"}, {"icon.svg", ".svg"}, {"font.woff2", ".woff2"},
		{"video.mp4", ".mp4"}, {"doc.pdf", ".pdf"}, {"sheet.xlsx", ".xlsx"},
		{"data.db", ".db"}, {"data.sqlite", ".sqlite"}, {"archive.zip", ".zip"},
		{"data.parquet", ".parquet"}, {"data.avro", ".avro"},
		{"addon.node", ".node"}, {"module.beam", ".beam"},
		{"build.tsbuildinfo", ".tsbuildinfo"},
		{"cert.pem", ".pem"}, {"key.key", ".key"},
	}

	for _, b := range binaries {
		path := filepath.Join(dir, b.name)
		os.WriteFile(path, []byte("binary content"), 0644)
		info, _ := os.Stat(path)
		v := c.ClassifyFile(path, info, nil)
		if v.Pipeline != PipelineExcluded {
			t.Errorf("binary file %q (ext=%s) should be excluded, got %s (reason=%s)",
				b.name, b.ext, v.Pipeline, v.Reason)
		}
	}
}

// ── Mixed Monorepo (Go + Node + Python) ──

func TestEcosystem_MixedMonorepo(t *testing.T) {
	runEcosystemTest(t, ecosystemCase{
		name: "MixedMonorepo",
		dirs: []string{
			"backend/cmd", "backend/internal",
			"frontend/src", "frontend/dist",
			"ml/src", "ml/notebooks",
		},
		files: map[string]string{
			"backend/go.mod":              "module example.com/backend\n",
			"backend/cmd/main.go":         "package main\n",
			"backend/internal/handler.go": "package internal\n",
			"frontend/package.json":       `{"name":"frontend"}`,
			"frontend/src/App.tsx":        "export default function App() {}\n",
			"frontend/src/utils.ts":       "export const id = (x: any) => x;\n",
			"ml/requirements.txt":         "torch>=2.0\n",
			"ml/src/train.py":             "def train(): pass\n",
			"README.md":                   "# Monorepo\n",
			"design/architecture.md":      "# Architecture\n## Overview\n",
			"config/deploy.yaml":          "replicas: 3\n",
		},
		expectDirSkip: map[string]bool{
			"node_modules": true,  // safety core
			"vendor":       true,  // always-skip
			"__pycache__":  true,  // always-skip
			".venv":        true,  // always-skip
			"dist":         true,  // always-skip (frontend/dist)
			"backend":      false, // user code
			"frontend":     false,
			"ml":           false,
			"design":       false, // docs are user content
			"config":       false,
		},
		expectFile: map[string]Pipeline{
			"backend/cmd/main.go":         PipelineCKG,
			"backend/internal/handler.go": PipelineCKG,
			"frontend/src/App.tsx":        PipelineCKG,
			"frontend/src/utils.ts":       PipelineCKG,
			"ml/src/train.py":             PipelineCKG,
			"frontend/package.json":       PipelineExcluded, // JSON blacklist
			"README.md":                   PipelineKnowledge,
			"design/architecture.md":      PipelineKnowledge,
			"ml/requirements.txt":         PipelineL3, // .txt but notDocumentTxt → L3
			"config/deploy.yaml":          PipelineL3,
			"backend/go.mod":              PipelineL3,
		},
	})
}
