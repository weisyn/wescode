package boundary

const (
	MaxCodeFileSize = 1 << 20       // 1 MB — exported for callers outside boundary
	MaxDocFileSize  = 5 * (1 << 20) // 5 MB

	maxCodeFileSize = MaxCodeFileSize
	maxDocFileSize  = MaxDocFileSize
)

// ── Safety Core: NEVER indexable, .wescodeignore `!` cannot override ──
// Source: CBM discover.c is_safety_core_dir + worktree protection

var safetyCoreDir = map[string]bool{
	".git":              true,
	"node_modules":      true,
	".hg":               true,
	".svn":              true,
	".worktrees":        true, // git worktree — prevents re-indexing entire repo
	".claude-worktrees": true, // Claude Code worktrees
}

// ── Layer 0: OS/editor junk ──

var osJunkDir = map[string]bool{
	".DS_Store":    true,
	"__MACOSX":     true,
	"$RECYCLE.BIN": true,
}

var osJunkFile = map[string]bool{
	".DS_Store":   true,
	"Thumbs.db":   true,
	"desktop.ini": true,
	"Icon\r":      true,
}

// ── Layer 1: Always-skip directories (all modes) ──
// Source: CBM discover.c ALWAYS_SKIP_DIRS + wescode ecosystem knowledge
// Audit: 2026-07-31 line-by-line aligned with CBM

var alwaysSkipDir = map[string]bool{
	// VCS / IDE / AI assistants
	".git": true, ".hg": true, ".svn": true, ".worktrees": true,
	".idea": true, ".vscode": true, ".vs": true, ".eclipse": true,
	".claude": true, ".claude-worktrees": true, ".cursor": true,
	".wesgine": true, ".wescode": true,

	// Go
	"vendor": true, "vendored": true,

	// Node / JS / TS
	"node_modules": true, ".next": true, ".nuxt": true, ".svelte-kit": true,
	".pnpm-store": true, "bower_components": true,
	".npm": true, ".yarn": true, ".nyc_output": true,
	".angular": true, ".turbo": true, ".parcel-cache": true,
	".docusaurus": true, ".expo": true,

	// Python
	"__pycache__": true, ".venv": true, "venv": true, "env": true,
	"site-packages": true,
	".tox":          true, ".nox": true, ".eggs": true,
	".mypy_cache": true, ".pytest_cache": true, ".ruff_cache": true,
	"htmlcov": true,

	// Rust
	"target": true, ".cargo": true,

	// Java / Kotlin
	".gradle": true, ".m2": true,

	// C / C++ LSP caches
	"CMakeFiles": true, ".ccls-cache": true, ".clangd": true,

	// C# / .NET
	"obj": true,

	// Dart / Flutter
	".dart_tool": true, ".packages": true, ".pub-cache": true,

	// Swift
	".build": true,

	// Elixir
	"deps": true, "_build": true,

	// Zig
	"zig-cache": true, "zig-out": true,

	// Haskell
	".stack-work": true,

	// Scala
	".metals": true, ".bloop": true, ".bsp": true,

	// OCaml
	"_opam": true,

	// Clojure
	".cpcache": true, ".shadow-cljs": true,

	// Elm
	"elm-stuff": true,

	// Xcode
	"DerivedData": true,

	// Build outputs (universal)
	"dist": true, "build": true, "out": true,
	"tmp": true, ".tmp": true, "temp": true,
	"coverage": true,

	// Container / IaC / Cloud
	".terraform": true, "Pods": true,
	"bazel-bin": true, "bazel-out": true, "bazel-testlogs": true,
	".serverless": true, ".vercel": true, ".netlify": true,
	"deploy": true, "deployed": true,

	// Embedding / AI caches
	".qdrant_code_embeddings": true,

	// wescode internal
	".plans": true, ".scratch": true, "results": true,
	".cache": true,
}

// moderateSkipDir: additionally skipped in Moderate+Fast modes (not Full).
var moderateSkipDir = map[string]bool{
	"third_party": true, "thirdparty": true, "3rdparty": true, "external": true,
	"generated": true, "gen": true, "auto-generated": true, "autogen": true,
}

// fastSkipDir: additionally skipped in Fast mode only.
var fastSkipDir = map[string]bool{
	"docs": true, "doc": true, "documentation": true,
	"examples": true, "example": true, "samples": true, "sample": true,
	"assets": true, "static": true, "public": true, "media": true,
	"fixtures": true, "testdata": true, "test_data": true,
	"__tests__": true, "__test__": true, "__mocks__": true,
	"__snapshots__": true, "__fixtures__": true,
	"migrations": true, "seeds": true, "seed": true,
	"e2e": true, "integration": true,
	"locale": true, "locales": true, "i18n": true, "l10n": true,
	"scripts": true, "tools": true, "hack": true,
	"bin": true,
}

// ── Layer 1: Lock files ──

var lockFile = map[string]bool{
	// Go
	"go.sum": true,
	// Node
	"package-lock.json": true, "pnpm-lock.yaml": true, "yarn.lock": true,
	"bun.lockb": true, "bun.lock": true,
	// Rust
	"Cargo.lock": true,
	// Ruby
	"Gemfile.lock": true,
	// PHP
	"composer.lock": true,
	// Python
	"Pipfile.lock": true, "poetry.lock": true,
	// .NET
	"packages.lock.json": true,
	// Dart
	"pubspec.lock": true,
	// Swift
	"Package.resolved": true,
	// Elixir
	"mix.lock": true,
	// Nix
	"flake.lock": true,
}

// ── Layer 1: Fast-skip filenames (skipped in Moderate+Fast modes) ──
// Source: CBM discover.c FAST_SKIP_FILENAMES
// These are metadata files with no code structure value.

var fastSkipFilename = map[string]bool{
	// License variants
	"LICENSE": true, "LICENSE.txt": true, "LICENSE.md": true,
	"LICENSE-MIT": true, "LICENSE-APACHE": true,
	"LICENCE": true, "LICENCE.txt": true, "LICENCE.md": true,
	// Changelog variants
	"CHANGELOG": true, "CHANGELOG.md": true,
	"CHANGES.md": true, "HISTORY": true, "HISTORY.md": true,
	// Contributor metadata
	"AUTHORS": true, "AUTHORS.md": true,
	"CONTRIBUTORS": true, "CONTRIBUTORS.md": true,
	"CODEOWNERS": true,
	// Autotools generated
	"configure": true, "Makefile.in": true,
	"config.guess": true, "config.sub": true,
}

// ── Layer 1: JSON file blacklist (no code structure value) ──
// Source: CBM discover.c IGNORED_JSON_FILES
// These JSON files are pure metadata — no symbols, no edges, pure noise for CKG.

var ignoredJSONFile = map[string]bool{
	// Package managers
	"package.json": true, "composer.json": true,
	// TypeScript / JavaScript config
	"tsconfig.json": true, "jsconfig.json": true, "tslint.json": true,
	// API specs
	"openapi.json": true, "swagger.json": true,
	// Tooling config
	"jest.config.json": true, ".eslintrc.json": true, ".prettierrc.json": true,
	".babelrc.json": true, ".stylelintrc.json": true,
	"angular.json": true, "firebase.json": true,
	"renovate.json": true, "lerna.json": true, "turbo.json": true,
	"deno.json": true, "biome.json": true,
	// Dev containers
	"devcontainer.json": true, ".devcontainer.json": true,
	// VS Code workspace
	"launch.json": true, "settings.json": true,
	"extensions.json": true, "tasks.json": true,
}

// ── Layer 1: Generated file patterns ──
// Split into always-skip (all modes) and fast-only (tests/mocks indexed in moderate/full).

// alwaysGeneratedPatterns: machine output that is never user code. Skip in ALL modes.
var alwaysGeneratedPatterns = []string{
	// Protobuf
	".pb.go", "_pb2.py", ".pb2.py", "_grpc.pb.go", "_pb2_grpc.py",
	// Code generators
	".generated.ts", ".generated.go", ".generated.js",
	"_string.go", // Go stringer
	// TypeScript declarations
	".d.ts",
	// Minified / bundled
	".min.js", ".min.css",
	".bundle.js", ".chunk.js",
}

// fastGeneratedPatterns: tests/mocks/stories — indexed in moderate/full for TESTS edges.
var fastGeneratedPatterns = []string{
	"mock_", "_mock.",
	".stories.", // Storybook
	".spec.",    // test file
	".test.",    // test file
}

var generatedHeaderMarkers = []string{
	"// Code generated",
	"// DO NOT EDIT",
	"// AUTO-GENERATED",
	"@generated",
	"# Generated by",
	"/* Auto-generated",
	"// This file is auto-generated",
}

// ── Layer 0: Binary file extensions ──
// Audit: 2026-07-31 aligned with CBM ALWAYS_IGNORED_SUFFIXES + FAST_IGNORED_SUFFIXES

var binaryExt = map[string]bool{
	// Compiled
	".o": true, ".a": true, ".so": true, ".dylib": true, ".dll": true,
	".exe": true, ".bin": true, ".elf": true,
	".class": true, ".jar": true, ".war": true, ".ear": true,
	".pyc": true, ".pyo": true, ".whl": true,
	".wasm": true,
	".node": true, // Node.js native addon
	".beam": true, // Erlang/Elixir BEAM
	".elc":  true, // Emacs Lisp compiled
	".rlib": true, // Rust library
	".pb":   true, // Protocol Buffers binary

	// Archives
	".zip": true, ".tar": true, ".gz": true, ".bz2": true, ".xz": true,
	".7z": true, ".rar": true, ".tgz": true, ".zst": true,

	// Images
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true,
	".ico": true, ".webp": true, ".tiff": true, ".tif": true, ".heic": true,
	".svg": true,

	// Fonts
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,

	// Audio/Video
	".mp3": true, ".mp4": true, ".wav": true, ".ogg": true, ".flac": true,
	".avi": true, ".mov": true, ".mkv": true, ".webm": true,

	// Documents (binary)
	".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true,
	".ppt": true, ".pptx": true,
	".odt": true, ".ods": true, // OpenDocument

	// Database
	".db": true, ".sqlite": true, ".sqlite3": true, ".mdb": true,

	// Data formats (binary)
	".avro": true, ".parquet": true,

	// Maps/sourcemaps
	".map": true,

	// Coverage / profiling
	".coverage": true, ".prof": true,

	// TypeScript build info (incremental compilation cache, auto-generated JSON)
	".tsbuildinfo": true,

	// Patches
	".patch": true, ".diff": true,

	// Output
	".out": true, ".tmp": true,

	// Certificates / secrets
	".pem": true, ".key": true, ".crt": true, ".cer": true, ".p12": true, ".pfx": true,

	// Misc binary
	".dat": true, ".DS_Store": true,
}

// ── Document extensions (prose for Knowledge pipeline) ──

var documentExt = map[string]bool{
	".md":       true,
	".markdown": true,
	".rst":      true,
	".txt":      true,
	".adoc":     true,
}

// notDocumentTxt lists .txt filenames that are NOT prose documents.
// These are build configs or dependency manifests that happen to use .txt extension.
var notDocumentTxt = map[string]bool{
	"CMakeLists.txt":   true, // CMake build configuration
	"requirements.txt": true, // Python dependency manifest
	"constraints.txt":  true, // Python pip constraints
	"meson.build":      true, // Meson (no .txt but included for pattern)
}
