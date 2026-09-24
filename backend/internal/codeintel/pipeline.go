package codeintel

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/weisyn/wescode/internal/treesitter"
)

// Pipeline orchestrates the 10-Pass CKG indexing process.
// Each Pass has a single responsibility and well-defined input/output.
//
//	Pass 1:  Discover       — file detection + mtime/hash delta
//	Pass 2:  Structure      — package/module skeleton graph
//	Pass 3:  Extract        — tree-sitter parse + symbol/edge extraction (parallel Workers)
//	Pass 4:  Resolve        — cross-file reference resolution (three-layer)
//	Pass 5:  Enrich         — Git co-change + semantic vectors + route linkage
//	Pass 6:  Analyze        — test association + community detection + convention mining + temporal snapshot
//	Pass 7:  (reserved)     — future use
//	Pass 8:  TypeHierarchy  — IMPLEMENTS + OVERRIDES edges (explicit decl + Go method-set satisfaction)
//	Pass 9:  VCallAnnot     — virtual call annotation + sole-implementor resolution
//	Pass 10: Dump           — staging.db → quick_check → atomic rename → code.db
type Pipeline struct {
	WorkDir     string
	DBPath      string
	ParserPool  *treesitter.ParserPool
	WorkerCount int
	GitSince    string // e.g. "6months"
	FullRescan  bool   // skip delta detection in pass1 — index ALL files regardless of mtime/hash
	OnProgress  func(pass int, pct float64)
	Enricher    *LLMEnricher  // optional: set to enable Pass 6.5 LLM summary generation
	TemporalDB  *sql.DB       // optional: set to enable temporal snapshots (node_history)
	EmbeddingFn EmbeddingFunc // optional: Cell Provider embedding (dense vectors, higher quality than RI)
	buf         *GraphBuffer
	conventions []Convention // populated by pass6 MineConventions
	modulePath  string       // go.mod module name (best-effort) — maps import paths to filesystem paths in pass4
}

// EmbeddingFunc generates dense vectors for a batch of text inputs.
// Returns one []float64 per input (768-dim). Called by Pass 5 when set.
type EmbeddingFunc func(ctx context.Context, texts []string) ([][]float64, error)

// PipelineResult contains metrics from a completed pipeline run.
type PipelineResult struct {
	TotalFiles      int
	NodesCreated    int
	EdgesCreated    int
	Duration        time.Duration
	PassTimes       [10]time.Duration
	Conventions     []Convention
	DiscoveredFiles []string // absolute paths from pass1 (avoids redundant DiscoverFiles call)
	ImplementsEdges int      // count of IMPLEMENTS edges from Pass 8
	OverridesEdges  int      // count of OVERRIDES edges from Pass 8
	VirtualCalls    int      // count of virtual-annotated CALLS edges from Pass 9
}

// NewPipeline creates a pipeline for the given workspace.
func NewPipeline(workDir, dbPath string, ts *treesitter.ParserPool) *Pipeline {
	return &Pipeline{
		WorkDir:     workDir,
		DBPath:      dbPath,
		ParserPool:  ts,
		WorkerCount: 4,
		GitSince:    "6months",
		buf:         NewGraphBuffer(),
		modulePath:  detectGoModule(workDir),
	}
}

// detectGoModule reads the module name from go.mod at dir (best-effort).
// Returns "" when go.mod is missing or unreadable — import edges then stay
// unresolved (consumers skip them, preserving pre-fix behaviour).
func detectGoModule(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// Run executes the full 10-Pass pipeline.
// Returns PipelineResult on success. Any Pass failure aborts the entire run
// (staging.db is not promoted, production DB remains unchanged).
func (p *Pipeline) Run(ctx context.Context) (*PipelineResult, error) {
	start := time.Now()
	result := &PipelineResult{}
	p.buf.Reset()

	slog.Info("CKG pipeline starting", "workdir", p.WorkDir)

	// Pass 1: Discover
	t1 := time.Now()
	files, err := p.pass1Discover(ctx)
	if err != nil {
		return nil, fmt.Errorf("pass1 discover: %w", err)
	}
	result.PassTimes[0] = time.Since(t1)
	result.TotalFiles = len(files)
	p.progress(1, 1.0)

	if len(files) == 0 {
		slog.Info("CKG pipeline: no files to index")
		return result, nil
	}

	// Pass 2: Structure
	t2 := time.Now()
	if err := p.pass2Structure(ctx, files); err != nil {
		return nil, fmt.Errorf("pass2 structure: %w", err)
	}
	result.PassTimes[1] = time.Since(t2)
	p.progress(2, 1.0)

	// Pass 3: Extract (parallel Workers)
	t3 := time.Now()
	if err := p.pass3Extract(ctx, files); err != nil {
		return nil, fmt.Errorf("pass3 extract: %w", err)
	}
	result.PassTimes[2] = time.Since(t3)
	p.progress(3, 1.0)

	// Pass 4: Resolve
	t4 := time.Now()
	if err := p.pass4Resolve(ctx); err != nil {
		return nil, fmt.Errorf("pass4 resolve: %w", err)
	}
	result.PassTimes[3] = time.Since(t4)
	p.progress(4, 1.0)

	// Pass 5: Enrich
	t5 := time.Now()
	if err := p.pass5Enrich(ctx); err != nil {
		return nil, fmt.Errorf("pass5 enrich: %w", err)
	}
	result.PassTimes[4] = time.Since(t5)
	p.progress(5, 1.0)

	// Pass 6: Analyze
	t6 := time.Now()
	if err := p.pass6Analyze(ctx); err != nil {
		return nil, fmt.Errorf("pass6 analyze: %w", err)
	}
	result.PassTimes[5] = time.Since(t6)
	p.progress(6, 1.0)

	// Pass 6.5: LLM Enrichment (optional — INV-P5-05/INV-P5-07)
	if p.Enricher != nil {
		t65 := time.Now()
		enriched := p.Enricher.Enrich(ctx, p.buf)
		if enriched > 0 {
			slog.Info("pass6.5: LLM enrichment", "enriched", enriched, "duration", time.Since(t65))
		}
	}

	// Pass 7: Reserved (no-op)
	result.PassTimes[6] = 0
	p.progress(7, 1.0)

	// Pass 8: TypeHierarchy
	t8 := time.Now()
	implCount, overrideCount := p.pass8TypeHierarchy(ctx)
	result.PassTimes[7] = time.Since(t8)
	result.ImplementsEdges = implCount
	result.OverridesEdges = overrideCount
	p.progress(8, 1.0)

	// Pass 9: VirtualCallAnnotation
	t9 := time.Now()
	vcallCount := p.pass9VCallAnnotation(ctx)
	result.PassTimes[8] = time.Since(t9)
	result.VirtualCalls = vcallCount
	p.progress(9, 1.0)

	// Pass 10: Dump
	t10 := time.Now()
	if err := DumpToProduction(p.buf, p.DBPath); err != nil {
		return nil, fmt.Errorf("pass10 dump: %w", err)
	}
	result.PassTimes[9] = time.Since(t10)
	p.progress(10, 1.0)

	result.NodesCreated = p.buf.NodeCount()
	result.EdgesCreated = p.buf.EdgeCount()
	result.Conventions = p.conventions
	result.Duration = time.Since(start)

	slog.Info("CKG pipeline complete",
		"files", result.TotalFiles,
		"nodes", result.NodesCreated,
		"edges", result.EdgesCreated,
		"implements", result.ImplementsEdges,
		"overrides", result.OverridesEdges,
		"virtual_calls", result.VirtualCalls,
		"duration", result.Duration,
	)
	return result, nil
}

// RunWithoutDump executes Pass 1-9 (discover through virtual call annotation) and returns
// the populated buffer without performing the Pass 10 atomic dump. The caller
// is responsible for merging buffers from multiple roots and calling
// DumpToProduction once with the combined buffer.
func (p *Pipeline) RunWithoutDump(ctx context.Context) (*PipelineResult, *GraphBuffer, error) {
	start := time.Now()
	result := &PipelineResult{}
	p.buf.Reset()

	slog.Info("CKG pipeline starting", "workdir", p.WorkDir)

	t1 := time.Now()
	files, err := p.pass1Discover(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("pass1 discover: %w", err)
	}
	result.PassTimes[0] = time.Since(t1)
	result.TotalFiles = len(files)
	for _, f := range files {
		result.DiscoveredFiles = append(result.DiscoveredFiles, f.Path)
	}
	p.progress(1, 1.0)

	if len(files) == 0 {
		slog.Info("CKG pipeline: no files to index")
		return result, p.buf, nil
	}

	t2 := time.Now()
	if err := p.pass2Structure(ctx, files); err != nil {
		return nil, nil, fmt.Errorf("pass2 structure: %w", err)
	}
	result.PassTimes[1] = time.Since(t2)
	p.progress(2, 1.0)

	t3 := time.Now()
	if err := p.pass3Extract(ctx, files); err != nil {
		return nil, nil, fmt.Errorf("pass3 extract: %w", err)
	}
	result.PassTimes[2] = time.Since(t3)
	p.progress(3, 1.0)

	t4 := time.Now()
	if err := p.pass4Resolve(ctx); err != nil {
		return nil, nil, fmt.Errorf("pass4 resolve: %w", err)
	}
	result.PassTimes[3] = time.Since(t4)
	p.progress(4, 1.0)

	t5 := time.Now()
	if err := p.pass5Enrich(ctx); err != nil {
		return nil, nil, fmt.Errorf("pass5 enrich: %w", err)
	}
	result.PassTimes[4] = time.Since(t5)
	p.progress(5, 1.0)

	t6 := time.Now()
	if err := p.pass6Analyze(ctx); err != nil {
		return nil, nil, fmt.Errorf("pass6 analyze: %w", err)
	}
	result.PassTimes[5] = time.Since(t6)
	p.progress(6, 1.0)

	if p.Enricher != nil {
		t65 := time.Now()
		enriched := p.Enricher.Enrich(ctx, p.buf)
		if enriched > 0 {
			slog.Info("pass6.5: LLM enrichment", "enriched", enriched, "duration", time.Since(t65))
		}
	}

	// Pass 7: Reserved
	result.PassTimes[6] = 0
	p.progress(7, 1.0)

	// Pass 8: TypeHierarchy
	t8 := time.Now()
	implCount, overrideCount := p.pass8TypeHierarchy(ctx)
	result.PassTimes[7] = time.Since(t8)
	result.ImplementsEdges = implCount
	result.OverridesEdges = overrideCount
	p.progress(8, 1.0)

	// Pass 9: VCallAnnotation
	t9 := time.Now()
	vcallCount := p.pass9VCallAnnotation(ctx)
	result.PassTimes[8] = time.Since(t9)
	result.VirtualCalls = vcallCount
	p.progress(9, 1.0)

	result.NodesCreated = p.buf.NodeCount()
	result.EdgesCreated = p.buf.EdgeCount()
	result.Conventions = p.conventions
	result.Duration = time.Since(start)

	slog.Info("CKG pipeline complete (no dump)",
		"files", result.TotalFiles,
		"nodes", result.NodesCreated,
		"edges", result.EdgesCreated,
		"implements", result.ImplementsEdges,
		"overrides", result.OverridesEdges,
		"virtual_calls", result.VirtualCalls,
		"duration", result.Duration,
	)
	return result, p.buf, nil
}

func (p *Pipeline) progress(pass int, pct float64) {
	if p.OnProgress != nil {
		p.OnProgress(pass, pct)
	}
}

// Conventions returns conventions discovered in the last pass6 run.
func (p *Pipeline) Conventions() []Convention {
	return p.conventions
}

// --- Pass stubs (Sprint 2-3 will implement) ---

// FileToParse represents a file discovered in Pass 1.
type FileToParse struct {
	Path     string
	MtimeNs  int64
	Language string
	Size     int64
}

func (p *Pipeline) pass2Structure(ctx context.Context, files []FileToParse) error {
	for _, f := range files {
		pkg := filepath.Dir(f.Path)
		pkgName := filepath.Base(pkg)

		pkgQName := "pkg:" + pkg
		p.buf.AddNode(&Node{
			QualifiedName: pkgQName,
			FilePath:      pkg,
			Kind:          NodePackage,
			Name:          pkgName,
			PackagePath:   pkg,
		})

		fileQName := "file:" + f.Path
		p.buf.AddNode(&Node{
			QualifiedName: fileQName,
			FilePath:      f.Path,
			Kind:          NodeFile,
			Name:          filepath.Base(f.Path),
			PackagePath:   pkg,
		})
		p.buf.AddFile(f.Path, f.MtimeNs)

		p.buf.AddEdge(Edge{
			SourceQName: pkgQName,
			TargetQName: fileQName,
			Kind:        EdgeContains,
			// Both endpoints were minted by this loop; there is nothing to resolve.
			Resolution: ResolutionExact,
			Source:     "structure",
		})
	}
	return nil
}

func (p *Pipeline) pass3Extract(ctx context.Context, files []FileToParse) error {
	return dispatchToWorkers(ctx, files, p.buf, p.WorkerCount)
}

func (p *Pipeline) pass4Resolve(ctx context.Context) error {
	// Three-layer cross-file reference resolution.
	p.buf.mu.Lock()

	nameIndex := make(map[string][]string)
	// Index by both Name and Parent.Name for receiver-qualified call resolution.
	parentNameIndex := make(map[string][]string) // "Parent.Name" → []qname
	for qname, n := range p.buf.Nodes {
		if n.Kind == NodeImport || n.Kind == NodeFile || n.Kind == NodePackage {
			continue
		}
		nameIndex[n.Name] = append(nameIndex[n.Name], qname)
		if n.Parent != "" {
			parentNameIndex[n.Parent+"."+n.Name] = append(parentNameIndex[n.Parent+"."+n.Name], qname)
		}
	}

	// Package index: filesystem package path → package node qname.
	// Resolves import edges whose TargetName is a module path.
	pkgIndex := make(map[string]string)
	for qname, n := range p.buf.Nodes {
		if n.Kind == NodePackage {
			pkgIndex[n.PackagePath] = qname
		}
	}

	for i := range p.buf.Edges {
		e := &p.buf.Edges[i]
		if e.TargetQName != "" || e.TargetName == "" {
			continue
		}
		if e.Kind == EdgeImport {
			// Resolve intra-workspace imports to package nodes. TargetName is
			// the module path from the import statement (e.g. "fmt",
			// "example.com/mod/internal/foo", "./local"); map it back to a
			// filesystem package path via the go.mod module prefix. Standard-
			// library and external-module imports stay unresolved (TargetQName
			// empty) — consumers skip them, preserving pre-fix behaviour.
			if qn, ok := pkgIndex[e.TargetName]; ok {
				e.TargetQName = qn
				e.Resolution = ResolutionExact
			} else if p.modulePath != "" && strings.HasPrefix(e.TargetName, p.modulePath+"/") {
				rel := strings.TrimPrefix(e.TargetName, p.modulePath+"/")
				if qn, ok := pkgIndex[filepath.Join(p.WorkDir, rel)]; ok {
					e.TargetQName = qn
					e.Resolution = ResolutionExact
				}
			} else if strings.HasPrefix(e.TargetName, ".") {
				if qn, ok := pkgIndex[filepath.Join(p.WorkDir, e.TargetName)]; ok {
					e.TargetQName = qn
					e.Resolution = ResolutionExact
				}
			}
			continue
		}

		// If TargetName is qualified ("Registry.Get"), use parent+name index for precise resolution.
		if strings.Contains(e.TargetName, ".") {
			qualCandidates := parentNameIndex[e.TargetName]
			if len(qualCandidates) > 0 {
				sourceNode := p.buf.Nodes[e.SourceQName]
				resolved := false
				if sourceNode != nil {
					for _, cand := range qualCandidates {
						cn := p.buf.Nodes[cand]
						if cn != nil && cn.PackagePath == sourceNode.PackagePath {
							e.TargetQName = cand
							e.Resolution = ResolutionExact
							resolved = true
							break
						}
					}
				}
				if !resolved {
					var exported []string
					for _, cand := range qualCandidates {
						cn := p.buf.Nodes[cand]
						if cn != nil && cn.Exported {
							exported = append(exported, cand)
						}
					}
					if len(exported) == 1 {
						e.TargetQName = exported[0]
						e.Resolution = ResolutionInferred
						resolved = true
					} else if len(exported) > 1 {
						e.Resolution = ResolutionAmbiguous
					}
				}
				if !resolved && len(qualCandidates) == 1 {
					e.TargetQName = qualCandidates[0]
					e.Resolution = ResolutionInferred
				} else if !resolved && len(qualCandidates) > 1 {
					e.Resolution = ResolutionAmbiguous
				}
				continue
			}
			// Fallback: strip prefix and try bare name.
			parts := strings.SplitN(e.TargetName, ".", 2)
			e.TargetName = parts[len(parts)-1]
		}

		candidates := nameIndex[e.TargetName]
		if len(candidates) == 0 {
			continue
		}

		sourceNode := p.buf.Nodes[e.SourceQName]
		if sourceNode != nil {
			for _, cand := range candidates {
				cn := p.buf.Nodes[cand]
				if cn != nil && cn.PackagePath == sourceNode.PackagePath {
					e.TargetQName = cand
					e.Resolution = ResolutionExact
					break
				}
			}
		}
		if e.TargetQName != "" {
			continue
		}

		var exported []string
		for _, cand := range candidates {
			cn := p.buf.Nodes[cand]
			if cn != nil && cn.Exported {
				exported = append(exported, cand)
			}
		}
		if len(exported) == 1 {
			e.TargetQName = exported[0]
			e.Resolution = ResolutionInferred
		} else if len(exported) == 0 && len(candidates) == 1 {
			e.TargetQName = candidates[0]
			e.Resolution = ResolutionInferred
		} else {
			e.Resolution = ResolutionAmbiguous
		}
	}
	p.buf.mu.Unlock()

	// Data flow resolution: generate DATA_FLOWS_TO edges from resolved CALLS edges.
	dfCount := ResolveDataFlow(p.buf)
	if dfCount > 0 {
		slog.Info("pass4: data flow edges generated", "count", dfCount)
	}

	// Implements inference belongs to Pass 8 alone. Pass 4 used to run a weaker copy
	// of the same method-set check first, and Pass 8 then deleted the pairs it
	// re-derived itself — so the edges that survived were exactly the ones Pass 8
	// had deliberately filtered out (cross-package matches from vendor/ and
	// _test.go). A second producer of the same relationship did not add coverage,
	// it defeated the first one's filters.

	return nil
}

func (p *Pipeline) pass5Enrich(ctx context.Context) error {
	// Git co-change edges.
	if err := AnalyzeGitCoChanges(p.WorkDir, p.buf, p.GitSince); err != nil {
		slog.Warn("pass5: git co-change failed (non-fatal)", "err", err)
	}

	// Semantic vector edges: prefer dense embedding (Cell Provider), fallback to local RI.
	embeddingUsed := false
	if p.EmbeddingFn != nil {
		if err := ComputeSemanticEdgesWithEmbedding(ctx, p.buf, p.EmbeddingFn, 0.7); err != nil {
			if strings.Contains(err.Error(), "no embedding-capable provider") {
				slog.Debug("pass5: no embedding provider, using RI", "err", err)
			} else {
				slog.Warn("pass5: dense embedding failed, falling back to RI", "err", err)
			}
		} else {
			embeddingUsed = true
		}
	}
	if !embeddingUsed {
		if err := ComputeSemanticEdges(p.buf, 0.7); err != nil {
			slog.Warn("pass5: RI semantic edges failed (non-fatal)", "err", err)
		}
	}

	// Route linkage: Route nodes + HANDLES/HTTP_CALLS edges.
	p.enrichRoutes(ctx)

	return nil
}

// enrichRoutes extracts HTTP route definitions and API calls from all file nodes,
// creating Route nodes + HANDLES edges (route → handler) + HTTP_CALLS edges
// (caller → route). Non-fatal: logs warnings and continues on errors.
func (p *Pipeline) enrichRoutes(ctx context.Context) {
	if p.ParserPool == nil {
		return
	}

	p.buf.mu.RLock()
	var filePaths []string
	for _, n := range p.buf.Nodes {
		if n.Kind == NodeFile {
			filePaths = append(filePaths, n.FilePath)
		}
	}
	p.buf.mu.RUnlock()

	// Collect all routes and API calls.
	var allRoutes []treesitter.RouteEntry
	var allAPICalls []treesitter.APICallEntry

	for _, fp := range filePaths {
		if ctx.Err() != nil {
			return
		}

		lang, ok := treesitter.DetectLang(fp)
		if !ok {
			continue
		}

		content, err := os.ReadFile(fp)
		if err != nil {
			continue
		}

		tree, err := p.ParserPool.Parse(lang, content, nil)
		if err != nil {
			continue
		}

		routes := treesitter.ExtractRoutesFromTree(lang, tree, content, fp)
		calls := treesitter.ExtractAPICallsFromTree(lang, tree, content, fp)
		tree.Close()

		allRoutes = append(allRoutes, routes...)
		allAPICalls = append(allAPICalls, calls...)
	}

	if len(allRoutes) == 0 && len(allAPICalls) == 0 {
		return
	}

	// Create Route nodes + HANDLES edges for each extracted route.
	for _, r := range allRoutes {
		routeQName := "route:" + r.Method + " " + r.Path
		p.buf.AddNode(&Node{
			QualifiedName: routeQName,
			FilePath:      r.FilePath,
			Kind:          NodeRoute,
			Name:          r.Method + " " + r.Path,
			LineStart:     r.Line,
			LineEnd:       r.Line,
		})

		// HANDLES edge: handler symbol → route node.
		if r.Handler != "" {
			p.buf.AddEdge(Edge{
				SourceQName: routeQName,
				TargetName:  r.Handler,
				Kind:        EdgeHandles,
				// Pass 5 runs after Pass 4, so nothing in the buffer will bind
				// this handler name — resolveEdgeTargets does it against the
				// production DB. Claiming a resolution here would be a claim
				// about a lookup that has not happened.
				Resolution: ResolutionUnresolved,
				Source:     "route-extract",
			})
		}
	}

	// HTTP_CALLS edges: match API calls against known routes.
	for _, call := range allAPICalls {
		callPath := normalizePath(call.URL)
		if callPath == "" {
			continue
		}

		for _, r := range allRoutes {
			if call.Method != "" && call.Method != r.Method && r.Method != "*" {
				continue
			}
			if !pathMatches(r.Path, callPath) {
				continue
			}

			routeQName := "route:" + r.Method + " " + r.Path
			callerQName := fmt.Sprintf("%s:apicall:%d:%d", call.FilePath, call.Line, call.Col)
			p.buf.AddEdge(Edge{
				SourceQName: callerQName,
				TargetQName: routeQName,
				Kind:        EdgeHTTPCalls,
				// A request URL matched a route pattern (pathMatches), which is
				// a conclusion about two strings, not a fact in the source.
				Resolution: ResolutionInferred,
				Source:     "route-extract",
			})
			break
		}
	}

	slog.Info("pass5: route enrichment", "routes", len(allRoutes), "api_calls", len(allAPICalls))
}

func (p *Pipeline) pass6Analyze(ctx context.Context) error {
	// 1. Test association: TestXxx → Xxx mapping.
	p.buf.mu.RLock()
	testNodes := make(map[string]string)
	for qname, n := range p.buf.Nodes {
		if n.Kind != NodeFunction && n.Kind != NodeMethod {
			continue
		}
		if strings.HasPrefix(n.Name, "Test") && len(n.Name) > 4 {
			target := n.Name[4:]
			if target != "" {
				testNodes[qname] = target
			}
		}
	}

	var testEdges []Edge
	for testQName, targetName := range testNodes {
		for qname, n := range p.buf.Nodes {
			if n.Name == targetName && (n.Kind == NodeFunction || n.Kind == NodeMethod || n.Kind == NodeType) {
				testEdges = append(testEdges, Edge{
					SourceQName: testQName,
					TargetQName: qname,
					TargetName:  targetName,
					Kind:        EdgeTests,
					// TestFoo → Foo is a naming convention, and the first
					// same-named node wins. Inference, not a call site.
					Resolution: ResolutionInferred,
					Source:     "test-association",
				})
				break
			}
		}
	}
	p.buf.mu.RUnlock()

	if len(testEdges) > 0 {
		p.buf.mu.Lock()
		p.buf.Edges = append(p.buf.Edges, testEdges...)
		p.buf.mu.Unlock()
	}

	// 2. Cross-service edge inference (always `inferred`; the URL-vs-route match
	// tier travels in metadata, not in the resolution).
	crossCount := InferCrossServiceEdges(p.buf)
	if crossCount > 0 {
		slog.Info("pass6: cross-service edges inferred", "count", crossCount)
	}

	// 3. Leiden community detection (INV-P4-02: only adds community_id, no edge mutation).
	communities := ComputeLeidenCommunities(p.buf)
	if len(communities) > 0 {
		p.buf.mu.Lock()
		for qname, cid := range communities {
			if n, ok := p.buf.Nodes[qname]; ok {
				n.CommunityID = cid
			}
		}
		p.buf.mu.Unlock()
		slog.Info("pass6: Leiden communities computed", "communities", countUniqueCommunities(communities))
	}

	// 4. Convention mining (INV-P6-01: coverage ≥ 80% threshold).
	p.conventions = MineConventions(p.buf)

	// 5. Effect propagation (INV-P5-02: pure graph traversal, no IO).
	lang := p.dominantLanguage()
	if lang != "" {
		PropagateEffects(p.buf, lang)
	}

	// 6. Taint propagation (INV-P5-04: only marks node properties, no new edges).
	taintCount := PropagateTaint(p.buf)
	if taintCount > 0 {
		slog.Info("pass6: taint propagation complete", "nodes_tainted", taintCount)
	}

	// 7. Record temporal snapshot (INV-P6-02: ≤ 30 per symbol).
	if p.TemporalDB != nil {
		if err := RecordSnapshot(p.buf, p.TemporalDB); err != nil {
			slog.Warn("pass6: temporal snapshot failed (non-fatal)", "err", err)
		}
	}

	return nil
}

// dominantLanguage returns the most frequent language among file nodes.
func (p *Pipeline) dominantLanguage() string {
	p.buf.mu.RLock()
	defer p.buf.mu.RUnlock()

	counts := make(map[string]int)
	for _, n := range p.buf.Nodes {
		if n.Kind != NodeFile {
			continue
		}
		lc := DetectLanguage(n.FilePath)
		if lc.Language != "" {
			counts[lc.Language]++
		}
	}
	var best string
	var bestCount int
	for lang, c := range counts {
		if c > bestCount {
			best = lang
			bestCount = c
		}
	}
	return best
}

func countUniqueCommunities(m map[string]int) int {
	seen := make(map[int]struct{}, len(m))
	for _, v := range m {
		seen[v] = struct{}{}
	}
	return len(seen)
}

// ── Route path matching utilities ───────────────────────────────────────────

// pathMatches checks if a route pattern matches a concrete URL path.
// Route pattern segments starting with : or enclosed in {} are wildcards.
func pathMatches(pattern, url string) bool {
	patParts := splitPath(pattern)
	urlParts := splitPath(url)

	if len(patParts) != len(urlParts) {
		return false
	}

	for i, pp := range patParts {
		if isParam(pp) {
			continue
		}
		if !strings.EqualFold(pp, urlParts[i]) {
			return false
		}
	}
	return true
}

func isParam(segment string) bool {
	return strings.HasPrefix(segment, ":") ||
		(strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}")) ||
		segment == "*"
}

func splitPath(p string) []string {
	p = strings.TrimPrefix(p, "/")
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func normalizePath(p string) string {
	if idx := strings.IndexByte(p, '?'); idx >= 0 {
		p = p[:idx]
	}
	if idx := strings.IndexByte(p, '#'); idx >= 0 {
		p = p[:idx]
	}
	return strings.TrimSuffix(p, "/")
}
