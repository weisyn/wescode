package engine

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/weisyn/wescode/internal/codeintel/boundary"
	wesgine "github.com/weisyn/wesgine"
	"github.com/weisyn/wesgine/knowledge"
)

// IsDocFile reports whether a file has a document extension eligible for
// the Knowledge pipeline. Delegates to the unified boundary package.
func IsDocFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return boundary.IsDocumentExt(ext)
}

// DocRefreshFn is called after a document file is successfully ingested,
// allowing cross-index updates (e.g. Doc↔Code cross-references).
type DocRefreshFn func(ctx context.Context, kbFileID, docPath string)

type DocSync struct {
	cell      *wesgine.Cell
	cellID    string
	roots     []string
	mu        sync.Mutex
	logger    *slog.Logger
	onRefresh DocRefreshFn // optional post-ingest hook
}

func NewDocSync(cell *wesgine.Cell, cellID string, logger *slog.Logger) *DocSync {
	if logger == nil {
		logger = slog.Default()
	}
	return &DocSync{
		cell:   cell,
		cellID: cellID,
		logger: logger,
	}
}

// SetOnRefresh registers a callback invoked after each successful file ingest.
func (d *DocSync) SetOnRefresh(fn DocRefreshFn) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onRefresh = fn
}

// sourceIDForRoot derives a deterministic KB Source ID per workspace root.
func (d *DocSync) sourceIDForRoot(root string) string {
	h := sha256.Sum256([]byte(root))
	return fmt.Sprintf("ws-docs-%s-%x", d.cellID, h[:4])
}

// allSourceIDs returns the source IDs for all currently tracked roots.
func (d *DocSync) allSourceIDs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	ids := make([]string, len(d.roots))
	for i, r := range d.roots {
		ids[i] = d.sourceIDForRoot(r)
	}
	return ids
}

// Start registers per-root Knowledge Sources and performs the initial scan.
func (d *DocSync) Start(_ context.Context, roots []string) {
	d.SyncRoots(context.Background(), roots)
}

// sourceIDPrefix bounds the set of KB sources this DocSync owns. A source
// added through the knowledge UI carries an unrelated ID and must survive a
// workspace-root change untouched.
func (d *DocSync) sourceIDPrefix() string {
	return fmt.Sprintf("ws-docs-%s-", d.cellID)
}

// addSourceForRoot registers a KB Source for a root that is known to be
// absent. Callers must establish that from ListSources first; the error is
// returned, not classified. The previous version called AddSource
// unconditionally and then decided whether the failure mattered by looking
// for "UNIQUE" or "already exists" in the driver's message — a predicate
// that is wrong in both directions. It swallowed genuine insert failures
// whose text happens to contain those words, and, worse, it returned
// nothing at all, so the caller ingested 300 files into a source row that
// was never created.
func (d *DocSync) addSourceForRoot(ctx context.Context, root string) error {
	kh := d.cell.Knowledge()
	if kh == nil {
		return fmt.Errorf("knowledge handle unavailable")
	}
	return kh.AddSource(ctx, knowledge.SourceConfig{
		ID:   d.sourceIDForRoot(root),
		Type: "local",
		Path: root,
	})
}

// SyncRoots reconciles the KB source set against the workspace roots.
//
// The baseline is the engine's source list, not the in-process d.roots.
// d.roots is nil in every fresh process, so diffing against it computed
// "removed: 0" on every boot: a folder dropped from the workspace kept its
// source, files and chunks forever. That leak was invisible from the file
// list and from search — both filter by the current roots — but Stats()
// does not filter, so the two panels disagreed while the cell DB grew
// without bound. The engine's table is the only baseline that outlives the
// process, which makes it the only one that can answer "what is actually
// indexed right now".
func (d *DocSync) SyncRoots(ctx context.Context, newRoots []string) {
	d.syncRoots(ctx, newRoots)
}

// syncRoots is SyncRoots with the queued-file count returned, so Rescan can
// report it without running a second ingest pass.
func (d *DocSync) syncRoots(ctx context.Context, newRoots []string) int {
	kh := d.cell.Knowledge()
	if kh == nil {
		d.logger.Error("[docsync] knowledge handle unavailable; workspace documents will not be indexed")
		return 0
	}

	// Set the roots first: they are what search and path→source lookup
	// must use from here on, whether or not reconciliation succeeds. A
	// missing source makes those queries return nothing; a stale root
	// makes them return the wrong workspace's documents.
	d.mu.Lock()
	d.roots = append([]string(nil), newRoots...)
	d.mu.Unlock()

	live, err := d.reconcileSources(ctx, newRoots)
	if err != nil {
		d.logger.Error("[docsync] ListSources failed; roots not reconciled this run",
			"roots", len(newRoots), "error", err)
		return 0
	}

	// Ingest every live root, not only the ones whose source was just
	// created. Ingest is how edits made while wescode was closed get
	// picked up, and IngestLocalFile skips unchanged content by hash, so
	// the steady-state cost is a stat plus a hash per file. Ingesting
	// only new roots would silently freeze the index of every existing
	// root at whatever it held when the folder was first added.
	var queued int
	for _, root := range newRoots { // slice order, not map order: reproducible logs
		if !live[d.sourceIDForRoot(root)] {
			continue // AddSource failed; reconcileSources already logged it
		}
		queued += d.ingestRoot(ctx, root)
	}
	return queued
}

// rootSource pairs a workspace root with the KB source ID that holds it.
type rootSource struct {
	ID   string
	Root string
}

// reconcilePlan decides which sources to create and which to drop. It is
// separated from the I/O because every bug this file had lived in exactly
// this decision and nowhere else:
//
//   - the baseline was the in-process root cache, which is nil in every
//     fresh process, so `remove` was empty on every boot and a folder
//     dropped from the workspace kept its source, files and chunks forever;
//   - "already present" was inferred from an AddSource error message
//     containing "UNIQUE", which is both a false negative (a genuine insert
//     failure whose text happens to match is swallowed) and unnecessary
//     (the source list answers the question directly).
//
// `add` follows want order and `remove` follows existing order so the
// resulting log lines are reproducible run to run.
func reconcilePlan(existing []string, want []rootSource, prefix string) (add []rootSource, remove []string) {
	wanted := make(map[string]bool, len(want))
	for _, w := range want {
		wanted[w.ID] = true
	}

	have := make(map[string]bool, len(existing))
	for _, id := range existing {
		// Sources outside our prefix belong to the knowledge UI. They
		// must survive a workspace-root change untouched — the whole
		// point of scoping the sweep rather than deleting "everything
		// not in the current roots".
		if !strings.HasPrefix(id, prefix) {
			continue
		}
		have[id] = true
		if !wanted[id] {
			remove = append(remove, id)
		}
	}

	for _, w := range want {
		if !have[w.ID] {
			add = append(add, w)
		}
	}
	return add, remove
}

// reconcileSources makes the engine's source table match roots and returns
// the set of source IDs that are usable afterwards. Roots whose AddSource
// failed are absent from that set: ingesting into a source row that does
// not exist is how a root reports hundreds of successful ingests and then
// searches empty.
func (d *DocSync) reconcileSources(ctx context.Context, roots []string) (map[string]bool, error) {
	kh := d.cell.Knowledge()
	existing, err := kh.ListSources(ctx)
	if err != nil {
		return nil, err
	}

	existingIDs := make([]string, len(existing))
	for i, s := range existing {
		existingIDs[i] = s.ID
	}
	want := make([]rootSource, len(roots))
	for i, r := range roots {
		want[i] = rootSource{ID: d.sourceIDForRoot(r), Root: r}
	}

	add, remove := reconcilePlan(existingIDs, want, d.sourceIDPrefix())

	live := make(map[string]bool, len(want))
	for _, w := range want {
		live[w.ID] = true
	}

	var added, removed int
	for _, w := range add {
		if addErr := d.addSourceForRoot(ctx, w.Root); addErr != nil {
			delete(live, w.ID) // not usable: skip the ingest, not just the log
			d.logger.Error("[docsync] AddSource failed; this root will not be indexed",
				"root", w.Root, "sourceID", w.ID, "error", addErr)
			continue
		}
		added++
	}

	for _, sid := range remove {
		// One transaction over bindings → chunks → files → source
		// (INV-KB-02). The per-file DeleteFile loop this replaces was
		// neither atomic nor complete: it left the wes_kb_sources row
		// registered and counted attempts as removals.
		if rmErr := kh.RemoveSource(ctx, sid); rmErr != nil {
			d.logger.Error("[docsync] RemoveSource failed; documents from a removed folder stay searchable",
				"sourceID", sid, "error", rmErr)
			continue
		}
		removed++
	}

	if added > 0 || removed > 0 {
		d.logger.Info("[docsync] sources reconciled",
			"roots", len(roots), "added", added, "removed", removed)
	}
	return live, nil
}

// ingestRoot scans a single root directory and ingests all document files.
// Returns the number of files for which indexing was actually queued.
func (d *DocSync) ingestRoot(ctx context.Context, folder string) int {
	kh := d.cell.Knowledge()
	if kh == nil {
		return 0
	}
	sid := d.sourceIDForRoot(folder)
	files := scanDocFiles(folder)
	var queued, failed int
	for _, f := range files {
		if ctx.Err() != nil {
			break
		}
		info, err := kh.IngestLocalFile(ctx, sid, f)
		if err != nil {
			// Per-file detail at Debug, the count at Error below. The
			// old code capped the log at the first five failures, which
			// turned "308 of 312 files never reached the index" into
			// five lines that read like isolated hiccups.
			failed++
			d.logger.Debug("[docsync] ingest failed", "file", f, "sourceID", sid, "error", err)
			continue
		}
		// IngestLocalFile is content-hash idempotent: an unchanged file
		// returns its stored record without doing work. Counting those
		// made every boot report the same number and read as a full
		// re-index of a workspace nobody had touched.
		if info != nil && info.Status == knowledge.StatusPending {
			queued++
		}
	}
	if failed > 0 {
		d.logger.Error("[docsync] root ingested with failures", "folder", folder,
			"files_found", len(files), "queued", queued, "failed", failed)
		return queued
	}
	// Debug when there was nothing to do: this runs on every boot and
	// every root change, and a line that always says "queued 0" is the
	// kind of noise that trains people to stop reading the log.
	if queued == 0 {
		d.logger.Debug("[docsync] root up to date", "folder", folder, "files_found", len(files))
		return 0
	}
	d.logger.Info("[docsync] root ingested", "folder", folder,
		"files_found", len(files), "queued", queued)
	return queued
}

func (d *DocSync) OnFileSaved(path string) {
	if !IsDocFile(path) {
		return
	}
	kh := d.cell.Knowledge()
	if kh == nil {
		return
	}
	sid := d.sourceIDForPath(path)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		info, err := kh.IngestLocalFile(ctx, sid, path)
		if err != nil {
			d.logger.Debug("[docsync] save-ingest failed", "path", path, "error", err)
			return
		}
		// Notify cross-index (Doc↔Code references) after successful ingest.
		d.mu.Lock()
		fn := d.onRefresh
		d.mu.Unlock()
		if fn != nil && info != nil {
			fn(ctx, info.ID, path)
		}
	}()
}

func (d *DocSync) OnFileDeleted(path string) {
	if !IsDocFile(path) {
		return
	}
	kh := d.cell.Knowledge()
	if kh == nil {
		return
	}
	sid := d.sourceIDForPath(path)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		files, err := kh.ListFiles(ctx, knowledge.FileListOptions{SourceID: sid})
		if err != nil {
			return
		}
		for _, f := range files {
			if f.Path == path || f.RawPath == path {
				if err := kh.DeleteFile(ctx, f.ID); err != nil {
					// A deleted file left in the index is a document the
					// agent can still cite. Rescan is the recovery path.
					d.logger.Error("[docsync] delete-from-KB failed; a deleted file stays searchable",
						"path", path, "fileID", f.ID, "error", err)
					return
				}
				d.logger.Debug("[docsync] deleted from KB", "path", path, "fileID", f.ID)
				return
			}
		}
	}()
}

// sourceIDForPath finds the owning root for a file path and returns its source ID.
func (d *DocSync) sourceIDForPath(path string) string {
	d.mu.Lock()
	roots := append([]string(nil), d.roots...)
	d.mu.Unlock()
	for _, r := range roots {
		if strings.HasPrefix(path, r+string(filepath.Separator)) || path == r {
			return d.sourceIDForRoot(r)
		}
	}
	if len(roots) > 0 {
		return d.sourceIDForRoot(roots[0])
	}
	return fmt.Sprintf("ws-docs-%s-orphan", d.cellID)
}

func (d *DocSync) Rescan(ctx context.Context) (int, int, error) {
	kh := d.cell.Knowledge()
	if kh == nil {
		return 0, 0, nil
	}

	d.mu.Lock()
	roots := append([]string(nil), d.roots...)
	d.mu.Unlock()

	// Adds and updates are exactly what syncRoots does; reproducing the
	// ingest loop here is how the two paths drift. What Rescan adds is
	// the delete half: files that left the disk while the watcher was
	// not running.
	queued := d.syncRoots(ctx, roots)

	var orphaned int
	for _, root := range roots {
		orphaned += d.pruneOrphans(ctx, root)
	}

	d.logger.Info("[docsync] rescan complete", "queued", queued, "orphans_removed", orphaned)
	return queued, orphaned, nil
}

// pruneOrphans deletes index entries under root whose file is gone from
// disk. Returns the number actually deleted — not attempted: the `_ =` this
// replaces reported every orphan as cleaned while it stayed searchable.
func (d *DocSync) pruneOrphans(ctx context.Context, root string) int {
	kh := d.cell.Knowledge()
	sid := d.sourceIDForRoot(root)

	onDisk := make(map[string]bool)
	for _, f := range scanDocFiles(root) {
		onDisk[f] = true
	}

	indexed, err := kh.ListFiles(ctx, knowledge.FileListOptions{SourceID: sid})
	if err != nil {
		d.logger.Error("[docsync] rescan: ListFiles failed; orphans under this root not cleaned",
			"root", root, "sourceID", sid, "error", err)
		return 0
	}

	var removed int
	for _, fi := range indexed {
		if ctx.Err() != nil {
			break
		}
		p := fi.Path
		if p == "" {
			p = fi.RawPath
		}
		if onDisk[p] {
			continue
		}
		if delErr := kh.DeleteFile(ctx, fi.ID); delErr != nil {
			d.logger.Error("[docsync] rescan: DeleteFile failed; orphan stays in the index",
				"fileID", fi.ID, "path", p, "error", delErr)
			continue
		}
		removed++
	}
	return removed
}

func (d *DocSync) ListFiles(ctx context.Context) ([]knowledge.FileInfo, error) {
	kh := d.cell.Knowledge()
	if kh == nil {
		return nil, nil
	}
	var all []knowledge.FileInfo
	for _, sid := range d.allSourceIDs() {
		files, err := kh.ListFiles(ctx, knowledge.FileListOptions{SourceID: sid})
		if err != nil {
			return nil, err
		}
		all = append(all, files...)
	}
	return all, nil
}

func (d *DocSync) Stats(ctx context.Context) (*knowledge.KnowledgeStats, error) {
	kh := d.cell.Knowledge()
	if kh == nil {
		return nil, nil
	}
	return kh.Stats(ctx)
}

func (d *DocSync) Search(ctx context.Context, query string) ([]knowledge.SearchResult, error) {
	kh := d.cell.Knowledge()
	if kh == nil {
		return nil, nil
	}
	return kh.Search(ctx, query, knowledge.SearchOptions{SourceIDs: d.allSourceIDs()})
}

func scanDocFiles(root string) []string {
	classifier := boundary.NewClassifier(boundary.ModeModerate, nil)
	classifier.SetWescodeignore(boundary.LoadWescodeignoreCached(root))

	var result []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			rel, _ := filepath.Rel(root, path)
			if skip, _ := classifier.ShouldSkipDir(d.Name(), rel); skip {
				return filepath.SkipDir
			}
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		verdict := classifier.ClassifyFile(path, info, nil)
		if verdict.Pipeline == boundary.PipelineKnowledge {
			result = append(result, path)
		}
		return nil
	})
	return result
}
