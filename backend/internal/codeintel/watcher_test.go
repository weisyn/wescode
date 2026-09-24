package codeintel

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Regression: a batch of >= pipelineThreshold file changes must NOT rebuild
// the production DB from a single root. The former flushPending pipeline
// branch called RunPipeline(fw.workDir): its Pass 10 (DumpToProduction)
// atomically replaced code.db with one root's delta buffer, destroying every
// other workspace root's CKG data. The incremental worker path only applies
// row-level deltas and must be used for all batches.
func TestFlushPendingBatchPreservesOtherRootData(t *testing.T) {
	ci, dir := newTestIndex(t)
	rootA := filepath.Join(dir, "rootA")
	rootB := filepath.Join(dir, "rootB")
	if err := os.MkdirAll(rootA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rootB, 0o755); err != nil {
		t.Fatal(err)
	}

	// Root B: one file whose symbols must survive any root-A-only re-index.
	bGo := filepath.Join(rootB, "b.go")
	if err := os.WriteFile(bGo, []byte("package main\n\nfunc RootBSentinel() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ci.IndexFile(context.Background(), bGo); err != nil {
		t.Fatalf("index rootB file: %v", err)
	}

	// Root A: 11 files (>= pipelineThreshold). One is changed after indexing;
	// the other ten are new files. Together they form the batch.
	const batchSize = 11
	aFiles := make([]string, 0, batchSize)
	changed := filepath.Join(rootA, "a0.go")
	if err := os.WriteFile(changed, []byte("package main\n\nfunc RootAOld() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ci.IndexFile(context.Background(), changed); err != nil {
		t.Fatalf("index rootA changed file: %v", err)
	}
	aFiles = append(aFiles, changed)

	for i := 1; i < batchSize; i++ {
		p := filepath.Join(rootA, fmt.Sprintf("a%d.go", i))
		if err := os.WriteFile(p, []byte(fmt.Sprintf("package main\n\nfunc RootANew%d() {}\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		aFiles = append(aFiles, p)
	}

	// Make a0.go a real delta: different content, different mtime.
	future := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(changed, []byte("package main\n\nfunc RootANew0() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(changed, future, future); err != nil {
		t.Fatal(err)
	}

	// Simulate the watcher batch: queue all paths, then flush immediately.
	fw := NewFileWatcher(ci, ci.ts, rootA)
	// 0, not time.Nanosecond: on Windows two adjacent time.Now() calls can
	// return the same instant, so `elapsed >= 1ns` is never satisfied and
	// flushPending returns having done nothing — no row, no log, and a test
	// that reads as "the classifier rejected everything". `>= 0` holds on every
	// platform and is what "no debounce" actually means.
	fw.debounce = 0 // bypass debounce wait
	for _, p := range aFiles {
		fw.NotifyChange(p)
	}
	fw.flushPending(context.Background())

	// Root B sentinel must still be present. (Before the fix, the pipeline
	// branch replaced code.db with the root-A delta buffer and this symbol
	// disappeared.)
	db, err := sql.Open("sqlite", ci.dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM symbols WHERE name = 'RootBSentinel'").Scan(&count); err != nil {
		t.Fatalf("query rootB sentinel: %v", err)
	}
	if count != 1 {
		t.Fatalf("RootBSentinel count = %d, want 1 (root B data must survive a root A batch)", count)
	}

	// The changed root A file must have been re-indexed with its new content.
	var newCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM symbols WHERE name = 'RootANew0'").Scan(&newCount); err != nil {
		t.Fatalf("query rootA new symbol: %v", err)
	}
	if newCount != 1 {
		t.Fatalf("RootANew0 count = %d, want 1 (changed file must be re-indexed)", newCount)
	}
}

// Regression: while a full (multi-root) index is running, flushPending must
// NOT consume pending changes (their incremental write would race the atomic
// DB replacement at the end of the full index). Events stay queued until the
// next flush after indexing finishes.
func TestFlushPendingGateDuringIndexing(t *testing.T) {
	ci, dir := newTestIndex(t)

	goFile := filepath.Join(dir, "a.go")
	if err := os.WriteFile(goFile, []byte("package main\n\nfunc Before() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ci.IndexFile(context.Background(), goFile); err != nil {
		t.Fatalf("initial index: %v", err)
	}

	// Change the file so it becomes a pending delta.
	future := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(goFile, []byte("package main\n\nfunc After() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(goFile, future, future); err != nil {
		t.Fatal(err)
	}

	fw := NewFileWatcher(ci, ci.ts, dir)
	fw.debounce = 0

	// While indexing is in progress, a flush must not process the change.
	ci.SetIndexing(true)
	fw.NotifyChange(goFile)
	time.Sleep(time.Millisecond)
	fw.flushPending(context.Background())

	db, err := sql.Open("sqlite", ci.dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	var beforeCount, afterCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM symbols WHERE name = 'Before'").Scan(&beforeCount); err != nil {
		t.Fatalf("query Before: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM symbols WHERE name = 'After'").Scan(&afterCount); err != nil {
		t.Fatalf("query After: %v", err)
	}
	if afterCount != 0 {
		t.Fatalf("After count = %d, want 0 (change must not be applied while indexing)", afterCount)
	}

	// The pending change must still be queued for a later flush.
	fw.mu.Lock()
	pending := len(fw.pending)
	fw.mu.Unlock()
	if pending != 1 {
		t.Fatalf("pending = %d, want 1 (change must not be consumed during indexing)", pending)
	}

	// Once indexing finishes, the next flush applies the queued change.
	ci.SetIndexing(false)
	fw.flushPending(context.Background())

	if err := db.QueryRow("SELECT COUNT(*) FROM symbols WHERE name = 'After'").Scan(&afterCount); err != nil {
		t.Fatalf("query After: %v", err)
	}
	if afterCount != 1 {
		t.Fatalf("After count = %d, want 1 (queued change must be applied after indexing)", afterCount)
	}
}

// Regression: when a tracked file disappears from disk, flushPending must
// remove its stale index data instead of silently skipping it.
func TestFlushPendingRemovesDeletedFile(t *testing.T) {
	ci, dir := newTestIndex(t)

	goFile := filepath.Join(dir, "gone.go")
	if err := os.WriteFile(goFile, []byte("package main\n\nfunc GoneSymbol() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ci.IndexFile(context.Background(), goFile); err != nil {
		t.Fatalf("index file: %v", err)
	}

	if err := os.Remove(goFile); err != nil {
		t.Fatal(err)
	}

	fw := NewFileWatcher(ci, ci.ts, dir)
	fw.debounce = 0
	fw.NotifyChange(goFile)
	time.Sleep(time.Millisecond)
	fw.flushPending(context.Background())

	db, err := sql.Open("sqlite", ci.dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	var symCount, hashCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM symbols WHERE name = 'GoneSymbol'").Scan(&symCount); err != nil {
		t.Fatalf("query symbol: %v", err)
	}
	if symCount != 0 {
		t.Fatalf("symbol count = %d, want 0 (deleted file's symbols must be removed)", symCount)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM file_hashes WHERE file_path = ?", goFile).Scan(&hashCount); err != nil {
		t.Fatalf("query hash: %v", err)
	}
	if hashCount != 0 {
		t.Fatalf("hash count = %d, want 0 (deleted file's hash row must be removed)", hashCount)
	}
}

// Regression: ScanStalenessFiles must report files whose indexed mtime
// differs from disk — both changed files and files deleted since indexing.
func TestScanStalenessFiles(t *testing.T) {
	ci, dir := newTestIndex(t)

	f1 := filepath.Join(dir, "changed.go")
	if err := os.WriteFile(f1, []byte("package main\n\nfunc V1() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ci.IndexFile(context.Background(), f1); err != nil {
		t.Fatalf("index f1: %v", err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(f1, []byte("package main\n\nfunc V2() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(f1, future, future); err != nil {
		t.Fatal(err)
	}

	f2 := filepath.Join(dir, "deleted.go")
	if err := os.WriteFile(f2, []byte("package main\n\nfunc DeletedSym() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ci.IndexFile(context.Background(), f2); err != nil {
		t.Fatalf("index f2: %v", err)
	}
	if err := os.Remove(f2); err != nil {
		t.Fatal(err)
	}

	f3 := filepath.Join(dir, "stable.go")
	if err := os.WriteFile(f3, []byte("package main\n\nfunc StableSym() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ci.IndexFile(context.Background(), f3); err != nil {
		t.Fatalf("index f3: %v", err)
	}

	stale := ci.ScanStalenessFiles(context.Background())
	got := make(map[string]bool, len(stale))
	for _, p := range stale {
		got[p] = true
	}
	// The index returns its own representation (slash-normalized), so the
	// expectations are normalized too rather than converted back — CKG hands
	// slash paths to every consumer, and os.Stat/ReadFile accept them.
	if !got[IndexPath(f1)] {
		t.Errorf("changed file not reported stale: %v", stale)
	}
	if !got[IndexPath(f2)] {
		t.Errorf("deleted file not reported stale: %v", stale)
	}
	if got[IndexPath(f3)] {
		t.Errorf("stable file reported stale: %v", stale)
	}
}

// Regression: the watcher's staleness recovery must re-index changed files
// and remove deleted files whose change events were missed.
func TestWatcherStalenessRecovery(t *testing.T) {
	ci, dir := newTestIndex(t)

	f1 := filepath.Join(dir, "changed.go")
	if err := os.WriteFile(f1, []byte("package main\n\nfunc V1() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ci.IndexFile(context.Background(), f1); err != nil {
		t.Fatalf("index f1: %v", err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(f1, []byte("package main\n\nfunc V2() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(f1, future, future); err != nil {
		t.Fatal(err)
	}

	f2 := filepath.Join(dir, "deleted.go")
	if err := os.WriteFile(f2, []byte("package main\n\nfunc DeletedSym() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ci.IndexFile(context.Background(), f2); err != nil {
		t.Fatalf("index f2: %v", err)
	}
	if err := os.Remove(f2); err != nil {
		t.Fatal(err)
	}

	fw := NewFileWatcher(ci, ci.ts, dir)
	fw.reindexStale(context.Background())

	db, err := sql.Open("sqlite", ci.dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	var v2Count, deletedCount, stableCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM symbols WHERE name = 'V2'").Scan(&v2Count); err != nil {
		t.Fatalf("query V2: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM symbols WHERE name = 'DeletedSym'").Scan(&deletedCount); err != nil {
		t.Fatalf("query DeletedSym: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM symbols WHERE name = 'StableSym'").Scan(&stableCount); err != nil {
		t.Fatalf("query StableSym: %v", err)
	}
	if v2Count != 1 {
		t.Fatalf("V2 count = %d, want 1 (changed file must be re-indexed)", v2Count)
	}
	if deletedCount != 0 {
		t.Fatalf("DeletedSym count = %d, want 0 (deleted file must be removed)", deletedCount)
	}
	if stableCount != 0 {
		t.Fatalf("StableSym count = %d, want 0 (never existed)", stableCount)
	}
}

// A batch holding only files tree-sitter cannot parse must still converge.
// IndexFilesViaWorker used to return before writing file_hashes when the batch
// produced no parseable work, so the same JSON/shell files were re-reported as
// stale on every tick — the log said "reindexed: 23" forever while nothing
// changed. The assertion is the second scan, not the first.
func TestWatcherStalenessRecovery_NonParseableBatchConverges(t *testing.T) {
	ci, dir := newTestIndex(t)
	ctx := context.Background()

	cfg := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfg, []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ci.IndexFile(ctx, cfg); err != nil {
		t.Fatalf("index cfg: %v", err)
	}

	future := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(cfg, []byte(`{"a":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(cfg, future, future); err != nil {
		t.Fatal(err)
	}

	// Vacuity guard: without this the convergence assertion below passes on an
	// index that never considered the file stale in the first place.
	if before := ci.ScanStalenessFiles(ctx); len(before) != 1 || before[0] != IndexPath(cfg) {
		t.Fatalf("pre-recovery staleness = %v, want exactly [%s]", before, IndexPath(cfg))
	}

	fw := NewFileWatcher(ci, ci.ts, dir)
	fw.reindexStale(ctx)

	if after := ci.ScanStalenessFiles(ctx); len(after) != 0 {
		t.Fatalf("post-recovery staleness = %v, want empty (recovery must converge)", after)
	}
}

func hashRowCount(t *testing.T, ci *CodeIndex, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", ci.dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	var n int
	// file_path is stored slash-normalized, while fixtures build paths with
	// filepath.Join. Querying the raw form matches nothing on Windows, and the
	// zero shows up as "the classifier rejected this file" rather than as a
	// representation mismatch.
	if err := db.QueryRow("SELECT COUNT(*) FROM file_hashes WHERE file_path = ?", IndexPath(path)).Scan(&n); err != nil {
		t.Fatalf("query hash row for %s: %v", path, err)
	}
	return n
}

// The directory half of the boundary pipeline (ShouldSkipDir) only ran during
// a full walk. The watcher never walks — VSCode pushes paths — so .git and
// node_modules reached ClassifyFile, which judges files and admits them.
func TestFlushPendingExcludesNonIndexablePaths(t *testing.T) {
	ci, dir := newTestIndex(t)
	ctx := context.Background()

	mkfile := func(rel, body string) string {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	fetchHead := mkfile(".git/FETCH_HEAD", "abc123\tbranch 'main' of github.com/x/y\n")
	vendored := mkfile("node_modules/pkg/index.js", "module.exports = 1\n")
	source := mkfile("main.go", "package main\n\nfunc Admitted() {}\n")

	fw := NewFileWatcher(ci, ci.ts, dir)
	fw.debounce = 0
	for _, p := range []string{fetchHead, vendored, source} {
		fw.NotifyChange(p)
	}
	fw.flushPending(ctx)

	// Positive first: an all-rejecting classifier would satisfy the two
	// exclusions below while indexing nothing at all.
	if n := hashRowCount(t, ci, source); n != 1 {
		t.Fatalf("main.go hash rows = %d, want 1 (source files must still be indexed)", n)
	}
	if n := hashRowCount(t, ci, fetchHead); n != 0 {
		t.Errorf(".git/FETCH_HEAD hash rows = %d, want 0", n)
	}
	if n := hashRowCount(t, ci, vendored); n != 0 {
		t.Errorf("node_modules/pkg/index.js hash rows = %d, want 0", n)
	}
}

// Rows written before the watcher classified anything must be evicted, not
// skipped: a skipped row stays stale forever, so every fetch re-reports it and
// the recovery count never reaches zero.
func TestReindexStaleEvictsExcludedRows(t *testing.T) {
	ci, dir := newTestIndex(t)
	ctx := context.Background()

	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fetchHead := filepath.Join(gitDir, "FETCH_HEAD")
	if err := os.WriteFile(fetchHead, []byte("abc123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// IndexFile bypasses classification — this is exactly how the row got
	// there before the watcher learned to classify.
	if err := ci.IndexFile(ctx, fetchHead); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	if n := hashRowCount(t, ci, fetchHead); n != 1 {
		t.Fatalf("seeded hash rows = %d, want 1", n)
	}

	future := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(fetchHead, []byte("def456\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fetchHead, future, future); err != nil {
		t.Fatal(err)
	}
	if before := ci.ScanStalenessFiles(ctx); len(before) != 1 || before[0] != IndexPath(fetchHead) {
		t.Fatalf("pre-recovery staleness = %v, want exactly [%s]", before, IndexPath(fetchHead))
	}

	fw := NewFileWatcher(ci, ci.ts, dir)
	fw.reindexStale(ctx)

	if n := hashRowCount(t, ci, fetchHead); n != 0 {
		t.Fatalf("post-recovery hash rows = %d, want 0 (excluded row must be evicted)", n)
	}
	if after := ci.ScanStalenessFiles(ctx); len(after) != 0 {
		t.Fatalf("post-recovery staleness = %v, want empty", after)
	}
}

// Classification is root-relative and one watcher serves every root, so a file
// in a secondary folder is only classifiable once the watcher knows the live
// root set. Without it the file is "outside the workspace" and never indexed
// incrementally — while the full index, which iterates WorkspaceRoots(), keeps
// indexing it. The two passes would disagree about the same file.
func TestFlushPendingClassifiesSecondaryRoots(t *testing.T) {
	ci, dir := newTestIndex(t)
	ctx := context.Background()

	rootA := filepath.Join(dir, "rootA")
	rootB := filepath.Join(dir, "rootB")
	for _, r := range []string{rootA, rootB} {
		if err := os.MkdirAll(r, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bGo := filepath.Join(rootB, "b.go")
	if err := os.WriteFile(bGo, []byte("package main\n\nfunc SecondaryRoot() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Primary root only: rootB is outside it, so the event is dropped.
	lone := NewFileWatcher(ci, ci.ts, rootA)
	lone.debounce = 0
	lone.NotifyChange(bGo)
	lone.flushPending(ctx)
	if n := hashRowCount(t, ci, bGo); n != 0 {
		t.Fatalf("hash rows without root provider = %d, want 0 (guards the assertion below)", n)
	}

	fw := NewFileWatcher(ci, ci.ts, rootA)
	fw.debounce = 0
	fw.SetRootProvider(func() []string { return []string{rootA, rootB} })
	fw.NotifyChange(bGo)
	fw.flushPending(ctx)
	if n := hashRowCount(t, ci, bGo); n != 1 {
		t.Fatalf("hash rows with root provider = %d, want 1 (secondary root must be indexed)", n)
	}
}
