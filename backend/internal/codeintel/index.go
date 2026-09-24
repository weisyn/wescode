package codeintel

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/weisyn/wescode/internal/codeintel/boundary"
	"github.com/weisyn/wescode/internal/platform"
	"github.com/weisyn/wescode/internal/treesitter"
	_ "modernc.org/sqlite"
)

// CodeIndex manages the three-layer code index (text search + structure + semantic).
// Uses dual SQLite connections for lock-free concurrent access:
//   - writerDB: exclusive writer (MaxOpenConns=1), used by IndexFile/Clear
//   - readerDB: concurrent reader pool, used by Search/List/Find operations
//
// SQLite WAL mode guarantees readers are never blocked by the writer.
type CodeIndex struct {
	writerDB *sql.DB
	readerDB *sql.DB
	readerMu sync.RWMutex // protects readerDB swap during reopenReader/ReopenAt
	ts       *treesitter.ParserPool
	dbPath   string
	workDir  string

	// Readiness tracking (CI-24).
	totalFiles       int32 // atomic: total indexable files discovered
	indexedCount     int32 // atomic: files successfully indexed
	staleCount       int32 // atomic: indexed files whose disk mtime differs from indexed mtime
	indexing         int32 // atomic: 1 = full project indexing in progress
	lastFullScanNano atomic.Int64

	// Symbols reached from outside the graph (LLM tool protocol). Not persisted:
	// see SetExternalEntryPoints.
	entryMu             sync.RWMutex
	externalEntryPoints map[string]bool

	EmbeddingFn EmbeddingFunc // optional: Cell Provider embedding for Pipeline Pass 5
}

// DB returns the reader database handle for direct queries (e.g. CKG-based
// constraint inference). Callers must not close the returned handle.
func (ci *CodeIndex) DB() *sql.DB {
	if ci == nil {
		return nil
	}
	return ci.readerDB
}

// WriterDB returns the writer database handle for mutation queries
// (e.g. constraint persistence to code.db). Serialized via MaxOpenConns=1.
func (ci *CodeIndex) WriterDB() *sql.DB {
	if ci == nil {
		return nil
	}
	return ci.writerDB
}

// hasReader returns true if the index is initialized and has a reader DB.
func (ci *CodeIndex) hasReader() bool {
	return ci != nil && ci.readerDB != nil
}

// SetWorkDir sets the workspace root directory used by ripgrep-based search.
func (ci *CodeIndex) SetWorkDir(dir string) {
	ci.workDir = dir
}

// WorkDir returns the workspace root directory.
func (ci *CodeIndex) WorkDir() string {
	return ci.workDir
}

// RegenerateMindMapSync generates and persists the MindMap synchronously.
// Intended for callers that need the write to land on the current writerDB
// (e.g. after DumpAndReopen). Skips if workDir is unset or the map is fresh.
func (ci *CodeIndex) RegenerateMindMapSync(ctx context.Context) {
	if ci.workDir == "" || !ci.ShouldRegenerateMindMap() {
		return
	}
	mmCache.mu.RLock()
	hints := mmCache.hints
	mmCache.mu.RUnlock()
	ci.GenerateMindMap(ctx, ci.workDir, hints)
}

// LeidenCommunity returns the community_id of the first symbol in the given file.
// Returns 0 if unknown or index not ready.
func (ci *CodeIndex) LeidenCommunity(filePath string) int {
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()
	if !ci.hasReader() || filePath == "" {
		return 0
	}
	var cid int
	ci.readerDB.QueryRow("SELECT community_id FROM symbols WHERE file_path=? AND community_id>0 LIMIT 1", IndexPath(filePath)).Scan(&cid)
	return cid
}

// NewCodeIndex opens or creates a code index at the given path.
// Creates two SQLite connections: a single-writer for indexing and a reader
// pool for concurrent queries. WAL mode ensures readers never block on writes.
func NewCodeIndex(dbPath string, ts *treesitter.ParserPool) (*CodeIndex, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("create index dir: %w", err)
	}

	// Writer: single connection for IndexFile/Clear (self-serializing via MaxOpenConns=1).
	// busy_timeout=30s to handle brief WAL checkpoint contention.
	writerDB, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(30000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open index writer db: %w", err)
	}
	writerDB.SetMaxOpenConns(1)
	writerDB.Exec("PRAGMA foreign_keys = ON")

	// Reader: concurrent pool for Search/List/Find queries.
	// WAL mode allows readers to proceed without blocking on the writer.
	readerDB, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		writerDB.Close()
		return nil, fmt.Errorf("open index reader db: %w", err)
	}
	readerDB.SetMaxOpenConns(4)

	ci := &CodeIndex{writerDB: writerDB, readerDB: readerDB, ts: ts, dbPath: dbPath}
	if err := migrateSchema(writerDB); err != nil {
		writerDB.Close()
		readerDB.Close()
		return nil, fmt.Errorf("migrate index: %w", err)
	}
	return ci, nil
}

// NewCodeIndexDeferred creates a CodeIndex without opening any database.
// The instance is safe to reference in closures (e.g. specOverride hooks)
// before the Cell directory exists. Call BindPath after Cell creation to
// open the real database. All query methods return empty results until bound.
func NewCodeIndexDeferred(ts *treesitter.ParserPool) *CodeIndex {
	return &CodeIndex{ts: ts}
}

// BindPath opens (or creates) the database at dbPath. Must be called exactly
// once, after the Cell directory is stable. Replaces NewCodeIndex for the
// deferred-init pattern (see design/20-cell-lifecycle-v2.md §四).
func (ci *CodeIndex) BindPath(dbPath string) error {
	if ci.writerDB != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("BindPath mkdir: %w", err)
	}
	writerDB, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(30000)&_pragma=foreign_keys(1)")
	if err != nil {
		return fmt.Errorf("BindPath open writer: %w", err)
	}
	writerDB.SetMaxOpenConns(1)
	readerDB, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		writerDB.Close()
		return fmt.Errorf("BindPath open reader: %w", err)
	}
	readerDB.SetMaxOpenConns(4)
	if err := migrateSchema(writerDB); err != nil {
		writerDB.Close()
		readerDB.Close()
		return fmt.Errorf("BindPath migrate: %w", err)
	}
	ci.writerDB = writerDB
	ci.readerDB = readerDB
	ci.dbPath = dbPath
	slog.Info("[codeintel] BindPath succeeded", "path", dbPath)
	return nil
}

// NewCodeIndexReadOnly wraps an already-opened read-only *sql.DB as a
// CodeIndex suitable for query-only consumers (e.g. MCP Server, INV-P4-03).
// The caller owns the DB handle and must close it separately.
func NewCodeIndexReadOnly(readerDB *sql.DB) *CodeIndex {
	return &CodeIndex{readerDB: readerDB}
}

// IndexFile indexes a single file via direct tree-sitter extraction.
// Skips if the content hash hasn't changed (CI-02).
// For full-project indexing, use the 10-Pass Pipeline (RunWithoutDump +
// DumpAndReopen, see engine.backgroundIndexAll) — never call RunPipeline
// from incremental paths: its atomic DB replacement destroys other roots.
func (ci *CodeIndex) IndexFile(ctx context.Context, path string) error {
	// Callers hand over native separators (walker, watcher events, RPC params);
	// the index speaks one representation. Normalizing here covers both the SQL
	// lookups below and the write that follows, so a file cannot be stored under
	// one spelling and looked up under another. Safe for the os calls too:
	// Stat/ReadFile accept forward slashes on Windows.
	path = IndexPath(path)
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()
	info, statErr := os.Stat(path)
	if statErr != nil {
		return statErr
	}
	if info.IsDir() || info.Size() > boundary.MaxCodeFileSize {
		return nil
	}
	currentMtime := info.ModTime().UnixNano()

	var indexedMtime int64
	_ = ci.readerDB.QueryRowContext(ctx, "SELECT mtime_ns FROM file_hashes WHERE file_path=?", path).Scan(&indexedMtime)
	if indexedMtime > 0 && indexedMtime == currentMtime {
		return nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	if IsMinified(path, content) {
		return nil
	}

	hash := contentHash(content)

	var existingHash string
	_ = ci.readerDB.QueryRowContext(ctx, "SELECT content_hash FROM file_hashes WHERE file_path=?", path).Scan(&existingHash)
	if existingHash == hash {
		return nil
	}

	if err := ci.indexFileViaWorker(ctx, path, content, hash, currentMtime); err != nil {
		return err
	}
	if existingHash == "" {
		atomic.AddInt32(&ci.indexedCount, 1)
	}
	return nil
}

// indexFileContent is DELETED — all parsing now routes through Worker subprocesses.
// See incremental.go:indexFileViaWorker for the replacement.
// Retained as a compile guard to catch any lingering callers.
func init() {
	// If you see a compile error referencing indexFileContent, migrate to indexFileViaWorker.
	_ = (*CodeIndex)(nil)
}

// resolveEdgesKinds are the edge kinds whose target_name is a symbol reference
// this resolver can bind. The other kinds either arrive already bound (contains,
// extends, semantic_related) or point at something that is not a symbol.
const resolveEdgesKinds = `('call', 'implements', 'handles')`

// ResolveEdgeTargets binds target_id for every edge whose producer left a name
// instead of a node, and records how each binding was reached in `resolution`.
// It is the only place edges acquire a target_id after the buffer flush, and it
// is idempotent: re-running it on an already-resolved graph is a no-op, so the
// incremental path and the full pipeline can both call it unconditionally.
//
// Five layers, precise first. Each stops at the first that answers, and each
// binds only when the answer is *unique* — a name with several candidates is
// marked `ambiguous` and left unbound so the read side can present all of them
// (queryCallersForAmbiguousTarget) rather than silently pick one.
//
//  1. Type.Method → the method whose `parent` is that type.        exact
//  2. recv.Method → the sole symbol with that bare method name.    inferred
//  3. Name → a symbol in the caller's own package.                 exact
//  4. Name → the sole *exported* symbol with that name.            inferred
//  5. Name → the sole symbol with that name, anywhere.             inferred
//
// Layers 1 and 3 are `exact` because the qualifier (declaring type, package)
// came from the source and picks out one symbol. Layers 2, 4 and 5 are
// `inferred` because uniqueness is a property of today's corpus, not of the
// call site: add a second `Close()` and the same edge becomes ambiguous.
//
// Shape matters here. The predecessor ran correlated subqueries with a COUNT(*)
// re-scan per candidate edge, which on 142k edges took minutes — long enough
// that it was only ever run after a full dump, never on the incremental path,
// which is how P0-1 stayed invisible. Grouping the name→symbol maps once into
// indexed temp tables makes the same five layers a sequence of index joins
// (~0.5s on this repo), cheap enough to run after every file save.
// Runs on the writer connection, which is capped at MaxOpenConns=1 — the temp
// tables above and the layers below are guaranteed the same session.
func (ci *CodeIndex) ResolveEdgeTargets(ctx context.Context) error {
	// Every unbound row starts each run as `unresolved`, so the label below is
	// recomputed from the current corpus rather than carried forward.
	//
	// The case that needs this is `ambiguous`. Nothing else revisits it: the FK
	// and the unbind trigger only touch rows whose target_id matches a deleted
	// symbol, and an ambiguous row has no target_id, so deleting every candidate
	// leaves it still claiming a candidate group. The read side then offers
	// queryCallersForAmbiguousTarget a group with no members. Without this
	// statement the resolver is only idempotent on graphs that never shrink.
	//
	// It also covers `exact`/`inferred` with a NULL target, which the pairing
	// CHECK makes unreachable — kept in the predicate because `!= unresolved` is
	// the honest way to say "recompute all of them", and narrowing it to the
	// reachable value would just be a second place to update if the domain grows.
	if _, err := ci.writerDB.ExecContext(ctx, `
		UPDATE edges SET resolution = ? WHERE target_id IS NULL AND resolution != ?`,
		ResolutionUnresolved, ResolutionUnresolved); err != nil {
		return fmt.Errorf("normalize unbound resolutions: %w", err)
	}

	// Name→symbol maps, built once. `HAVING COUNT(*) = 1` is what makes
	// uniqueness a property of the table rather than something each of the 5
	// layers below has to re-derive: a name that reaches these tables at all is
	// a name with exactly one candidate.
	for _, ddl := range []string{
		`DROP TABLE IF EXISTS temp.r_parent`,
		`DROP TABLE IF EXISTS temp.r_pkg`,
		`DROP TABLE IF EXISTS temp.r_exported`,
		`DROP TABLE IF EXISTS temp.r_global`,

		`CREATE TEMP TABLE r_parent AS
		   SELECT parent, name, MIN(id) AS id FROM symbols
		    WHERE kind NOT IN ('import','const','var') AND parent != ''
		    GROUP BY parent, name HAVING COUNT(*) = 1`,
		`CREATE INDEX temp.r_parent_k ON r_parent(parent, name)`,

		`CREATE TEMP TABLE r_pkg AS
		   SELECT package_path, name, MIN(id) AS id FROM symbols
		    WHERE kind NOT IN ('import','const','var') AND package_path != ''
		    GROUP BY package_path, name HAVING COUNT(*) = 1`,
		`CREATE INDEX temp.r_pkg_k ON r_pkg(package_path, name)`,

		`CREATE TEMP TABLE r_exported AS
		   SELECT name, MIN(id) AS id FROM symbols
		    WHERE kind NOT IN ('import','const','var') AND exported = 1
		    GROUP BY name HAVING COUNT(*) = 1`,
		`CREATE INDEX temp.r_exported_k ON r_exported(name)`,

		`CREATE TEMP TABLE r_global AS
		   SELECT name, MIN(id) AS id FROM symbols
		    WHERE kind NOT IN ('import','const','var')
		    GROUP BY name HAVING COUNT(*) = 1`,
		`CREATE INDEX temp.r_global_k ON r_global(name)`,
	} {
		if _, err := ci.writerDB.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("build resolver index: %w", err)
		}
	}
	defer func() {
		for _, t := range []string{"r_parent", "r_pkg", "r_exported", "r_global"} {
			ci.writerDB.ExecContext(ctx, `DROP TABLE IF EXISTS temp.`+t)
		}
	}()

	// Dotted names are split on the first '.': "Store.Save" → ("Store", "Save").
	// Receivers are single-segment in every language the extractor emits
	// (qualifyWithReceiver, INV-CKG-EDGE-01), so first-dot and last-dot agree.
	const recv = `substr(target_name, 1, instr(target_name, '.') - 1)`
	const meth = `substr(target_name, instr(target_name, '.') + 1)`

	// Both columns move in one statement. The schema CHECK requires resolution
	// and target_id to agree, so a two-statement "bind then label" would fail on
	// the first one — which is the point of expressing the pairing as a
	// constraint rather than a convention.
	layers := []struct {
		what string
		res  Resolution
		sql  string
	}{
		{"qualified by declaring type", ResolutionExact, `
			UPDATE edges SET target_id = (
				SELECT id FROM temp.r_parent WHERE parent = ` + recv + ` AND name = ` + meth + `
			), resolution = ?
			WHERE target_id IS NULL AND target_name LIKE '%.%'
			  AND kind IN ` + resolveEdgesKinds + `
			  AND EXISTS (SELECT 1 FROM temp.r_parent WHERE parent = ` + recv + ` AND name = ` + meth + `)`},

		{"qualified, receiver untyped", ResolutionInferred, `
			UPDATE edges SET target_id = (
				SELECT id FROM temp.r_global WHERE name = ` + meth + `
			), resolution = ?
			WHERE target_id IS NULL AND target_name LIKE '%.%'
			  AND kind IN ` + resolveEdgesKinds + `
			  AND EXISTS (SELECT 1 FROM temp.r_global WHERE name = ` + meth + `)`},

		{"bare name in caller's package", ResolutionExact, `
			UPDATE edges SET target_id = (
				SELECT p.id FROM temp.r_pkg p
				 WHERE p.name = edges.target_name
				   AND p.package_path = (SELECT s.package_path FROM symbols s WHERE s.id = edges.source_id)
			), resolution = ?
			WHERE target_id IS NULL AND target_name != ''
			  AND kind IN ` + resolveEdgesKinds + `
			  AND EXISTS (
				SELECT 1 FROM temp.r_pkg p
				 WHERE p.name = edges.target_name
				   AND p.package_path = (SELECT s.package_path FROM symbols s WHERE s.id = edges.source_id))`},

		{"sole exported name", ResolutionInferred, `
			UPDATE edges SET target_id = (
				SELECT id FROM temp.r_exported WHERE name = edges.target_name
			), resolution = ?
			WHERE target_id IS NULL AND target_name != ''
			  AND kind IN ` + resolveEdgesKinds + `
			  AND EXISTS (SELECT 1 FROM temp.r_exported WHERE name = edges.target_name)`},

		{"sole name anywhere", ResolutionInferred, `
			UPDATE edges SET target_id = (
				SELECT id FROM temp.r_global WHERE name = edges.target_name
			), resolution = ?
			WHERE target_id IS NULL AND target_name != ''
			  AND kind IN ` + resolveEdgesKinds + `
			  AND EXISTS (SELECT 1 FROM temp.r_global WHERE name = edges.target_name)`},
	}
	for _, l := range layers {
		if _, err := ci.writerDB.ExecContext(ctx, l.sql, l.res); err != nil {
			return fmt.Errorf("resolve %s: %w", l.what, err)
		}
	}

	// Whatever is still unbound had candidates but not one candidate — the read
	// side will show the group. Distinguishing this from "no candidate at all"
	// is the distinction the old single float could not express: both were 1.0.
	if _, err := ci.writerDB.ExecContext(ctx, `
		UPDATE edges SET resolution = ?
		 WHERE target_id IS NULL AND target_name != '' AND resolution = ?
		   AND kind IN `+resolveEdgesKinds+`
		   AND EXISTS (SELECT 1 FROM symbols s
			WHERE s.kind NOT IN ('import','const','var')
			  AND s.name = CASE WHEN target_name LIKE '%.%' THEN `+meth+` ELSE target_name END)`,
		ResolutionAmbiguous, ResolutionUnresolved); err != nil {
		return fmt.Errorf("mark ambiguous: %w", err)
	}
	return nil
}

// SetExternalEntryPoints records symbol names that are reached from outside the
// code graph — today, the methods the LLM tool protocol calls by name. Nothing
// in the corpus refers to them, so FindOrphans would otherwise report every one
// as dead code.
//
// This used to be `tool_wrap` rows in `edges` with `source_id = 0`, standing in
// for "the caller is not a symbol". Every such insert violated the source_id
// foreign key (symbol ids start at 1) and the returned error was discarded, so
// the table has never held one of these rows: on this repo 12 of the 21
// registered methods are reported as dead code today, `FindOrphans` itself among
// them. The schema was right to refuse. Reachability from outside is a property
// of one symbol, not a relation between two, and the two other external entry
// classes — RPC handlers and route handlers — are already name sets consulted at
// query time. This is the third, no longer the odd one out.
//
// In memory rather than in the DB, because the set is a fact about the running
// binary that the engine recomputes at boot: persisting it would add a copy that
// can be stale, and one that a staging→production swap silently drops.
func (ci *CodeIndex) SetExternalEntryPoints(names []string) {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		if n != "" {
			set[n] = true
		}
	}
	ci.entryMu.Lock()
	ci.externalEntryPoints = set
	ci.entryMu.Unlock()
}

// isExternalEntryPoint reports whether a symbol name was registered by
// SetExternalEntryPoints.
func (ci *CodeIndex) isExternalEntryPoint(name string) bool {
	ci.entryMu.RLock()
	defer ci.entryMu.RUnlock()
	return ci.externalEntryPoints[name]
}

// packagePathFromFile derives a logical package path from a file path.
// For Go: the directory path serves as package identifier.
// For TS/JS: the directory path relative to the nearest package.json.
func packagePathFromFile(filePath string) string {
	dir := filepath.Dir(filePath)
	return dir
}

// ListSymbols returns exported symbols without a text query (browse mode).
func (ci *CodeIndex) ListSymbols(ctx context.Context, limit int) ([]SymbolEntry, error) {
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()
	if !ci.hasReader() {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := ci.readerDB.QueryContext(ctx,
		`SELECT file_path, kind, name, signature, parent, line_start, line_end, exported, visibility
		 FROM symbols WHERE exported = 1 ORDER BY name LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []SymbolEntry
	for rows.Next() {
		var e SymbolEntry
		if err := rows.Scan(&e.FilePath, &e.Kind, &e.Name, &e.Signature, &e.Parent, &e.LineStart, &e.LineEnd, &e.Exported, &e.Visibility); err != nil {
			continue
		}
		results = append(results, e)
	}
	return results, nil
}

// SearchSymbols finds symbols matching a text query using FTS5.
// Optional extFilter limits results to files with matching extensions
// (CI-09: prefer same-language results, cross-language as fail-open fallback).
//
// When query contains "." (e.g. "Registry.Get"), it's treated as a qualified
// name search: bypasses FTS5 (which chokes on ".") and queries the symbols
// table directly via parent + name columns.
func (ci *CodeIndex) SearchSymbols(ctx context.Context, query string, limit int, extFilter ...string) ([]SymbolEntry, error) {
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()
	if !ci.hasReader() {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	// Qualified name path: Type.Method or pkg.Type.Method
	if strings.Contains(query, ".") {
		return ci.searchQualifiedSymbol(ctx, query, limit, extFilter...)
	}

	stmt := `SELECT s.file_path, s.kind, s.name, s.signature, s.parent, s.line_start, s.line_end, s.exported, s.visibility
		 FROM symbol_fts f JOIN symbols s ON f.rowid = s.id
		 WHERE symbol_fts MATCH ?`
	args := []any{query}

	if len(extFilter) > 0 {
		clauses := make([]string, len(extFilter))
		for i, ext := range extFilter {
			clauses[i] = "s.file_path LIKE ?"
			args = append(args, "%"+ext)
		}
		stmt += " AND (" + strings.Join(clauses, " OR ") + ")"
	}

	stmt += " ORDER BY rank LIMIT ?"
	args = append(args, limit)

	rows, err := ci.readerDB.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SymbolEntry
	for rows.Next() {
		var e SymbolEntry
		if err := rows.Scan(&e.FilePath, &e.Kind, &e.Name, &e.Signature, &e.Parent, &e.LineStart, &e.LineEnd, &e.Exported, &e.Visibility); err != nil {
			continue
		}
		results = append(results, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// searchQualifiedSymbol handles "Type.Method" or "pkg.Type.Method" queries
// by direct SQL on the symbols table (parent + name), avoiding FTS5 tokenizer
// issues with "." characters.
func (ci *CodeIndex) searchQualifiedSymbol(ctx context.Context, qualified string, limit int, extFilter ...string) ([]SymbolEntry, error) {
	parts := strings.SplitN(qualified, ".", 3)

	var stmt string
	var args []any

	switch len(parts) {
	case 3:
		// pkg.Type.Method → file_path LIKE %/pkg/%, parent=Type, name=Method
		stmt = `SELECT s.file_path, s.kind, s.name, s.signature, s.parent, s.line_start, s.line_end, s.exported, s.visibility
			FROM symbols s
			WHERE s.name = ? AND s.parent = ? AND s.file_path LIKE ? AND s.kind IN ('function','method','type','interface','class')
			ORDER BY s.exported DESC LIMIT ?`
		args = []any{parts[2], parts[1], "%/" + parts[0] + "/%", limit}
	case 2:
		// Type.Method → parent=Type, name=Method
		stmt = `SELECT s.file_path, s.kind, s.name, s.signature, s.parent, s.line_start, s.line_end, s.exported, s.visibility
			FROM symbols s
			WHERE s.name = ? AND s.parent = ? AND s.kind IN ('function','method','type','interface','class')
			ORDER BY s.exported DESC LIMIT ?`
		args = []any{parts[1], parts[0], limit}
	default:
		stmt = `SELECT s.file_path, s.kind, s.name, s.signature, s.parent, s.line_start, s.line_end, s.exported, s.visibility
			FROM symbols s
			WHERE s.name = ? AND s.kind IN ('function','method','type','interface','class')
			ORDER BY s.exported DESC LIMIT ?`
		args = []any{qualified, limit}
	}

	if len(extFilter) > 0 {
		clauses := make([]string, len(extFilter))
		filterArgs := make([]any, len(extFilter))
		for i, ext := range extFilter {
			clauses[i] = "s.file_path LIKE ?"
			filterArgs[i] = "%" + ext
		}
		stmt = strings.Replace(stmt, "ORDER BY", "AND ("+strings.Join(clauses, " OR ")+") ORDER BY", 1)
		// Insert extFilter args before the LIMIT arg.
		newArgs := make([]any, 0, len(args)+len(filterArgs))
		newArgs = append(newArgs, args[:len(args)-1]...)
		newArgs = append(newArgs, filterArgs...)
		newArgs = append(newArgs, args[len(args)-1])
		args = newArgs
	}

	rows, err := ci.readerDB.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SymbolEntry
	for rows.Next() {
		var e SymbolEntry
		if err := rows.Scan(&e.FilePath, &e.Kind, &e.Name, &e.Signature, &e.Parent, &e.LineStart, &e.LineEnd, &e.Exported, &e.Visibility); err != nil {
			continue
		}
		results = append(results, e)
	}
	return results, rows.Err()
}

// SearchFiles searches file content using ripgrep for full-text matching.
// Returns FileEntry results with snippet context.
func (ci *CodeIndex) SearchFiles(ctx context.Context, query string, limit int, extFilter ...string) ([]FileEntry, error) {
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()
	if limit <= 0 {
		limit = 10
	}

	args := []string{
		"--max-count", "3",
		"--max-filesize", "1M",
		"-C", "1",
		"--no-heading",
		"--with-filename",
		"--line-number",
	}
	for _, ext := range extFilter {
		args = append(args, "-g", "*"+ext)
	}
	args = append(args, "--", query)

	workDir := ci.workDir
	if workDir == "" {
		workDir = "."
	}

	rg, err := platform.ResolveTool("rg")
	if err != nil {
		return nil, err
	}
	cmd := rg.CommandContext(ctx, args...)
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("rg search: %w", err)
	}

	return parseRipgrepOutput(out, workDir, limit), nil
}

// parseRipgrepOutput groups ripgrep output by file path and builds FileEntry results.
func parseRipgrepOutput(out []byte, workDir string, limit int) []FileEntry {
	type fileMatch struct {
		lines []string
	}
	files := map[string]*fileMatch{}
	var order []string

	for _, line := range strings.Split(string(out), "\n") {
		if line == "" || line == "--" {
			continue
		}
		// rg --no-heading output: "path:line:content". Context lines use "-"
		// separators and are skipped (same as before).
		path, _, content, ok := platform.ParseRipgrepLine(line)
		if !ok {
			continue
		}
		if _, exists := files[path]; !exists {
			files[path] = &fileMatch{}
			order = append(order, path)
		}

		fm := files[path]
		if len(fm.lines) < 6 {
			fm.lines = append(fm.lines, content)
		}
	}

	var results []FileEntry
	for _, fp := range order {
		if len(results) >= limit {
			break
		}
		fm := files[fp]

		absPath := fp
		if !filepath.IsAbs(fp) && workDir != "." {
			absPath = filepath.Join(workDir, fp)
		}

		lang := DetectLanguage(absPath).Language
		snippet := strings.Join(fm.lines, "\n")

		results = append(results, FileEntry{
			FilePath: absPath,
			Language: lang,
			Snippet:  snippet,
		})
	}
	return results
}

// FindSymbol returns symbols with the given name (exact match).
func (ci *CodeIndex) FindSymbol(ctx context.Context, name string) ([]SymbolEntry, error) {
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()
	if !ci.hasReader() {
		return nil, nil
	}
	rows, err := ci.readerDB.QueryContext(ctx,
		`SELECT file_path, kind, name, signature, parent, line_start, line_end, exported, visibility
		 FROM symbols WHERE name=?`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SymbolEntry
	for rows.Next() {
		var e SymbolEntry
		if err := rows.Scan(&e.FilePath, &e.Kind, &e.Name, &e.Signature, &e.Parent, &e.LineStart, &e.LineEnd, &e.Exported, &e.Visibility); err != nil {
			continue
		}
		results = append(results, e)
	}
	return results, rows.Err()
}

// FindSymbolInLang returns symbols with the given name, filtered to files
// matching the specified language extensions. This prevents cross-language
// pollution (e.g. Go's Lock() matching TypeScript's Lock).
func (ci *CodeIndex) FindSymbolInLang(ctx context.Context, name string, langExts []string) ([]SymbolEntry, error) {
	all, err := ci.FindSymbol(ctx, name)
	if err != nil {
		return nil, err
	}
	if len(langExts) == 0 {
		return all, nil
	}
	var filtered []SymbolEntry
	for _, e := range all {
		ext := strings.ToLower(filepath.Ext(e.FilePath))
		for _, allowed := range langExts {
			if ext == allowed {
				filtered = append(filtered, e)
				break
			}
		}
	}
	if len(filtered) == 0 {
		return all, nil
	}
	return filtered, nil
}

// ListFileSymbols returns all symbols in a given file.
func (ci *CodeIndex) ListFileSymbols(ctx context.Context, path string) ([]SymbolEntry, error) {
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()
	if !ci.hasReader() {
		return nil, nil
	}
	rows, err := ci.readerDB.QueryContext(ctx,
		`SELECT file_path, kind, name, signature, parent, line_start, line_end, exported, visibility
		 FROM symbols WHERE file_path=? ORDER BY line_start`, IndexPath(path))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SymbolEntry
	for rows.Next() {
		var e SymbolEntry
		if err := rows.Scan(&e.FilePath, &e.Kind, &e.Name, &e.Signature, &e.Parent, &e.LineStart, &e.LineEnd, &e.Exported, &e.Visibility); err != nil {
			continue
		}
		results = append(results, e)
	}
	return results, rows.Err()
}

// TopSymbols returns the names of top-level exported symbols in a file,
// suitable for file_card generation. Returns at most 15 names. Uses a
// background context with 2s timeout to avoid blocking compression.
func (ci *CodeIndex) TopSymbols(path string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	entries, err := ci.ListFileSymbols(ctx, path)
	if err != nil || len(entries) == 0 {
		return nil
	}

	var names []string
	for _, e := range entries {
		if !e.Exported || e.Kind == "import" {
			continue
		}
		display := e.Name
		if e.Kind == "function" || e.Kind == "method" {
			display += "()"
		}
		names = append(names, display)
		if len(names) >= 15 {
			break
		}
	}
	return names
}

// ListImports returns all imports for a given file.
func (ci *CodeIndex) ListImports(ctx context.Context, path string) ([]string, error) {
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()
	if !ci.hasReader() {
		return nil, nil
	}
	rows, err := ci.readerDB.QueryContext(ctx,
		`SELECT name FROM symbols WHERE file_path=? AND kind='import'`, IndexPath(path))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var imports []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		imports = append(imports, name)
	}
	return imports, rows.Err()
}

// ReverseImports returns all file paths that import the given import path.
// This enables dependency graph expansion: "who depends on this package?"
// Language-agnostic — works for any language whose imports are indexed.
func (ci *CodeIndex) ReverseImports(ctx context.Context, importPath string) ([]string, error) {
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()
	if !ci.hasReader() {
		return nil, nil
	}
	rows, err := ci.readerDB.QueryContext(ctx,
		`SELECT DISTINCT file_path FROM symbols WHERE kind='import' AND name=?`, importPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			continue
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// PackageOfFile returns the import path of the package containing the given file.
// For Go: walks up to find go.mod, then computes module_path/relative_dir.
// For other languages: returns the directory path as a pseudo-package identifier.
func (ci *CodeIndex) PackageOfFile(_ context.Context, filePath string) (string, error) {
	dir := filepath.Dir(filePath)
	modDir, modPath := findGoMod(dir)
	if modPath == "" {
		return dir, nil
	}
	rel, err := filepath.Rel(modDir, dir)
	if err != nil || rel == "." {
		return modPath, nil
	}
	return modPath + "/" + filepath.ToSlash(rel), nil
}

func findGoMod(dir string) (modDir, modPath string) {
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "module ") {
					return dir, strings.TrimSpace(strings.TrimPrefix(line, "module"))
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ""
		}
		dir = parent
	}
}

// Close closes the database connections. Safe to call on unbound (idle) index.
func (ci *CodeIndex) Close() error {
	// Serialise with in-flight readers (RLock) and handle swaps so a query
	// never runs against a closed DB.
	ci.readerMu.Lock()
	defer ci.readerMu.Unlock()
	if ci.readerDB != nil {
		ci.readerDB.Close()
	}
	if ci.writerDB != nil {
		return ci.writerDB.Close()
	}
	return nil
}

// DBPath returns the path to the production code.db file.
func (ci *CodeIndex) DBPath() string {
	return ci.dbPath
}

// DumpAndReopen writes the combined buffer to production code.db and reopens
// both writer and reader connections. Required because DumpToProduction does
// an atomic rename (staging.db → code.db) which replaces the inode — existing
// fd-based connections still point to the old (unlinked) inode.
//
// On Windows, os.Rename cannot overwrite a file that is open by another process.
// We pass a preRename callback to DumpToProduction that closes the old reader
// and writer connections before the rename, then reopen after.
func (ci *CodeIndex) DumpAndReopen(buf *GraphBuffer) error {
	preRename := func() {
		ci.readerMu.Lock()
		if ci.readerDB != nil {
			ci.readerDB.Close()
			ci.readerDB = nil
		}
		if ci.writerDB != nil {
			ci.writerDB.Close()
			ci.writerDB = nil
		}
		ci.readerMu.Unlock()
	}
	if err := DumpToProduction(buf, ci.dbPath, preRename); err != nil {
		ci.reopenWriter()
		ci.reopenReader()
		return err
	}
	ci.reopenWriter()
	ci.reopenReader()
	return nil
}

// reopenWriter closes and reopens the writer DB connection.
// Required after DumpToProduction atomically replaces code.db via rename —
// the old connection's fd still references the unlinked pre-rename inode,
// so all subsequent writes (ResolveEdgeTargets, saveMindMap, IndexFile, etc.)
// would silently go to the dead inode.
// Swap is protected by readerMu (same as ReopenAt) to prevent concurrent
// IndexFile calls from observing a partially-swapped or closed writerDB.
func (ci *CodeIndex) reopenWriter() {
	newWriter, err := sql.Open("sqlite", ci.dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(30000)&_pragma=foreign_keys(1)")
	if err != nil {
		slog.Warn("[codeintel] reopenWriter failed, keeping old connection", "err", err)
		return
	}
	newWriter.SetMaxOpenConns(1)
	newWriter.Exec("PRAGMA foreign_keys = ON")

	if err := newWriter.Ping(); err != nil {
		newWriter.Close()
		slog.Warn("[codeintel] reopenWriter ping failed, keeping old connection", "err", err)
		return
	}

	ci.readerMu.Lock()
	old := ci.writerDB
	ci.writerDB = newWriter
	ci.readerMu.Unlock()
	if old != nil {
		old.Close()
	}
	slog.Info("[codeintel] reopenWriter succeeded", "path", ci.dbPath)
}

// reopenReader closes and reopens the reader DB connection pool.
// Required after Pipeline's Pass 10 atomically replaces code.db via rename.
// Uses sync/atomic pattern: old pool is closed after new one is confirmed open.
func (ci *CodeIndex) reopenReader() {
	newReader, err := sql.Open("sqlite", ci.dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		slog.Warn("[codeintel] reopenReader failed, keeping old connection", "err", err)
		return
	}
	newReader.SetMaxOpenConns(4)

	if err := newReader.Ping(); err != nil {
		newReader.Close()
		slog.Warn("[codeintel] reopenReader ping failed, keeping old connection", "err", err)
		return
	}

	ci.readerMu.Lock()
	old := ci.readerDB
	ci.readerDB = newReader
	ci.readerMu.Unlock()
	if old != nil {
		old.Close()
	}
}

// ReopenAt closes both writer and reader, re-creates them at newPath, and
// runs schema migration. Used after Cell directory stabilisation to recover
// from orphan-rename races (see design/20-cell-lifecycle-v2.md §2.3).
func (ci *CodeIndex) ReopenAt(newPath string) error {
	if newPath == ci.dbPath {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return fmt.Errorf("ReopenAt mkdir: %w", err)
	}
	dsn := newPath + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(30000)&_pragma=foreign_keys(1)"
	newWriter, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("ReopenAt open writer: %w", err)
	}
	newWriter.SetMaxOpenConns(1)
	rdsn := newPath + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	newReader, err := sql.Open("sqlite", rdsn)
	if err != nil {
		newWriter.Close()
		return fmt.Errorf("ReopenAt open reader: %w", err)
	}
	newReader.SetMaxOpenConns(4)

	if err := migrateSchema(newWriter); err != nil {
		newWriter.Close()
		newReader.Close()
		return fmt.Errorf("ReopenAt migrate: %w", err)
	}

	// Swap both handles under readerMu so in-flight readers never observe a
	// closed DB (N-2): reopenReader already locks; ReopenAt must too. Close
	// happens after the unlock — any reader that started before the swap
	// holds RLock and has finished by the time we get here.
	ci.readerMu.Lock()
	oldWriter := ci.writerDB
	oldReader := ci.readerDB
	ci.writerDB = newWriter
	ci.readerDB = newReader
	ci.dbPath = newPath
	ci.readerMu.Unlock()
	if oldWriter != nil {
		oldWriter.Close()
	}
	if oldReader != nil {
		oldReader.Close()
	}
	slog.Info("[codeintel] ReopenAt succeeded", "new_path", newPath)
	return nil
}

// CallersOf returns symbols that call the given function name.
// Supports qualified names: "Get" matches all Get methods (legacy behavior),
// "Registry.Get" filters by target symbol's parent type = "Registry",
// "execution.Registry.Get" filters by target symbol's parent = "Registry"
// AND target file path containing "execution".
func (ci *CodeIndex) CallersOf(ctx context.Context, calleeName string, limit int) ([]SymbolEntry, error) {
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()
	if !ci.hasReader() {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	// Resolve the target symbol(s) to get their IDs.
	targetIDs, targetIsInterfaceMethod, err := ci.resolveCallTarget(ctx, calleeName)
	if err != nil || len(targetIDs) == 0 {
		return ci.callersOfFallback(ctx, calleeName, limit)
	}

	seen := make(map[int64]bool)
	var results []SymbolEntry

	// ① Direct callers: CALLS WHERE target_id IN targetIDs
	directCallers, err := ci.queryCallersForTargets(ctx, targetIDs, limit)
	if err != nil {
		return nil, err
	}
	for _, c := range directCallers {
		if !seen[c.id] {
			seen[c.id] = true
			results = append(results, c.entry)
		}
	}

	// ② If target is a concrete method → find OVERRIDES edges → collect interface callers
	if !targetIsInterfaceMethod {
		for _, tid := range targetIDs {
			ifaceMethodIDs, _ := ci.queryOverriddenInterfaces(ctx, tid)
			if len(ifaceMethodIDs) > 0 {
				indirectCallers, _ := ci.queryCallersForTargets(ctx, ifaceMethodIDs, limit-len(results))
				for _, c := range indirectCallers {
					if !seen[c.id] {
						seen[c.id] = true
						results = append(results, c.entry)
					}
				}
			}
		}
	}

	// ③ If target is an interface method → find concrete overrides → collect concrete callers
	if targetIsInterfaceMethod {
		for _, tid := range targetIDs {
			concreteMethodIDs, _ := ci.queryConcreteOverrides(ctx, tid)
			if len(concreteMethodIDs) > 0 {
				concreteCallers, _ := ci.queryCallersForTargets(ctx, concreteMethodIDs, limit-len(results))
				for _, c := range concreteCallers {
					if !seen[c.id] {
						seen[c.id] = true
						results = append(results, c.entry)
					}
				}
			}
		}
	}

	// ④ Ambiguous edges: target_id IS NULL but target_name matches.
	// These were intentionally left unresolved because multiple candidates
	// existed across packages. Include their callers so they aren't silently lost.
	if remaining := limit - len(results); remaining > 0 {
		ambiguousCallers, _ := ci.queryCallersForAmbiguousTarget(ctx, calleeName, remaining)
		for _, c := range ambiguousCallers {
			if !seen[c.id] {
				seen[c.id] = true
				results = append(results, c.entry)
			}
		}
	}

	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

type callerWithID struct {
	id    int64
	entry SymbolEntry
}

// resolveCallTarget resolves a symbol name to database IDs, respecting qualified forms.
// Returns (targetIDs, isInterfaceMethod, err).
func (ci *CodeIndex) resolveCallTarget(ctx context.Context, calleeName string) ([]int64, bool, error) {
	db := ci.readerDB
	var rows *sql.Rows
	var err error

	parts := strings.SplitN(calleeName, ".", 3)
	// Declared here so every case can populate them (Go switch cases are
	// separate lexical scopes — per-case declarations are invisible outside).
	var ids []int64
	isIfaceMethod := false
	switch len(parts) {
	case 3:
		pkgHint, parentName, methodName := parts[0], parts[1], parts[2]
		rows, err = db.QueryContext(ctx,
			`SELECT id, kind, parent FROM symbols
			 WHERE name = ? AND parent = ? AND file_path LIKE ?`,
			methodName, parentName, "%/"+pkgHint+"/%")
	case 2:
		parentName, methodName := parts[0], parts[1]
		// INV-CKG-EDGE-01: three-stage resolution for "Receiver.method".
		// 1. Exact: receiver is a real type (parent column matches).
		// 2. Package path: receiver is a package name ("fmt.Errorf" → any
		//    Errorf whose file lives under .../fmt/...).
		// 3. Bare fallback: receiver was a local variable ("svc.CreateUser")
		//    whose type cannot be resolved statically — match every
		//    same-named symbol rather than dropping the edge entirely.
		rows, err = db.QueryContext(ctx,
			`SELECT id, kind, parent FROM symbols WHERE name = ? AND parent = ?`,
			methodName, parentName)
		if err != nil {
			return nil, false, err
		}
		ids = scanSymbolIDs(rows)
		if len(ids) == 0 {
			rows, err = db.QueryContext(ctx,
				`SELECT id, kind, parent FROM symbols
				 WHERE name = ? AND kind IN ('function','method','type','interface','class') AND file_path LIKE ?`,
				methodName, "%/"+parentName+"/%")
			if err != nil {
				return nil, false, err
			}
			ids = scanSymbolIDs(rows)
		}
		if len(ids) == 0 {
			rows, err = db.QueryContext(ctx,
				`SELECT id, kind, parent FROM symbols WHERE name = ? AND kind IN ('function','method','type','interface','class')`,
				methodName)
			if err != nil {
				return nil, false, err
			}
			ids = scanSymbolIDs(rows)
		}
		// isInterfaceMethod detection below reuses ids (re-scanned rows
		// already consumed); skip re-scan — interface check runs on ids.
		if len(ids) > 0 {
			isIfaceMethod = detectInterfaceMethod(ctx, db, ids)
		}
		return ids, isIfaceMethod, nil
	default:
		rows, err = db.QueryContext(ctx,
			`SELECT id, kind, parent FROM symbols WHERE name = ? AND kind IN ('function','method','type','interface','class')`,
			calleeName)
	}
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var kind, parent string
		if rows.Scan(&id, &kind, &parent) == nil {
			ids = append(ids, id)
			if parent != "" && detectInterfaceMethod(ctx, db, []int64{id}) {
				isIfaceMethod = true
			}
		}
	}
	return ids, isIfaceMethod, rows.Err()
}

// scanSymbolIDs consumes query rows of (id, kind, parent) and returns the IDs.
func scanSymbolIDs(rows *sql.Rows) []int64 {
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		var kind, parent string
		if rows.Scan(&id, &kind, &parent) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// detectInterfaceMethod reports whether any of the given symbol IDs is a
// method whose parent type is an interface.
func detectInterfaceMethod(ctx context.Context, db *sql.DB, ids []int64) bool {
	if len(ids) == 0 {
		return false
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	var cnt int
	_ = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM symbols s
		 JOIN symbols p ON p.name = s.parent AND p.kind = 'interface'
		 WHERE s.id IN (`+strings.Join(placeholders, ",")+`)`, args...).Scan(&cnt)
	return cnt > 0
}

// queryCallersForTargets returns callers of any of the given target IDs.
func (ci *CodeIndex) queryCallersForTargets(ctx context.Context, targetIDs []int64, limit int) ([]callerWithID, error) {
	if len(targetIDs) == 0 || limit <= 0 {
		return nil, nil
	}
	db := ci.readerDB

	placeholders := make([]string, len(targetIDs))
	args := make([]any, len(targetIDs))
	for i, id := range targetIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	args = append(args, limit)

	query := fmt.Sprintf(`
		SELECT s.id, s.file_path, s.kind, s.name, s.signature, s.parent,
		       s.line_start, s.line_end, s.exported, s.visibility
		FROM edges e
		JOIN symbols s ON e.source_id = s.id
		WHERE e.kind = 'call' AND e.target_id IN (%s)
		LIMIT ?`, strings.Join(placeholders, ","))

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []callerWithID
	for rows.Next() {
		var c callerWithID
		if rows.Scan(&c.id, &c.entry.FilePath, &c.entry.Kind, &c.entry.Name,
			&c.entry.Signature, &c.entry.Parent, &c.entry.LineStart,
			&c.entry.LineEnd, &c.entry.Exported, &c.entry.Visibility) == nil {
			results = append(results, c)
		}
	}
	return results, rows.Err()
}

// queryCallersForAmbiguousTarget returns callers of edges where target_id IS NULL
// but target_name matches the callee name. These edges are left unbound by
// ResolveEdgeTargets with resolution='ambiguous' — several same-named candidates
// exist across packages and picking one would be a guess. Naming the whole
// candidate group is the honest answer; silently binding one is not.
//
// It matches on target_name rather than resolution because a row can also be
// unbound as 'unresolved' (no candidate at all), and both shapes answer the same
// read: "who calls something by this name that we could not pin down."
func (ci *CodeIndex) queryCallersForAmbiguousTarget(ctx context.Context, calleeName string, limit int) ([]callerWithID, error) {
	if limit <= 0 {
		return nil, nil
	}
	db := ci.readerDB

	// INV-CKG-EDGE-01: match both storage forms. Pipeline may store an
	// unresolved edge as "Registry.Get" (qualified) or "Get" (after the
	// prefix-strip fallback), and the caller may query either form — match
	// the full name AND the bare method name.
	methodName := calleeName
	if idx := strings.LastIndexByte(calleeName, '.'); idx >= 0 {
		methodName = calleeName[idx+1:]
	}
	query := `
		SELECT s.id, s.file_path, s.kind, s.name, s.signature, s.parent,
		       s.line_start, s.line_end, s.exported, s.visibility
		FROM edges e
		JOIN symbols s ON e.source_id = s.id
		WHERE e.kind = 'call' AND e.target_id IS NULL
		  AND (e.target_name = ? OR e.target_name = ? OR e.target_name LIKE '%.' || ?)
		LIMIT ?`

	rows, err := db.QueryContext(ctx, query, calleeName, methodName, methodName, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []callerWithID
	for rows.Next() {
		var c callerWithID
		if rows.Scan(&c.id, &c.entry.FilePath, &c.entry.Kind, &c.entry.Name,
			&c.entry.Signature, &c.entry.Parent, &c.entry.LineStart,
			&c.entry.LineEnd, &c.entry.Exported, &c.entry.Visibility) == nil {
			results = append(results, c)
		}
	}
	return results, rows.Err()
}

// queryOverriddenInterfaces returns interface method IDs that a concrete method overrides.
func (ci *CodeIndex) queryOverriddenInterfaces(ctx context.Context, concreteMethodID int64) ([]int64, error) {
	rows, err := ci.readerDB.QueryContext(ctx,
		`SELECT target_id FROM edges WHERE source_id = ? AND kind = 'overrides' AND target_id IS NOT NULL`,
		concreteMethodID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

// queryConcreteOverrides returns concrete method IDs that override an interface method.
func (ci *CodeIndex) queryConcreteOverrides(ctx context.Context, interfaceMethodID int64) ([]int64, error) {
	rows, err := ci.readerDB.QueryContext(ctx,
		`SELECT source_id FROM edges WHERE target_id = ? AND kind = 'overrides'`,
		interfaceMethodID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

// callersOfFallback is the legacy name-based fallback when ID resolution fails.
func (ci *CodeIndex) callersOfFallback(ctx context.Context, calleeName string, limit int) ([]SymbolEntry, error) {
	db := ci.readerDB
	var rows *sql.Rows
	var err error

	if parts := strings.SplitN(calleeName, ".", 3); len(parts) >= 2 {
		methodName := parts[len(parts)-1]
		parentName := parts[len(parts)-2]

		if len(parts) == 3 {
			pkgHint := parts[0]
			rows, err = db.QueryContext(ctx,
				`SELECT s.file_path, s.kind, s.name, s.signature, s.parent, s.line_start, s.line_end, s.exported, s.visibility
				 FROM edges e
				 JOIN symbols s ON e.source_id=s.id
				 LEFT JOIN symbols t ON e.target_id=t.id
				 WHERE e.kind='call' AND e.target_name=?
				   AND (t.parent=? OR (t.id IS NULL AND e.target_name=?))
				   AND (t.file_path LIKE ? OR t.id IS NULL)
				 LIMIT ?`, methodName, parentName, methodName, "%/"+pkgHint+"/%", limit)
		} else {
			rows, err = db.QueryContext(ctx,
				`SELECT s.file_path, s.kind, s.name, s.signature, s.parent, s.line_start, s.line_end, s.exported, s.visibility
				 FROM edges e
				 JOIN symbols s ON e.source_id=s.id
				 LEFT JOIN symbols t ON e.target_id=t.id
				 WHERE e.kind='call' AND e.target_name=?
				   AND (t.parent=? OR t.id IS NULL)
				 LIMIT ?`, methodName, parentName, limit)
		}
	} else {
		rows, err = db.QueryContext(ctx,
			`SELECT s.file_path, s.kind, s.name, s.signature, s.parent, s.line_start, s.line_end, s.exported, s.visibility
			 FROM edges e JOIN symbols s ON e.source_id=s.id
			 WHERE e.kind='call' AND e.target_name=? LIMIT ?`, calleeName, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []SymbolEntry
	for rows.Next() {
		var s SymbolEntry
		if err := rows.Scan(&s.FilePath, &s.Kind, &s.Name, &s.Signature, &s.Parent, &s.LineStart, &s.LineEnd, &s.Exported, &s.Visibility); err != nil {
			continue
		}
		results = append(results, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// CalleesOf returns names called by the given symbol (outgoing call edges).
func (ci *CodeIndex) CalleesOf(ctx context.Context, callerPath, callerName string, callerLine int) ([]string, error) {
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()
	if !ci.hasReader() {
		return nil, nil
	}
	rows, err := ci.readerDB.QueryContext(ctx,
		`SELECT e.target_name FROM edges e
		 JOIN symbols s ON e.source_id=s.id
		 WHERE e.kind='call' AND s.file_path=? AND s.name=? AND s.line_start=?`, IndexPath(callerPath), callerName, callerLine)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			continue
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

// Clear removes all indexed data. Used when switching workspaces.
// Covers the core CKG tables plus the auxiliary tables (mind_map,
// node_history, constraints, exploration_paths) — leaving those behind
// would leak stale data from the previous workspace (see index_test.go).
func (ci *CodeIndex) Clear() {
	ci.writerDB.Exec("DELETE FROM edges")
	ci.writerDB.Exec("DELETE FROM symbols")
	ci.writerDB.Exec("DELETE FROM file_hashes")
	ci.writerDB.Exec("DELETE FROM mind_map")
	ci.writerDB.Exec("DELETE FROM node_history")
	ci.writerDB.Exec("DELETE FROM constraints")
	ci.writerDB.Exec("DELETE FROM exploration_paths")
}

// ClearProject removes all indexed data whose file_path falls under the given
// project root prefix. Edges whose source symbol belongs to that project are
// cascade-deleted by the FK constraint on symbols(id).
//
// root must be a non-trivial absolute path (rejected: "", "/", or any path
// that is not absolute). LIKE wildcards (% _) in the path are escaped so
// they match literally.
func (ci *CodeIndex) ClearProject(root string) {
	clean := filepath.Clean(root)
	if clean == "" || clean == "/" || clean == "." || !filepath.IsAbs(clean) {
		slog.Warn("[codeintel] ClearProject: refusing dangerous or invalid root", "root", root)
		return
	}
	// Escape LIKE wildcards so % and _ in directory names match literally.
	// The prefix is built from the index representation, not from Clean's output:
	// Clean nativizes, so on Windows `F:\proj` + "/" produced `F:\proj/%`, which
	// matches none of the stored `F:\proj\...` rows — the clear RPC reported
	// success and deleted nothing.
	escaped := strings.NewReplacer("%", "\\%", "_", "\\_").Replace(IndexPath(clean))
	prefix := escaped + "/"
	pattern := prefix + "%"
	if _, err := ci.writerDB.Exec("DELETE FROM symbols WHERE file_path LIKE ? ESCAPE '\\'", pattern); err != nil {
		slog.Warn("[codeintel] ClearProject: failed to delete symbols", "root", root, "err", err)
	}
	if _, err := ci.writerDB.Exec("DELETE FROM file_hashes WHERE file_path LIKE ? ESCAPE '\\'", pattern); err != nil {
		slog.Warn("[codeintel] ClearProject: failed to delete file_hashes", "root", root, "err", err)
	}
	// exploration_paths is deliberately not touched here: it has no file_path
	// column (run_id / session_id / paths_json), so the DELETE that used to sit
	// here failed with "no such column" on every call and was swallowed into a
	// Warn. Run traces are keyed by run, not by file, so a per-root prefix has
	// nothing to match on; clearing them belongs to session cleanup.
}

// SymbolEntry is a database row from the symbols table.
type SymbolEntry struct {
	FilePath   string
	Kind       string
	Name       string
	Signature  string
	Parent     string
	LineStart  int
	LineEnd    int
	Exported   bool
	Visibility string
}

// SymbolIDs returns the database IDs for symbols in a file.
func (ci *CodeIndex) SymbolIDs(ctx context.Context, filePath string) ([]int64, error) {
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()

	rows, err := ci.readerDB.QueryContext(ctx,
		"SELECT id FROM symbols WHERE file_path=? ORDER BY line_start", IndexPath(filePath))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SymbolCount returns the total number of indexed symbols.
func (ci *CodeIndex) SymbolCount(ctx context.Context) (int, error) {
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()
	if ci.readerDB == nil {
		return 0, nil
	}
	var count int
	err := ci.readerDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM symbols").Scan(&count)
	return count, err
}

// ── Reachability Classification (INV-CKG-SINGLE-CLASS) ────────────────────
//
// ClassifyReachability writes symbols.reachability_class for every function
// and method after the full index pipeline completes. All downstream consumers
// — queryCoverageSummary, handleCodeIntelCoverage, FindOrphans, Agent tool
// chain — read this column. No consumer derives reachability from edges.
//
// Classification domain (closed, six values):
//
//	connected          has both incoming and outgoing edges
//	source_only        has outgoing but no incoming (entry points, uncalled funcs)
//	sink_only          has incoming but no outgoing (leaf functions)
//	isolated           no edges at all, no name references — true blind spot
//	name_reachable     no ID-bound edges, but name-matched references — graph degradation
//	structural_exempt  testdata / build-tag / interface / external entry / RPC handler
func (ci *CodeIndex) ClassifyReachability(ctx context.Context) error {
	db := ci.WriterDB()
	if db == nil {
		return nil
	}
	start := time.Now()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ClassifyReachability: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Phase 1: reset all function/method symbols to defaults.
	if _, err := tx.ExecContext(ctx,
		`UPDATE symbols SET reachability_class = 'connected', structural_tag = NULL
		 WHERE kind IN ('function','method','type','interface','class')`); err != nil {
		return fmt.Errorf("ClassifyReachability: reset: %w", err)
	}

	// Phase 2: main classification — single CASE UPDATE covering all six values.
	const classifySQL = `
UPDATE symbols SET reachability_class = CASE
  WHEN file_path LIKE '%/testdata/%' THEN 'structural_exempt'
  WHEN (file_path LIKE '%_test.go' OR file_path LIKE '%_test.py'
        OR file_path LIKE '%.test.ts' OR file_path LIKE '%.test.js'
        OR file_path LIKE '%.spec.ts' OR file_path LIKE '%.spec.js')
    AND NOT EXISTS (SELECT 1 FROM edges e WHERE e.source_id = symbols.id)
    AND NOT EXISTS (SELECT 1 FROM edges e WHERE e.target_id = symbols.id)
    THEN 'structural_exempt'
  WHEN NOT EXISTS (SELECT 1 FROM edges e WHERE e.source_id = symbols.id)
    AND NOT EXISTS (SELECT 1 FROM edges e WHERE e.target_id = symbols.id)
    AND NOT EXISTS (SELECT 1 FROM edges e WHERE e.target_name = symbols.name AND e.kind != 'import')
    THEN 'isolated'
  WHEN NOT EXISTS (SELECT 1 FROM edges e WHERE e.source_id = symbols.id)
    AND NOT EXISTS (SELECT 1 FROM edges e WHERE e.target_id = symbols.id)
    AND EXISTS (SELECT 1 FROM edges e WHERE e.target_name = symbols.name AND e.kind != 'import')
    THEN 'name_reachable'
  WHEN NOT EXISTS (SELECT 1 FROM edges e WHERE e.target_id = symbols.id)
    AND NOT EXISTS (SELECT 1 FROM edges e WHERE e.target_name = symbols.name AND e.kind != 'import')
    AND EXISTS (SELECT 1 FROM edges e WHERE e.source_id = symbols.id)
    THEN 'source_only'
  WHEN NOT EXISTS (SELECT 1 FROM edges e WHERE e.source_id = symbols.id)
    AND EXISTS (SELECT 1 FROM edges e WHERE e.target_id = symbols.id
                OR (e.target_name = symbols.name AND e.kind != 'import'))
    THEN 'sink_only'
  ELSE 'connected'
END
WHERE kind IN ('function','method','type','interface','class')`

	if _, err := tx.ExecContext(ctx, classifySQL); err != nil {
		return fmt.Errorf("ClassifyReachability: classify: %w", err)
	}

	// Phase 3: structural tags for file-pattern based exemptions.
	if _, err := tx.ExecContext(ctx,
		`UPDATE symbols SET structural_tag = 'testdata'
		 WHERE kind IN ('function','method','type','interface','class') AND reachability_class = 'structural_exempt'
		   AND file_path LIKE '%/testdata/%'`); err != nil {
		return fmt.Errorf("ClassifyReachability: tag testdata: %w", err)
	}

	// Build-tag specific files: only exempt if currently isolated/source_only.
	for _, pat := range []string{
		"%_windows.go", "%_linux.go", "%_darwin.go", "%_freebsd.go",
		"%_amd64.go", "%_arm64.go", "%_386.go", "%_js.go", "%_wasm.go",
	} {
		if _, err := tx.ExecContext(ctx,
			`UPDATE symbols SET reachability_class = 'structural_exempt', structural_tag = 'build_tag'
			 WHERE kind IN ('function','method','type','interface','class') AND reachability_class IN ('isolated','source_only')
			   AND file_path LIKE ?`, pat); err != nil {
			return fmt.Errorf("ClassifyReachability: tag build_tag %q: %w", pat, err)
		}
	}

	// Phase 4: Go-level structural exemptions (require in-memory maps).

	// 4a: external entry points (tool protocol dispatch).
	ci.entryMu.RLock()
	entryNames := make([]string, 0, len(ci.externalEntryPoints))
	for name := range ci.externalEntryPoints {
		entryNames = append(entryNames, name)
	}
	ci.entryMu.RUnlock()
	for _, name := range entryNames {
		if _, err := tx.ExecContext(ctx,
			`UPDATE symbols SET reachability_class = 'structural_exempt', structural_tag = 'external_entry'
			 WHERE kind IN ('function','method','type','interface','class') AND name = ?
			   AND reachability_class NOT IN ('connected','sink_only')`, name); err != nil {
			return fmt.Errorf("ClassifyReachability: tag external_entry %q: %w", name, err)
		}
	}

	// 4b: RPC handler methods (cross-language JSON-RPC).
	rpcRows, rpcErr := tx.QueryContext(ctx,
		`SELECT DISTINCT s.name FROM symbols s
		 WHERE s.kind = 'method' AND s.exported = 1
		   AND (s.parent LIKE '%Service' OR s.parent LIKE '%Handler')`)
	if rpcErr != nil {
		return fmt.Errorf("ClassifyReachability: query rpc_handler: %w", rpcErr)
	}
	var rpcNames []string
	for rpcRows.Next() {
		var n string
		if rpcRows.Scan(&n) == nil {
			rpcNames = append(rpcNames, n)
		}
	}
	rpcRows.Close()
	if err := rpcRows.Err(); err != nil {
		return fmt.Errorf("ClassifyReachability: rpc_handler scan: %w", err)
	}
	for _, name := range rpcNames {
		if _, err := tx.ExecContext(ctx,
			`UPDATE symbols SET reachability_class = 'structural_exempt', structural_tag = 'rpc_handler'
			 WHERE kind = 'method' AND name = ?
			   AND reachability_class NOT IN ('connected','sink_only')`, name); err != nil {
			return fmt.Errorf("ClassifyReachability: tag rpc_handler %q: %w", name, err)
		}
	}

	// 4c: interface method declarations — methods whose parent is an interface
	// in the same file are declarations, not implementations.
	if _, err := tx.ExecContext(ctx,
		`UPDATE symbols SET reachability_class = 'structural_exempt', structural_tag = 'interface_decl'
		 WHERE kind IN ('function','method','type','interface','class') AND structural_tag IS NULL
		   AND reachability_class NOT IN ('connected','sink_only')
		   AND EXISTS (
		     SELECT 1 FROM symbols p
		     WHERE p.name = symbols.parent AND p.kind = 'interface'
		       AND p.file_path = symbols.file_path
		   )`); err != nil {
		return fmt.Errorf("ClassifyReachability: tag interface_decl: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ClassifyReachability: commit: %w", err)
	}

	// Log classification distribution.
	rows, qErr := db.QueryContext(ctx,
		`SELECT reachability_class, COUNT(*) FROM symbols
		 WHERE kind IN ('function','method','type','interface','class') GROUP BY reachability_class ORDER BY 2 DESC`)
	if qErr == nil {
		defer rows.Close()
		var parts []string
		for rows.Next() {
			var cls string
			var cnt int
			if rows.Scan(&cls, &cnt) == nil {
				parts = append(parts, fmt.Sprintf("%s=%d", cls, cnt))
			}
		}
		if err := rows.Err(); err != nil {
			slog.Warn("[codeintel] ClassifyReachability stats iteration error", "err", err)
		}
		slog.Info("[codeintel] reachability classified",
			"elapsed", time.Since(start).Round(time.Millisecond),
			"distribution", strings.Join(parts, " "))
	}
	return nil
}

// collectReachabilityNeighbors returns seedPaths plus the file paths of all
// symbols reachable within one ID-bound edge hop from any symbol in seedPaths.
// Name-only references are intentionally excluded; they will be corrected on
// the next full pipeline run. On query error the function logs a warning and
// returns seedPaths unchanged (stale classification is acceptable).
func (ci *CodeIndex) collectReachabilityNeighbors(ctx context.Context, db *sql.DB, seedPaths []string) []string {
	if len(seedPaths) == 0 {
		return nil
	}
	inClause := sqlInList(seedPaths)
	query := `
SELECT DISTINCT file_path FROM symbols WHERE id IN (
  SELECT e.target_id FROM edges e
    JOIN symbols s ON s.id = e.source_id
   WHERE s.file_path IN ` + inClause + `
     AND e.target_id IS NOT NULL
  UNION
  SELECT e.source_id FROM edges e
    JOIN symbols s ON s.id = e.target_id
   WHERE s.file_path IN ` + inClause + `
     AND e.source_id IS NOT NULL
)`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		slog.Warn("[codeintel] collectReachabilityNeighbors query failed", "err", err)
		return seedPaths
	}
	defer rows.Close()

	seen := make(map[string]struct{}, len(seedPaths)*2)
	for _, p := range seedPaths {
		seen[p] = struct{}{}
	}
	for rows.Next() {
		var fp string
		if rows.Scan(&fp) == nil {
			seen[fp] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		slog.Warn("[codeintel] collectReachabilityNeighbors iteration error", "err", err)
	}
	result := make([]string, 0, len(seen))
	for fp := range seen {
		result = append(result, fp)
	}
	return result
}

// ClassifyReachabilityForFiles performs incremental reachability classification
// for a subset of files plus their one-hop ID-bound edge neighbors. It applies
// the same six-value classification and structural tagging as the full
// ClassifyReachability, but scoped to the affected file set. rawPaths are
// normalised via IndexPath before use.
func (ci *CodeIndex) ClassifyReachabilityForFiles(ctx context.Context, rawPaths []string) error {
	if len(rawPaths) == 0 {
		return nil
	}
	db := ci.WriterDB()
	if db == nil {
		return nil
	}
	start := time.Now()

	// Normalise and deduplicate input paths.
	norm := make([]string, 0, len(rawPaths))
	dedup := make(map[string]struct{}, len(rawPaths))
	for _, p := range rawPaths {
		np := IndexPath(p)
		if _, dup := dedup[np]; !dup {
			dedup[np] = struct{}{}
			norm = append(norm, np)
		}
	}

	// Expand to one-hop ID-bound neighbors.
	affected := ci.collectReachabilityNeighbors(ctx, db, norm)
	if len(affected) == 0 {
		return nil
	}
	fileIn := sqlInList(affected)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ClassifyReachabilityForFiles: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Phase 1: reset affected function/method symbols to defaults.
	if _, err := tx.ExecContext(ctx,
		`UPDATE symbols SET reachability_class = 'connected', structural_tag = NULL
		 WHERE kind IN ('function','method','type','interface','class') AND file_path IN `+fileIn); err != nil {
		return fmt.Errorf("ClassifyReachabilityForFiles: reset: %w", err)
	}

	// Phase 2: main classification — same CASE logic as full, scoped to affected files.
	classifySQL := `
UPDATE symbols SET reachability_class = CASE
  WHEN file_path LIKE '%/testdata/%' THEN 'structural_exempt'
  WHEN (file_path LIKE '%_test.go' OR file_path LIKE '%_test.py'
        OR file_path LIKE '%.test.ts' OR file_path LIKE '%.test.js'
        OR file_path LIKE '%.spec.ts' OR file_path LIKE '%.spec.js')
    AND NOT EXISTS (SELECT 1 FROM edges e WHERE e.source_id = symbols.id)
    AND NOT EXISTS (SELECT 1 FROM edges e WHERE e.target_id = symbols.id)
    THEN 'structural_exempt'
  WHEN NOT EXISTS (SELECT 1 FROM edges e WHERE e.source_id = symbols.id)
    AND NOT EXISTS (SELECT 1 FROM edges e WHERE e.target_id = symbols.id)
    AND NOT EXISTS (SELECT 1 FROM edges e WHERE e.target_name = symbols.name AND e.kind != 'import')
    THEN 'isolated'
  WHEN NOT EXISTS (SELECT 1 FROM edges e WHERE e.source_id = symbols.id)
    AND NOT EXISTS (SELECT 1 FROM edges e WHERE e.target_id = symbols.id)
    AND EXISTS (SELECT 1 FROM edges e WHERE e.target_name = symbols.name AND e.kind != 'import')
    THEN 'name_reachable'
  WHEN NOT EXISTS (SELECT 1 FROM edges e WHERE e.target_id = symbols.id)
    AND NOT EXISTS (SELECT 1 FROM edges e WHERE e.target_name = symbols.name AND e.kind != 'import')
    AND EXISTS (SELECT 1 FROM edges e WHERE e.source_id = symbols.id)
    THEN 'source_only'
  WHEN NOT EXISTS (SELECT 1 FROM edges e WHERE e.source_id = symbols.id)
    AND EXISTS (SELECT 1 FROM edges e WHERE e.target_id = symbols.id
                OR (e.target_name = symbols.name AND e.kind != 'import'))
    THEN 'sink_only'
  ELSE 'connected'
END
WHERE kind IN ('function','method','type','interface','class') AND file_path IN ` + fileIn

	if _, err := tx.ExecContext(ctx, classifySQL); err != nil {
		return fmt.Errorf("ClassifyReachabilityForFiles: classify: %w", err)
	}

	// Phase 3: structural tags — file-pattern based exemptions.
	if _, err := tx.ExecContext(ctx,
		`UPDATE symbols SET structural_tag = 'testdata'
		 WHERE kind IN ('function','method','type','interface','class') AND reachability_class = 'structural_exempt'
		   AND file_path LIKE '%/testdata/%' AND file_path IN `+fileIn); err != nil {
		return fmt.Errorf("ClassifyReachabilityForFiles: tag testdata: %w", err)
	}

	for _, pat := range []string{
		"%_windows.go", "%_linux.go", "%_darwin.go", "%_freebsd.go",
		"%_amd64.go", "%_arm64.go", "%_386.go", "%_js.go", "%_wasm.go",
	} {
		if _, err := tx.ExecContext(ctx,
			`UPDATE symbols SET reachability_class = 'structural_exempt', structural_tag = 'build_tag'
			 WHERE kind IN ('function','method','type','interface','class') AND reachability_class IN ('isolated','source_only')
			   AND file_path LIKE ? AND file_path IN `+fileIn, pat); err != nil {
			return fmt.Errorf("ClassifyReachabilityForFiles: tag build_tag %q: %w", pat, err)
		}
	}

	// Phase 4: Go-level structural exemptions scoped to affected files.

	// 4a: external entry points.
	ci.entryMu.RLock()
	entryNames := make([]string, 0, len(ci.externalEntryPoints))
	for name := range ci.externalEntryPoints {
		entryNames = append(entryNames, name)
	}
	ci.entryMu.RUnlock()
	for _, name := range entryNames {
		if _, err := tx.ExecContext(ctx,
			`UPDATE symbols SET reachability_class = 'structural_exempt', structural_tag = 'external_entry'
			 WHERE kind IN ('function','method','type','interface','class') AND name = ?
			   AND reachability_class NOT IN ('connected','sink_only')
			   AND file_path IN `+fileIn, name); err != nil {
			return fmt.Errorf("ClassifyReachabilityForFiles: tag external_entry %q: %w", name, err)
		}
	}

	// 4b: RPC handler methods scoped to affected files.
	rpcRows, rpcErr := tx.QueryContext(ctx,
		`SELECT DISTINCT s.name FROM symbols s
		 WHERE s.kind = 'method' AND s.exported = 1
		   AND (s.parent LIKE '%Service' OR s.parent LIKE '%Handler')
		   AND s.file_path IN `+fileIn)
	if rpcErr != nil {
		return fmt.Errorf("ClassifyReachabilityForFiles: query rpc_handler: %w", rpcErr)
	}
	var rpcNames []string
	for rpcRows.Next() {
		var n string
		if rpcRows.Scan(&n) == nil {
			rpcNames = append(rpcNames, n)
		}
	}
	rpcRows.Close()
	if err := rpcRows.Err(); err != nil {
		return fmt.Errorf("ClassifyReachabilityForFiles: rpc_handler scan: %w", err)
	}
	for _, name := range rpcNames {
		if _, err := tx.ExecContext(ctx,
			`UPDATE symbols SET reachability_class = 'structural_exempt', structural_tag = 'rpc_handler'
			 WHERE kind = 'method' AND name = ?
			   AND reachability_class NOT IN ('connected','sink_only')
			   AND file_path IN `+fileIn, name); err != nil {
			return fmt.Errorf("ClassifyReachabilityForFiles: tag rpc_handler %q: %w", name, err)
		}
	}

	// 4c: interface method declarations.
	if _, err := tx.ExecContext(ctx,
		`UPDATE symbols SET reachability_class = 'structural_exempt', structural_tag = 'interface_decl'
		 WHERE kind IN ('function','method','type','interface','class') AND structural_tag IS NULL
		   AND reachability_class NOT IN ('connected','sink_only')
		   AND file_path IN `+fileIn+`
		   AND EXISTS (
		     SELECT 1 FROM symbols p
		     WHERE p.name = symbols.parent AND p.kind = 'interface'
		       AND p.file_path = symbols.file_path
		   )`); err != nil {
		return fmt.Errorf("ClassifyReachabilityForFiles: tag interface_decl: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ClassifyReachabilityForFiles: commit: %w", err)
	}

	slog.Info("[codeintel] incremental reachability classified",
		"elapsed", time.Since(start).Round(time.Millisecond),
		"seed_files", len(norm), "affected_files", len(affected))
	return nil
}

// FileReachabilityProfile returns per-file reachability counts and edge
// resolution rate. filePath must be a CKG-normalised relative path (use
// IndexPath to convert). All counts read symbols.reachability_class
// (INV-CKG-SINGLE-CLASS).
func (ci *CodeIndex) FileReachabilityProfile(filePath string) (*FileReachProfile, error) {
	ci.readerMu.RLock()
	db := ci.readerDB
	ci.readerMu.RUnlock()
	if db == nil {
		return nil, fmt.Errorf("code index not open")
	}

	p := &FileReachProfile{EdgeResolutionRate: -1}

	// 1. Per-class symbol counts for function/method in this file.
	row := db.QueryRow(`
		SELECT
			COALESCE(SUM(CASE WHEN reachability_class='connected' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN reachability_class='source_only' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN reachability_class='sink_only' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN reachability_class='name_reachable' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN reachability_class='isolated' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN reachability_class='structural_exempt' THEN 1 ELSE 0 END),0),
			COUNT(*)
		FROM symbols
		WHERE file_path = ? AND kind IN ('function','method','type','interface','class')`,
		filePath)
	if err := row.Scan(
		&p.Connected, &p.SourceOnly, &p.SinkOnly,
		&p.NameReachable, &p.Isolated, &p.StructuralExempt,
		&p.TotalFunctions,
	); err != nil {
		return nil, fmt.Errorf("file reachability profile: %w", err)
	}

	// 2. Edge resolution rate: bound call edges / total call edges originating
	//    from symbols in this file. A "bound" edge has resolution = exact|inferred.
	var totalEdges, boundEdges int
	row = db.QueryRow(`
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN e.resolution IN ('exact','inferred') THEN 1 ELSE 0 END),0)
		FROM edges e
		JOIN symbols s ON e.source_id = s.id
		WHERE s.file_path = ? AND e.kind = 'call'`,
		filePath)
	if err := row.Scan(&totalEdges, &boundEdges); err != nil {
		return nil, fmt.Errorf("file edge resolution: %w", err)
	}
	if totalEdges > 0 {
		p.EdgeResolutionRate = float64(boundEdges) / float64(totalEdges)
	}

	return p, nil
}

// ── CKG Graph Queries ──────────────────────────────────────────────────────

// FindOrphans returns exported symbols that have no incoming edges
// (no callers, no references). These are candidates for dead code.
//
// Certainty here grades *this diagnostic's* confidence that the symbol is dead.
// It is unrelated to edges.resolution, which grades one edge's target binding.
// Both used to be floats named "certainty" and were not the same quantity.
//
//   - 1.0 no_callers:           no edge reaches it, no blind spot applies
//   - 0.6 no_callers:           …but it carries JSON tags (deserialization)
//   - 0.4 no_callers:           …but it is a common interface method name
//   - 0.2 rpc_handler:          matches a JSON-RPC method the extension calls
//   - 0.1 external_entry_point: host-registered as reachable from outside the graph
//
// There is no "only low-certainty edges" tier. It existed as an
// `edges.certainty < 0.7` probe that was unreachable by construction: 86% of
// FindOrphans returns exported symbols classified as potentially unreachable.
// INV-CKG-SINGLE-CLASS: reads symbols.reachability_class written by
// ClassifyReachability. No NOT EXISTS bypass — the classification column is
// the single source of truth for all reachability consumers.
func (ci *CodeIndex) FindOrphans(ctx context.Context, opts FindOrphanOpts) ([]OrphanResult, *ReadinessReport, error) {
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()
	r := ci.Readiness()

	// CI-24: completeness < 50% → return empty + report (false positives too likely).
	if r.Completeness < ReadinessMedium {
		return nil, &r, nil
	}

	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	kinds := opts.KindFilter
	if len(kinds) == 0 {
		kinds = []string{"function", "method", "type", "interface", "class"}
	}

	placeholders := make([]string, len(kinds))
	args := make([]any, 0, len(kinds)+2)
	for i, k := range kinds {
		placeholders[i] = "?"
		args = append(args, k)
	}

	// Read the classification column directly — no NOT EXISTS derivation.
	q := `SELECT s.file_path, s.kind, s.name, s.signature, s.parent,
	             s.line_start, s.line_end, s.exported, s.visibility,
	             s.reachability_class
		FROM symbols s
		WHERE s.exported = 1
		  AND s.kind IN (` + strings.Join(placeholders, ",") + `)
		  AND s.reachability_class IN ('isolated', 'name_reachable')`

	if opts.FileFilter != "" {
		q += " AND s.file_path LIKE ?"
		args = append(args, opts.FileFilter+"%")
	}
	if opts.ExcludeTests {
		q += " AND s.file_path NOT LIKE '%_test.go' AND s.file_path NOT LIKE '%_test.py' AND s.file_path NOT LIKE '%.test.ts' AND s.file_path NOT LIKE '%.spec.ts' AND s.file_path NOT LIKE '%.test.js' AND s.file_path NOT LIKE '%.spec.js'"
	}
	if opts.ExcludeBuildTags {
		q += " AND s.file_path NOT LIKE '%/e2e/%' AND s.file_path NOT LIKE '%/bench/%' AND s.file_path NOT LIKE '%/internal/e2e/%' AND s.file_path NOT LIKE '%/internal/bench/%'"
	}

	q += " ORDER BY s.file_path, s.line_start LIMIT ?"
	args = append(args, opts.Limit)

	rows, err := ci.readerDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, &r, err
	}
	defer rows.Close()

	var results []OrphanResult
	for rows.Next() {
		var s SymbolEntry
		var class string
		if err := rows.Scan(&s.FilePath, &s.Kind, &s.Name, &s.Signature, &s.Parent,
			&s.LineStart, &s.LineEnd, &s.Exported, &s.Visibility, &class); err != nil {
			continue
		}

		if opts.ExcludeMain && (s.Name == "main" || s.Name == "init") {
			continue
		}
		if strings.HasPrefix(s.Name, "Test") || strings.HasPrefix(s.Name, "Benchmark") || strings.HasPrefix(s.Name, "Example") {
			continue
		}

		orphan := OrphanResult{
			Symbol:            s,
			ReachabilityClass: class,
		}

		switch class {
		case "isolated":
			orphan.Certainty = 1.0
			orphan.Reason = "no_callers"
			orphan.Suggestion = "likely truly unused — safe to investigate for removal"
		case "name_reachable":
			orphan.Certainty = 0.3
			orphan.Reason = "name_reachable"
			orphan.KnownBlindSpots = []string{"ambiguous_binding"}
			orphan.Suggestion = "referenced by name but not ID-bound — verify with grep before concluding dead"
			orphan.SimilarNames = ci.findSimilarNames(ctx, s.Name)
		}

		if opts.MinCertainty > 0 && orphan.Certainty < opts.MinCertainty {
			continue
		}

		results = append(results, orphan)
	}
	return results, &r, rows.Err()
}

// findSimilarNames queries CKG for symbols with similar name prefixes,
// aiding AI disambiguation for name_reachable symbols.
func (ci *CodeIndex) findSimilarNames(ctx context.Context, name string) []string {
	prefix := name
	if len(prefix) > 4 {
		prefix = prefix[:4]
	}
	var names []string
	rows, err := ci.readerDB.QueryContext(ctx,
		`SELECT DISTINCT name FROM symbols
		 WHERE name LIKE ? AND name != ? AND kind NOT IN ('import','const','var')
		 LIMIT 3`,
		prefix+"%", name)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			names = append(names, n)
		}
	}
	return names
}

// ImpactAnalysis performs BFS from a symbol outward through reverse edges
// to find all symbols that would be affected by a change.
func (ci *CodeIndex) ImpactAnalysis(ctx context.Context, symbolName string, maxDepth int) ([]ImpactNode, *ReadinessReport, error) {
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()
	r := ci.Readiness()

	if maxDepth <= 0 {
		maxDepth = 3
	}
	// CI-24: low completeness → cap depth to reduce noise.
	if r.Completeness < ReadinessMedium && maxDepth > 1 {
		maxDepth = 1
	}

	// Find seed symbol IDs.
	seedRows, err := ci.readerDB.QueryContext(ctx,
		`SELECT id, file_path, kind, name, signature, parent, line_start, line_end, exported, visibility
		 FROM symbols WHERE name = ? AND kind != 'import'`, symbolName)
	if err != nil {
		return nil, &r, err
	}

	type symWithID struct {
		id int64
		SymbolEntry
	}
	var seeds []symWithID
	for seedRows.Next() {
		var s symWithID
		if err := seedRows.Scan(&s.id, &s.FilePath, &s.Kind, &s.Name, &s.Signature, &s.Parent, &s.LineStart, &s.LineEnd, &s.Exported, &s.Visibility); err != nil {
			continue
		}
		seeds = append(seeds, s)
	}
	seedRows.Close()

	if len(seeds) == 0 {
		return nil, &r, nil
	}

	visited := map[int64]bool{}
	parentPath := map[int64][]string{} // tracks the full path from root to each node
	for _, s := range seeds {
		visited[s.id] = true
		parentPath[s.id] = []string{symbolName}
	}

	// BFS layer by layer.
	currentIDs := make([]int64, len(seeds))
	currentNames := []string{symbolName}
	for i, s := range seeds {
		currentIDs[i] = s.id
	}

	// nameToParentPath maps target_name -> parent path for unresolved edges.
	nameToParentPath := map[string][]string{symbolName: {symbolName}}

	var results []ImpactNode

	for depth := 1; depth <= maxDepth; depth++ {
		if len(currentIDs) == 0 && len(currentNames) == 0 {
			break
		}

		var nextLayer []symWithID

		if len(currentIDs) > 0 {
			idPlaceholders := make([]string, len(currentIDs))
			idArgs := make([]any, len(currentIDs))
			for i, id := range currentIDs {
				idPlaceholders[i] = "?"
				idArgs[i] = id
			}
			q := `SELECT DISTINCT s.id, s.file_path, s.kind, s.name, s.signature, s.parent, s.line_start, s.line_end, s.exported, s.visibility, e.kind, e.resolution, e.target_id
				FROM edges e JOIN symbols s ON e.source_id = s.id
				WHERE e.target_id IN (` + strings.Join(idPlaceholders, ",") + `) LIMIT 50`
			rows, err := ci.readerDB.QueryContext(ctx, q, idArgs...)
			if err == nil {
				for rows.Next() {
					var s symWithID
					var ek string
					var res Resolution
					var targetID *int64
					if err := rows.Scan(&s.id, &s.FilePath, &s.Kind, &s.Name, &s.Signature, &s.Parent, &s.LineStart, &s.LineEnd, &s.Exported, &s.Visibility, &ek, &res, &targetID); err != nil {
						continue
					}
					if visited[s.id] {
						continue
					}
					visited[s.id] = true

					var path []string
					if targetID != nil {
						if pp, ok := parentPath[*targetID]; ok {
							path = append(append([]string{}, pp...), s.Name)
						}
					}
					if len(path) == 0 {
						path = []string{symbolName, s.Name}
					}
					parentPath[s.id] = path

					nextLayer = append(nextLayer, s)
					results = append(results, ImpactNode{
						Symbol:     s.SymbolEntry,
						Depth:      depth,
						EdgeKind:   ek,
						Resolution: res,
						Path:       path,
					})
				}
				rows.Close()
			}
		}

		if len(currentNames) > 0 {
			namePlaceholders := make([]string, len(currentNames))
			nameArgs := make([]any, len(currentNames))
			for i, n := range currentNames {
				namePlaceholders[i] = "?"
				nameArgs[i] = n
			}
			q := `SELECT DISTINCT s.id, s.file_path, s.kind, s.name, s.signature, s.parent, s.line_start, s.line_end, s.exported, s.visibility, e.kind, e.resolution, e.target_name
				FROM edges e JOIN symbols s ON e.source_id = s.id
				WHERE e.target_name IN (` + strings.Join(namePlaceholders, ",") + `) AND e.target_id IS NULL LIMIT 50`
			rows, err := ci.readerDB.QueryContext(ctx, q, nameArgs...)
			if err == nil {
				for rows.Next() {
					var s symWithID
					var ek string
					var res Resolution
					var targetName string
					if err := rows.Scan(&s.id, &s.FilePath, &s.Kind, &s.Name, &s.Signature, &s.Parent, &s.LineStart, &s.LineEnd, &s.Exported, &s.Visibility, &ek, &res, &targetName); err != nil {
						continue
					}
					if visited[s.id] {
						continue
					}
					visited[s.id] = true

					var path []string
					if pp, ok := nameToParentPath[targetName]; ok {
						path = append(append([]string{}, pp...), s.Name)
					} else {
						path = []string{symbolName, s.Name}
					}
					parentPath[s.id] = path

					nextLayer = append(nextLayer, s)
					results = append(results, ImpactNode{
						Symbol:     s.SymbolEntry,
						Depth:      depth,
						EdgeKind:   ek,
						Resolution: res,
						Path:       path,
					})
				}
				rows.Close()
			}
		}

		// Prepare next BFS layer.
		currentIDs = currentIDs[:0]
		currentNames = currentNames[:0]
		nameToParentPath = map[string][]string{}
		for _, s := range nextLayer {
			currentIDs = append(currentIDs, s.id)
			currentNames = append(currentNames, s.Name)
			nameToParentPath[s.Name] = parentPath[s.id]
		}
	}

	return results, &r, nil
}

// FindSimilar returns symbols that are similar to the named symbol,
// using a multi-strategy approach: name fuzzy match, signature pattern,
// callee set overlap, and optional embedding cosine similarity.
func (ci *CodeIndex) FindSimilar(ctx context.Context, name string, opts FindSimilarOpts) ([]SimilarResult, error) {
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()

	if opts.Limit <= 0 {
		opts.Limit = 10
	}
	if opts.MinScore <= 0 {
		opts.MinScore = 0.3
	}

	// Strategy 1: FTS5 name/signature fuzzy match.
	candidates := map[string]*SimilarResult{} // keyed by "filepath:name:line"

	ftsRows, err := ci.readerDB.QueryContext(ctx,
		`SELECT s.file_path, s.kind, s.name, s.signature, s.parent, s.line_start, s.line_end, s.exported, s.visibility
		 FROM symbol_fts f JOIN symbols s ON f.rowid = s.id
		 WHERE symbol_fts MATCH ? AND s.name != ? AND s.kind IN ('function','method','type','interface','class')
		 ORDER BY rank LIMIT 50`, name, name)
	if err == nil {
		for ftsRows.Next() {
			var s SymbolEntry
			if err := ftsRows.Scan(&s.FilePath, &s.Kind, &s.Name, &s.Signature, &s.Parent, &s.LineStart, &s.LineEnd, &s.Exported, &s.Visibility); err != nil {
				continue
			}
			key := fmt.Sprintf("%s:%s:%d", s.FilePath, s.Name, s.LineStart)
			nameScore := nameSimilarity(name, s.Name)
			candidates[key] = &SimilarResult{
				Symbol:    s,
				Score:     nameScore * 0.4,
				MatchType: "name",
			}
		}
		ftsRows.Close()
	}

	// Strategy 2: Signature pattern similarity (if provided).
	if opts.Signature != "" {
		sigRows, err := ci.readerDB.QueryContext(ctx,
			`SELECT file_path, kind, name, signature, parent, line_start, line_end, exported, visibility
			 FROM symbols WHERE kind IN ('function','method','type','interface','class') AND name != ? LIMIT 200`, name)
		if err == nil {
			for sigRows.Next() {
				var s SymbolEntry
				if err := sigRows.Scan(&s.FilePath, &s.Kind, &s.Name, &s.Signature, &s.Parent, &s.LineStart, &s.LineEnd, &s.Exported, &s.Visibility); err != nil {
					continue
				}
				sigScore := signatureSimilarity(opts.Signature, s.Signature)
				if sigScore < 0.2 {
					continue
				}
				key := fmt.Sprintf("%s:%s:%d", s.FilePath, s.Name, s.LineStart)
				if existing, ok := candidates[key]; ok {
					existing.Score += sigScore * 0.3
					existing.MatchType = "combined"
				} else {
					candidates[key] = &SimilarResult{
						Symbol:    s,
						Score:     sigScore * 0.3,
						MatchType: "signature",
					}
				}
			}
			sigRows.Close()
		}
	}

	// Strategy 3: Callee set Jaccard similarity.
	if opts.CallerPath != "" {
		sourceCallees, _ := ci.calleesOfUnlocked(ctx, opts.CallerPath, name, opts.CallerLine)
		if len(sourceCallees) > 0 {
			srcSet := map[string]bool{}
			for _, c := range sourceCallees {
				srcSet[c] = true
			}

			// Compare with other functions in the same or nearby files.
			otherRows, err := ci.readerDB.QueryContext(ctx,
				`SELECT DISTINCT s.file_path, s.name, s.line_start FROM symbols s
				 WHERE s.kind IN ('function','method','type','interface','class') AND s.name != ? LIMIT 100`, name)
			if err == nil {
				for otherRows.Next() {
					var fp, n string
					var line int
					if err := otherRows.Scan(&fp, &n, &line); err != nil {
						continue
					}
					otherCallees, _ := ci.calleesOfUnlocked(ctx, fp, n, line)
					if len(otherCallees) == 0 {
						continue
					}
					j := jaccard(srcSet, otherCallees)
					if j < 0.2 {
						continue
					}
					key := fmt.Sprintf("%s:%s:%d", fp, n, line)
					if existing, ok := candidates[key]; ok {
						existing.Score += j * 0.3
						existing.MatchType = "combined"
					}
				}
				otherRows.Close()
			}
		}
	}

	// Collect and sort results.
	var results []SimilarResult
	for _, r := range candidates {
		if r.Score >= opts.MinScore {
			results = append(results, *r)
		}
	}

	// Sort by score descending.
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
	if len(results) > opts.Limit {
		results = results[:opts.Limit]
	}
	return results, nil
}

// calleesOfUnlocked is like CalleesOf but assumes the read lock is already held.
func (ci *CodeIndex) calleesOfUnlocked(ctx context.Context, path, name string, line int) ([]string, error) {
	rows, err := ci.readerDB.QueryContext(ctx,
		`SELECT e.target_name FROM edges e
		 JOIN symbols s ON e.source_id=s.id
		 WHERE e.kind='call' AND s.file_path=? AND s.name=? AND s.line_start=?`, IndexPath(path), name, line)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			continue
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// nameSimilarity computes a simple similarity score between two names
// based on common prefix length and total length ratio.
func nameSimilarity(a, b string) float64 {
	if a == b {
		return 1.0
	}
	a = strings.ToLower(a)
	b = strings.ToLower(b)

	// Common prefix.
	common := 0
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for i := 0; i < limit; i++ {
		if a[i] == b[i] {
			common++
		} else {
			break
		}
	}

	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	if maxLen == 0 {
		return 0
	}

	// Also check substring containment.
	containsBonus := 0.0
	if strings.Contains(a, b) || strings.Contains(b, a) {
		containsBonus = 0.3
	}

	prefixScore := float64(common) / float64(maxLen)
	return prefixScore*0.7 + containsBonus
}

// signatureSimilarity compares two function signatures by extracting
// parameter type tokens and computing Jaccard similarity.
func signatureSimilarity(a, b string) float64 {
	tokensA := sigTokens(a)
	tokensB := sigTokens(b)
	if len(tokensA) == 0 && len(tokensB) == 0 {
		return 0.5
	}
	setA := map[string]bool{}
	for _, t := range tokensA {
		setA[t] = true
	}
	intersection := 0
	unionSet := map[string]bool{}
	for k := range setA {
		unionSet[k] = true
	}
	for _, t := range tokensB {
		unionSet[t] = true
		if setA[t] {
			intersection++
		}
	}
	if len(unionSet) == 0 {
		return 0
	}
	return float64(intersection) / float64(len(unionSet))
}

func sigTokens(sig string) []string {
	// Extract type-like tokens from a signature.
	var tokens []string
	for _, word := range strings.FieldsFunc(sig, func(r rune) bool {
		return r == '(' || r == ')' || r == ',' || r == ' ' || r == '{' || r == '}' || r == '\t' || r == '\n'
	}) {
		w := strings.TrimSpace(word)
		if w == "" || w == "func" || w == "function" || w == "def" || w == "fn" || w == "void" || w == "return" {
			continue
		}
		tokens = append(tokens, strings.ToLower(w))
	}
	return tokens
}

func jaccard(setA map[string]bool, listB []string) float64 {
	union := map[string]bool{}
	for k := range setA {
		union[k] = true
	}
	intersection := 0
	for _, b := range listB {
		union[b] = true
		if setA[b] {
			intersection++
		}
	}
	if len(union) == 0 {
		return 0
	}
	return float64(intersection) / float64(len(union))
}

func contentHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:16])
}

func isBuiltinOrKeyword(w string) bool {
	switch w {
	case "func", "function", "def", "fn", "return", "error", "string", "int",
		"int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32",
		"uint64", "float32", "float64", "bool", "byte", "rune", "any",
		"interface", "struct", "map", "chan", "nil", "true", "false",
		"String", "Integer", "Boolean", "Float", "Double", "Object", "Void",
		"None", "True", "False", "Self":
		return true
	}
	return false
}

// extractMethodNamesFromBody extracts method-like identifiers from an interface
// body. Uses heuristic: lines containing a function-like pattern (word followed
// by parenthesis) are treated as method declarations.
func extractMethodNamesFromBody(body string) []string {
	var methods []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") {
			continue
		}
		// Go interface methods: Name(params) (returns)
		if idx := strings.Index(line, "("); idx > 0 {
			candidate := strings.TrimSpace(line[:idx])
			// Take last word before paren as method name.
			parts := strings.Fields(candidate)
			if len(parts) > 0 {
				name := parts[len(parts)-1]
				if len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z' {
					methods = append(methods, name)
				}
			}
		}
	}
	return methods
}

// methodSetSatisfies checks if the concrete type's methods are a superset
// of the interface's required methods.
func methodSetSatisfies(typeMethods, ifaceMethods []string) bool {
	if len(ifaceMethods) == 0 {
		return false
	}
	set := map[string]bool{}
	for _, m := range typeMethods {
		set[m] = true
	}
	for _, m := range ifaceMethods {
		if !set[m] {
			return false
		}
	}
	return true
}

// EnrichWithLSP uses language server precision to confirm heuristic CKG edges.
// For implements edges, LSP References on the interface method can confirm that
// the type's method is a genuine implementation, which promotes the edge from
// `inferred` (method-set satisfaction — see pass8 stage B) to `exact`.
//
// Only already-bound edges are eligible: References confirms that the relation
// holds, it does not hand back a symbol id, so an unbound edge has nothing to
// become exact *about*. The old query took every `certainty < 1.0` edge, which
// swept in unbound ones and would now be refused by the schema — correctly, and
// too late to notice, since the UPDATE discarded its error.
//
// This is a best-effort enrichment — if LSP is unavailable or returns errors,
// the heuristic edges remain unchanged. Called after background indexing completes.
func (ci *CodeIndex) EnrichWithLSP(ctx context.Context, lsp LSPBridge) int {
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()
	if lsp == nil {
		return 0
	}
	if _, isNoop := lsp.(NoopLSP); isNoop {
		return 0
	}

	// Find inferred implements edges that LSP could promote to exact. The
	// resolution filter is what makes repeated enrichment idempotent: a
	// promoted edge reads `exact` and is never selected again.
	rows, err := ci.readerDB.QueryContext(ctx,
		`SELECT e.id, e.target_name, s.file_path, s.line_start
		 FROM edges e JOIN symbols s ON e.source_id = s.id
		 WHERE e.kind = 'implements'
		   AND e.resolution = 'inferred'
		   AND e.target_id IS NOT NULL
		 LIMIT 100`)
	if err != nil {
		return 0
	}

	type candidate struct {
		edgeID     int64
		targetName string
		filePath   string
		line       int
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if rows.Scan(&c.edgeID, &c.targetName, &c.filePath, &c.line) == nil {
			candidates = append(candidates, c)
		}
	}
	rows.Close()

	// Lang-level circuit breaker: when the IDE LSP returns empty or errors
	// for a language twice in a row, that language has no answering
	// extension. Remaining candidates of the same language are skipped
	// instead of repeating the same fruitless request. Skipping leaves
	// the edge at `inferred` (CKG), which is the documented next hop
	// when the extension has no answer (INV-LSP-10). Background
	// enrichment uses a 5s per-query timeout — longer than the 200ms
	// OnDemandQuery budget because the editor's language server can take
	// seconds on large projects and enrichment is not on the Agent hot path.
	failedLangs := make(map[string]int)
	const maxConsecutiveEmpty = 2

	enriched := 0
	for _, c := range candidates {
		langID := LangForPath(c.filePath)
		if langID != "" && failedLangs[langID] >= maxConsecutiveEmpty {
			continue
		}
		// INV-LSP-03: LSP only upgrades resolution. The edge itself was
		// produced by tree-sitter; `source` records who confirmed it.
		queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		refs, err := lsp.References(queryCtx, c.filePath, c.line, 0)
		cancel()
		if err != nil {
			failedLangs[langID]++
			continue
		}
		if len(refs) == 0 {
			failedLangs[langID]++
			continue
		}
		failedLangs[langID] = 0
		if _, err := ci.writerDB.ExecContext(ctx,
			`UPDATE edges SET resolution = 'exact', source = 'lsp' WHERE id = ?`,
			c.edgeID); err != nil {
			slog.Warn("[codeintel] LSP enrich update failed", "edge_id", c.edgeID, "error", err)
			continue
		}
		enriched++
	}
	return enriched
}

// extractDeserializationTargets finds type names that are targets of JSON/YAML
// deserialization calls in a function body. Patterns detected:
//   - json.Unmarshal(data, &Type{})  / json.Unmarshal(data, &v) where v is typed
//   - json.NewDecoder(...).Decode(&Type{})
//   - yaml.Unmarshal(data, &Type{})
//   - var v Type; json.Unmarshal(data, &v)
//
// Uses string heuristics (not full AST) — misses indirect patterns but catches
// the common case with zero tree-sitter overhead.
func extractDeserializationTargets(body string) []string {
	seen := map[string]bool{}
	var targets []string

	lines := strings.Split(body, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Pattern 1: Unmarshal/Decode with &Type{} literal
		// e.g., json.Unmarshal(data, &Config{})
		//       decoder.Decode(&AppConfig{})
		if strings.Contains(trimmed, "Unmarshal") || strings.Contains(trimmed, "Decode") {
			// Look for &TypeName{ or &TypeName)
			for i := 0; i < len(trimmed)-1; i++ {
				if trimmed[i] == '&' {
					// Extract the type name after &
					j := i + 1
					for j < len(trimmed) && ((trimmed[j] >= 'A' && trimmed[j] <= 'Z') || (trimmed[j] >= 'a' && trimmed[j] <= 'z') || (trimmed[j] >= '0' && trimmed[j] <= '9') || trimmed[j] == '_') {
						j++
					}
					if j > i+1 {
						name := trimmed[i+1 : j]
						if len(name) >= 2 && name[0] >= 'A' && name[0] <= 'Z' && !isBuiltinOrKeyword(name) && !seen[name] {
							seen[name] = true
							targets = append(targets, name)
						}
					}
				}
			}
		}

		// Pattern 2: var v Type (followed by Unmarshal on next lines referencing &v)
		// We only capture the Type from "var xxx Type" declarations in the body
		// where the variable is later used in an Unmarshal-like call.
		// Simplified: just extract types from "var x Type" where Type is exported.
		if strings.HasPrefix(trimmed, "var ") {
			fields := strings.Fields(trimmed)
			if len(fields) >= 3 {
				typeName := fields[2]
				// Clean trailing characters
				typeName = strings.TrimRight(typeName, ";,)")
				typeName = strings.TrimPrefix(typeName, "*")
				typeName = strings.TrimPrefix(typeName, "[]")
				typeName = strings.TrimPrefix(typeName, "*")
				if len(typeName) >= 2 && typeName[0] >= 'A' && typeName[0] <= 'Z' && !isBuiltinOrKeyword(typeName) && !seen[typeName] {
					// Only include if the body also has Unmarshal/Decode
					if strings.Contains(body, "Unmarshal") || strings.Contains(body, "Decode") {
						seen[typeName] = true
						targets = append(targets, typeName)
					}
				}
			}
		}
	}
	return targets
}

// IndexFileFromBuffer indexes a file from provided content (instead of reading
// from disk). Used by PreWriteCheck to refresh CKG from dirty editor buffers
// (CI-26). Writes content to a temp file so the Worker subprocess can parse it.
func (ci *CodeIndex) IndexFileFromBuffer(ctx context.Context, path string, content []byte) error {
	path = IndexPath(path)
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()
	if IsMinified(path, content) {
		return nil
	}

	hash := contentHash(content)

	var existingHash string
	_ = ci.readerDB.QueryRowContext(ctx, "SELECT content_hash FROM file_hashes WHERE file_path=?", path).Scan(&existingHash)
	if existingHash == hash {
		return nil
	}

	// Write buffer content to a temp file so the Worker subprocess can read it.
	tmpDir := os.TempDir()
	tmpFile := filepath.Join(tmpDir, "wescode-buf-"+filepath.Base(path))
	if err := os.WriteFile(tmpFile, content, 0600); err != nil {
		return fmt.Errorf("write temp buffer: %w", err)
	}
	defer os.Remove(tmpFile)

	lang, hasLang := treesitter.DetectLang(path)
	if !hasLang {
		return ci.indexNonParseable(ctx, path, hash, 0)
	}

	// Parse via Worker using temp file, but store results under the real path.
	nodes, edges, err := parseViaWorker(ctx, []WorkerFile{{Path: tmpFile, Language: string(lang)}})
	if err != nil {
		return fmt.Errorf("worker parse buffer %s: %w", filepath.Base(path), err)
	}

	// Rewrite file paths in results to point to the real path.
	for _, n := range nodes {
		n.FilePath = path
	}

	if err := ci.applyFileDelta(ctx, path, hash, 0, nodes, edges); err != nil {
		return err
	}

	return ci.ResolveEdgeTargets(ctx)
}

// ── Readiness tracking ─────────────────────────────────────────────────────

// SetTotalFiles records the total number of indexable files discovered during
// project scan. Called by the background indexer before starting IndexFile loop.
func (ci *CodeIndex) SetTotalFiles(n int) {
	atomic.StoreInt32(&ci.totalFiles, int32(n))
}

// SetIndexing marks whether a full project index is in progress.
// MindMap generation is NOT triggered here — callers (backgroundIndexAll,
// FileWatcher) invoke RegenerateMindMapSync / GenerateMindMap explicitly
// after ensuring writerDB points to the current code.db.
func (ci *CodeIndex) SetIndexing(v bool) {
	if v {
		atomic.StoreInt32(&ci.indexing, 1)
	} else {
		atomic.StoreInt32(&ci.indexing, 0)
		ci.lastFullScanNano.Store(time.Now().UnixNano())
	}
}

// InitReadinessCounts bootstraps indexedCount from file_hashes rows on startup.
func (ci *CodeIndex) InitReadinessCounts(ctx context.Context) {
	var count int
	ci.readerDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM file_hashes").Scan(&count)
	atomic.StoreInt32(&ci.indexedCount, int32(count))
}

// ScanStaleness compares indexed file mtimes against current disk state.
// Updates staleCount atomically. Should be called periodically (e.g. on
// FileWatcher tick) to keep the Freshness dimension accurate.
func (ci *CodeIndex) ScanStaleness(ctx context.Context) int {

	rows, err := ci.readerDB.QueryContext(ctx, "SELECT file_path, mtime_ns FROM file_hashes WHERE mtime_ns > 0")
	if err != nil {
		return 0
	}
	defer rows.Close()

	stale := 0
	for rows.Next() {
		var fp string
		var indexedMtime int64
		if rows.Scan(&fp, &indexedMtime) != nil {
			continue
		}
		info, err := os.Stat(fp)
		if err != nil {
			stale++ // file deleted or inaccessible
			continue
		}
		if info.ModTime().UnixNano() != indexedMtime {
			stale++
		}
	}
	atomic.StoreInt32(&ci.staleCount, int32(stale))
	return stale
}

// ScanStalenessFiles returns the paths of indexed files whose disk mtime
// differs from the indexed mtime — including files deleted since indexing.
// Unlike ScanStaleness (which only counts for readiness reporting), this
// feeds the FileWatcher's staleness recovery: re-index changed files and
// RemoveFile the deleted ones to converge the index after missed events.
func (ci *CodeIndex) ScanStalenessFiles(ctx context.Context) []string {
	rows, err := ci.readerDB.QueryContext(ctx, "SELECT file_path, mtime_ns FROM file_hashes WHERE mtime_ns > 0")
	if err != nil {
		return nil
	}
	defer rows.Close()

	var stale []string
	for rows.Next() {
		var fp string
		var indexedMtime int64
		if rows.Scan(&fp, &indexedMtime) != nil {
			continue
		}
		info, err := os.Stat(fp)
		if err != nil {
			stale = append(stale, fp)
			continue
		}
		if info.ModTime().UnixNano() != indexedMtime {
			stale = append(stale, fp)
		}
	}
	return stale
}
