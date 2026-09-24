package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ProjectMindMap is a deterministic, zero-LLM project summary extracted purely
// from the code index (symbols/edges/file_hashes) and git history.
// It provides AI with project-level understanding without consuming any tokens.
type ProjectMindMap struct {
	Version      int             `json:"version"`
	GeneratedAt  time.Time       `json:"generated_at"`
	IndexVersion int64           `json:"index_version"`
	Languages    []LangStat      `json:"languages"`
	Scale        string          `json:"scale"`
	TopModules   []ModuleStat    `json:"top_modules"`
	EntryPoints  []string        `json:"entry_points"`
	CoreTypes    []CoreTypeStat  `json:"core_types"`
	HotFiles     []HotFileStat   `json:"hot_files"`
	ModuleDeps   []ModuleDepEdge `json:"module_deps"`
	BuildSystem  string          `json:"build_system"`
	Frameworks   []string        `json:"frameworks"`
}

type LangStat struct {
	Language string `json:"lang"`
	Files    int    `json:"files"`
	Percent  int    `json:"pct"`
}

type ModuleStat struct {
	Path        string `json:"path"`
	SymbolCount int    `json:"symbols"`
	InDegree    int    `json:"in_degree"`
	OutDegree   int    `json:"out_degree"`
}

type CoreTypeStat struct {
	Name     string `json:"name"`
	FilePath string `json:"file"`
	Kind     string `json:"kind"`
	InDegree int    `json:"in_degree"`
}

type HotFileStat struct {
	Path    string `json:"path"`
	Commits int    `json:"commits"`
}

type ModuleDepEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Count int    `json:"count"`
}

// mindMapCache holds the in-memory cached MindMap to avoid repeated DB reads.
type mindMapCache struct {
	mu          sync.RWMutex
	data        *ProjectMindMap
	lastGenTime time.Time
	hints       *MindMapHints // injected from engine/wsintel, used by GenerateMindMap
}

var mmCache mindMapCache

// SetMindMapHints stores external project facts (from WsIntel) that
// GenerateMindMap will use instead of its own BuildSystem/Frameworks detection.
// Thread-safe; called from engine layer after WsIntel probe completes.
func (ci *CodeIndex) SetMindMapHints(hints *MindMapHints) {
	mmCache.mu.Lock()
	defer mmCache.mu.Unlock()
	mmCache.hints = hints
}

// GetMindMap returns the cached ProjectMindMap, or loads from DB if not cached.
// Returns nil if no mind map has been generated yet.
func (ci *CodeIndex) GetMindMap(ctx context.Context) *ProjectMindMap {
	// loadMindMapFromDB reads readerDB directly; hold RLock so a concurrent
	// ReopenAt/Close cannot close the DB between the nil-check and query.
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()

	mmCache.mu.RLock()
	if mmCache.data != nil {
		mmCache.mu.RUnlock()
		return mmCache.data
	}
	mmCache.mu.RUnlock()

	mm := ci.loadMindMapFromDB()
	if mm != nil {
		mmCache.mu.Lock()
		mmCache.data = mm
		mmCache.mu.Unlock()
	}
	return mm
}

// InvalidateMindMapCache forces the next GetMindMap to reload from DB.
func InvalidateMindMapCache() {
	mmCache.mu.Lock()
	mmCache.data = nil
	mmCache.mu.Unlock()
}

// MindMapHints provides external project facts (typically from WsIntel) to
// avoid duplicate filesystem probing. When non-nil fields are set, MindMap
// uses them instead of its own detection logic.
type MindMapHints struct {
	BuildSystem string
	Frameworks  []string
}

// GenerateMindMap builds a ProjectMindMap from the current index state.
// CE-INV-01: must complete in < 500ms (pure SQL + git).
// CE-INV-07: rate-limited to at most once per minute.
// hints is optional — when provided, BuildSystem/Frameworks are copied from
// WsIntel rather than independently probed (eliminates duplicate detection).
func (ci *CodeIndex) GenerateMindMap(ctx context.Context, workDir string, hints *MindMapHints) (*ProjectMindMap, error) {
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()

	mmCache.mu.RLock()
	if time.Since(mmCache.lastGenTime) < time.Minute {
		mmCache.mu.RUnlock()
		return mmCache.data, nil
	}
	mmCache.mu.RUnlock()

	start := time.Now()
	mm := &ProjectMindMap{
		Version:     1,
		GeneratedAt: start,
	}

	mm.IndexVersion = ci.fileHashCount()
	mm.Languages = ci.queryLanguages()
	mm.Scale = classifyScale(int(mm.IndexVersion))
	mm.TopModules = ci.queryTopModules()
	mm.EntryPoints = ci.queryEntryPoints()
	mm.CoreTypes = ci.queryCoreTypes()
	mm.ModuleDeps = ci.queryModuleDeps()
	mm.HotFiles = queryHotFiles(workDir)

	if hints != nil && hints.BuildSystem != "" {
		mm.BuildSystem = hints.BuildSystem
	} else {
		mm.BuildSystem = detectBuildSystem(workDir)
	}
	if hints != nil && len(hints.Frameworks) > 0 {
		mm.Frameworks = hints.Frameworks
	} else {
		mm.Frameworks = ci.detectFrameworks()
	}

	ci.saveMindMap(mm)

	mmCache.mu.Lock()
	mmCache.data = mm
	mmCache.lastGenTime = time.Now()
	mmCache.mu.Unlock()

	slog.Info("[codeintel] MindMap generated",
		"workspace", workDir,
		"files", mm.IndexVersion,
		"scale", mm.Scale,
		"modules", len(mm.TopModules),
		"elapsed", time.Since(start).String())

	return mm, nil
}

// ShouldRegenerateMindMap checks if the mind map is stale relative to current index.
func (ci *CodeIndex) ShouldRegenerateMindMap() bool {
	mm := ci.GetMindMap(context.Background())
	if mm == nil {
		return true
	}
	current := ci.fileHashCount()
	if mm.IndexVersion == 0 {
		return true
	}
	diff := current - mm.IndexVersion
	if diff < 0 {
		diff = -diff
	}
	threshold := mm.IndexVersion / 10
	if threshold < 5 {
		threshold = 5
	}
	return diff > threshold
}

// ToContextString renders the mind map as a text summary for LLM injection.
// Output detail scales with maxTokens budget.
func (mm *ProjectMindMap) ToContextString(maxTokens int) string {
	if mm == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("[Project Overview]\n")

	// Level 1 (~80 tokens): always included
	sb.WriteString(fmt.Sprintf("Language: %s\n", formatLangs(mm.Languages)))
	sb.WriteString(fmt.Sprintf("Scale: %s\n", mm.Scale))
	if mm.BuildSystem != "" && mm.BuildSystem != "unknown" {
		sb.WriteString(fmt.Sprintf("Build: %s\n", mm.BuildSystem))
	}
	if len(mm.Frameworks) > 0 {
		sb.WriteString(fmt.Sprintf("Frameworks: %s\n", strings.Join(mm.Frameworks, ", ")))
	}

	if maxTokens < 150 {
		return sb.String()
	}

	// Level 2 (+100 tokens): module structure
	if len(mm.TopModules) > 0 {
		sb.WriteString("Modules: ")
		for i, m := range mm.TopModules {
			if i >= 5 {
				break
			}
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("%s(%d)", m.Path, m.SymbolCount))
		}
		sb.WriteByte('\n')
	}

	if maxTokens < 300 {
		return sb.String()
	}

	// Level 3 (+150 tokens): core types + entry points
	if len(mm.CoreTypes) > 0 {
		sb.WriteString("Core types: ")
		for i, t := range mm.CoreTypes {
			if i >= 8 {
				break
			}
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("%s(%s,in:%d)", t.Name, t.Kind, t.InDegree))
		}
		sb.WriteByte('\n')
	}
	if len(mm.EntryPoints) > 0 {
		limit := 3
		if len(mm.EntryPoints) < limit {
			limit = len(mm.EntryPoints)
		}
		sb.WriteString(fmt.Sprintf("Entry: %s\n", strings.Join(mm.EntryPoints[:limit], ", ")))
	}

	if maxTokens < 500 {
		return sb.String()
	}

	// Level 4 (+200 tokens): hot files + deps
	if len(mm.HotFiles) > 0 {
		sb.WriteString("Hot files (30d): ")
		for i, h := range mm.HotFiles {
			if i >= 8 {
				break
			}
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("%s(%d)", filepath.Base(h.Path), h.Commits))
		}
		sb.WriteByte('\n')
	}

	if len(mm.ModuleDeps) > 0 && maxTokens >= 700 {
		sb.WriteString("Key deps: ")
		for i, d := range mm.ModuleDeps {
			if i >= 5 {
				break
			}
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("%s→%s(%d)", d.From, d.To, d.Count))
		}
		sb.WriteByte('\n')
	}

	return sb.String()
}

// ── Internal helpers ──────────────────────────────────────────────────────────

func (ci *CodeIndex) fileHashCount() int64 {
	var count int64
	ci.readerDB.QueryRow("SELECT COUNT(*) FROM file_hashes").Scan(&count)
	return count
}

func (ci *CodeIndex) queryLanguages() []LangStat {
	rows, err := ci.readerDB.Query(`
		SELECT CASE
			WHEN file_path LIKE '%.go' THEN 'Go'
			WHEN file_path LIKE '%.ts' OR file_path LIKE '%.tsx' THEN 'TypeScript'
			WHEN file_path LIKE '%.js' OR file_path LIKE '%.jsx' THEN 'JavaScript'
			WHEN file_path LIKE '%.py' THEN 'Python'
			WHEN file_path LIKE '%.rs' THEN 'Rust'
			WHEN file_path LIKE '%.java' THEN 'Java'
			WHEN file_path LIKE '%.rb' THEN 'Ruby'
			WHEN file_path LIKE '%.swift' THEN 'Swift'
			WHEN file_path LIKE '%.kt' OR file_path LIKE '%.kts' THEN 'Kotlin'
			WHEN file_path LIKE '%.cpp' OR file_path LIKE '%.cc' THEN 'C++'
			WHEN file_path LIKE '%.c' OR file_path LIKE '%.h' THEN 'C'
			ELSE 'Other'
		END as lang,
		COUNT(*) as cnt
		FROM file_hashes
		GROUP BY lang
		ORDER BY cnt DESC
		LIMIT 5`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var stats []LangStat
	var total int
	for rows.Next() {
		var s LangStat
		rows.Scan(&s.Language, &s.Files)
		total += s.Files
		stats = append(stats, s)
	}
	for i := range stats {
		if total > 0 {
			stats[i].Percent = stats[i].Files * 100 / total
		}
	}
	return stats
}

func (ci *CodeIndex) queryTopModules() []ModuleStat {
	// file_path is slash-normalized on every platform (schemaVersion 6), so one
	// separator is enough. The backslash arm this used to carry was the D-9
	// workaround for native storage; keeping it now would assert that a stored
	// path might contain `\`, which is the invariant IndexPath exists to deny.
	rows, err := ci.readerDB.Query(`
		SELECT
			CASE
				WHEN INSTR(file_path, '/') > 0
				THEN SUBSTR(file_path, 1, INSTR(file_path, '/') - 1)
				ELSE '.'
			END as module,
			COUNT(*) as sym_count
		FROM symbols
		WHERE kind IN ('function','method','type','interface','class')
		GROUP BY module
		HAVING module != '.'
		ORDER BY sym_count DESC
		LIMIT 10`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var modules []ModuleStat
	for rows.Next() {
		var m ModuleStat
		rows.Scan(&m.Path, &m.SymbolCount)
		modules = append(modules, m)
	}

	// Enrich with in/out degree from edges
	// D-9: LIKE prefixes must use the OS-native separator — index file_path /
	// target_name values are absolute OS-native paths (backslash on Windows).
	for i, m := range modules {
		prefix := IndexPath(m.Path) + "/%"
		ci.readerDB.QueryRow(`
			SELECT COUNT(DISTINCT source_id) FROM edges
			WHERE target_name LIKE ? AND kind='import'`,
			prefix).Scan(&modules[i].InDegree)
		ci.readerDB.QueryRow(`
			SELECT COUNT(DISTINCT target_name) FROM edges
			WHERE source_id IN (SELECT rowid FROM symbols WHERE file_path LIKE ?) AND kind='import'`,
			prefix).Scan(&modules[i].OutDegree)
	}
	return modules
}

func (ci *CodeIndex) queryEntryPoints() []string {
	rows, err := ci.readerDB.Query(`
		SELECT file_path FROM symbols
		WHERE name='main' AND kind='function'
		LIMIT 5`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var entries []string
	for rows.Next() {
		var p string
		rows.Scan(&p)
		entries = append(entries, p)
	}
	return entries
}

func (ci *CodeIndex) queryCoreTypes() []CoreTypeStat {
	rows, err := ci.readerDB.Query(`
		SELECT s.name, s.file_path, s.kind, COUNT(e.rowid) as in_deg
		FROM symbols s
		LEFT JOIN edges e ON e.target_name = s.name AND e.kind IN ('call','implements')
		WHERE s.kind IN ('interface','type','class')
		GROUP BY s.name, s.file_path
		ORDER BY in_deg DESC
		LIMIT 15`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var types []CoreTypeStat
	for rows.Next() {
		var t CoreTypeStat
		rows.Scan(&t.Name, &t.FilePath, &t.Kind, &t.InDegree)
		types = append(types, t)
	}
	return types
}

func (ci *CodeIndex) queryModuleDeps() []ModuleDepEdge {
	rows, err := ci.readerDB.Query(`
		SELECT from_mod, to_mod, cnt FROM (
			SELECT
				CASE WHEN INSTR(s.file_path, '/') > 0
					THEN SUBSTR(s.file_path, 1, INSTR(s.file_path, '/') - 1)
					ELSE '' END as from_mod,
				CASE WHEN INSTR(e.target_name, '/') > 0
					THEN SUBSTR(e.target_name, 1, INSTR(e.target_name, '/') - 1)
					ELSE '' END as to_mod,
				COUNT(*) as cnt
			FROM edges e
			JOIN symbols s ON s.rowid = e.source_id
			WHERE e.kind = 'import'
			GROUP BY from_mod, to_mod
		) WHERE from_mod != '' AND to_mod != '' AND from_mod != to_mod
		ORDER BY cnt DESC
		LIMIT 20`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var deps []ModuleDepEdge
	for rows.Next() {
		var d ModuleDepEdge
		rows.Scan(&d.From, &d.To, &d.Count)
		deps = append(deps, d)
	}
	return deps
}

func queryHotFiles(workDir string) []HotFileStat {
	if workDir == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "log", "--format=", "--name-only", "--since=30 days ago")
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	counts := make(map[string]int)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		counts[line]++
	}

	type kv struct {
		path  string
		count int
	}
	var sorted []kv
	for p, c := range counts {
		sorted = append(sorted, kv{p, c})
	}
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].count > sorted[i].count {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	var result []HotFileStat
	for i, item := range sorted {
		if i >= 15 {
			break
		}
		result = append(result, HotFileStat{Path: item.path, Commits: item.count})
	}
	return result
}

func detectBuildSystem(workDir string) string {
	if workDir == "" {
		return "unknown"
	}
	checks := []struct {
		file   string
		system string
	}{
		{"go.mod", "go_mod"},
		{"package.json", "npm"},
		{"Cargo.toml", "cargo"},
		{"build.gradle", "gradle"},
		{"settings.gradle", "gradle"},
		{"CMakeLists.txt", "cmake"},
		{"Makefile", "make"},
		{"pyproject.toml", "python"},
		{"pubspec.yaml", "dart"},
	}
	for _, c := range checks {
		path := filepath.Join(workDir, c.file)
		if fileExists(path) {
			return c.system
		}
	}
	return "unknown"
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func (ci *CodeIndex) detectFrameworks() []string {
	rows, err := ci.readerDB.Query(`
		SELECT DISTINCT target_name FROM edges
		WHERE kind='import'
		LIMIT 500`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	known := map[string]string{
		"github.com/gin-gonic/gin": "gin",
		"github.com/labstack/echo": "echo",
		"github.com/gofiber/fiber": "fiber",
		"net/http":                 "net/http",
		"react":                    "react",
		"vue":                      "vue",
		"@angular/core":            "angular",
		"svelte":                   "svelte",
		"next":                     "next.js",
		"express":                  "express",
		"fastapi":                  "fastapi",
		"django":                   "django",
		"flask":                    "flask",
		"actix-web":                "actix-web",
		"tokio":                    "tokio",
	}

	found := make(map[string]bool)
	for rows.Next() {
		var imp string
		rows.Scan(&imp)
		for prefix, name := range known {
			if strings.Contains(imp, prefix) {
				found[name] = true
			}
		}
	}

	var frameworks []string
	for f := range found {
		frameworks = append(frameworks, f)
		if len(frameworks) >= 5 {
			break
		}
	}
	return frameworks
}

func classifyScale(fileCount int) string {
	if fileCount < 100 {
		return "small"
	}
	if fileCount < 1000 {
		return "medium"
	}
	return "large"
}

func formatLangs(langs []LangStat) string {
	if len(langs) == 0 {
		return "unknown"
	}
	var parts []string
	for _, l := range langs {
		if l.Percent > 0 {
			parts = append(parts, fmt.Sprintf("%s(%d%%)", l.Language, l.Percent))
		}
	}
	return strings.Join(parts, ", ")
}

func (ci *CodeIndex) saveMindMap(mm *ProjectMindMap) {
	data, err := json.Marshal(mm)
	if err != nil {
		slog.Warn("[codeintel] saveMindMap: marshal failed", "err", err)
		return
	}
	if _, err := ci.writerDB.Exec(`
		INSERT OR REPLACE INTO mind_map (workspace_root, json_data, generated_at, index_version)
		VALUES (?, ?, ?, ?)`,
		ci.workDir, string(data), mm.GeneratedAt.Format(time.RFC3339), mm.IndexVersion); err != nil {
		slog.Warn("[codeintel] saveMindMap: write failed", "err", err, "workspace", ci.workDir)
	}
}

func (ci *CodeIndex) loadMindMapFromDB() *ProjectMindMap {
	var data string
	err := ci.readerDB.QueryRow(`SELECT json_data FROM mind_map WHERE workspace_root = ?`, ci.workDir).Scan(&data)
	if err != nil {
		return nil
	}
	var mm ProjectMindMap
	if json.Unmarshal([]byte(data), &mm) != nil {
		return nil
	}
	return &mm
}
