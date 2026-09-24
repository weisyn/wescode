package wsintel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// markerSet maps marker file names to the project type they indicate.
var markerSet = map[string]string{
	"go.mod":              "go",
	"go.sum":              "go",
	"package.json":        "node",
	"tsconfig.json":       "node",
	"Cargo.toml":          "rust",
	"pom.xml":             "java",
	"build.gradle":        "java",
	"build.gradle.kts":    "java",
	"settings.gradle":     "java",
	"settings.gradle.kts": "java",
	"requirements.txt":    "python",
	"pyproject.toml":      "python",
	"setup.py":            "python",
	"Pipfile":             "python",
	"CMakeLists.txt":      "cpp",
	"Makefile.am":         "cpp",
	"meson.build":         "cpp",
	"*.csproj":            "csharp",
	"*.sln":               "csharp",
	"Package.swift":       "swift",
	"Gemfile":             "ruby",
	"composer.json":       "php",
	"pubspec.yaml":        "dart",
	"mix.exs":             "elixir",
}

// csprojGlobs are checked via directory listing since they use wildcard patterns.
var csprojGlobs = []struct {
	ext string
	typ string
}{
	{".csproj", "csharp"},
	{".sln", "csharp"},
}

// ProbeL0 detects the project type by stat'ing known marker files. Target <200ms.
func ProbeL0(path string) ProjectInfo {
	info := ProjectInfo{
		Path: path,
		Name: filepath.Base(path),
		Type: "unknown",
	}

	markers, typeCounts := scanMarkers(path)
	info.Markers = markers

	// A repository that keeps its manifests one level down (backend/go.mod,
	// web/package.json) has nothing to stat at the root, so the probe above
	// finds no type at all and the prompt ends up telling the model the
	// project's type is unknown. Root manifests still win outright — this
	// runs only when there are none, leaving working layouts untouched.
	if len(typeCounts) == 0 {
		info.Modules, info.Markers, typeCounts = scanSubmodules(path)
	}

	// Name, frameworks and manifest description all come from a manifest, so
	// they must be read where the manifest actually is.
	manifestDir := primaryManifestDir(info)

	if name := readProjectName(manifestDir, typeCounts); name != "" {
		info.Name = name
	}

	switch len(typeCounts) {
	case 0:
		info.Type = "unknown"
	case 1:
		for t := range typeCounts {
			info.Type = t
		}
	default:
		info.Type = "mixed"
	}

	probeExtendedInfo(path, &info)
	probeFrameworks(manifestDir, &info)
	probeDescription(path, manifestDir, &info)
	probeTestPattern(&info)

	return info
}

// primaryManifestDir is the directory whose manifest describes the project.
// For a monorepo that is the first module in sorted order; sorting is what
// keeps the choice — and therefore the prompt bytes — stable across runs.
func primaryManifestDir(info ProjectInfo) string {
	if len(info.Modules) > 0 {
		return filepath.Join(info.Path, info.Modules[0].Dir)
	}
	return info.Path
}

// scanMarkers stats the known manifest files in dir and returns the markers
// found plus a per-type tally. Markers are sorted because they reach a
// prompt-cached system prompt, and Go randomizes map iteration order.
func scanMarkers(dir string) ([]string, map[string]int) {
	var markers []string
	typeCounts := map[string]int{}

	for marker, typ := range markerSet {
		if strings.HasPrefix(marker, "*") {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			markers = append(markers, marker)
			typeCounts[typ]++
		}
	}

	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			for _, g := range csprojGlobs {
				if strings.HasSuffix(e.Name(), g.ext) {
					markers = append(markers, e.Name())
					typeCounts[g.typ]++
					break
				}
			}
		}
	}

	sort.Strings(markers)
	return markers, typeCounts
}

// submoduleSkipDirs never hold the project's own manifest. Descending into
// them would report a vendored dependency's manifest as if it were this
// repository's, which is worse than reporting nothing.
var submoduleSkipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "third_party": true, "thirdparty": true,
	"testdata": true, "fixtures": true, "examples": true, "example": true,
	"dist": true, "build": true, "out": true, "target": true, "bin": true,
}

// scanSubmodules looks one level below root for manifest-bearing directories
// and returns them alongside the markers (path-qualified, so the prompt shows
// where each lives) and the combined type tally.
//
// Depth stays at one deliberately: this probe runs on every request under a
// <200ms budget, and a recursive walk would be both unbounded and less
// accurate — nested manifests below the first level belong to a module's own
// dependencies far more often than to the project.
func scanSubmodules(root string) (modules []Module, markers []string, typeCounts map[string]int) {
	typeCounts = map[string]int{}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, nil, typeCounts
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || submoduleSkipDirs[e.Name()] {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		subMarkers, subTypes := scanMarkers(filepath.Join(root, name))
		if len(subTypes) == 0 {
			continue
		}
		modType := "mixed"
		if len(subTypes) == 1 {
			for t := range subTypes {
				modType = t
			}
		}
		modules = append(modules, Module{Dir: name, Type: modType})
		for _, m := range subMarkers {
			// Markers are prose for the system prompt, not paths to open, so
			// they read as `backend/go.mod` on every platform. filepath.Join
			// alone yields `backend\go.mod` on Windows, which also breaks the
			// sort order this list is kept in (markers reach a prompt-cached
			// system prompt, so their order has to be stable across machines).
			markers = append(markers, filepath.ToSlash(filepath.Join(name, m)))
		}
		for t, n := range subTypes {
			typeCounts[t] += n
		}
	}

	return modules, markers, typeCounts
}

// probeExtendedInfo fills the P2 flywheel fields on ProjectInfo.
// Each sub-probe has an independent 2s timeout and fails silently.
func probeExtendedInfo(path string, info *ProjectInfo) {
	info.DeprecatedCount = probeDeprecatedCount(path)
	info.LastCommitAge = probeLastCommitAge(path)
	info.TestFileCount = probeTestFileCount(path, info.Type)
	info.FileCount = probeFileCount(path)
	info.CommitterCount = probeCommitterCount(path)
}

func probeDeprecatedCount(path string) int {
	count := 0
	walkFiles(path, 0, func(p string, d fs.DirEntry) bool {
		switch filepath.Ext(p) {
		case ".go", ".ts", ".py", ".java":
		default:
			return true
		}
		f, err := os.Open(p)
		if err != nil {
			return true
		}
		buf := make([]byte, 8192)
		n, _ := f.Read(buf)
		f.Close()
		s := string(buf[:n])
		if strings.Contains(s, "@deprecated") || strings.Contains(s, "Deprecated:") {
			count++
		}
		return true
	})
	return count
}

func probeLastCommitAge(path string) time.Duration {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "log", "-1", "--format=%ci")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	t, err := time.Parse("2006-01-02 15:04:05 -0700", strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return time.Since(t)
}

func probeTestFileCount(path string, projectType string) int {
	var patterns []string
	switch projectType {
	case "go":
		patterns = []string{"*_test.go"}
	case "node":
		patterns = []string{"*.test.ts", "*.spec.ts", "*.test.tsx", "*.spec.tsx", "*.test.js", "*.spec.js"}
	case "python":
		patterns = []string{"test_*.py", "*_test.py"}
	case "java":
		patterns = []string{"*Test.java", "*Tests.java"}
	case "rust":
		patterns = []string{} // Rust tests are inline; skip file-level counting
	default:
		return 0
	}
	if len(patterns) == 0 {
		return 0
	}
	total := 0
	walkFiles(path, 0, func(p string, d fs.DirEntry) bool {
		for _, pat := range patterns {
			if matched, _ := filepath.Match(pat, d.Name()); matched {
				total++
				break
			}
		}
		return true
	})
	return total
}

func probeFileCount(path string) int {
	count := 0
	walkFiles(path, 5, func(p string, d fs.DirEntry) bool {
		count++
		return true
	})
	return count
}

// errStopWalk aborts the WalkDir traversal (returned by the visitor).
var errStopWalk = errors.New("stop walk")

// walkFiles visits files under root with a 2-second budget (mirroring the
// previous exec-based probes). Skipped directories match the old find
// invocation (-not -path */vendor/*, */node_modules/*, */.git/*,
// */.build/*, */dist/*) plus *.lock / *.sum files. maxDepth > 0 limits
// directory depth relative to root. The visitor returning false stops the
// walk early.
//
// Pure Go traversal replaces the POSIX-only grep/find commands so project
// probing behaves identically on macOS and Windows.
func walkFiles(root string, maxDepth int, fn func(p string, d fs.DirEntry) bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				switch d.Name() {
				case "vendor", "node_modules", ".git", ".build", "dist":
					return filepath.SkipDir
				}
				if maxDepth > 0 {
					rel, err := filepath.Rel(root, p)
					if err == nil && rel != "." && strings.Count(rel, string(filepath.Separator)) >= maxDepth {
						return filepath.SkipDir
					}
				}
				return nil
			}
			base := d.Name()
			if strings.HasSuffix(base, ".lock") || strings.HasSuffix(base, ".sum") {
				return nil
			}
			if !fn(p, d) {
				return errStopWalk
			}
			return nil
		})
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func probeCommitterCount(path string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "shortlog", "-sn", "--no-merges", "--since=1 year ago")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	count := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			count++
		}
	}
	return count
}

func readProjectName(path string, types map[string]int) string {
	if types["go"] > 0 {
		if name := goModuleName(filepath.Join(path, "go.mod")); name != "" {
			return name
		}
	}
	if types["node"] > 0 {
		if name := nodePackageName(filepath.Join(path, "package.json")); name != "" {
			return name
		}
	}
	if types["rust"] > 0 {
		if name := cargoPackageName(filepath.Join(path, "Cargo.toml")); name != "" {
			return name
		}
	}
	return ""
}

func goModuleName(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module"))
		}
	}
	return ""
}

func nodePackageName(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var pkg struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(data, &pkg) == nil && pkg.Name != "" {
		return pkg.Name
	}
	return ""
}

func cargoPackageName(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	inPackage := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "[package]" {
			inPackage = true
			continue
		}
		if strings.HasPrefix(line, "[") {
			inPackage = false
			continue
		}
		if inPackage && strings.HasPrefix(line, "name") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				return strings.Trim(strings.TrimSpace(parts[1]), "\"'")
			}
		}
	}
	return ""
}

// descriptionSources maps a manifest to the reader that extracts its
// self-description. A slice, not a map: when a directory holds several
// manifests the winner must be the same on every run, because this text lands
// in a prompt-cached system prompt.
var descriptionSources = []struct {
	file string
	read func(string) string
}{
	{"package.json", jsonDescription},   // node
	{"composer.json", jsonDescription},  // php
	{"pyproject.toml", tomlDescription}, // python
	{"Cargo.toml", tomlDescription},     // rust
	{"pom.xml", pomDescription},         // java
}

// probeDescription fills the project's one-line self-description.
//
// The manifest wins because it is the project's own declared summary. The
// README is the fallback that carries every ecosystem whose manifest has no
// description field — Go, C/C++, and plain Make among them, which is most of
// them. Without either, the only thing naming the project is its directory,
// and a model asked what the project does will infer an answer from that name.
func probeDescription(root, manifestDir string, info *ProjectInfo) {
	for _, src := range descriptionSources {
		if d := src.read(filepath.Join(manifestDir, src.file)); d != "" {
			info.Description = normalizeDescription(d)
			return
		}
	}
	if d := readmeDescription(root); d != "" {
		info.Description = normalizeDescription(d)
	}
}

func jsonDescription(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var m struct {
		Description string `json:"description"`
	}
	if json.Unmarshal(data, &m) != nil {
		return ""
	}
	return strings.TrimSpace(m.Description)
}

func tomlDescription(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok || strings.TrimSpace(key) != "description" {
			continue
		}
		return strings.Trim(strings.TrimSpace(value), "\"'")
	}
	return ""
}

func pomDescription(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	_, rest, ok := strings.Cut(string(data), "<description>")
	if !ok {
		return ""
	}
	body, _, ok := strings.Cut(rest, "</description>")
	if !ok {
		return ""
	}
	return strings.TrimSpace(body)
}

var readmeNames = []string{"README.md", "README.rst", "README.txt", "README"}

func readmeDescription(root string) string {
	for _, name := range readmeNames {
		f, err := os.Open(filepath.Join(root, name))
		if err != nil {
			continue
		}
		line := firstProseLine(f)
		f.Close()
		if line != "" {
			return line
		}
	}
	return ""
}

// readmeHeadBytes caps the read: the description sits in the opening lines and
// a README can be arbitrarily large. Reading a bounded prefix into memory also
// avoids bufio.Scanner, whose "line too long" is indistinguishable from EOF —
// a minified or single-line README would otherwise read as empty.
const readmeHeadBytes = 8 << 10

// firstProseLine returns the README's first line of actual prose. The title is
// skipped because it is normally just the repository name, which the caller
// already has; badges, HTML banners and blockquote taglines are skipped
// because they describe the project's CI, not the project.
func firstProseLine(r io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(r, readmeHeadBytes))
	if err != nil {
		return ""
	}

	inFence := false
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence || line == "" {
			continue
		}
		// Compared as a byte, not a rune: every leading byte of a multi-byte
		// UTF-8 sequence is >= 0x80, so a line opening in Chinese (or any
		// non-ASCII script) can never collide with these ASCII markers.
		switch line[0] {
		case '#', // heading — normally just the repository name
			'<',           // HTML banner
			'>',           // blockquote tagline
			'!',           // image badge
			'[',           // linked badge or bare link
			'-', '*', '=', // list item or setext rule
			'|': // table row
			continue
		}
		return line
	}
	return ""
}

// descriptionMaxChars bounds the workspace block: this text ships in every
// request's system prompt, once per open project.
const descriptionMaxChars = 120

// normalizeDescription collapses the value onto a single line — manifest
// fields and README prose both wrap freely, and the workspace block is
// line-oriented.
func normalizeDescription(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= descriptionMaxChars {
		return s
	}
	return string(runes[:descriptionMaxChars]) + "…"
}

// ProbeL1 reads build system configuration to determine build/test/lint commands.
// Target <500ms.
func ProbeL1(path string, projectType string) *BuildInfo {
	switch projectType {
	case "go":
		return probeGoBuild(path)
	case "node":
		return probeNodeBuild(path)
	case "rust":
		return &BuildInfo{Build: "cargo build", Test: "cargo test", Lint: "cargo clippy"}
	case "java":
		return probeJavaBuild(path)
	case "python":
		return &BuildInfo{Build: "", Test: "pytest", Lint: "ruff check ."}
	case "cpp":
		return probeCppBuild(path)
	case "csharp":
		return &BuildInfo{Build: "dotnet build", Test: "dotnet test", Lint: ""}
	case "swift":
		return &BuildInfo{Build: "swift build", Test: "swift test", Lint: ""}
	case "ruby":
		return &BuildInfo{Build: "", Test: "bundle exec rspec", Lint: "bundle exec rubocop"}
	case "php":
		return &BuildInfo{Build: "", Test: "vendor/bin/phpunit", Lint: "vendor/bin/phpstan"}
	case "dart":
		return &BuildInfo{Build: "", Test: "dart test", Lint: "dart analyze"}
	case "elixir":
		return &BuildInfo{Build: "mix compile", Test: "mix test", Lint: "mix credo"}
	default:
		return nil
	}
}

func probeGoBuild(path string) *BuildInfo {
	bi := &BuildInfo{Build: "go build ./...", Test: "go test ./...", Lint: "go vet ./..."}
	if _, err := os.Stat(filepath.Join(path, "Makefile")); err == nil {
		if cmds := parseMakefileTargets(filepath.Join(path, "Makefile")); cmds != nil {
			if cmds.Build != "" {
				bi.Build = "make " + cmds.Build
			}
			if cmds.Test != "" {
				bi.Test = "make " + cmds.Test
			}
			if cmds.Lint != "" {
				bi.Lint = "make " + cmds.Lint
			}
		}
	}
	return bi
}

func probeNodeBuild(path string) *BuildInfo {
	data, err := os.ReadFile(filepath.Join(path, "package.json"))
	if err != nil {
		return &BuildInfo{Build: "npm run build", Test: "npm test", Lint: "npm run lint"}
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil || pkg.Scripts == nil {
		return &BuildInfo{Build: "npm run build", Test: "npm test", Lint: "npm run lint"}
	}
	bi := &BuildInfo{}
	if _, ok := pkg.Scripts["build"]; ok {
		bi.Build = "npm run build"
	}
	if _, ok := pkg.Scripts["test"]; ok {
		bi.Test = "npm test"
	}
	if _, ok := pkg.Scripts["lint"]; ok {
		bi.Lint = "npm run lint"
	}
	return bi
}

func probeJavaBuild(path string) *BuildInfo {
	if _, err := os.Stat(filepath.Join(path, "pom.xml")); err == nil {
		return &BuildInfo{Build: "mvn compile", Test: "mvn test", Lint: "mvn checkstyle:check"}
	}
	return &BuildInfo{Build: "gradle build", Test: "gradle test", Lint: "gradle check"}
}

func probeCppBuild(path string) *BuildInfo {
	if _, err := os.Stat(filepath.Join(path, "CMakeLists.txt")); err == nil {
		return &BuildInfo{Build: "cmake --build build", Test: "ctest --test-dir build", Lint: ""}
	}
	if _, err := os.Stat(filepath.Join(path, "meson.build")); err == nil {
		return &BuildInfo{Build: "meson compile -C build", Test: "meson test -C build", Lint: ""}
	}
	return &BuildInfo{Build: "make", Test: "make test", Lint: ""}
}

type makeTargets struct {
	Build string
	Test  string
	Lint  string
}

func parseMakefileTargets(path string) *makeTargets {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	targets := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if len(line) == 0 || line[0] == '\t' || line[0] == ' ' || line[0] == '#' {
			continue
		}
		if idx := strings.Index(line, ":"); idx > 0 {
			name := strings.TrimSpace(line[:idx])
			if !strings.ContainsAny(name, " =$%") {
				targets[name] = true
			}
		}
	}

	mt := &makeTargets{}
	for _, name := range []string{"build", "all"} {
		if targets[name] {
			mt.Build = name
			break
		}
	}
	for _, name := range []string{"test", "tests", "check"} {
		if targets[name] {
			mt.Test = name
			break
		}
	}
	for _, name := range []string{"lint", "vet"} {
		if targets[name] {
			mt.Lint = name
			break
		}
	}
	return mt
}

// ProbeRelations analyzes dependency manifests to discover relationships
// between projects in the workspace.
func ProbeRelations(primary string, siblings []string) []Relation {
	if len(siblings) == 0 {
		return nil
	}

	siblingSet := make(map[string]bool, len(siblings))
	for _, s := range siblings {
		abs, err := filepath.Abs(s)
		if err == nil {
			siblingSet[abs] = true
		}
	}

	var relations []Relation

	relations = append(relations, probeGoReplace(primary, siblingSet)...)
	relations = append(relations, probeNodeWorkspaces(primary, siblingSet)...)

	return relations
}

func probeGoReplace(root string, siblings map[string]bool) []Relation {
	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil
	}
	defer f.Close()

	var relations []Relation
	sc := bufio.NewScanner(f)
	inReplace := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "replace") && strings.Contains(line, "(") {
			inReplace = true
			continue
		}
		if line == ")" {
			inReplace = false
			continue
		}
		if strings.HasPrefix(line, "replace ") && !strings.Contains(line, "(") {
			if rel := parseReplaceLine(root, strings.TrimPrefix(line, "replace "), siblings); rel != nil {
				relations = append(relations, *rel)
			}
			continue
		}
		if inReplace {
			if rel := parseReplaceLine(root, line, siblings); rel != nil {
				relations = append(relations, *rel)
			}
		}
	}
	return relations
}

func parseReplaceLine(root, line string, siblings map[string]bool) *Relation {
	parts := strings.Fields(line)
	idx := -1
	for i, p := range parts {
		if p == "=>" {
			idx = i
			break
		}
	}
	if idx < 0 || idx+1 >= len(parts) {
		return nil
	}
	target := parts[idx+1]
	if !strings.HasPrefix(target, ".") && !strings.HasPrefix(target, "/") {
		return nil
	}
	abs := target
	if !filepath.IsAbs(target) {
		abs = filepath.Join(root, target)
	}
	abs = filepath.Clean(abs)
	if siblings[abs] {
		return &Relation{From: root, To: abs, Kind: "depends_on"}
	}
	return nil
}

// probeFrameworks detects key framework dependencies from manifest files.
func probeFrameworks(path string, info *ProjectInfo) {
	switch info.Type {
	case "go":
		info.Frameworks = probeGoFrameworks(path)
	case "node":
		info.Frameworks = probeNodeFrameworks(path)
	case "java":
		info.Frameworks = probeJavaFrameworks(path)
	case "python":
		info.Frameworks = probePythonFrameworks(path)
	}
}

func probeGoFrameworks(path string) []string {
	f, err := os.Open(filepath.Join(path, "go.mod"))
	if err != nil {
		return nil
	}
	defer f.Close()

	knownFrameworks := map[string]string{
		"github.com/spf13/cobra":    "cobra",
		"github.com/gin-gonic/gin":  "gin",
		"github.com/labstack/echo":  "echo",
		"github.com/gofiber/fiber":  "fiber",
		"google.golang.org/grpc":    "grpc",
		"gorm.io/gorm":              "gorm",
		"github.com/jmoiron/sqlx":   "sqlx",
		"github.com/lib/pq":         "postgresql",
		"github.com/go-redis/redis": "redis",
		"go.uber.org/zap":           "zap",
	}

	var frameworks []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		// Indirect requirements are dependencies of dependencies and say
		// nothing about what this project is built with. Counting them
		// labelled an SDK that never imports grpc as a grpc project.
		if strings.Contains(line, "// indirect") {
			continue
		}
		for prefix, name := range knownFrameworks {
			if strings.HasPrefix(line, prefix) {
				frameworks = append(frameworks, name)
			}
		}
	}
	// Sorted because knownFrameworks is a map and this list reaches a
	// prompt-cached system prompt.
	sort.Strings(frameworks)
	return frameworks
}

func probeNodeFrameworks(path string) []string {
	data, err := os.ReadFile(filepath.Join(path, "package.json"))
	if err != nil {
		return nil
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return nil
	}

	knownFrameworks := map[string]string{
		"react":         "react",
		"vue":           "vue",
		"next":          "next.js",
		"@angular/core": "angular",
		"express":       "express",
		"fastify":       "fastify",
		"vite":          "vite",
		"tailwindcss":   "tailwindcss",
		"typescript":    "typescript",
	}

	var frameworks []string
	allDeps := make(map[string]string)
	for k, v := range pkg.Dependencies {
		allDeps[k] = v
	}
	for k, v := range pkg.DevDependencies {
		allDeps[k] = v
	}
	for dep, name := range knownFrameworks {
		if _, ok := allDeps[dep]; ok {
			frameworks = append(frameworks, name)
		}
	}
	sort.Strings(frameworks)
	return frameworks
}

func probeJavaFrameworks(path string) []string {
	data, err := os.ReadFile(filepath.Join(path, "pom.xml"))
	if err != nil {
		return nil
	}
	content := string(data)
	var frameworks []string
	if strings.Contains(content, "spring-boot") {
		frameworks = append(frameworks, "spring-boot")
	}
	if strings.Contains(content, "mybatis") {
		frameworks = append(frameworks, "mybatis")
	}
	return frameworks
}

func probePythonFrameworks(path string) []string {
	for _, reqFile := range []string{"requirements.txt", "pyproject.toml"} {
		data, err := os.ReadFile(filepath.Join(path, reqFile))
		if err != nil {
			continue
		}
		content := strings.ToLower(string(data))
		var frameworks []string
		for _, fw := range []struct{ dep, name string }{
			{"django", "django"},
			{"flask", "flask"},
			{"fastapi", "fastapi"},
			{"sqlalchemy", "sqlalchemy"},
			{"pytest", "pytest"},
			{"torch", "pytorch"},
			{"tensorflow", "tensorflow"},
		} {
			if strings.Contains(content, fw.dep) {
				frameworks = append(frameworks, fw.name)
			}
		}
		if len(frameworks) > 0 {
			return frameworks
		}
	}
	return nil
}

// probeTestPattern sets the conventional test file pattern for the project type.
func probeTestPattern(info *ProjectInfo) {
	switch info.Type {
	case "go":
		info.TestPattern = "*_test.go"
	case "node":
		info.TestPattern = "*.test.ts / *.spec.ts"
	case "python":
		info.TestPattern = "test_*.py"
	case "java":
		info.TestPattern = "*Test.java"
	case "rust":
		info.TestPattern = "#[cfg(test)] inline"
	}
}

func probeNodeWorkspaces(root string, siblings map[string]bool) []Relation {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	var pkg struct {
		Workspaces []string `json:"workspaces"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return nil
	}

	var relations []Relation
	for _, ws := range pkg.Workspaces {
		abs := ws
		if !filepath.IsAbs(ws) {
			abs = filepath.Join(root, ws)
		}
		abs = filepath.Clean(abs)
		if siblings[abs] {
			relations = append(relations, Relation{From: root, To: abs, Kind: "depends_on"})
		}
	}
	return relations
}
