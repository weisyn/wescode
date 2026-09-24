package codeintel

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weisyn/wescode/internal/treesitter"
)

// TestIncremental_InboundEdgesSurviveCalleeReindex is the regression for the
// silent edge loss that shipped for as long as `edges.target_id` carried
// ON DELETE CASCADE.
//
// The shape: applyFileDelta deletes the changed file's symbols before
// re-inserting them, and re-inserts only the edges that file itself authored.
// Under CASCADE the delete also took every edge pointing AT those symbols —
// including edges whose source_id lives in a different, untouched file — and
// nothing rebuilt them. So saving a widely-called file made CallersOf on it
// permanently under-report until the next full pipeline run. No error, no
// threshold, every save.
//
// The fix has two halves and this test needs both, which is why it asserts
// bound count rather than just row count: SET NULL + the unbind trigger keep the
// row alive (target_name intact), and the incremental path re-runs
// ResolveEdgeTargets so the row gets its target_id back. Asserting only "row
// survived" would pass with the resolver removed, and the query layer's
// name-based fallback would hide that from CallersOf too.
func TestIncremental_InboundEdgesSurviveCalleeReindex(t *testing.T) {
	dir := t.TempDir()

	calleeFile := filepath.Join(dir, "callee.go")
	callerFile := filepath.Join(dir, "caller.go")

	if err := os.WriteFile(calleeFile, []byte(`package p

func Lookup(k string) string { return k }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(callerFile, []byte(`package p

func DriveIt() string { return Lookup("x") }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Phase 1: full pipeline, so the cross-file edge starts out bound.
	pool := treesitter.NewParserPoolN(2)
	defer pool.Close()

	dbPath := filepath.Join(t.TempDir(), "code.db")
	pipe := NewPipeline(dir, dbPath, pool)
	pipe.FullRescan = true
	pipe.WorkerCount = -1 // in-process parsing, required inside the test binary
	if _, err := pipe.Run(context.Background()); err != nil {
		t.Fatalf("Pipeline.Run: %v", err)
	}

	ci, err := NewCodeIndex(dbPath, pool)
	if err != nil {
		t.Fatalf("NewCodeIndex: %v", err)
	}
	defer ci.Close()

	ctx := context.Background()

	rows, bound := dumpCallEdges(t, ctx, ci, "after full pipeline")
	if rows == 0 {
		t.Fatal("setup broken: full pipeline produced no call edges")
	}
	if bound == 0 {
		t.Fatal("setup broken: full pipeline left every call edge unresolved " +
			"(target_id NULL); an unbound edge cannot demonstrate the loss")
	}

	before, err := ci.CallersOf(ctx, "Lookup", 10)
	if err != nil {
		t.Fatalf("CallersOf before: %v", err)
	}
	if len(before) == 0 {
		t.Fatal("setup broken: no callers found even before re-index")
	}

	// Phase 2: touch ONLY callee.go, re-index it through the incremental path.
	if err := os.WriteFile(calleeFile, []byte(`package p

func Lookup(k string) string { return k + "!" }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ci.IndexFile(ctx, calleeFile); err != nil {
		t.Fatalf("IndexFile(callee.go): %v", err)
	}

	rowsAfter, boundAfter := dumpCallEdges(t, ctx, ci, "after incremental re-index of callee.go")

	if rowsAfter < rows {
		t.Errorf("call edge rows dropped %d -> %d: the foreign file's edge was deleted, "+
			"not demoted (target_id is back on ON DELETE CASCADE, or the unbind trigger is gone)",
			rows, rowsAfter)
	}
	if boundAfter < bound {
		t.Errorf("bound call edges dropped %d -> %d: the row survived but nothing re-bound it "+
			"(the incremental path is not calling ResolveEdgeTargets)", bound, boundAfter)
	}

	after, err := ci.CallersOf(ctx, "Lookup", 10)
	if err != nil {
		t.Fatalf("CallersOf after: %v", err)
	}
	if len(after) < len(before) {
		t.Errorf("CallersOf(Lookup) dropped %d -> %d after re-indexing only the callee file",
			len(before), len(after))
	}

	assertResolutionPairing(t, ctx, ci.readerDB)
}

// dumpCallEdges returns (rows, bound) for kind='call' and logs each row, so a
// failure above carries the evidence instead of just two numbers.
func dumpCallEdges(t *testing.T, ctx context.Context, ci *CodeIndex, label string) (rows, bound int) {
	t.Helper()
	t.Logf("── %s", label)
	q, err := ci.readerDB.QueryContext(ctx,
		`SELECT e.source_id, COALESCE(e.target_id,-1), e.target_name, e.resolution,
		        COALESCE(src.name,'?'), COALESCE(src.file_path,'?')
		   FROM edges e LEFT JOIN symbols src ON e.source_id = src.id
		  WHERE e.kind='call'`)
	if err != nil {
		t.Fatalf("query edges: %v", err)
	}
	defer q.Close()
	for q.Next() {
		var sid, tid int64
		var tn, sn, sf string
		var res Resolution
		if err := q.Scan(&sid, &tid, &tn, &res, &sn, &sf); err != nil {
			t.Fatal(err)
		}
		t.Logf("   call edge: src=%d(%s @ %s) target_id=%d target_name=%q resolution=%s",
			sid, sn, filepath.Base(sf), tid, tn, res)
		rows++
		if tid > 0 {
			bound++
		}
	}
	if err := q.Err(); err != nil {
		t.Fatalf("iterate edges: %v", err)
	}
	return rows, bound
}

// assertResolutionPairing checks the invariant the schema CHECK exists to
// guarantee: `exact`/`inferred` iff target_id is set. The CHECK only fires on
// write, so a stored row can only violate this if someone rebuilt the table
// without it — which is exactly the regression worth catching.
func assertResolutionPairing(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var bad int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM edges WHERE (`+resolutionBoundSQL+`) != (target_id IS NOT NULL)`,
	).Scan(&bad); err != nil {
		t.Fatalf("pairing invariant query: %v", err)
	}
	if bad != 0 {
		t.Errorf("%d edge(s) violate the resolution/target_id pairing invariant", bad)
	}
}

// TestSchema_SymbolDeleteDemotesInboundEdges pins the mechanism the test above
// depends on, one layer down: deleting a symbol must demote its inbound edges
// (target_id NULL + resolution unresolved, target_name kept) rather than delete
// them or fail the DELETE.
//
// Both failure modes are live risks and they look nothing alike. Restore
// CASCADE and the row vanishes silently. Drop the unbind trigger and the FK's
// SET NULL leaves resolution='exact' with target_id NULL, which the pairing
// CHECK rejects — so every incremental re-index of a called file errors out.
func TestSchema_SymbolDeleteDemotesInboundEdges(t *testing.T) {
	ci, ctx := newSchemaTestIndex(t)

	callerID := insertTestSymbol(t, ci, "/x/caller.go", "function", "DriveIt")
	calleeID := insertTestSymbol(t, ci, "/x/callee.go", "function", "Lookup")

	if _, err := ci.writerDB.ExecContext(ctx,
		`INSERT INTO edges (source_id, target_id, target_name, kind, resolution, source)
		 VALUES (?, ?, 'Lookup', 'call', ?, 'tree-sitter')`,
		callerID, calleeID, ResolutionExact); err != nil {
		t.Fatalf("insert bound edge: %v", err)
	}

	if _, err := ci.writerDB.ExecContext(ctx, `DELETE FROM symbols WHERE id = ?`, calleeID); err != nil {
		t.Fatalf("delete callee symbol: %v", err)
	}

	var (
		targetID sql.NullInt64
		name     string
		res      Resolution
	)
	if err := ci.writerDB.QueryRowContext(ctx,
		`SELECT target_id, target_name, resolution FROM edges WHERE source_id = ?`, callerID,
	).Scan(&targetID, &name, &res); err != nil {
		if err == sql.ErrNoRows {
			t.Fatal("the edge was deleted with its target: target_id is back on ON DELETE CASCADE")
		}
		t.Fatalf("read edge after delete: %v", err)
	}
	if targetID.Valid {
		t.Errorf("target_id survived the delete as %d; it must be NULL", targetID.Int64)
	}
	if res != ResolutionUnresolved {
		t.Errorf("resolution = %q after unbind, want %q", res, ResolutionUnresolved)
	}
	if name != "Lookup" {
		t.Errorf("target_name = %q, want %q: the name is what lets a later resolver re-bind the edge", name, "Lookup")
	}
}

// TestSchema_ResolutionTargetPairingRejectsBothDirections asserts the CHECK is
// actually installed and symmetric. Only one direction is intuitive (a bound
// resolution with no target); the other one is what the old `certainty` column
// let through for 86% of unresolved call edges — a row claiming full confidence
// while pointing nowhere.
func TestSchema_ResolutionTargetPairingRejectsBothDirections(t *testing.T) {
	ci, ctx := newSchemaTestIndex(t)

	srcID := insertTestSymbol(t, ci, "/x/a.go", "function", "A")
	dstID := insertTestSymbol(t, ci, "/x/b.go", "function", "B")

	cases := []struct {
		name       string
		targetID   any
		resolution Resolution
	}{
		{"bound resolution without target", nil, ResolutionExact},
		{"inferred without target", nil, ResolutionInferred},
		{"unresolved with target", dstID, ResolutionUnresolved},
		{"ambiguous with target", dstID, ResolutionAmbiguous},
	}
	for _, tc := range cases {
		_, err := ci.writerDB.ExecContext(ctx,
			`INSERT INTO edges (source_id, target_id, target_name, kind, resolution, source)
			 VALUES (?, ?, 'B', 'call', ?, 'test')`,
			srcID, tc.targetID, tc.resolution)
		if err == nil {
			t.Errorf("%s: insert succeeded; the pairing CHECK is missing", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), "CHECK") {
			t.Errorf("%s: rejected by %v, want a CHECK violation", tc.name, err)
		}
	}
}

// TestSchema_ScoreRestrictedToMeasuredKinds pins the other half of the split:
// `score` exists only for the two kinds that carry a real measurement (cosine
// similarity, Jaccard coefficient). Letting any kind store a score is how the
// old column ended up meaning three different things at once.
func TestSchema_ScoreRestrictedToMeasuredKinds(t *testing.T) {
	ci, ctx := newSchemaTestIndex(t)

	srcID := insertTestSymbol(t, ci, "/x/a.go", "function", "A")
	dstID := insertTestSymbol(t, ci, "/x/b.go", "function", "B")

	score := 0.83
	for _, kind := range scoredEdgeKinds {
		if _, err := ci.writerDB.ExecContext(ctx,
			`INSERT INTO edges (source_id, target_id, target_name, kind, resolution, score, source)
			 VALUES (?, ?, 'B', ?, ?, ?, 'test')`,
			srcID, dstID, kind, ResolutionExact, score); err != nil {
			t.Errorf("%s should accept a score: %v", kind, err)
		}
	}

	_, err := ci.writerDB.ExecContext(ctx,
		`INSERT INTO edges (source_id, target_id, target_name, kind, resolution, score, source)
		 VALUES (?, ?, 'B', 'call', ?, ?, 'test')`,
		srcID, dstID, ResolutionExact, score)
	if err == nil {
		t.Error("a call edge accepted a score; the scored-kind CHECK is missing")
	} else if !strings.Contains(err.Error(), "CHECK") {
		t.Errorf("call+score rejected by %v, want a CHECK violation", err)
	}
}

func newSchemaTestIndex(t *testing.T) (*CodeIndex, context.Context) {
	t.Helper()
	pool := treesitter.NewParserPoolN(1)
	t.Cleanup(pool.Close)
	ci, err := NewCodeIndex(filepath.Join(t.TempDir(), "schema.db"), pool)
	if err != nil {
		t.Fatalf("NewCodeIndex: %v", err)
	}
	t.Cleanup(func() { ci.Close() })
	return ci, context.Background()
}

func insertTestSymbol(t *testing.T, ci *CodeIndex, path, kind, name string) int64 {
	t.Helper()
	res, err := ci.writerDB.Exec(
		`INSERT INTO symbols (file_path, kind, name, line_start, line_end) VALUES (?, ?, ?, 1, 1)`,
		path, kind, name)
	if err != nil {
		t.Fatalf("insert symbol %s: %v", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId %s: %v", name, err)
	}
	return id
}
