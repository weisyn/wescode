package codeintel

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/weisyn/wescode/internal/codeintel/boundary"
	"github.com/weisyn/wescode/internal/treesitter"
)

// FileWatcher monitors workspace files for changes and triggers incremental
// re-indexing. It is event-driven: the VSCode extension pushes file change
// events via editor/fileChanged RPC → NotifyChange(). A background loop
// flushes pending changes (debounced) and periodically checks git HEAD
// as a safety net for bulk operations like git checkout.
type FileWatcher struct {
	index   *CodeIndex
	ts      *treesitter.ParserPool
	workDir string
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	mu       sync.Mutex
	pending  map[string]time.Time // path → first-seen time (debounce)
	wake     chan struct{}        // signaled when NotifyChange adds to pending
	debounce time.Duration
	roots    func() []string // live workspace roots; nil → workDir only
}

// SetRootProvider supplies the live workspace root set. Boundary classification
// bounds its ancestor walk at the enclosing root, and one watcher serves every
// root (one process, one index), so without the set a file in a secondary root
// would be judged against the primary root's ancestry — and judged "outside",
// which excludes it.
func (fw *FileWatcher) SetRootProvider(fn func() []string) {
	fw.mu.Lock()
	fw.roots = fn
	fw.mu.Unlock()
}

// rootFor returns the workspace root containing path, or "" when none does.
// Longest match wins: nested roots are legal and the inner one owns the file's
// .wescodeignore.
func (fw *FileWatcher) rootFor(path string) string {
	fw.mu.Lock()
	provider := fw.roots
	fw.mu.Unlock()

	roots := []string{fw.workDir}
	if provider != nil {
		if live := provider(); len(live) > 0 {
			roots = live
		}
	}
	best := ""
	for _, root := range roots {
		if root == "" {
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if len(root) > len(best) {
			best = root
		}
	}
	return best
}

// admitter reports whether a path may hold rows in the index, running the same
// pipeline a full walk would.
//
// ModeModerate is not a preference — engine.backgroundIndexAll runs the full
// pass with FullRescan, which is ModeModerate. Any other mode here makes the
// two passes disagree about the same file: the full pass indexes docs/ and
// scripts/, a stricter incremental pass then declines to refresh them, and
// their symbols go stale with nothing logged (reindexGitDiff used ModeFast).
//
// One classifier per root, memoized: .wescodeignore is per-root and a batch
// routinely spans roots.
func (fw *FileWatcher) admitter() func(path string, info os.FileInfo) bool {
	cache := make(map[string]*boundary.Classifier)
	return func(path string, info os.FileInfo) bool {
		root := fw.rootFor(path)
		if root == "" {
			return false
		}
		c, ok := cache[root]
		if !ok {
			c = boundary.NewClassifier(boundary.ModeModerate, nil)
			c.SetWorkDir(root) // .wescodeignore patterns are root-relative
			c.SetWescodeignore(boundary.LoadWescodeignoreCached(root))
			cache[root] = c
		}
		return c.ClassifyPathUnder(root, path, info, nil).Pipeline != boundary.PipelineExcluded
	}
}

// NewFileWatcher creates a watcher that monitors the workspace for file changes.
func NewFileWatcher(index *CodeIndex, ts *treesitter.ParserPool, workDir string) *FileWatcher {
	return &FileWatcher{
		index:    index,
		ts:       ts,
		workDir:  workDir,
		pending:  make(map[string]time.Time),
		wake:     make(chan struct{}, 1),
		debounce: 200 * time.Millisecond,
	}
}

// Start begins the background file watching loop.
func (fw *FileWatcher) Start(ctx context.Context) {
	ctx, fw.cancel = context.WithCancel(ctx)
	fw.wg.Add(1)
	go fw.watchLoop(ctx)
	slog.Info("[codeintel] file watcher started (event-driven)", "workDir", fw.workDir)
}

// Stop halts the watcher.
func (fw *FileWatcher) Stop() {
	if fw.cancel != nil {
		fw.cancel()
	}
	fw.wg.Wait()
}

// NotifyChange adds a file path to the pending re-index queue (debounced).
// Called from RPC handlers when VSCode FileSystemWatcher or editor events fire.
func (fw *FileWatcher) NotifyChange(path string) {
	if filepath.Base(path) == ".wescodeignore" {
		// Invalidate the root that owns this file, not the primary one: in a
		// multi-root workspace those differ, and dropping the wrong entry
		// leaves the edited rules cached until restart.
		if root := fw.rootFor(path); root != "" {
			boundary.InvalidateIgnoreCache(root)
		}
	}
	fw.mu.Lock()
	if _, ok := fw.pending[path]; !ok {
		fw.pending[path] = time.Now()
	}
	fw.mu.Unlock()
	// Non-blocking wake signal.
	select {
	case fw.wake <- struct{}{}:
	default:
	}
}

func (fw *FileWatcher) watchLoop(ctx context.Context) {
	defer fw.wg.Done()

	// Git HEAD check at 10s intervals (safety net for bulk ops).
	gitTicker := time.NewTicker(10 * time.Second)
	defer gitTicker.Stop()

	var lastGitHead string
	var gitTickCount int

	for {
		select {
		case <-ctx.Done():
			return
		case <-fw.wake:
			// Wait for debounce period then flush.
			time.Sleep(fw.debounce)
			fw.flushPending(ctx)
		case <-gitTicker.C:
			// Flush any pending changes (from save events etc.).
			fw.flushPending(ctx)
			lastGitHead = fw.checkGitHead(ctx, lastGitHead)

			// Periodic staleness scan every ~60s.
			gitTickCount++
			if gitTickCount%6 == 0 {
				fw.index.ScanStaleness(ctx) // keep the readiness counter current
				fw.reindexStale(ctx)        // converge changed/deleted files
			}
		}
	}
}

// reindexStale converges the index with disk state for files whose change
// events were missed (bulk operations without a git HEAD change, dropped
// RPC events). Changed files are re-indexed incrementally; deleted files
// have their stale data removed. Skipped while a full index is running to
// avoid racing the atomic DB replacement.
func (fw *FileWatcher) reindexStale(ctx context.Context) {
	if fw.index.IsIndexing() {
		return
	}
	stale := fw.index.ScanStalenessFiles(ctx)
	if len(stale) == 0 {
		return
	}

	admit := fw.admitter()
	var toParse []FileToParse
	var removed, evicted int
	for _, path := range stale {
		info, err := os.Stat(path)
		if err != nil {
			if removeErr := fw.index.RemoveFile(ctx, path); removeErr == nil {
				removed++
			} else {
				slog.Warn("[codeintel] staleness remove failed", "path", path, "err", removeErr)
			}
			continue
		}
		// Rows written before classification reached this path (or by a mode
		// the pipeline has since tightened) are evicted rather than refreshed.
		// Skipping them instead would leave .git/FETCH_HEAD reported stale on
		// every fetch, forever — visible only as a recovery count that never
		// reaches zero.
		if !admit(path, info) {
			if removeErr := fw.index.RemoveFile(ctx, path); removeErr == nil {
				evicted++
			} else {
				slog.Warn("[codeintel] staleness evict failed", "path", path, "err", removeErr)
			}
			continue
		}
		lang := ""
		if l, ok := treesitter.DetectLang(path); ok {
			lang = string(l)
		}
		toParse = append(toParse, FileToParse{Path: path, MtimeNs: info.ModTime().UnixNano(), Language: lang})
	}

	if len(toParse) > 0 {
		if err := fw.index.IndexFilesViaWorker(ctx, toParse); err != nil {
			slog.Warn("[codeintel] staleness re-index failed", "files", len(toParse), "err", err)
			return
		}
	}
	if len(toParse) > 0 || removed > 0 || evicted > 0 {
		slog.Info("[codeintel] staleness recovery",
			"reindexed", len(toParse), "removed", removed, "evicted", evicted)
	}
}

func (fw *FileWatcher) flushPending(ctx context.Context) {
	fw.mu.Lock()
	if len(fw.pending) == 0 {
		fw.mu.Unlock()
		return
	}
	// While a full (multi-root) index is running, do NOT consume pending
	// changes: their incremental write would race the atomic DB replacement
	// at the end of the full index. Events stay queued and are flushed on a
	// later wake/git-tick once indexing completes.
	if fw.index.IsIndexing() {
		fw.mu.Unlock()
		return
	}

	now := time.Now()
	var ready []string
	for path, firstSeen := range fw.pending {
		if now.Sub(firstSeen) >= fw.debounce {
			ready = append(ready, path)
		}
	}
	for _, path := range ready {
		delete(fw.pending, path)
	}
	fw.mu.Unlock()

	if len(ready) == 0 {
		return
	}

	// All batches (including >=10 files) go through the incremental Worker
	// path (row-level delta). Previously, large batches ran RunPipeline, whose
	// Pass 10 atomically replaced the whole code.db with a single-root delta
	// buffer — destroying every other workspace root's CKG data (multi-root
	// loss regression, see watcher_test.go). Pipeline is only safe for a
	// full multi-root rebuild (engine.backgroundIndexAll).
	// Dispatch to Worker subprocess (crash-isolated, no in-process tree-sitter).
	admit := fw.admitter()
	filesToParse := make([]FileToParse, 0, len(ready))
	for _, path := range ready {
		info, err := os.Stat(path)
		if err != nil {
			// File disappeared since the change event: remove its stale
			// index data instead of silently skipping it (symbols are
			// cascade-deleted from edges; FTS is synced by trigger).
			if removeErr := fw.index.RemoveFile(ctx, path); removeErr != nil {
				slog.Warn("[codeintel] remove stale index failed", "path", path, "err", removeErr)
			}
			continue
		}
		// VSCode's FileSystemWatcher reports everything under the workspace,
		// including .git/FETCH_HEAD and node_modules. Nothing downstream
		// filters, so an unclassified event is an index row.
		if !admit(path, info) {
			continue
		}
		lang := ""
		if l, ok := treesitter.DetectLang(path); ok {
			lang = string(l)
		}
		filesToParse = append(filesToParse, FileToParse{
			Path:     path,
			MtimeNs:  info.ModTime().UnixNano(),
			Language: lang,
		})
	}
	if len(filesToParse) == 0 {
		return
	}

	start := time.Now()
	if err := fw.index.IndexFilesViaWorker(ctx, filesToParse); err != nil {
		slog.Warn("[codeintel] incremental worker index failed", "files", len(filesToParse), "err", err)
		return
	}
	slog.Info("[codeintel] incremental index via worker",
		"files", len(filesToParse), "elapsed", time.Since(start))
}

func (fw *FileWatcher) checkGitHead(ctx context.Context, lastHead string) string {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	cmd.Dir = fw.workDir
	out, err := cmd.Output()
	if err != nil {
		return lastHead
	}
	currentHead := strings.TrimSpace(string(out))

	if lastHead != "" && currentHead != lastHead {
		slog.Info("[codeintel] git HEAD changed, checking diff", "from", lastHead[:8], "to", currentHead[:min(8, len(currentHead))])
		fw.reindexGitDiff(ctx, lastHead, currentHead)
		// CE-INV-07: invalidate MindMap on HEAD change (rate-limited inside GenerateMindMap).
		InvalidateMindMapCache()
		if fw.index.ShouldRegenerateMindMap() {
			mmCache.mu.RLock()
			cachedHints := mmCache.hints
			mmCache.mu.RUnlock()
			go fw.index.GenerateMindMap(context.Background(), fw.workDir, cachedHints)
		}
	}

	return currentHead
}

func (fw *FileWatcher) reindexGitDiff(ctx context.Context, fromRef, toRef string) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", fromRef, toRef)
	cmd.Dir = fw.workDir
	out, err := cmd.Output()
	if err != nil {
		slog.Debug("[codeintel] git diff failed", "error", err)
		return
	}

	admit := fw.admitter()
	var reindexed, removed int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		path := filepath.Join(fw.workDir, line)
		info, statErr := os.Stat(path)
		if statErr != nil {
			// Deleted by the checkout. Skipping leaves its symbols searchable
			// under a path that no longer exists — the branch's files bleed
			// into the branch that replaced it.
			if removeErr := fw.index.RemoveFile(ctx, path); removeErr == nil {
				removed++
			}
			continue
		}
		if !admit(path, info) {
			continue
		}
		if err := fw.index.IndexFile(ctx, path); err == nil {
			reindexed++
		}
	}
	if reindexed > 0 || removed > 0 {
		slog.Info("[codeintel] git checkout reindex", "files", reindexed, "removed", removed)
	}
}
