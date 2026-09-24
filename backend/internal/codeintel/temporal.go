package codeintel

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// NodeSnapshot captures a point-in-time state of a symbol for drift detection.
type NodeSnapshot struct {
	SymbolName  string    `json:"symbol_name"`
	FilePath    string    `json:"file_path"`
	Complexity  int       `json:"complexity"`
	CallerCount int       `json:"caller_count"`
	CalleeCount int       `json:"callee_count"`
	LineCount   int       `json:"line_count"`
	Effects     string    `json:"effects"`
	SnapshotAt  time.Time `json:"snapshot_at"`
}

const maxSnapshotsPerSymbol = 30 // INV-P6-02

// RecordSnapshot saves the current state of all function/method nodes.
// INV-P6-02: ≤ 30 snapshots per symbol (FIFO).
func RecordSnapshot(buf *GraphBuffer, db *sql.DB) error {
	buf.mu.RLock()

	// Compute caller/callee counts from edges.
	callerCount := make(map[string]int)
	calleeCount := make(map[string]int)
	for _, e := range buf.Edges {
		if e.Kind == EdgeCall {
			callerCount[e.TargetQName]++
			calleeCount[e.SourceQName]++
		}
	}

	type snapshot struct {
		name      string
		filePath  string
		lineCount int
		callers   int
		callees   int
		effects   string
	}

	var snapshots []snapshot
	for _, n := range buf.Nodes {
		if n.Kind != NodeFunction && n.Kind != NodeMethod {
			continue
		}
		eff := ""
		if n.Effects != nil {
			eff = n.Effects.String()
		}
		snapshots = append(snapshots, snapshot{
			name:      n.QualifiedName,
			filePath:  n.FilePath,
			lineCount: n.LineEnd - n.LineStart + 1,
			callers:   callerCount[n.QualifiedName],
			callees:   calleeCount[n.QualifiedName],
			effects:   eff,
		})
	}
	buf.mu.RUnlock()

	if len(snapshots) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin snapshot tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)

	stmt, err := tx.Prepare(`INSERT INTO node_history (symbol_name, file_path, snapshot_at, complexity, caller_count, callee_count, line_count, effects)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare snapshot insert: %w", err)
	}
	defer stmt.Close()

	for _, s := range snapshots {
		_, err := stmt.Exec(s.name, IndexPath(s.filePath), now, 0, s.callers, s.callees, s.lineCount, s.effects)
		if err != nil {
			slog.Warn("snapshot insert failed", "symbol", s.name, "err", err)
			continue
		}
	}

	// INV-P6-02: Enforce ≤ 30 snapshots per symbol (FIFO).
	_, err = tx.Exec(`DELETE FROM node_history WHERE id IN (
		SELECT id FROM node_history AS nh
		WHERE (SELECT count(*) FROM node_history AS nh2 WHERE nh2.symbol_name = nh.symbol_name AND nh2.snapshot_at > nh.snapshot_at) >= ?
	)`, maxSnapshotsPerSymbol)
	if err != nil {
		slog.Warn("snapshot FIFO cleanup failed", "err", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit snapshot tx: %w", err)
	}

	slog.Info("pass6: temporal snapshots recorded", "symbols", len(snapshots))
	return nil
}

// EnsureNodeHistorySchema creates the node_history table if it doesn't exist.
func EnsureNodeHistorySchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS node_history (
		id           INTEGER PRIMARY KEY,
		symbol_name  TEXT NOT NULL,
		file_path    TEXT NOT NULL,
		snapshot_at  TEXT NOT NULL,
		complexity   INTEGER NOT NULL DEFAULT 0,
		caller_count INTEGER NOT NULL DEFAULT 0,
		callee_count INTEGER NOT NULL DEFAULT 0,
		line_count   INTEGER NOT NULL DEFAULT 0,
		effects      TEXT NOT NULL DEFAULT ''
	)`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_history_sym ON node_history(symbol_name, snapshot_at)`)
	return err
}
