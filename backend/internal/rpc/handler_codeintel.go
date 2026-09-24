package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/weisyn/wescode/internal/codeintel"
	"github.com/weisyn/wescode/internal/codeintel/langs"
)

// ---------------------------------------------------------------------------
// Test detection helpers
// ---------------------------------------------------------------------------

func isTestFile(path string) bool {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	langCfg := langs.Default().ByExtension(ext)
	if langCfg == nil {
		return false
	}
	for _, pattern := range langCfg.Language.TestFilePatterns {
		if matched, _ := filepath.Match(pattern, base); matched {
			return true
		}
	}
	return false
}

func isTestSymbol(name string) bool {
	for _, prefix := range []string{
		"Test", "Benchmark", "Example", // Go
		"test_",                  // Python
		"test", "it", "describe", // JS/TS (jest/mocha)
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	for _, suffix := range []string{
		"Test", "Tests", "Spec", // Java/Kotlin/C#/Swift
	} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// codeintel/stats
// ---------------------------------------------------------------------------

type languageStat struct {
	Language    string `json:"language"`
	FileCount   int    `json:"file_count"`
	SymbolCount int    `json:"symbol_count"`
}

type projectStat struct {
	Root                 string         `json:"root"`
	Name                 string         `json:"name"`
	IsTopologyDiscovered bool           `json:"is_topology_discovered"`
	FileCount            int            `json:"file_count"`
	SymbolCount          int            `json:"symbol_count"`
	Languages            []string       `json:"languages"`
	LanguageStats        []languageStat `json:"language_stats"`
}

type dependencyEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

type codeIntelStatsResponse struct {
	Status       string           `json:"status"` // "not_initialized" | "indexing" | "ready" | "empty"
	TotalFiles   int              `json:"total_files"`
	IndexedFiles int              `json:"indexed_files"`
	StaleFiles   int              `json:"stale_files"`
	Indexing     bool             `json:"indexing"`
	Completeness float64          `json:"completeness"`
	Freshness    float64          `json:"freshness"`
	LastUpdated  string           `json:"last_updated"`
	SymbolsCount int              `json:"symbols_count"`
	Languages    []languageStat   `json:"languages"`
	Projects     []projectStat    `json:"projects"`
	Dependencies []dependencyEdge `json:"dependencies"`
}

const statsCacheTTL = 3 * time.Second

func (h *Handler) handleCodeIntelStats(ctx context.Context, _ Request) (any, *RPCError) {
	h.statsCacheMu.Lock()
	if h.statsCacheResp != nil && time.Since(h.statsCacheTime) < statsCacheTTL {
		cached := h.statsCacheResp
		h.statsCacheMu.Unlock()
		return cached, nil
	}
	h.statsCacheMu.Unlock()

	idx := h.engine.GetCodeIndex()
	if idx == nil {
		L(ctx).Warn("[codeintel/stats] CodeIndex is nil")
		return &codeIntelStatsResponse{
			Status:       "not_initialized",
			Languages:    []languageStat{},
			Projects:     []projectStat{},
			Dependencies: []dependencyEdge{},
		}, nil
	}
	if idx.DB() == nil {
		L(ctx).Warn("[codeintel/stats] CodeIndex.DB() is nil", "dbPath", idx.DBPath())
		return &codeIntelStatsResponse{
			Status:       "not_initialized",
			Languages:    []languageStat{},
			Projects:     []projectStat{},
			Dependencies: []dependencyEdge{},
		}, nil
	}

	r := idx.Readiness()
	L(ctx).Info("[codeintel/stats] readiness", "indexing", r.Indexing, "totalFiles", r.TotalFiles, "indexedFiles", r.IndexedFiles)
	symbolCount, _ := idx.SymbolCount(ctx)

	languages := queryLanguageStats(ctx, idx)

	roots := h.engine.WorkspaceRoots()
	var projects []projectStat
	var dependencies []dependencyEdge

	seen := map[string]bool{}
	for _, root := range roots {
		if seen[root] {
			continue
		}
		seen[root] = true
		ps := projectStat{
			Root:      root,
			Name:      filepath.Base(root),
			Languages: []string{},
		}
		queryProjectStats(ctx, idx, root, &ps)
		projects = append(projects, ps)
	}

	td := h.engine.GetTopologyDetector()
	if td != nil {
		for _, root := range roots {
			discovered := td.Detect(ctx, root)
			for _, dp := range discovered {
				if !seen[dp] {
					seen[dp] = true
					ps := projectStat{
						Root:                 dp,
						Name:                 filepath.Base(dp),
						IsTopologyDiscovered: true,
						Languages:            []string{},
					}
					queryProjectStats(ctx, idx, dp, &ps)
					projects = append(projects, ps)
				}
				dependencies = append(dependencies, dependencyEdge{
					From: filepath.Base(root),
					To:   filepath.Base(dp),
					Kind: "topology_discovered",
				})
			}
		}
	}

	if projects == nil {
		projects = []projectStat{}
	}
	if dependencies == nil {
		dependencies = []dependencyEdge{}
	}

	// Reconcile global counters with per-project SQL-derived totals.
	// The atomic counters (indexedCount/totalFiles) may lag behind the actual
	// DB state in multi-root workspaces (set by full-scan which may not have run).
	if r.IndexedFiles == 0 && len(projects) > 0 {
		var sumFiles, sumSymbols int
		for _, p := range projects {
			sumFiles += p.FileCount
			sumSymbols += p.SymbolCount
		}
		if sumFiles > 0 {
			r.IndexedFiles = sumFiles
			if r.TotalFiles < sumFiles {
				r.TotalFiles = sumFiles
			}
			if symbolCount == 0 {
				symbolCount = sumSymbols
			}
		}
	}

	// Deduplicate dependencies
	{
		dedup := map[string]bool{}
		var out []dependencyEdge
		for _, d := range dependencies {
			key := d.From + "|" + d.To
			if !dedup[key] {
				dedup[key] = true
				out = append(out, d)
			}
		}
		dependencies = out
		if dependencies == nil {
			dependencies = []dependencyEdge{}
		}
	}

	var lastUpdated string
	if !r.LastUpdated.IsZero() {
		lastUpdated = r.LastUpdated.Format("2006-01-02T15:04:05Z07:00")
	}

	status := "ready"
	if r.Indexing {
		status = "indexing"
	} else if r.TotalFiles == 0 && symbolCount == 0 {
		status = "empty"
	}

	resp := &codeIntelStatsResponse{
		Status:       status,
		TotalFiles:   r.TotalFiles,
		IndexedFiles: r.IndexedFiles,
		StaleFiles:   r.StaleFiles,
		Indexing:     r.Indexing,
		Completeness: r.Completeness,
		Freshness:    r.Freshness,
		LastUpdated:  lastUpdated,
		SymbolsCount: symbolCount,
		Languages:    languages,
		Projects:     projects,
		Dependencies: dependencies,
	}

	if !r.Indexing {
		h.statsCacheMu.Lock()
		h.statsCacheResp = resp
		h.statsCacheTime = time.Now()
		h.statsCacheMu.Unlock()
	}

	return resp, nil
}

func queryLanguageStats(ctx context.Context, idx *codeintel.CodeIndex) []languageStat {
	rows, err := idx.DB().QueryContext(ctx,
		`SELECT DISTINCT file_path FROM symbols WHERE kind != 'import'`)
	if err != nil {
		return []languageStat{}
	}
	defer rows.Close()

	type langAgg struct {
		files   map[string]bool
		symbols int
	}
	agg := map[string]*langAgg{}

	var paths []string
	for rows.Next() {
		var fp string
		if rows.Scan(&fp) == nil {
			paths = append(paths, fp)
		}
	}

	fileToLang := make(map[string]string, len(paths))
	for _, fp := range paths {
		if _, ok := fileToLang[fp]; !ok {
			lang := codeintel.DetectLanguage(fp).Language
			if lang == "" {
				lang = "other"
			}
			fileToLang[fp] = lang
		}
	}

	symRows, err := idx.DB().QueryContext(ctx,
		`SELECT file_path, COUNT(*) FROM symbols WHERE kind != 'import' GROUP BY file_path`)
	if err != nil {
		return []languageStat{}
	}
	defer symRows.Close()

	for symRows.Next() {
		var fp string
		var count int
		if symRows.Scan(&fp, &count) != nil {
			continue
		}
		lang := fileToLang[fp]
		if lang == "" {
			lang = codeintel.DetectLanguage(fp).Language
			if lang == "" {
				lang = "other"
			}
		}
		a, ok := agg[lang]
		if !ok {
			a = &langAgg{files: map[string]bool{}}
			agg[lang] = a
		}
		a.files[fp] = true
		a.symbols += count
	}

	result := make([]languageStat, 0, len(agg))
	for lang, a := range agg {
		result = append(result, languageStat{
			Language:    lang,
			FileCount:   len(a.files),
			SymbolCount: a.symbols,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].FileCount > result[j].FileCount })
	return result
}

// sqlRootPrefix returns a LIKE prefix matching files under root.
//
// The prefix must be built with codeintel.IndexPath, not filepath: file_path is
// stored slash-normalized on every platform (schemaVersion 6). This line has
// now been wrong in both directions — it hardcoded "/" while storage was
// native, then used filepath.Separator after that was noticed (D-9) — because
// the column had two contradictory readers: this prefix assumed native
// separators while every `LIKE '%/pkg/%'` package predicate assumed slashes.
// Normalizing storage is what makes one answer correct for both.
func sqlRootPrefix(root string) string {
	return codeintel.IndexPath(root) + "/%"
}

func queryProjectStats(ctx context.Context, idx *codeintel.CodeIndex, root string, ps *projectStat) {
	db := idx.DB()
	prefix := sqlRootPrefix(root)

	// Count distinct code files that have symbols (not file_hashes which includes non-code).
	row := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT file_path) FROM symbols WHERE file_path LIKE ? AND kind NOT IN ('import','package','file')`, prefix)
	if err := row.Scan(&ps.FileCount); err != nil {
		L(ctx).Warn("[codeintel/project-stats] scan file_count failed", "root", root, "err", err)
	}

	row = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM symbols WHERE file_path LIKE ? AND kind NOT IN ('import','package','file')`, prefix)
	if err := row.Scan(&ps.SymbolCount); err != nil {
		L(ctx).Warn("[codeintel/project-stats] scan symbol_count failed", "root", root, "err", err)
	}

	langRows, err := db.QueryContext(ctx,
		`SELECT DISTINCT file_path FROM symbols WHERE file_path LIKE ? AND kind NOT IN ('import','package','file')`, prefix)
	if err != nil {
		return
	}
	defer langRows.Close()

	type langAccum struct {
		files   map[string]bool
		symbols int
	}
	langMap := map[string]*langAccum{}
	for langRows.Next() {
		var fp string
		if langRows.Scan(&fp) != nil {
			continue
		}
		lang := codeintel.DetectLanguage(fp).Language
		if lang == "" {
			continue
		}
		acc, ok := langMap[lang]
		if !ok {
			acc = &langAccum{files: map[string]bool{}}
			langMap[lang] = acc
		}
		acc.files[fp] = true
	}

	symLangRows, err := db.QueryContext(ctx,
		`SELECT file_path FROM symbols WHERE file_path LIKE ? AND kind NOT IN ('import','package','file')`, prefix)
	if err == nil {
		defer symLangRows.Close()
		for symLangRows.Next() {
			var fp string
			if symLangRows.Scan(&fp) != nil {
				continue
			}
			lang := codeintel.DetectLanguage(fp).Language
			if lang != "" {
				if acc, ok := langMap[lang]; ok {
					acc.symbols++
				}
			}
		}
	}

	for lang, acc := range langMap {
		ps.Languages = append(ps.Languages, lang)
		ps.LanguageStats = append(ps.LanguageStats, languageStat{
			Language:    lang,
			FileCount:   len(acc.files),
			SymbolCount: acc.symbols,
		})
	}
	sort.Strings(ps.Languages)
	sort.Slice(ps.LanguageStats, func(i, j int) bool {
		return ps.LanguageStats[i].FileCount > ps.LanguageStats[j].FileCount
	})
}

// ---------------------------------------------------------------------------
// codeintel/project-tree
// ---------------------------------------------------------------------------

type moduleInfo struct {
	Path       string        `json:"path"`
	FileCount  int           `json:"file_count"`
	Languages  []string      `json:"languages"`
	TopSymbols []symbolBrief `json:"top_symbols"`
}

type symbolBrief struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type projectTreeResponse struct {
	Status      string       `json:"status"` // "not_initialized" | "ready" | "empty"
	ProjectRoot string       `json:"project_root"`
	ProjectName string       `json:"project_name"`
	Modules     []moduleInfo `json:"modules"`
}

func (h *Handler) handleCodeIntelProjectTree(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		ProjectRoot string `json:"project_root"`
		Depth       int    `json:"depth"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}

	idx := h.engine.GetCodeIndex()
	if idx == nil {
		return &projectTreeResponse{Status: "not_initialized", Modules: []moduleInfo{}}, nil
	}

	root := params.ProjectRoot
	if root == "" {
		roots := h.engine.WorkspaceRoots()
		if len(roots) > 0 {
			root = roots[0]
		}
	}
	if root == "" {
		return &projectTreeResponse{Status: "not_initialized", Modules: []moduleInfo{}}, nil
	}

	depth := params.Depth
	if depth <= 0 {
		depth = 2
	}
	if depth > 5 {
		depth = 5
	}

	// Query symbols table directly — only directories with actual code
	// symbols appear in the project tree. This is the core design boundary:
	// CKG project-tree shows code structure, not file listings.
	prefix := sqlRootPrefix(root)
	db := idx.DB()
	if db == nil {
		L(ctx).Warn("[codeintel/project-tree] readerDB is nil", "root", root)
		return &projectTreeResponse{Status: "not_initialized", Modules: []moduleInfo{}}, nil
	}
	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT file_path FROM symbols WHERE file_path LIKE ? AND kind NOT IN ('import','package','file')`,
		prefix)
	if err != nil {
		L(ctx).Error("[codeintel/project-tree] query failed", "root", root, "prefix", prefix, "err", err)
		return nil, internalError(err)
	}
	defer rows.Close()

	type dirAgg struct {
		files   int
		langSet map[string]bool
	}
	dirs := map[string]*dirAgg{}

	fileCount := 0
	for rows.Next() {
		fileCount++
		var filePath string
		if err := rows.Scan(&filePath); err != nil {
			continue
		}
		lang := codeintel.DetectLanguage(filePath).Language
		rel, err := filepath.Rel(root, filepath.Dir(filePath))
		if err != nil || rel == "" {
			rel = "."
		}

		parts := splitRelPath(rel)
		if len(parts) > depth {
			parts = parts[:depth]
			rel = filepath.Join(parts...)
		}

		agg, ok := dirs[rel]
		if !ok {
			agg = &dirAgg{langSet: map[string]bool{}}
			dirs[rel] = agg
		}
		agg.files++
		if lang != "" {
			agg.langSet[lang] = true
		}
	}

	L(ctx).Info("[codeintel/project-tree] query result", "root", root, "prefix", prefix, "files_matched", fileCount, "dirs_before_dedup", len(dirs))

	// Remove parent directories that have child directories in the list.
	// D-9: separator must match how dirs keys were built (filepath.Join →
	// OS-native). Hardcoding "/" broke the dedup on Windows.
	for dir := range dirs {
		for other := range dirs {
			if other != dir && strings.HasPrefix(other, dir+string(filepath.Separator)) {
				delete(dirs, dir)
				break
			}
		}
	}

	var modules []moduleInfo
	for dir, agg := range dirs {
		mi := moduleInfo{
			Path:      dir,
			FileCount: agg.files,
		}
		for lang := range agg.langSet {
			mi.Languages = append(mi.Languages, lang)
		}
		sort.Strings(mi.Languages)

		absDir := filepath.Join(root, dir)
		symRows, serr := idx.DB().QueryContext(ctx,
			`SELECT name, kind FROM symbols WHERE file_path LIKE ? AND parent = '' AND kind IN ('function','type','interface','class','struct','method') LIMIT 8`,
			sqlRootPrefix(absDir))
		if serr == nil {
			for symRows.Next() {
				var sb symbolBrief
				if symRows.Scan(&sb.Name, &sb.Kind) == nil {
					mi.TopSymbols = append(mi.TopSymbols, sb)
				}
			}
			symRows.Close()
		}
		if mi.TopSymbols == nil {
			mi.TopSymbols = []symbolBrief{}
		}
		modules = append(modules, mi)
	}

	sort.Slice(modules, func(i, j int) bool { return modules[i].Path < modules[j].Path })

	if modules == nil {
		modules = []moduleInfo{}
	}

	status := "ready"
	if len(modules) == 0 {
		status = "empty"
	}

	return &projectTreeResponse{
		Status:      status,
		ProjectRoot: root,
		ProjectName: filepath.Base(root),
		Modules:     modules,
	}, nil
}

func splitRelPath(rel string) []string {
	if rel == "." || rel == "" {
		return nil
	}
	var parts []string
	for rel != "" && rel != "." {
		dir, file := filepath.Split(rel)
		if file != "" {
			parts = append([]string{file}, parts...)
		}
		if dir == rel {
			break
		}
		rel = filepath.Clean(dir)
	}
	return parts
}

// ---------------------------------------------------------------------------
// codeintel/callgraph — recursive BFS + test marking
// ---------------------------------------------------------------------------

// 调用图的 wire 形状（GraphNode / GraphEdge / ImpactSummary / ReadinessInfo /
// CallGraphData）住在 `internal/codeintel/callgraph_wire.go`。
//
// 它们曾在本文件里未导出，因为当时只有这里的两个 RPC 端点产出图。现在 CKG 工具
// 也产出（PC-04），而工具在 codeintel、依赖方向是 rpc → codeintel，所以形状必须
// 搬到两个生产者都能到的地方。在两处各定义一份会漂移，而漂移的那一半是 snake_case
// 键名——`is_focus` 写成 `isFocus` 不报错，只是每个节点都不是焦点。

const perLayerLimit = 10

func (h *Handler) handleCodeIntelCallgraph(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		File   string `json:"file"`
		Symbol string `json:"symbol"`
		Depth  int    `json:"depth"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	// The renderer sends a VS Code fsPath (backslashes on Windows) while stored
	// paths are slash-normalized, so this is converted once at the boundary
	// rather than at each comparison below. Getting it wrong is silent: the
	// file never matches, so same-named symbols resolve to whichever row came
	// back first and the graph focuses the wrong homonym.
	params.File = codeintel.IndexPath(params.File)

	idx := h.engine.GetCodeIndex()
	if idx == nil {
		return &codeintel.CallGraphData{
			Nodes:     []codeintel.GraphNode{},
			Edges:     []codeintel.GraphEdge{},
			Readiness: &codeintel.ReadinessInfo{Status: "not_initialized"},
		}, nil
	}

	r := idx.Readiness()
	riStatus := "ready"
	if r.Indexing {
		riStatus = "indexing"
	}
	ri := &codeintel.ReadinessInfo{Status: riStatus, Completeness: r.Completeness, Indexing: r.Indexing}

	if params.File == "" && params.Symbol == "" {
		return &codeintel.CallGraphData{Nodes: []codeintel.GraphNode{}, Edges: []codeintel.GraphEdge{}, Readiness: ri}, nil
	}

	depth := params.Depth
	if depth <= 0 {
		depth = 2
	}
	if depth > 4 {
		depth = 4
	}

	nodeMap := map[string]*codeintel.GraphNode{}
	var edges []codeintel.GraphEdge
	truncated := false

	addNode := func(s codeintel.SymbolEntry, focus bool) string {
		id := s.FilePath + ":" + s.Name + ":" + s.Kind
		if _, exists := nodeMap[id]; !exists {
			nodeMap[id] = &codeintel.GraphNode{
				ID:        id,
				Name:      s.Name,
				Kind:      s.Kind,
				File:      s.FilePath,
				Line:      s.LineStart,
				Signature: s.Signature,
				IsFocus:   focus,
				IsTest:    isTestFile(s.FilePath) || isTestSymbol(s.Name),
			}
		} else if focus {
			nodeMap[id].IsFocus = true
		}
		return id
	}

	addSyntheticNode := func(name string) string {
		id := "synthetic:" + name
		if _, exists := nodeMap[id]; !exists {
			nodeMap[id] = &codeintel.GraphNode{
				ID:     id,
				Name:   name,
				Kind:   "function",
				IsTest: isTestSymbol(name),
			}
		}
		return id
	}

	bfsCallers := func(seedNames []string) {
		frontier := make([]string, len(seedNames))
		copy(frontier, seedNames)
		visited := map[string]bool{}
		for _, n := range seedNames {
			visited[n] = true
		}

		for layer := 0; layer < depth; layer++ {
			var nextFrontier []string
			for _, symName := range frontier {
				callers, _ := idx.CallersOf(ctx, symName, perLayerLimit+1)
				if len(callers) > perLayerLimit {
					truncated = true
					callers = callers[:perLayerLimit]
				}
				targetSyms, _ := idx.FindSymbol(ctx, symName)
				var targetID string
				if len(targetSyms) > 0 {
					targetID = addNode(targetSyms[0], false)
				} else {
					targetID = addSyntheticNode(symName)
				}
				for _, c := range callers {
					cID := addNode(c, false)
					edges = append(edges, codeintel.GraphEdge{Source: cID, Target: targetID, Kind: "call", Resolved: true})
					if !visited[c.Name] {
						visited[c.Name] = true
						nextFrontier = append(nextFrontier, c.Name)
					}
				}
			}
			frontier = nextFrontier
		}
	}

	bfsCallees := func(seeds []codeintel.SymbolEntry) {
		type calleeTask struct {
			filePath  string
			name      string
			lineStart int
		}
		frontier := make([]calleeTask, 0, len(seeds))
		for _, s := range seeds {
			frontier = append(frontier, calleeTask{s.FilePath, s.Name, s.LineStart})
		}
		visited := map[string]bool{}
		for _, s := range seeds {
			visited[s.Name] = true
		}

		for layer := 0; layer < depth; layer++ {
			var nextFrontier []calleeTask
			for _, task := range frontier {
				calleeNames, _ := idx.CalleesOf(ctx, task.filePath, task.name, task.lineStart)
				if len(calleeNames) > perLayerLimit {
					truncated = true
					calleeNames = calleeNames[:perLayerLimit]
				}
				sourceSyms, _ := idx.FindSymbol(ctx, task.name)
				var sourceID string
				if len(sourceSyms) > 0 {
					sourceID = addNode(sourceSyms[0], false)
				} else {
					sourceID = addSyntheticNode(task.name)
				}
				for _, cn := range calleeNames {
					targetSyms, _ := idx.FindSymbol(ctx, cn)
					if len(targetSyms) > 0 {
						ts := targetSyms[0]
						tID := addNode(ts, false)
						edges = append(edges, codeintel.GraphEdge{Source: sourceID, Target: tID, Kind: "call", Resolved: true})
						if !visited[ts.Name] {
							visited[ts.Name] = true
							if ts.Kind == "function" || ts.Kind == "method" {
								nextFrontier = append(nextFrontier, calleeTask{ts.FilePath, ts.Name, ts.LineStart})
							}
						}
					} else {
						tID := addSyntheticNode(cn)
						edges = append(edges, codeintel.GraphEdge{Source: sourceID, Target: tID, Kind: "call", Resolved: false})
					}
				}
			}
			frontier = nextFrontier
		}
	}

	computeImpact := func(symbolNames []string) *codeintel.ImpactSummary {
		directCallers := 0
		indirectDeps := 0
		affectedFiles := map[string]bool{}
		affectedTests := 0
		for _, name := range symbolNames {
			impactNodes, _, _ := idx.ImpactAnalysis(ctx, name, depth)
			for _, n := range impactNodes {
				if n.Depth == 1 {
					directCallers++
				} else {
					indirectDeps++
				}
				affectedFiles[n.Symbol.FilePath] = true
				if isTestFile(n.Symbol.FilePath) || isTestSymbol(n.Symbol.Name) {
					affectedTests++
				}
				addNode(n.Symbol, false)
			}
		}
		return &codeintel.ImpactSummary{
			DirectCallers:      directCallers,
			IndirectDependents: indirectDeps,
			AffectedFiles:      len(affectedFiles),
			AffectedTests:      affectedTests,
		}
	}

	if params.Symbol != "" {
		symbols, err := idx.FindSymbol(ctx, params.Symbol)
		if err != nil || len(symbols) == 0 {
			return &codeintel.CallGraphData{Nodes: []codeintel.GraphNode{}, Edges: []codeintel.GraphEdge{}, Readiness: ri}, nil
		}

		focusSym := symbols[0]
		if params.File != "" {
			for _, s := range symbols {
				if s.FilePath == params.File {
					focusSym = s
					break
				}
			}
		}
		addNode(focusSym, true)

		bfsCallers([]string{params.Symbol})
		bfsCallees([]codeintel.SymbolEntry{focusSym})

		impact := computeImpact([]string{params.Symbol})

		return &codeintel.CallGraphData{
			Nodes:         collectNodes(nodeMap),
			Edges:         deduplicateEdges(edges),
			ImpactSummary: impact,
			Readiness:     ri,
			Truncated:     truncated,
		}, nil
	}

	// File mode: enumerate top-level symbols
	fileSymbols, err := idx.ListFileSymbols(ctx, params.File)
	if err != nil || len(fileSymbols) == 0 {
		return &codeintel.CallGraphData{Nodes: []codeintel.GraphNode{}, Edges: []codeintel.GraphEdge{}, Readiness: ri}, nil
	}

	var topLevelNames []string
	var topLevelCallable []codeintel.SymbolEntry
	for _, sym := range fileSymbols {
		if sym.Parent != "" {
			continue
		}
		switch sym.Kind {
		case "function", "method", "type", "interface", "class", "struct":
			addNode(sym, true)
			topLevelNames = append(topLevelNames, sym.Name)
			if sym.Kind == "function" || sym.Kind == "method" {
				topLevelCallable = append(topLevelCallable, sym)
			}
		}
	}

	bfsCallers(topLevelNames)
	bfsCallees(topLevelCallable)

	impact := computeImpact(topLevelNames)

	return &codeintel.CallGraphData{
		Nodes:         collectNodes(nodeMap),
		Edges:         deduplicateEdges(edges),
		ImpactSummary: impact,
		Readiness:     ri,
		Truncated:     truncated,
	}, nil
}

// ---------------------------------------------------------------------------
// codeintel/expand-node — lazy expand a single node
// ---------------------------------------------------------------------------

type expandNodeResponse struct {
	Nodes     []codeintel.GraphNode `json:"nodes"`
	Edges     []codeintel.GraphEdge `json:"edges"`
	Truncated bool                  `json:"truncated"`
}

func (h *Handler) handleCodeIntelExpandNode(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Symbol    string `json:"symbol"`
		File      string `json:"file"`
		Direction string `json:"direction"`
		Limit     int    `json:"limit"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}

	if params.Symbol == "" || (params.Direction != "callers" && params.Direction != "callees") {
		return nil, invalidParams(fmt.Errorf("symbol is required and direction must be callers or callees"))
	}
	// Same boundary conversion as handleCodeIntelCallgraph: fsPath in, index
	// representation out, so the two file comparisons below can match.
	params.File = codeintel.IndexPath(params.File)

	limit := params.Limit
	if limit <= 0 {
		limit = perLayerLimit
	}
	if limit > 50 {
		limit = 50
	}

	idx := h.engine.GetCodeIndex()
	if idx == nil {
		return &expandNodeResponse{Nodes: []codeintel.GraphNode{}, Edges: []codeintel.GraphEdge{}}, nil
	}

	nodeMap := map[string]*codeintel.GraphNode{}
	var edges []codeintel.GraphEdge
	truncated := false

	addNode := func(s codeintel.SymbolEntry) string {
		id := s.FilePath + ":" + s.Name + ":" + s.Kind
		if _, exists := nodeMap[id]; !exists {
			nodeMap[id] = &codeintel.GraphNode{
				ID:        id,
				Name:      s.Name,
				Kind:      s.Kind,
				File:      s.FilePath,
				Line:      s.LineStart,
				Signature: s.Signature,
				IsTest:    isTestFile(s.FilePath) || isTestSymbol(s.Name),
			}
		}
		return id
	}

	if params.Direction == "callers" {
		callers, _ := idx.CallersOf(ctx, params.Symbol, limit+1)
		if len(callers) > limit {
			truncated = true
			callers = callers[:limit]
		}
		focusSyms, _ := idx.FindSymbol(ctx, params.Symbol)
		var focusID string
		if len(focusSyms) > 0 {
			fs := focusSyms[0]
			if params.File != "" {
				for _, s := range focusSyms {
					if s.FilePath == params.File {
						fs = s
						break
					}
				}
			}
			focusID = addNode(fs)
		} else {
			focusID = "synthetic:" + params.Symbol
			nodeMap[focusID] = &codeintel.GraphNode{ID: focusID, Name: params.Symbol, Kind: "function"}
		}
		for _, c := range callers {
			cID := addNode(c)
			edges = append(edges, codeintel.GraphEdge{Source: cID, Target: focusID, Kind: "call", Resolved: true})
		}
	} else {
		focusSyms, _ := idx.FindSymbol(ctx, params.Symbol)
		if len(focusSyms) == 0 {
			return &expandNodeResponse{Nodes: []codeintel.GraphNode{}, Edges: []codeintel.GraphEdge{}}, nil
		}
		fs := focusSyms[0]
		if params.File != "" {
			for _, s := range focusSyms {
				if s.FilePath == params.File {
					fs = s
					break
				}
			}
		}
		focusID := addNode(fs)
		calleeNames, _ := idx.CalleesOf(ctx, fs.FilePath, fs.Name, fs.LineStart)
		if len(calleeNames) > limit {
			truncated = true
			calleeNames = calleeNames[:limit]
		}
		for _, cn := range calleeNames {
			targetSyms, _ := idx.FindSymbol(ctx, cn)
			if len(targetSyms) > 0 {
				tID := addNode(targetSyms[0])
				edges = append(edges, codeintel.GraphEdge{Source: focusID, Target: tID, Kind: "call", Resolved: true})
			} else {
				synID := "synthetic:" + cn
				if _, exists := nodeMap[synID]; !exists {
					nodeMap[synID] = &codeintel.GraphNode{ID: synID, Name: cn, Kind: "function", IsTest: isTestSymbol(cn)}
				}
				edges = append(edges, codeintel.GraphEdge{Source: focusID, Target: synID, Kind: "call", Resolved: false})
			}
		}
	}

	return &expandNodeResponse{
		Nodes:     collectNodes(nodeMap),
		Edges:     deduplicateEdges(edges),
		Truncated: truncated,
	}, nil
}

// ---------------------------------------------------------------------------
// codeintel/module-deps — module-level dependency graph
// ---------------------------------------------------------------------------

type moduleNode struct {
	Dir         string `json:"dir"`
	SymbolCount int    `json:"symbol_count"`
}

type moduleEdge struct {
	SourceDir string `json:"source_dir"`
	TargetDir string `json:"target_dir"`
	Weight    int    `json:"weight"`
}

type moduleDepsResponse struct {
	Status  string       `json:"status"` // "not_initialized" | "ready"
	Modules []moduleNode `json:"modules"`
	Edges   []moduleEdge `json:"edges"`
}

func (h *Handler) handleCodeIntelModuleDeps(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		ProjectRoot string `json:"project_root"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}

	idx := h.engine.GetCodeIndex()
	if idx == nil {
		return &moduleDepsResponse{Status: "not_initialized", Modules: []moduleNode{}, Edges: []moduleEdge{}}, nil
	}

	root := params.ProjectRoot
	if root == "" {
		roots := h.engine.WorkspaceRoots()
		if len(roots) > 0 {
			root = roots[0]
		}
	}
	if root == "" {
		return &moduleDepsResponse{Modules: []moduleNode{}, Edges: []moduleEdge{}}, nil
	}

	rows, err := idx.DB().QueryContext(ctx,
		`SELECT s1.file_path, s2.file_path
		 FROM edges e
		 INNER JOIN symbols s1 ON s1.id = e.source_id
		 INNER JOIN symbols s2 ON s2.id = e.target_id
		 WHERE s1.file_path LIKE ? AND s2.file_path LIKE ?`,
		sqlRootPrefix(root), sqlRootPrefix(root))
	if err != nil {
		return nil, internalError(err)
	}
	defer rows.Close()

	type edgeKey struct{ src, tgt string }
	edgeCounts := map[edgeKey]int{}
	moduleSeen := map[string]bool{}

	for rows.Next() {
		var srcFile, tgtFile string
		if rows.Scan(&srcFile, &tgtFile) != nil {
			continue
		}
		srcRel, err1 := filepath.Rel(root, filepath.Dir(srcFile))
		tgtRel, err2 := filepath.Rel(root, filepath.Dir(tgtFile))
		if err1 != nil || err2 != nil {
			continue
		}
		if srcRel == tgtRel {
			continue
		}
		moduleSeen[srcRel] = true
		moduleSeen[tgtRel] = true
		edgeCounts[edgeKey{srcRel, tgtRel}]++
	}

	moduleSymCounts := map[string]int{}
	symRows, err := idx.DB().QueryContext(ctx,
		`SELECT file_path, COUNT(*) FROM symbols WHERE file_path LIKE ? GROUP BY file_path`, sqlRootPrefix(root))
	if err == nil {
		defer symRows.Close()
		for symRows.Next() {
			var fp string
			var cnt int
			if symRows.Scan(&fp, &cnt) != nil {
				continue
			}
			rel, err := filepath.Rel(root, filepath.Dir(fp))
			if err != nil {
				continue
			}
			moduleSymCounts[rel] += cnt
		}
	}

	modules := make([]moduleNode, 0, len(moduleSeen))
	for dir := range moduleSeen {
		modules = append(modules, moduleNode{Dir: dir, SymbolCount: moduleSymCounts[dir]})
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Dir < modules[j].Dir })

	edgesList := make([]moduleEdge, 0, len(edgeCounts))
	for ek, w := range edgeCounts {
		edgesList = append(edgesList, moduleEdge{SourceDir: ek.src, TargetDir: ek.tgt, Weight: w})
	}
	sort.Slice(edgesList, func(i, j int) bool { return edgesList[i].Weight > edgesList[j].Weight })

	return &moduleDepsResponse{
		Status:  "ready",
		Modules: modules,
		Edges:   edgesList,
	}, nil
}

// ---------------------------------------------------------------------------
// codeintel/coverage — CKG precision metrics
// ---------------------------------------------------------------------------

type blindSpot struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
}

type staleFileEntry struct {
	FilePath string `json:"file_path"`
}

type reachabilityBreakdown struct {
	Connected        int `json:"connected"`
	SourceOnly       int `json:"source_only"`
	SinkOnly         int `json:"sink_only"`
	Isolated         int `json:"isolated"`
	NameReachable    int `json:"name_reachable"`
	StructuralExempt int `json:"structural_exempt"`
}

type coverageResponse struct {
	Status                string                `json:"status"` // "not_initialized" | "ready"
	TotalFunctions        int                   `json:"total_functions"`
	EdgeResolutionRate    float64               `json:"edge_resolution_rate"`
	ReachabilityBreakdown reachabilityBreakdown `json:"reachability_breakdown"`
	TotalInterfaces       int                   `json:"total_interfaces"`
	ImplementsEdges       int                   `json:"implements_edges"`
	BlindSpots            []blindSpot           `json:"blind_spots"` // isolated classification only (INV-CKG-SINGLE-CLASS)
	StaleFiles            []staleFileEntry      `json:"stale_files"`
}

// handleCodeIntelCoverage reads reachability classification from the single
// source of truth (INV-CKG-SINGLE-CLASS). No consumer derives reachability
// from edges; all read symbols.reachability_class written by ClassifyReachability.
func (h *Handler) handleCodeIntelCoverage(ctx context.Context, _ Request) (any, *RPCError) {
	idx := h.engine.GetCodeIndex()
	if idx == nil {
		return &coverageResponse{Status: "not_initialized", BlindSpots: []blindSpot{}, StaleFiles: []staleFileEntry{}}, nil
	}

	db := idx.DB()

	// Reachability breakdown — single GROUP BY, no NOT EXISTS bypass.
	// Covers all 5 classified kinds; wire field "total_functions" is a legacy
	// name kept for frontend compat (actually includes type/interface/class).
	var rb reachabilityBreakdown
	var totalFuncs int
	rows, err := db.QueryContext(ctx,
		`SELECT reachability_class, COUNT(*) FROM symbols
		 WHERE kind IN ('function','method','type','interface','class')
		 GROUP BY reachability_class`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var class string
			var count int
			if rows.Scan(&class, &count) != nil {
				continue
			}
			totalFuncs += count
			switch class {
			case "connected":
				rb.Connected = count
			case "source_only":
				rb.SourceOnly = count
			case "sink_only":
				rb.SinkOnly = count
			case "isolated":
				rb.Isolated = count
			case "name_reachable":
				rb.NameReachable = count
			case "structural_exempt":
				rb.StructuralExempt = count
			}
		}
	}

	// Edge resolution rate: bound call edges / total call edges
	var edgeResolutionRate float64
	var totalCallEdges, boundCallEdges int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM edges WHERE kind = 'call'`).Scan(&totalCallEdges); err != nil {
		L(ctx).Warn("[codeintel/detailed-stats] scan total_call_edges failed", "err", err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM edges WHERE kind = 'call' AND target_id IS NOT NULL`).Scan(&boundCallEdges); err != nil {
		L(ctx).Warn("[codeintel/detailed-stats] scan bound_call_edges failed", "err", err)
	}
	if totalCallEdges > 0 {
		edgeResolutionRate = float64(boundCallEdges) / float64(totalCallEdges)
	}

	var totalInterfaces int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM symbols WHERE kind = 'interface'`).Scan(&totalInterfaces); err != nil {
		L(ctx).Warn("[codeintel/detailed-stats] scan total_interfaces failed", "err", err)
	}

	var implementsEdges int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM edges WHERE kind = 'implements'`).Scan(&implementsEdges); err != nil {
		L(ctx).Warn("[codeintel/detailed-stats] scan implements_edges failed", "err", err)
	}

	// Blind spots: only truly isolated symbols (INV-CKG-SINGLE-CLASS).
	// No NOT EXISTS — read the classification column directly.
	blindSpots := []blindSpot{}
	bsRows, err := db.QueryContext(ctx,
		`SELECT name, kind, file_path, line_start FROM symbols
		 WHERE reachability_class = 'isolated'
		 LIMIT 50`)
	if err == nil {
		defer bsRows.Close()
		for bsRows.Next() {
			var bs blindSpot
			if bsRows.Scan(&bs.Name, &bs.Kind, &bs.FilePath, &bs.Line) == nil {
				blindSpots = append(blindSpots, bs)
			}
		}
	}

	staleFiles := []staleFileEntry{}
	r := idx.Readiness()
	if r.StaleFiles > 0 {
		sfRows, err := db.QueryContext(ctx,
			`SELECT file_path FROM file_hashes ORDER BY indexed_at ASC LIMIT ?`, r.StaleFiles)
		if err == nil {
			defer sfRows.Close()
			for sfRows.Next() {
				var sf staleFileEntry
				if sfRows.Scan(&sf.FilePath) == nil {
					staleFiles = append(staleFiles, sf)
				}
			}
		}
	}

	return &coverageResponse{
		Status:                "ready",
		TotalFunctions:        totalFuncs,
		EdgeResolutionRate:    edgeResolutionRate,
		ReachabilityBreakdown: rb,
		TotalInterfaces:       totalInterfaces,
		ImplementsEdges:       implementsEdges,
		BlindSpots:            blindSpots,
		StaleFiles:            staleFiles,
	}, nil
}

// ---------------------------------------------------------------------------
// codeintel/affected-tests — find tests affected by a symbol change
// ---------------------------------------------------------------------------

type affectedTest struct {
	Name     string `json:"name"`
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
	Kind     string `json:"kind"`
}

type affectedTestsResponse struct {
	Symbol string         `json:"symbol"`
	Tests  []affectedTest `json:"tests"`
}

func (h *Handler) handleCodeIntelAffectedTests(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Symbol string `json:"symbol"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Symbol == "" {
		return nil, invalidParams(fmt.Errorf("symbol is required"))
	}

	idx := h.engine.GetCodeIndex()
	if idx == nil {
		return &affectedTestsResponse{Symbol: params.Symbol, Tests: []affectedTest{}}, nil
	}

	visited := map[string]bool{params.Symbol: true}
	frontier := []string{params.Symbol}
	var tests []affectedTest

	for len(frontier) > 0 && len(visited) < 500 {
		var nextFrontier []string
		for _, sym := range frontier {
			callers, _ := idx.CallersOf(ctx, sym, 50)
			for _, c := range callers {
				if visited[c.Name] {
					continue
				}
				visited[c.Name] = true
				if isTestFile(c.FilePath) || isTestSymbol(c.Name) {
					tests = append(tests, affectedTest{
						Name:     c.Name,
						FilePath: c.FilePath,
						Line:     c.LineStart,
						Kind:     c.Kind,
					})
				} else {
					nextFrontier = append(nextFrontier, c.Name)
				}
			}
		}
		frontier = nextFrontier
	}

	if tests == nil {
		tests = []affectedTest{}
	}

	return &affectedTestsResponse{
		Symbol: params.Symbol,
		Tests:  tests,
	}, nil
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

func collectNodes(m map[string]*codeintel.GraphNode) []codeintel.GraphNode {
	nodes := make([]codeintel.GraphNode, 0, len(m))
	for _, n := range m {
		nodes = append(nodes, *n)
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].IsFocus != nodes[j].IsFocus {
			return nodes[i].IsFocus
		}
		return nodes[i].Name < nodes[j].Name
	})
	return nodes
}

func deduplicateEdges(edges []codeintel.GraphEdge) []codeintel.GraphEdge {
	seen := map[string]bool{}
	var out []codeintel.GraphEdge
	for _, e := range edges {
		key := e.Source + "|" + e.Target + "|" + e.Kind
		if !seen[key] {
			seen[key] = true
			out = append(out, e)
		}
	}
	if out == nil {
		return []codeintel.GraphEdge{}
	}
	return out
}

// ---------------------------------------------------------------------------
// codeintel/reindex — drop project index + re-scan
// ---------------------------------------------------------------------------

func (h *Handler) handleCodeIntelReindex(_ context.Context, req Request) (any, *RPCError) {
	var params struct {
		ProjectRoot string `json:"project_root"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.ProjectRoot == "" {
		return nil, &RPCError{Code: -32602, Message: "project_root is required"}
	}
	h.engine.ReindexProject(params.ProjectRoot)
	return map[string]string{"status": "reindex_started", "project_root": params.ProjectRoot}, nil
}

// ---------------------------------------------------------------------------
// codeintel/clear — drop project index data, no re-scan
// ---------------------------------------------------------------------------

func (h *Handler) handleCodeIntelClear(_ context.Context, req Request) (any, *RPCError) {
	var params struct {
		ProjectRoot string `json:"project_root"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.ProjectRoot == "" {
		return nil, &RPCError{Code: -32602, Message: "project_root is required"}
	}
	h.engine.ClearProject(params.ProjectRoot)
	return map[string]string{"status": "cleared", "project_root": params.ProjectRoot}, nil
}
