package codeintel

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/weisyn/wescode/internal/treesitter"
)

// indexFileViaWorker parses a single file in an isolated Worker subprocess
// and applies the resulting nodes/edges as a delta to the production DB.
// This is the ONLY indexing path for incremental updates — tree-sitter CGO
// never executes in the main process for indexing.
func (ci *CodeIndex) indexFileViaWorker(ctx context.Context, path string, content []byte, hash string, mtimeNs int64) error {
	lang, hasLang := treesitter.DetectLang(path)
	if !hasLang {
		return ci.indexNonParseable(ctx, path, hash, mtimeNs)
	}

	nodes, edges, err := parseViaWorker(ctx, []WorkerFile{{Path: path, Language: string(lang)}})
	if err != nil {
		return fmt.Errorf("worker parse %s: %w", filepath.Base(path), err)
	}

	if err := ci.applyFileDelta(ctx, path, hash, mtimeNs, nodes, edges); err != nil {
		return err
	}
	// A single-file delta cannot resolve its own edges: the worker saw one file,
	// so calls into other files arrive as names (INV-CKG-EDGE-01). It also cannot
	// leave *other* files' edges alone — deleting this file's symbols unbinds
	// every inbound edge (the unbind trigger), and those have to find their new
	// symbol ids or CallersOf silently shrinks with each save (P0-1).
	//
	// So the full resolver runs here, not a cheap dotted-names subset. It is the
	// same call the background indexer makes; being idempotent and ~0.5s on this
	// repo is what makes one path serve both.
	if err := ci.ResolveEdgeTargets(ctx); err != nil {
		return err
	}
	if err := ci.ClassifyReachabilityForFiles(ctx, []string{path}); err != nil {
		slog.Warn("[codeintel] incremental reachability classification failed", "path", path, "err", err)
	}
	return nil
}

// IndexFilesViaWorker dispatches a batch of files to a Worker subprocess and
// applies the resulting delta to the production DB. Used by FileWatcher for
// small batches (< pipeline threshold).
func (ci *CodeIndex) IndexFilesViaWorker(ctx context.Context, files []FileToParse) error {
	if len(files) == 0 {
		return nil
	}

	// Hold readerMu for the whole batch: the post-batch pass4 helpers
	// (InferImplementsFromDB / AnnotateVirtualCallsDB / listAllTypeFiles)
	// query readerDB directly, and this watcher path is NOT called from a
	// pre-locked entry (unlike IndexFile/IndexFileFromBuffer which already
	// hold RLock before reaching applyFileDelta).
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()

	wf := make([]WorkerFile, 0, len(files))
	for _, f := range files {
		if _, ok := treesitter.DetectLang(f.Path); ok {
			wf = append(wf, WorkerFile{Path: f.Path, Language: f.Language})
		}
	}

	// A batch of files tree-sitter cannot parse (JSON, shell, extensionless)
	// still has to record its mtime: ScanStalenessFiles judges by that row
	// alone, so returning before the write leaves every such file permanently
	// stale and the recovery pass re-reports the identical batch every tick.
	// The single-file path never had this hole (indexNonParseable).
	if len(wf) == 0 {
		return recordFileHashes(ctx, ci.writerDB, files)
	}

	nodes, edges, err := parseViaWorker(ctx, wf)
	if err != nil {
		return err
	}

	tx, err := ci.writerDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Group by file for per-file delta application.
	nodesByFile := make(map[string][]*Node)
	for _, n := range nodes {
		nodesByFile[n.FilePath] = append(nodesByFile[n.FilePath], n)
	}

	for _, f := range files {
		if err := deleteFileSymbols(ctx, tx, f.Path); err != nil {
			return err
		}
	}

	qnameToID, err := insertNodes(ctx, tx, nodes)
	if err != nil {
		return err
	}

	if err := insertEdges(ctx, tx, edges, qnameToID); err != nil {
		return err
	}

	if err := recordFileHashes(ctx, tx, files); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// Post-batch: infer implements edges for all affected files.
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	InferImplementsFromDB(ci, paths)

	// If any affected file contains an interface definition, re-check ALL project types
	// against that interface (a new interface method invalidates existing matches).
	if hasInterfaceDefinition(ci, paths) {
		allTypeFiles := listAllTypeFiles(ci)
		if len(allTypeFiles) > 0 {
			InferImplementsFromDB(ci, allTypeFiles)
		}
	}

	// Post-batch: annotate virtual calls for newly created implements/overrides edges.
	AnnotateVirtualCallsDB(ci)

	// Same reason as the single-file path: this batch's deletes unbound inbound
	// edges from files nobody re-parsed, and its own cross-file calls arrived as
	// names. Without this the watcher path degrades the graph on every save (P0-1).
	if err := ci.ResolveEdgeTargets(ctx); err != nil {
		return err
	}
	if err := ci.ClassifyReachabilityForFiles(ctx, paths); err != nil {
		slog.Warn("[codeintel] incremental reachability classification failed", "paths", len(paths), "err", err)
	}
	return nil
}

// parseViaWorker spawns a Worker subprocess, sends the file list, and collects
// parsed nodes and edges. The Worker process is fully isolated (Setpgid) so a
// tree-sitter CGO crash cannot affect the main process.
func parseViaWorker(ctx context.Context, files []WorkerFile) ([]*Node, []Edge, error) {
	// Test binaries do not register --index-worker; parse in-process instead.
	// (worker.go isTestBinary; production binaries never take this branch.)
	if isTestBinary() {
		return parseViaWorkerInProcess(ctx, files)
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve executable: %w", err)
	}

	cmd := exec.CommandContext(ctx, executable, "--index-worker")
	setProcGroup(cmd)
	cmd.Env = append(os.Environ(), "GOMAXPROCS=2")
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start incremental worker: %w", err)
	}

	req := WorkerRequest{Files: files}
	if err := json.NewEncoder(stdin).Encode(&req); err != nil {
		_ = killGroup(cmd.Process.Pid)
		_ = cmd.Wait()
		return nil, nil, fmt.Errorf("send request: %w", err)
	}
	stdin.Close()

	var nodes []*Node
	var edges []Edge

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		var out WorkerOutput
		if err := json.Unmarshal(scanner.Bytes(), &out); err != nil {
			continue
		}
		switch out.Type {
		case "node":
			if out.Node != nil {
				n := workerNodeToNode(out.Node)
				nodes = append(nodes, &n)
			}
		case "edge":
			if out.Edge != nil {
				edges = append(edges, workerEdgeToEdge(out.Edge))
			}
		case "done":
			break
		case "error":
			slog.Debug("incremental worker file error", "err", out.Error)
		}
	}

	if err := cmd.Wait(); err != nil {
		return nil, nil, fmt.Errorf("incremental worker exited: %w", err)
	}
	return nodes, edges, nil
}

// parseViaWorkerInProcess runs the worker parsing logic in-process for test
// binaries, which do not register the --index-worker flag and therefore cannot
// be spawned as subprocesses. It mirrors parseViaWorker's wire protocol in
// memory: workerParseFile emits the same WorkerOutput stream to a buffer,
// which is then decoded with the identical node/edge conversion.
func parseViaWorkerInProcess(ctx context.Context, files []WorkerFile) ([]*Node, []Edge, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	pool := treesitter.NewParserPoolN(2)
	defer pool.Close()

	for _, f := range files {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if err := workerParseFile(pool, enc, f.Path, f.Language); err != nil {
			slog.Debug("in-process worker file error", "path", f.Path, "err", err)
		}
	}

	var nodes []*Node
	var edges []Edge
	scanner := bufio.NewScanner(&buf)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var out WorkerOutput
		if err := json.Unmarshal(scanner.Bytes(), &out); err != nil {
			continue
		}
		switch out.Type {
		case "node":
			if out.Node != nil {
				n := workerNodeToNode(out.Node)
				nodes = append(nodes, &n)
			}
		case "edge":
			if out.Edge != nil {
				edges = append(edges, workerEdgeToEdge(out.Edge))
			}
		case "error":
			slog.Debug("in-process worker file error", "err", out.Error)
		}
	}
	return nodes, edges, scanner.Err()
}

// applyFileDelta applies parsed nodes/edges for a single file to the production DB.
func (ci *CodeIndex) applyFileDelta(ctx context.Context, path, hash string, mtimeNs int64, nodes []*Node, edges []Edge) error {
	path = IndexPath(path)
	tx, err := ci.writerDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := deleteFileSymbols(ctx, tx, path); err != nil {
		return err
	}

	qnameToID, err := insertNodes(ctx, tx, nodes)
	if err != nil {
		return err
	}

	if err := insertEdges(ctx, tx, edges, qnameToID); err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO file_hashes (file_path, content_hash, mtime_ns) VALUES (?, ?, ?)`, path, hash, mtimeNs)
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// Post-delta: infer implements edges for the affected file.
	InferImplementsFromDB(ci, []string{path})

	// Annotate virtual calls for new implements/overrides edges.
	AnnotateVirtualCallsDB(ci)

	return nil
}

// indexNonParseable handles files without tree-sitter support (just record mtime).
func (ci *CodeIndex) indexNonParseable(ctx context.Context, path, hash string, mtimeNs int64) error {
	_, err := ci.writerDB.ExecContext(ctx, `INSERT OR REPLACE INTO file_hashes (file_path, content_hash, mtime_ns) VALUES (?, ?, ?)`, IndexPath(path), hash, mtimeNs)
	return err
}

func deleteFileSymbols(ctx context.Context, tx *sql.Tx, path string) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM symbols WHERE file_path=?", IndexPath(path))
	return err
}

// RemoveFile removes all indexed data for a file (symbols + hash row).
//
// The two FK directions on `edges` deliberately differ, and this is the call
// site where that matters most. Outbound edges (source_id) CASCADE away: an
// edge whose caller no longer exists is not a fact about anything. Inbound
// edges (target_id) SET NULL and demote to resolution='unresolved' via the
// unbind trigger, keeping target_name — because "this file called Lookup" is
// still true after Lookup's file is deleted, and a later re-index can re-bind
// it. Making both directions CASCADE is what silently deleted foreign files'
// call edges on every save (see TestIncremental_InboundEdgesSurviveCalleeReindex).
//
// The FTS index is kept in sync by the symbols AFTER DELETE trigger.
// Used by FileWatcher when a tracked file disappears from disk.
func (ci *CodeIndex) RemoveFile(ctx context.Context, path string) error {
	path = IndexPath(path)
	tx, err := ci.writerDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := deleteFileSymbols(ctx, tx, path); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM file_hashes WHERE file_path=?", path); err != nil {
		return err
	}
	return tx.Commit()
}

func insertNodes(ctx context.Context, tx *sql.Tx, nodes []*Node) (map[string]int64, error) {
	if len(nodes) == 0 {
		return nil, nil
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO symbols
		(file_path, kind, name, qualified, signature, doc, parent, line_start, line_end, byte_start, byte_end, content_hash, exported, visibility, package_path, community_id, summary, summary_hash, effects, runtime_frequency, coverage, cpu_pct)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	qnameToID := make(map[string]int64, len(nodes))
	for _, n := range nodes {
		exported := 0
		if n.Exported {
			exported = 1
		}
		effectsJSON := ""
		if n.Effects != nil {
			if b, err := json.Marshal(n.Effects); err == nil {
				effectsJSON = string(b)
			}
		}
		res, err := stmt.ExecContext(ctx,
			IndexPath(n.FilePath), string(n.Kind), n.Name, n.Qualified, n.Signature, n.Doc, n.Parent,
			n.LineStart, n.LineEnd, n.ByteStart, n.ByteEnd,
			n.ContentHash, exported, n.Visibility, IndexPath(n.PackagePath), n.CommunityID,
			n.Summary, n.SummaryHash, effectsJSON,
			n.RuntimeFrequency, n.Coverage, n.CpuPct,
		)
		if err != nil {
			return nil, fmt.Errorf("insert node %s: %w", n.QualifiedName, err)
		}
		id, _ := res.LastInsertId()
		qnameToID[n.QualifiedName] = id
	}
	return qnameToID, nil
}

func insertEdges(ctx context.Context, tx *sql.Tx, edges []Edge, qnameToID map[string]int64) error {
	if len(edges) == 0 {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO edges
		(source_id, target_id, target_name, kind, resolution, score, source, flow_type, param_idx)
		VALUES (?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, e := range edges {
		sourceID := qnameToID[e.SourceQName]
		if sourceID == 0 {
			continue
		}
		var targetID sql.NullInt64
		if tid, ok := qnameToID[e.TargetQName]; ok && tid != 0 {
			targetID = sql.NullInt64{Int64: tid, Valid: true}
		}
		targetName := e.TargetName
		if targetName == "" {
			targetName = e.TargetQName
		}
		paramIdx := e.ParamIdx
		if paramIdx == 0 && e.FlowType == "" {
			paramIdx = -1
		}
		// A producer's claim of `exact` is about the qname it asked for; whether
		// that qname is in *this* batch is decided here. Passing the claim through
		// unreconciled would trip the schema CHECK and — because the error used to
		// be discarded — drop the edge without a word.
		res, err := flushResolution(e, targetID.Valid)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, sourceID, targetID, targetName,
			string(e.Kind), res, e.Score, e.Source, e.FlowType, paramIdx); err != nil {
			return fmt.Errorf("insert edge %s→%s (%s): %w", e.SourceQName, targetName, e.Kind, err)
		}
	}
	return nil
}

// stmtPreparer is satisfied by both *sql.DB and *sql.Tx, so the hash write has
// one implementation whether or not the batch also carries a symbol delta.
type stmtPreparer interface {
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
}

// recordFileHashes persists content hash + mtime for every file in the batch.
// A failed write is returned, not logged: it leaves the file stale, which looks
// exactly like "never indexed" to the next staleness scan.
func recordFileHashes(ctx context.Context, db stmtPreparer, files []FileToParse) error {
	stmt, err := db.PrepareContext(ctx, `INSERT OR REPLACE INTO file_hashes (file_path, content_hash, mtime_ns) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, f := range files {
		if _, err := stmt.ExecContext(ctx, IndexPath(f.Path), contentHashForPath(f.Path), f.MtimeNs); err != nil {
			return fmt.Errorf("record file hash %s: %w", filepath.Base(f.Path), err)
		}
	}
	return nil
}

func contentHashForPath(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return contentHash(content)
}
