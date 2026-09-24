package verification

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// BaselineStore persists test baseline data in wescode-app.db.
// Each workspace (identified by work_dir_hash) maintains a FIFO of
// baseline runs. When L2 tests pass, the per-test outcomes are saved;
// when tests fail, the store enables regression detection by comparing
// against the latest passing baseline.
type BaselineStore struct {
	db *sql.DB
}

// BaselineRun is a summary of a captured baseline.
type BaselineRun struct {
	ID         int64
	WorkDir    string
	Packages   []string
	GitCommit  string
	Scope      string // "full" | "selective" — whether test selector narrowed the run
	TotalPass  int
	TotalSkip  int
	DurationMs int64
	CapturedAt time.Time
}

// BaselineResult is a single test outcome stored in a baseline run.
type BaselineResult struct {
	Package    string `json:"package"`
	TestName   string `json:"test_name"`
	Status     string `json:"status"` // "pass" | "skip"
	DurationMs int64  `json:"duration_ms,omitempty"`
	StdoutHash string `json:"stdout_hash,omitempty"`
	StderrHash string `json:"stderr_hash,omitempty"`
	SourceHash string `json:"source_hash,omitempty"`
}

// NewBaselineStore opens (and migrates) the baseline tables in the given DB.
func NewBaselineStore(db *sql.DB) (*BaselineStore, error) {
	bs := &BaselineStore{db: db}
	if err := bs.migrate(); err != nil {
		return nil, fmt.Errorf("baseline migrate: %w", err)
	}
	return bs, nil
}

func (bs *BaselineStore) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS wc_baseline_runs (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			work_dir_hash TEXT NOT NULL,
			work_dir      TEXT NOT NULL,
			packages      TEXT NOT NULL,
			git_commit    TEXT NOT NULL DEFAULT '',
			scope         TEXT NOT NULL DEFAULT 'full',
			total_pass    INTEGER NOT NULL,
			total_skip    INTEGER NOT NULL DEFAULT 0,
			duration_ms   INTEGER NOT NULL,
			captured_at   TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_baseline_runs_workdir ON wc_baseline_runs(work_dir_hash)`,

		`CREATE TABLE IF NOT EXISTS wc_baseline_results (
			run_id    INTEGER NOT NULL REFERENCES wc_baseline_runs(id) ON DELETE CASCADE,
			package   TEXT NOT NULL,
			test_name TEXT NOT NULL,
			status    TEXT NOT NULL DEFAULT 'pass',
			duration_ms INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (run_id, package, test_name)
		)`,

		`CREATE TABLE IF NOT EXISTS wc_baseline_flaky (
			work_dir_hash TEXT NOT NULL,
			package       TEXT NOT NULL,
			test_name     TEXT NOT NULL,
			flip_count    INTEGER NOT NULL DEFAULT 0,
			last_flip_at  TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (work_dir_hash, package, test_name)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := bs.db.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt[:min(len(stmt), 60)], err)
		}
	}
	// Migration: add scope column to existing tables (idempotent).
	bs.db.Exec(`ALTER TABLE wc_baseline_runs ADD COLUMN scope TEXT NOT NULL DEFAULT 'full'`)

	// Migration: add L2.5 output hash columns (idempotent).
	bs.db.Exec(`ALTER TABLE wc_baseline_results ADD COLUMN stdout_hash TEXT NOT NULL DEFAULT ''`)
	bs.db.Exec(`ALTER TABLE wc_baseline_results ADD COLUMN stderr_hash TEXT NOT NULL DEFAULT ''`)
	bs.db.Exec(`ALTER TABLE wc_baseline_results ADD COLUMN source_hash TEXT NOT NULL DEFAULT ''`)
	return nil
}

// WorkDirHash computes a stable hash for a workspace path.
func WorkDirHash(workDir string) string {
	h := sha256.Sum256([]byte(workDir))
	return hex.EncodeToString(h[:8])
}

// SaveRun persists a passing test run as a new baseline.
// scope is "full" (all package tests) or "selective" (test selector narrowed).
func (bs *BaselineStore) SaveRun(ctx context.Context, workDirHash, workDir string, packages []string, gitCommit, scope string, outcomes []TestOutcome) (int64, error) {
	pkgJSON, _ := json.Marshal(packages)
	if scope == "" {
		scope = "full"
	}

	totalPass := 0
	totalSkip := 0
	var durationMs int64
	for _, o := range outcomes {
		switch o.Status {
		case "pass":
			totalPass++
			durationMs += o.Duration.Milliseconds()
		case "skip":
			totalSkip++
		}
	}

	tx, err := bs.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO wc_baseline_runs (work_dir_hash, work_dir, packages, git_commit, scope, total_pass, total_skip, duration_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		workDirHash, workDir, string(pkgJSON), gitCommit, scope, totalPass, totalSkip, durationMs)
	if err != nil {
		return 0, err
	}
	runID, _ := res.LastInsertId()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO wc_baseline_results (run_id, package, test_name, status, duration_ms, stdout_hash, stderr_hash, source_hash)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	for _, o := range outcomes {
		if o.Status == "pass" || o.Status == "skip" {
			_, _ = stmt.ExecContext(ctx, runID, o.Package, o.TestName, o.Status,
				o.Duration.Milliseconds(), o.OutputHash, "", "")
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	// FIFO cleanup: keep only the most recent N runs per workspace.
	bs.Cleanup(ctx, workDirHash, 10)
	return runID, nil
}

// LatestRun returns the most recent baseline run for a workspace.
func (bs *BaselineStore) LatestRun(ctx context.Context, workDirHash string) (*BaselineRun, error) {
	row := bs.db.QueryRowContext(ctx,
		`SELECT id, work_dir, packages, git_commit, scope, total_pass, total_skip, duration_ms, captured_at
		 FROM wc_baseline_runs WHERE work_dir_hash = ? ORDER BY id DESC LIMIT 1`, workDirHash)

	var r BaselineRun
	var pkgJSON string
	var capturedAt string
	if err := row.Scan(&r.ID, &r.WorkDir, &pkgJSON, &r.GitCommit, &r.Scope, &r.TotalPass, &r.TotalSkip, &r.DurationMs, &capturedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	json.Unmarshal([]byte(pkgJSON), &r.Packages)
	r.CapturedAt, _ = time.Parse("2006-01-02 15:04:05", capturedAt)
	return &r, nil
}

// LoadResults returns all test results for a baseline run.
func (bs *BaselineStore) LoadResults(ctx context.Context, runID int64) ([]BaselineResult, error) {
	rows, err := bs.db.QueryContext(ctx,
		`SELECT package, test_name, status, duration_ms, stdout_hash, stderr_hash, source_hash
		 FROM wc_baseline_results WHERE run_id = ?`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []BaselineResult
	for rows.Next() {
		var r BaselineResult
		if err := rows.Scan(&r.Package, &r.TestName, &r.Status, &r.DurationMs,
			&r.StdoutHash, &r.StderrHash, &r.SourceHash); err != nil {
			continue
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// Cleanup removes old baseline runs, keeping only the most recent keepN.
func (bs *BaselineStore) Cleanup(ctx context.Context, workDirHash string, keepN int) {
	bs.db.ExecContext(ctx,
		`DELETE FROM wc_baseline_runs WHERE work_dir_hash = ? AND id NOT IN (
			SELECT id FROM wc_baseline_runs WHERE work_dir_hash = ? ORDER BY id DESC LIMIT ?
		)`, workDirHash, workDirHash, keepN)
}

// RecordFlip increments the flip counter for a test that changed status.
func (bs *BaselineStore) RecordFlip(ctx context.Context, workDirHash, pkg, testName string) error {
	_, err := bs.db.ExecContext(ctx,
		`INSERT INTO wc_baseline_flaky (work_dir_hash, package, test_name, flip_count, last_flip_at)
		 VALUES (?, ?, ?, 1, datetime('now'))
		 ON CONFLICT(work_dir_hash, package, test_name)
		 DO UPDATE SET flip_count = flip_count + 1, last_flip_at = datetime('now')`,
		workDirHash, pkg, testName)
	return err
}

// IsFlaky returns true if a test has flipped status >= 3 times.
func (bs *BaselineStore) IsFlaky(ctx context.Context, workDirHash, pkg, testName string) bool {
	var count int
	bs.db.QueryRowContext(ctx,
		`SELECT flip_count FROM wc_baseline_flaky WHERE work_dir_hash = ? AND package = ? AND test_name = ?`,
		workDirHash, pkg, testName).Scan(&count)
	return count >= 3
}

// ── Readiness ───────────────────────────────────────────────────────────────

// BaselineReadiness reports how ready the behavioral baseline is for
// regression detection.
type BaselineReadiness struct {
	Source        string  `json:"source"` // "baseline"
	HasBaseline   bool    `json:"has_baseline"`
	BaselineTests int     `json:"baseline_tests"` // tests in latest passing run
	Completeness  float64 `json:"completeness"`   // 0 = no baseline, 1 = has baseline
}

// Readiness returns the baseline store's readiness for a given workspace.
func (bs *BaselineStore) Readiness(ctx context.Context, workDirHash string) BaselineReadiness {
	latest, err := bs.LatestRun(ctx, workDirHash)
	if err != nil || latest == nil {
		return BaselineReadiness{Source: "baseline"}
	}
	return BaselineReadiness{
		Source:        "baseline",
		HasBaseline:   true,
		BaselineTests: latest.TotalPass + latest.TotalSkip,
		Completeness:  1.0,
	}
}
