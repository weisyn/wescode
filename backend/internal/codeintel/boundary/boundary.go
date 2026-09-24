// Package boundary implements the four-layer file filtering pipeline
// for wescode's CKG and Knowledge subsystems.
//
// The pipeline determines which files enter which subsystem:
//   - CKG (Code Knowledge Graph): code files with AST structure
//   - Knowledge: prose documents for semantic search
//   - L3: everything else — Agent accesses via read/grep on demand
//
// Four layers execute in order; any layer rejecting a file terminates:
//
//	Layer 0: Physical exclusion (binary, OS junk, VCS internals, safety core)
//	Layer 1: Language-ecosystem exclusion (per-language skip tables, ProbeL0-driven)
//	Layer 2: Project-level exclusion (.gitignore, .wescodeignore)
//	Layer 3: Content heuristics (minified, generated headers, high entropy)
//
// Design authority: design/09-workspace-intelligence.md §2.3.2
// CBM reference: codebase-memory-mcp discover.c
package boundary

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// Pipeline is the destination a file is routed to after passing all filters.
type Pipeline int

const (
	PipelineExcluded  Pipeline = iota // rejected by filter layers
	PipelineCKG                       // code → CKG 10-Pass
	PipelineKnowledge                 // prose document → Knowledge AgenticRAG
	PipelineL3                        // neither code nor doc → Agent read/grep on demand
)

func (p Pipeline) String() string {
	switch p {
	case PipelineCKG:
		return "ckg"
	case PipelineKnowledge:
		return "knowledge"
	case PipelineL3:
		return "l3"
	default:
		return "excluded"
	}
}

// ExcludeReason describes why a file or directory was excluded.
type ExcludeReason string

const (
	ReasonSafetyCore      ExcludeReason = "safety-core"
	ReasonBinaryMagic     ExcludeReason = "binary-magic"
	ReasonBinaryExt       ExcludeReason = "binary-ext"
	ReasonOSJunk          ExcludeReason = "os-junk"
	ReasonEditorTemp      ExcludeReason = "editor-temp"
	ReasonSymlink         ExcludeReason = "symlink"
	ReasonTooLarge        ExcludeReason = "too-large"
	ReasonZeroSize        ExcludeReason = "zero-size"
	ReasonLayer1Dir       ExcludeReason = "layer1-ecosystem-dir"
	ReasonLayer1LockFile  ExcludeReason = "layer1-lockfile"
	ReasonLayer1Generated ExcludeReason = "layer1-generated-pattern"
	ReasonLayer1Metadata  ExcludeReason = "layer1-metadata-file"
	ReasonLayer1Cache     ExcludeReason = "layer1-cache"
	ReasonGitignore       ExcludeReason = "gitignore"
	ReasonWescodeignore   ExcludeReason = "wescodeignore"
	ReasonMinified        ExcludeReason = "layer3-minified"
	ReasonGeneratedHeader ExcludeReason = "layer3-generated-header"
	ReasonHighEntropy     ExcludeReason = "layer3-high-entropy"
	ReasonNoLanguage      ExcludeReason = "no-language"
	ReasonNotDocument     ExcludeReason = "not-document"
	ReasonOutsideRoot     ExcludeReason = "outside-workspace-root"
)

// ExcludedFile records a filtered-out file with its reason for diagnostics.
type ExcludedFile struct {
	Path   string
	Reason ExcludeReason
}

// FileVerdict is the result of classifying a single file.
type FileVerdict struct {
	Path     string
	Pipeline Pipeline
	Reason   ExcludeReason // non-empty only when Pipeline == PipelineExcluded
	Language string        // non-empty only when Pipeline == PipelineCKG
}

// Mode controls the strictness of Layer 1 filtering.
type Mode int

const (
	ModeFast     Mode = iota // incremental: strictest, skip docs/examples/tests dirs
	ModeModerate             // first full index: standard exclusions
	ModeFull                 // deep analysis: only safety-core excluded
)

// Classifier is the unified file filtering pipeline.
// All CKG, Knowledge, and L3 consumers share a single Classifier instance.
type Classifier struct {
	mode           Mode
	workDir        string   // workspace root for resolving relative paths
	projectTypes   []string // detected by ProbeL0: "go", "node", "python", etc.
	wescodeignore  *IgnoreRules
	gitignoreCheck func(rel string) bool // optional: caller provides .gitignore matching
}

// NewClassifier creates a classifier with the given mode and detected project types.
func NewClassifier(mode Mode, projectTypes []string) *Classifier {
	return &Classifier{
		mode:         mode,
		projectTypes: projectTypes,
	}
}

// SetWorkDir sets the workspace root for .wescodeignore relative path resolution.
func (c *Classifier) SetWorkDir(dir string) {
	c.workDir = dir
}

// SetWescodeignore loads .wescodeignore rules from the workspace root.
func (c *Classifier) SetWescodeignore(rules *IgnoreRules) {
	c.wescodeignore = rules
}

// SetGitignoreCheck sets an external gitignore matcher (e.g. from git ls-files).
func (c *Classifier) SetGitignoreCheck(fn func(rel string) bool) {
	c.gitignoreCheck = fn
}

// ShouldSkipDir checks whether a directory should be skipped entirely during WalkDir.
// This is the hot path — called for every directory during traversal.
func (c *Classifier) ShouldSkipDir(name string, relPath string) (skip bool, reason ExcludeReason) {
	// Layer 0: Safety Core — never overridable
	if safetyCoreDir[name] {
		return true, ReasonSafetyCore
	}

	// Layer 0: OS/editor junk directories
	if osJunkDir[name] {
		return true, ReasonOSJunk
	}

	// Layer 0: All dot-prefix directories are hidden/internal.
	// Explicit exceptions are unnecessary — the alwaysSkipDir table
	// already contains all known dot-dirs, and unknown ones (e.g.
	// .env_backup, .custom_cache) should not be indexed either.
	// This matches CBM's behavior and the old shouldSkipDir catchall.
	if strings.HasPrefix(name, ".") && name != "." && name != ".." {
		return true, ReasonLayer1Dir
	}

	// Layer 1: Always-skip ecosystem directories (all modes)
	if alwaysSkipDir[name] {
		return true, ReasonLayer1Dir
	}

	// Layer 1: Mode-dependent skip (fast/moderate skip more)
	if c.mode != ModeFull {
		if moderateSkipDir[name] {
			return true, ReasonLayer1Dir
		}
	}
	if c.mode == ModeFast {
		if fastSkipDir[name] {
			return true, ReasonLayer1Dir
		}
	}

	// Layer 2: .wescodeignore
	if c.wescodeignore != nil && c.wescodeignore.MatchDir(name) {
		return true, ReasonWescodeignore
	}

	// Layer 2: .gitignore (if checker provided)
	if c.gitignoreCheck != nil && c.gitignoreCheck(relPath) {
		return true, ReasonGitignore
	}

	return false, ""
}

// ClassifyPathUnder classifies one path the way a directory walk would, running
// ShouldSkipDir over every ancestor before handing the file to ClassifyFile.
// Event-driven callers (file watcher, git-diff recovery, staleness recovery)
// never walk the tree, so the ancestor half of the pipeline has no other place
// to happen — and its absence is silent: .git/FETCH_HEAD is admitted by
// ClassifyFile alone, because .git is a directory rule.
//
// root bounds the walk. Segments above it are not the workspace's business — a
// checkout under ~/.cursor/worktrees/ is a legitimate root whose parent trips
// the dot-prefix rule. A path outside root is excluded rather than climbed:
// there is nothing to bound the walk with, and a file under no workspace root
// has no owner in the index.
func (c *Classifier) ClassifyPathUnder(root, path string, info os.FileInfo, detectLang func(string) (string, bool)) FileVerdict {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return FileVerdict{Path: path, Pipeline: PipelineExcluded, Reason: ReasonOutsideRoot}
	}
	segs := strings.Split(filepath.ToSlash(rel), "/")
	for i, name := range segs[:len(segs)-1] { // last segment is the file itself
		if skip, reason := c.ShouldSkipDir(name, strings.Join(segs[:i+1], "/")); skip {
			return FileVerdict{Path: path, Pipeline: PipelineExcluded, Reason: reason}
		}
	}
	return c.ClassifyFile(path, info, detectLang)
}

// ClassifyFile runs the four-layer pipeline on a single file.
// Returns the routing verdict. Caller has already passed ShouldSkipDir for parent dirs
// (ClassifyPathUnder does that for callers that have no traversal to piggyback on).
//
// detectLang is a callback for tree-sitter language detection (avoids import cycle).
// workDir is the workspace root for resolving .wescodeignore relative paths.
func (c *Classifier) ClassifyFile(path string, info os.FileInfo, detectLang func(string) (string, bool)) FileVerdict {
	name := filepath.Base(path)
	ext := strings.ToLower(filepath.Ext(name))

	// ── Layer 0: Physical exclusions ──

	// Symlinks: check via Lstat since WalkDir's d.Info() follows symlinks.
	// If the caller provides FileInfo from os.Lstat, ModeSymlink is set.
	// If from d.Info() (follows), we do a supplementary Lstat check.
	if info.Mode()&os.ModeSymlink != 0 {
		return FileVerdict{Path: path, Pipeline: PipelineExcluded, Reason: ReasonSymlink}
	}
	if linfo, err := os.Lstat(path); err == nil && linfo.Mode()&os.ModeSymlink != 0 {
		return FileVerdict{Path: path, Pipeline: PipelineExcluded, Reason: ReasonSymlink}
	}

	// Zero-size
	if info.Size() == 0 {
		return FileVerdict{Path: path, Pipeline: PipelineExcluded, Reason: ReasonZeroSize}
	}

	// OS junk files
	if osJunkFile[name] {
		return FileVerdict{Path: path, Pipeline: PipelineExcluded, Reason: ReasonOSJunk}
	}

	// Editor temp files
	if isEditorTemp(name) {
		return FileVerdict{Path: path, Pipeline: PipelineExcluded, Reason: ReasonEditorTemp}
	}

	// Binary by extension
	if binaryExt[ext] {
		return FileVerdict{Path: path, Pipeline: PipelineExcluded, Reason: ReasonBinaryExt}
	}

	// Size limits: code 1MB, docs 5MB
	if isDocumentExt(ext) {
		if info.Size() > maxDocFileSize {
			return FileVerdict{Path: path, Pipeline: PipelineExcluded, Reason: ReasonTooLarge}
		}
	} else {
		if info.Size() > maxCodeFileSize {
			return FileVerdict{Path: path, Pipeline: PipelineExcluded, Reason: ReasonTooLarge}
		}
	}

	// ── Layer 1: Language-ecosystem exclusions ──

	// Lock files
	if lockFile[name] {
		return FileVerdict{Path: path, Pipeline: PipelineExcluded, Reason: ReasonLayer1LockFile}
	}

	// Generated file patterns (always applied — .pb.go etc are never user code)
	if isAlwaysGeneratedPattern(name) {
		return FileVerdict{Path: path, Pipeline: PipelineExcluded, Reason: ReasonLayer1Generated}
	}

	// Fast-mode-only generated patterns (tests/mocks/stories — indexed in moderate/full)
	if c.mode == ModeFast && isFastGeneratedPattern(name) {
		return FileVerdict{Path: path, Pipeline: PipelineExcluded, Reason: ReasonLayer1Generated}
	}

	// Fast-skip filenames (LICENSE/CHANGELOG/AUTHORS/autotools — no code value)
	if c.mode != ModeFull && fastSkipFilename[name] {
		return FileVerdict{Path: path, Pipeline: PipelineExcluded, Reason: ReasonLayer1Metadata}
	}

	// JSON file blacklist (package.json/tsconfig.json etc — pure metadata)
	if ext == ".json" && ignoredJSONFile[name] {
		return FileVerdict{Path: path, Pipeline: PipelineExcluded, Reason: ReasonLayer1Metadata}
	}

	// ── Layer 2: Project-level exclusions ──

	if c.wescodeignore != nil && c.workDir != "" {
		rel, relErr := filepath.Rel(c.workDir, path)
		if relErr == nil && c.wescodeignore.MatchPath(rel) {
			return FileVerdict{Path: path, Pipeline: PipelineExcluded, Reason: ReasonWescodeignore}
		}
	}

	// ── Route to subsystem ──

	// CKG: tree-sitter parseable code
	if detectLang != nil {
		if lang, ok := detectLang(path); ok {
			return FileVerdict{Path: path, Pipeline: PipelineCKG, Language: lang}
		}
	}

	// Knowledge: prose document extensions (excluding known non-document .txt files)
	if isDocumentExt(ext) && !notDocumentTxt[name] {
		return FileVerdict{Path: path, Pipeline: PipelineKnowledge}
	}

	// L3: everything else — Agent read/grep on demand
	return FileVerdict{Path: path, Pipeline: PipelineL3}
}

// ClassifyFileContent applies Layer 3 content heuristics.
// Call after ClassifyFile when Pipeline != PipelineExcluded and content is available.
func (c *Classifier) ClassifyFileContent(path string, content []byte) *ExcludeReason {
	// Minified: single line > 10K chars
	if isMinifiedContent(content) {
		r := ReasonMinified
		return &r
	}

	// Generated header
	if hasGeneratedHeader(content) {
		r := ReasonGeneratedHeader
		return &r
	}

	// Binary content (NUL byte ratio)
	if isBinaryContent(content) {
		r := ReasonBinaryMagic
		return &r
	}

	return nil
}

// ProbeReadOnlyDirs returns directories that exist in the workspace root
// and should be marked as read-only in the Agent prompt.
// This replaces the old wsintel ProbeBoundary.
func ProbeReadOnlyDirs(root string) []string {
	var dirs []string
	candidates := []string{
		"vendor", "node_modules", ".git/objects",
		"third_party", "thirdparty", "3rdparty", "external",
		"vendored",
	}
	for _, d := range candidates {
		full := filepath.Join(root, d)
		if fi, err := os.Stat(full); err == nil && fi.IsDir() {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// ProbeGeneratedDirs returns directories that exist in the workspace root
// and appear to contain generated code.
func ProbeGeneratedDirs(root string) []string {
	var dirs []string
	candidates := []string{
		"gen", "generated", "auto-generated", "autogen",
		"pb", "proto/gen",
	}
	for _, d := range candidates {
		full := filepath.Join(root, d)
		if fi, err := os.Stat(full); err == nil && fi.IsDir() {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// ── Internal helpers ──

func isEditorTemp(name string) bool {
	return strings.HasSuffix(name, ".swp") ||
		strings.HasSuffix(name, ".swo") ||
		strings.HasSuffix(name, "~") ||
		strings.HasPrefix(name, "#") && strings.HasSuffix(name, "#")
}

func isAlwaysGeneratedPattern(name string) bool {
	lname := strings.ToLower(name)
	for _, p := range alwaysGeneratedPatterns {
		if strings.Contains(lname, p) {
			return true
		}
	}
	return false
}

func isFastGeneratedPattern(name string) bool {
	lname := strings.ToLower(name)
	for _, p := range fastGeneratedPatterns {
		if strings.Contains(lname, p) {
			return true
		}
	}
	return false
}

func isMinifiedContent(content []byte) bool {
	if len(content) < 10000 {
		return false
	}
	firstNewline := bytes.IndexByte(content, '\n')
	if firstNewline < 0 || firstNewline > 10000 {
		return true
	}
	lineCount := bytes.Count(content, []byte{'\n'}) + 1
	if lineCount == 0 {
		return false
	}
	avgLineLen := len(content) / lineCount
	return avgLineLen > 500
}

func hasGeneratedHeader(content []byte) bool {
	header := content
	if len(header) > 1024 {
		header = header[:1024]
	}
	headerStr := string(header)
	for _, marker := range generatedHeaderMarkers {
		if strings.Contains(headerStr, marker) {
			return true
		}
	}
	return false
}

func isBinaryContent(content []byte) bool {
	check := content
	if len(check) > 512 {
		check = check[:512]
	}
	if len(check) == 0 {
		return false
	}
	nulCount := 0
	for _, b := range check {
		if b == 0 {
			nulCount++
		}
	}
	return float64(nulCount)/float64(len(check)) > 0.1
}

func isDocumentExt(ext string) bool {
	return documentExt[ext]
}

// IsDocumentExt checks if a file extension is a prose document type.
func IsDocumentExt(ext string) bool {
	return documentExt[ext]
}

// ── Exported helpers for backward compatibility with callers outside the pipeline ──

// IsBinaryExt checks if a file extension is known-binary.
func IsBinaryExt(ext string) bool {
	return binaryExt[ext]
}

// IsLockFileName checks if a filename is a known lock file.
func IsLockFileName(name string) bool {
	return lockFile[name]
}

// ClassifyContent checks 512 bytes of content for binary indicators.
// Returns non-nil ExcludeReason if binary.
func ClassifyContent(header []byte) *ExcludeReason {
	if isBinaryContent(header) {
		r := ReasonBinaryMagic
		return &r
	}
	return nil
}

// AlwaysSkipDirSet returns a copy of the always-skip directory set for backward compat.
// Deprecated: new code should use Classifier.ShouldSkipDir().
func AlwaysSkipDirSet() map[string]bool {
	m := make(map[string]bool, len(alwaysSkipDir)+len(safetyCoreDir))
	for k := range safetyCoreDir {
		m[k] = true
	}
	for k := range alwaysSkipDir {
		m[k] = true
	}
	return m
}

// BinaryExtSet returns a copy of the binary extension set for backward compat.
// Deprecated: new code should use Classifier.ClassifyFile().
func BinaryExtSet() map[string]bool {
	m := make(map[string]bool, len(binaryExt))
	for k := range binaryExt {
		m[k] = true
	}
	return m
}

// LockFileSet returns a copy of the lock file name set for backward compat.
// Deprecated: new code should use Classifier.ClassifyFile().
func LockFileSet() map[string]bool {
	m := make(map[string]bool, len(lockFile))
	for k := range lockFile {
		m[k] = true
	}
	return m
}

// ClassifyContentFull runs Layer 3 content heuristics on full file content.
func ClassifyContentFull(path string, content []byte) *ExcludeReason {
	if isMinifiedContent(content) {
		r := ReasonMinified
		return &r
	}
	if hasGeneratedHeader(content) {
		r := ReasonGeneratedHeader
		return &r
	}
	if isBinaryContent(content) {
		r := ReasonBinaryMagic
		return &r
	}
	return nil
}
