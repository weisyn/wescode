package engine

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/weisyn/wescode/internal/codeintel"
	"github.com/weisyn/wescode/internal/codeintel/constraints"
	"github.com/weisyn/wescode/internal/editengine"
	"github.com/weisyn/wescode/internal/notify"
	wesgine "github.com/weisyn/wesgine"
)

// detectLSPLanguages returns which languages the current LSP bridge supports.
func detectLSPLanguages(lsp codeintel.LSPBridge) []string {
	switch lsp.(type) {
	case *codeintel.IDELSPBridge:
		return []string{"multi-language (IDE)"}
	default:
		return nil
	}
}

// backgroundAnchorLoop periodically anchors and validates Memory entries.
// C1: auto-anchors new Memory entries to code locations.
// C2: detects stale anchors by comparing content hashes.
func (s *Service) backgroundAnchorLoop(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("[anchor] background loop recovered from panic", "panic", r)
		}
	}()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			s.mu.Lock()
			cell := s.cell
			root := s.workspace
			s.mu.Unlock()
			if cell == nil || root == "" || cell.State() != wesgine.CellStateActive {
				continue
			}

			anchorStore := newMemoryAnchorAdapter(cell)

			// C1: auto-anchor unanchored Memory entries
			anchored, _ := editengine.AnchorRecentEntries(ctx, anchorStore, root, 20)
			if anchored > 0 {
				slog.Info("[anchor] periodic auto-anchor", "count", anchored)
			}

			// C2: validate existing anchors, mark stale on hash mismatch.
			// Drains the layer rather than sampling a first page — an anchor
			// that falls out of the window is not revalidated, and a stale
			// anchor that is never marked stale is worse than a missing one:
			// it keeps asserting code that no longer exists.
			entries, err := drainAgentMemory(ctx, cell.Memory(), 0, func(e wesgine.MemoryEntry) bool {
				return e.Metadata[editengine.AnchorFileKey] != "" && e.Metadata[editengine.AnchorHashKey] != ""
			})
			if err != nil {
				continue
			}
			var staleCount int
			for _, e := range entries {
				file := e.Metadata[editengine.AnchorFileKey]
				hash := e.Metadata[editengine.AnchorHashKey]
				currentHash := editengine.QuickFileHash(file)
				if currentHash == "" || currentHash == hash {
					continue
				}
				if e.Metadata[editengine.AnchorStaleKey] == "true" {
					continue
				}
				e.Metadata[editengine.AnchorStaleKey] = "true"
				if !strings.HasPrefix(e.Content, "[STALE") {
					e.Content = "[STALE: anchored code changed since extraction] " + e.Content
				}
				if err := cell.Memory().Save(ctx, e); err == nil {
					staleCount++
				}
			}
			if staleCount > 0 {
				slog.Info("[anchor] periodic stale detection", "stale", staleCount)
			}
		}
	}
}

// backgroundIndexAll indexes all workspace roots using the 10-Pass Pipeline.
// Pipeline handles discovery, parsing, resolution, semantic/git enrichment,
// and atomic DB promotion in a single orchestrated run.
func (s *Service) backgroundIndexAll(ctx context.Context) {
	roots := s.WorkspaceRoots()
	slog.Info("[codeintel] backgroundIndexAll starting", "rootCount", len(roots), "roots", roots)
	if len(roots) == 0 {
		slog.Warn("[codeintel] backgroundIndexAll: no workspace roots, skipping")
		if s.pendingNotifier != nil {
			if err := s.pendingNotifier(notify.IndexError, map[string]any{
				"error":  "no_workspace_roots",
				"status": "not_initialized",
			}); err != nil {
				slog.Warn("[codeintel] notify IndexError failed", "error", err)
			}
		}
		return
	}

	s.mu.Lock()
	idx := s.codeIndex
	pool := s.tsPool
	s.mu.Unlock()
	if idx == nil {
		slog.Warn("[codeintel] backgroundIndexAll: code index not initialized, skipping")
		if s.pendingNotifier != nil {
			if err := s.pendingNotifier(notify.IndexError, map[string]any{
				"error":  "code_index_nil",
				"status": "not_initialized",
			}); err != nil {
				slog.Warn("[codeintel] notify IndexError failed", "error", err)
			}
		}
		return
	}

	idx.SetIndexing(true)
	defer idx.SetIndexing(false)

	// Multi-root strategy: run Pass 1-6 for each root, accumulate all data
	// into a single combined buffer, then do ONE atomic dump to code.db.
	// This prevents each root's dump from overwriting previous roots' data.
	combinedBuf := codeintel.NewGraphBuffer()
	var allRoots []rootFilesEntry
	var totalFiles, totalNodes, totalEdges int

	for i, root := range roots {
		if ctx.Err() != nil {
			return
		}

		pipe := codeintel.NewPipeline(root, idx.DBPath(), pool)
		pipe.EmbeddingFn = idx.EmbeddingFn
		pipe.FullRescan = true
		result, rootBuf, err := pipe.RunWithoutDump(ctx)
		if err != nil {
			// Abort the whole run: the final dump replaces code.db with the
			// combined buffer, so skipping a root here would silently erase
			// that root's data (all of it, old and new). Keeping the previous
			// code.db intact and reporting the error is safer; the operator
			// can re-trigger indexing once the failing root is understood.
			slog.Error("[codeintel] pipeline failed for root — aborting full index, previous code.db kept intact",
				"root", root, "err", err)
			if s.pendingNotifier != nil {
				if nerr := s.pendingNotifier(notify.IndexError, map[string]any{
					"error": fmt.Sprintf("pipeline failed for %s: %v (previous index kept intact)", root, err),
					"root":  root,
				}); nerr != nil {
					slog.Warn("[codeintel] notify IndexError failed", "error", nerr)
				}
			}
			return
		}

		combinedBuf.MergeFrom(rootBuf)
		totalFiles += result.TotalFiles
		totalNodes += result.NodesCreated
		totalEdges += result.EdgesCreated

		allRoots = append(allRoots, rootFilesEntry{root: root, files: result.DiscoveredFiles})

		if s.pendingNotifier != nil {
			if err := s.pendingNotifier(notify.IndexProgress, map[string]any{
				"indexing":      true,
				"indexed_files": totalFiles,
				"total_files":   totalFiles,
				"completeness":  float64(i+1) / float64(len(roots)),
				"nodes":         totalNodes,
				"edges":         totalEdges,
				"duration_ms":   result.Duration.Milliseconds(),
			}); err != nil {
				slog.Warn("[codeintel] notify IndexProgress failed", "error", err)
			}
		}

		// Conventions stay observations, not constraints: a mined convention
		// names a Leiden community and a pattern string, with no file to bind a
		// checker to, so it has no executable predicate (INV-CSE-17). They are
		// surfaced through s.conventions for display and Knowledge.
		if len(result.Conventions) > 0 {
			s.mu.Lock()
			s.conventions = result.Conventions
			s.mu.Unlock()
		}

		slog.Info("[codeintel] pipeline complete for root", "root", root,
			"files", result.TotalFiles, "nodes", result.NodesCreated,
			"edges", result.EdgesCreated, "duration", result.Duration,
			"conventions", len(result.Conventions))
	}

	if ctx.Err() != nil {
		return
	}

	// Single atomic dump of ALL roots' combined data.
	if err := idx.DumpAndReopen(combinedBuf); err != nil {
		slog.Error("[codeintel] combined dump failed", "err", err)
		if s.pendingNotifier != nil {
			if nerr := s.pendingNotifier(notify.IndexError, map[string]any{
				"error": fmt.Sprintf("dump failed: %v", err),
			}); nerr != nil {
				slog.Warn("[codeintel] notify IndexError failed", "error", nerr)
			}
		}
		return
	}
	slog.Info("[codeintel] combined dump complete", "roots", len(roots),
		"files", totalFiles, "nodes", totalNodes, "edges", totalEdges)

	// The buffer resolved what one process could see; this binds the rest against
	// the freshly swapped production DB.
	if err := idx.ResolveEdgeTargets(ctx); err != nil {
		slog.Error("[codeintel] resolve edge targets failed", "err", err)
	} else {
		slog.Info("[codeintel] resolve edge targets complete")
	}

	// MindMap: synchronous after writerDB reconnected to new code.db.
	// Pure SQL + git, no LLM — typically < 500ms (CE-INV-01).
	idx.RegenerateMindMapSync(ctx)

	// Reachability classification: single source of truth for all consumers
	// (INV-CKG-SINGLE-CLASS). Must run after edge resolution + mind map so
	// all edges are finalized before classifying.
	if err := idx.ClassifyReachability(ctx); err != nil {
		slog.Error("[codeintel] reachability classification failed", "err", err)
	}

	if s.pendingNotifier != nil {
		progressPayload := map[string]any{
			"indexing":     false,
			"completeness": 1.0,
		}
		if cov := s.queryCoverageSummary(ctx, idx); cov != nil {
			progressPayload["edge_resolution_rate"] = cov.edgeResolutionRate
			progressPayload["symbols_count"] = cov.symbolsCount
			progressPayload["isolated_count"] = cov.isolatedCount
			progressPayload["name_reachable_count"] = cov.nameReachableCount
			progressPayload["source_only_count"] = cov.sourceOnlyCount
			progressPayload["connected_count"] = cov.connectedCount
			progressPayload["structural_exempt_count"] = cov.structuralExemptCount
		}
		if err := s.pendingNotifier(notify.IndexProgress, progressPayload); err != nil {
			slog.Warn("[codeintel] notify IndexProgress failed", "error", err)
		}
		if err := s.pendingNotifier(notify.IndexComplete, progressPayload); err != nil {
			slog.Warn("[codeintel] notify IndexComplete failed", "error", err)
		}
	}
	slog.Info("[codeintel] all roots indexed via Pipeline", "roots", len(roots))

	s.postIndexWork(ctx, allRoots)
}

type rootFilesEntry struct {
	root  string
	files []string
}

// postIndexWork runs constraint inference, LSP enrichment, and anchor management
// after all roots have been indexed.
func (s *Service) postIndexWork(ctx context.Context, allRoots []rootFilesEntry) {
	s.mu.Lock()
	idx := s.codeIndex
	cell := s.cell
	reg := s.constraintReg
	lspBridge := s.lsp
	s.mu.Unlock()

	for _, rf := range allRoots {
		if reg == nil {
			continue
		}
		readiness := idx.Readiness().Completeness
		// Stamp the cutoff before inference runs: anything this pass re-derives
		// gets a newer LastSeen and survives the sweep below.
		since := time.Now()
		added, sources := inferConstraintsForRoot(reg, idx.DB(), rf.root, readiness)
		reconciled := reg.ReconcileRoot(rf.root, sources, since)
		// Seeding is per-root: boot only seeds the primary workspace, so
		// without this every additional root would run with no baseline.
		seeded := 0
		if reg.CountForRoot(rf.root) == 0 {
			seeded = constraints.SeedGoConstraints(reg, rf.root)
		}
		if added > 0 || reconciled > 0 || seeded > 0 {
			slog.Info("[codeintel] re-inferred constraints after indexing",
				"root", rf.root,
				"readiness", fmt.Sprintf("%.0f%%", readiness*100),
				"added", added, "reconciled", reconciled, "seeded", seeded,
				"total", reg.Count(), "active", len(reg.Active()))
			s.PersistConstraints()
		}
	}

	if lspBridge != nil && idx != nil {
		enriched := idx.EnrichWithLSP(ctx, lspBridge)
		if enriched > 0 {
			slog.Info("[codeintel] LSP enrichment completed", "edges_confirmed", enriched)
		}
	}

	if cell != nil {
		anchorStore := newMemoryAnchorAdapter(cell)
		var totalStale, totalAnchored int
		for _, rf := range allRoots {
			for _, path := range rf.files {
				hash := editengine.QuickFileHash(path)
				if hash == "" {
					continue
				}
				n, err := editengine.MarkStaleByFile(ctx, anchorStore, path, hash)
				if err != nil {
					continue
				}
				totalStale += n
			}
			anchored, _ := editengine.AnchorRecentEntries(ctx, anchorStore, rf.root, 50)
			totalAnchored += anchored
		}
		if totalStale > 0 {
			slog.Info("[anchor] marked stale entries", "count", totalStale)
		}
		if totalAnchored > 0 {
			slog.Info("[anchor] auto-anchored entries", "count", totalAnchored)
		}
	}
}

// inferConstraintsForRoot runs every constraint inferrer for one root and
// returns how many rows it wrote plus the sources safe to reconcile.
//
// A source is only returned when *all* of its inferrers succeeded: several
// passes share one source (C1 and C11 both write inferred_ckg), and a failed
// pass produces nothing, which is indistinguishable from "the evidence is gone"
// to ReconcileRoot. Reporting a source with a failed pass would retire the
// constraints its siblings still legitimately derive.
func inferConstraintsForRoot(reg *constraints.Registry, db *sql.DB, root string, readiness float64) (added int, sources []string) {
	failed := make(map[string]bool, 2)
	run := func(source, pass string, fn func() (int, error)) {
		n, err := fn()
		if err != nil {
			failed[source] = true
			slog.Warn("[codeintel] constraint inference failed", "pass", pass, "root", root, "err", err)
			return
		}
		added += n
		if n > 0 {
			slog.Debug("[codeintel] constraints inferred", "pass", pass, "count", n)
		}
	}

	// The structure scan returns rows instead of writing them and cannot fail.
	for _, c := range constraints.InferFromProject(root, readiness) {
		if reg.Add(c) != nil {
			added++
		}
	}
	ran := []string{constraints.SourceInferred}

	// CKG-derived inference needs a near-complete graph; below that threshold
	// its absence is a cold index, not missing evidence, so nothing is swept.
	if readiness >= 0.9 {
		run(constraints.SourceInferredCKG, "ckg-edges", func() (int, error) {
			return constraints.InferFromCKG(db, reg, root)
		})
		run(constraints.SourceInferredCKG, "C1-type", func() (int, error) {
			return constraints.InferTypeConstraints(db, reg, root)
		})
		run(constraints.SourceInferredCKG, "C11-compat", func() (int, error) {
			return constraints.InferCompatibilityConstraints(db, reg, root)
		})
		run(constraints.SourceInferredPattern, "C4-concurrency", func() (int, error) {
			return constraints.InferConcurrencyConstraints(db, reg, root)
		})
		run(constraints.SourceInferredPattern, "C8-state-machine", func() (int, error) {
			return constraints.InferStateMachineConstraints(db, reg, root)
		})
		run(constraints.SourceInferredPattern, "C9-performance", func() (int, error) {
			return constraints.InferPerformanceConstraints(db, reg, root)
		})
		run(constraints.SourceInferredPattern, "C10-security", func() (int, error) {
			return constraints.InferSecurityConstraints(db, reg, root)
		})
		ran = append(ran, constraints.SourceInferredCKG, constraints.SourceInferredPattern)
	}

	for _, s := range ran {
		if !failed[s] {
			sources = append(sources, s)
		}
	}
	return added, sources
}

type coverageSummary struct {
	isolatedCount         int
	nameReachableCount    int
	sourceOnlyCount       int
	sinkOnlyCount         int
	connectedCount        int
	structuralExemptCount int
	edgeResolutionRate    float64
	symbolsCount          int
}

// queryCoverageSummary reads reachability classification from the single source
// of truth (INV-CKG-SINGLE-CLASS). No consumer derives reachability from edges.
func (s *Service) queryCoverageSummary(ctx context.Context, idx *codeintel.CodeIndex) *coverageSummary {
	db := idx.DB()
	if db == nil {
		return nil
	}

	cs := &coverageSummary{}

	rows, err := db.QueryContext(ctx,
		`SELECT reachability_class, COUNT(*) FROM symbols
		 WHERE kind IN ('function','method','type','interface','class')
		 GROUP BY reachability_class`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var class string
		var count int
		if rows.Scan(&class, &count) != nil {
			continue
		}
		switch class {
		case "connected":
			cs.connectedCount = count
		case "source_only":
			cs.sourceOnlyCount = count
		case "sink_only":
			cs.sinkOnlyCount = count
		case "isolated":
			cs.isolatedCount = count
		case "name_reachable":
			cs.nameReachableCount = count
		case "structural_exempt":
			cs.structuralExemptCount = count
		}
	}

	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM symbols`).Scan(&cs.symbolsCount); err != nil {
		slog.Warn("[codeintel] query symbols count failed", "error", err)
	}

	var totalCallEdges, boundCallEdges int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM edges WHERE kind = 'call'`).Scan(&totalCallEdges); err != nil {
		slog.Warn("[codeintel] query total call edges failed", "error", err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM edges WHERE kind = 'call' AND target_id IS NOT NULL`).Scan(&boundCallEdges); err != nil {
		slog.Warn("[codeintel] query bound call edges failed", "error", err)
	}
	if totalCallEdges > 0 {
		cs.edgeResolutionRate = float64(boundCallEdges) / float64(totalCallEdges)
	}

	return cs
}

// memoryAnchorAdapter adapts wesgine's public Memory API to editengine.MemoryAnchorStore.
type memoryAnchorAdapter struct {
	eng *wesgine.Cell
}

func newMemoryAnchorAdapter(eng *wesgine.Cell) *memoryAnchorAdapter {
	return &memoryAnchorAdapter{eng: eng}
}

// anchorPageSize is how many rows one drain step pulls. It is a page size, not
// a ceiling: every caller below pages until the store runs out. The three
// queries here each used to be a single `Limit: 500` with the real predicate
// applied in Go, which is the page-out filter this codebase keeps rediscovering
// (antipattern 317/343) — row 501 is not "missing", it is silently absent, and
// SetMetadata even returned nil for it, reporting success for a write that
// never happened.
const anchorPageSize = 200

// drainAgentMemory walks this actor's L4 rows, newest page first, handing each
// to match until match has taken want refs or the store is exhausted.
//
// Layer, not ScopeAgent: that scope also holds the agent sharing channels,
// whose rows carry no owner and belong to the Cell. Anchors are this actor's
// facts about their own code, so a scope-addressed query would drag the shared
// handbooks in and offer to re-anchor them.
func drainAgentMemory(
	ctx context.Context,
	mem *wesgine.MemoryHandle,
	want int,
	match func(wesgine.MemoryEntry) bool,
) ([]wesgine.MemoryEntry, error) {
	var out []wesgine.MemoryEntry
	for offset := 0; ; offset += anchorPageSize {
		page, err := mem.List(ctx, wesgine.MemoryListOptions{
			Layer:  wesgine.LayerAgentMemory,
			Actor:  memoryActor,
			Limit:  anchorPageSize,
			Offset: offset,
		})
		if err != nil {
			return nil, err
		}
		for _, e := range page {
			if !match(e) {
				continue
			}
			out = append(out, e)
			if want > 0 && len(out) >= want {
				return out, nil
			}
		}
		if len(page) < anchorPageSize {
			return out, nil
		}
	}
}

func (a *memoryAnchorAdapter) ListByAnchorFile(ctx context.Context, filePath string, limit int) ([]editengine.MemoryRef, error) {
	// The predicate is expressible as a MetaFilter, so it goes into the query
	// rather than into a loop after it — no paging needed, Limit means what it
	// says.
	entries, err := a.eng.Memory().List(ctx, wesgine.MemoryListOptions{
		Layer:      wesgine.LayerAgentMemory,
		Actor:      memoryActor,
		MetaFilter: map[string]string{editengine.AnchorFileKey: filePath},
		Limit:      limit,
	})
	if err != nil {
		return nil, err
	}
	refs := make([]editengine.MemoryRef, 0, len(entries))
	for _, e := range entries {
		refs = append(refs, editengine.MemoryRef{ID: e.ID, Metadata: e.Metadata})
	}
	return refs, nil
}

func (a *memoryAnchorAdapter) ListUnanchored(ctx context.Context, limit int) ([]editengine.MemoryRef, error) {
	// Absence of a metadata key is not expressible as a MetaFilter, so this one
	// genuinely has to page.
	entries, err := drainAgentMemory(ctx, a.eng.Memory(), limit, func(e wesgine.MemoryEntry) bool {
		return e.Metadata[editengine.AnchorFileKey] == ""
	})
	if err != nil {
		return nil, err
	}
	refs := make([]editengine.MemoryRef, 0, len(entries))
	for _, e := range entries {
		refs = append(refs, editengine.MemoryRef{ID: e.ID, Content: e.Content, Metadata: e.Metadata})
	}
	return refs, nil
}

func (a *memoryAnchorAdapter) SetMetadata(ctx context.Context, entryID, key, value string) error {
	found, err := drainAgentMemory(ctx, a.eng.Memory(), 1, func(e wesgine.MemoryEntry) bool {
		return e.ID == entryID
	})
	if err != nil {
		return err
	}
	if len(found) == 0 {
		// Not "nothing to do": the caller asked to record an anchor and the row
		// it names is unreachable. Returning nil here made a lost write look
		// like a completed one, which is how the 500-row window stayed invisible.
		return fmt.Errorf("anchor: memory entry %q not found in %s", entryID, wesgine.LayerAgentMemory)
	}
	entry := found[0]
	if entry.Metadata == nil {
		entry.Metadata = make(map[string]string)
	}
	entry.Metadata[key] = value
	return a.eng.Memory().Save(ctx, entry)
}
