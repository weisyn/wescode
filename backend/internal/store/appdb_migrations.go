package store

import (
	"context"
	"database/sql"
)

// AppDBVersion 是 wescode-app.db 的当前 schema 版本。
// 每次变更 schema 时：Version +1，在 AppDBMigrations 末尾追加一步。
const AppDBVersion = 1

// AppDBMigrations 返回 wescode-app.db 的迁移步骤序列。
//
// V1：从 DEV-1 "CREATE IF NOT EXISTS" 收编所有现有表。
// 已存在的库（user_version=0 + 表已存在）和全新的库都走同一条 DDL。
func AppDBMigrations() []MigrationStep {
	return []MigrationStep{
		{Version: 1, Up: appDBV1},
	}
}

// appDBV1 收编所有 wescode-app.db 表为 V1 基线。
// 对于已存在的表，CREATE IF NOT EXISTS 是 no-op；
// 对于新库，创建全部表和索引。
func appDBV1(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		// ── wc_agents ──
		`CREATE TABLE IF NOT EXISTS wc_agents (
			id         TEXT PRIMARY KEY,
			config     TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,

		// ── wc_group_* (4 表) ──
		`CREATE TABLE IF NOT EXISTS wc_groups (
			id      TEXT PRIMARY KEY,
			cell_id TEXT NOT NULL,
			actor   TEXT NOT NULL DEFAULT '',
			title   TEXT NOT NULL DEFAULT '群组',
			emoji   TEXT NOT NULL DEFAULT '◈'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_wc_groups_cell_actor ON wc_groups(cell_id, actor)`,

		`CREATE TABLE IF NOT EXISTS wc_group_members (
			cell_id    TEXT NOT NULL,
			group_id   TEXT NOT NULL,
			actor      TEXT NOT NULL DEFAULT '',
			agent_id   TEXT NOT NULL,
			sort_order INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (cell_id, group_id, agent_id)
		)`,

		`CREATE TABLE IF NOT EXISTS wc_group_conversations (
			id           TEXT PRIMARY KEY,
			cell_id      TEXT NOT NULL,
			actor        TEXT NOT NULL DEFAULT '',
			group_id     TEXT NOT NULL,
			title        TEXT NOT NULL DEFAULT '新话题',
			last_message TEXT NOT NULL DEFAULT '',
			last_time    TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_wc_group_conv_cell_group ON wc_group_conversations(cell_id, group_id, last_time DESC)`,

		`CREATE TABLE IF NOT EXISTS wc_group_messages (
			id                 TEXT PRIMARY KEY,
			cell_id            TEXT NOT NULL,
			actor              TEXT NOT NULL DEFAULT '',
			conv_id            TEXT NOT NULL,
			role               TEXT NOT NULL,
			content            TEXT NOT NULL DEFAULT '',
			content_parts_json TEXT NOT NULL DEFAULT '[]',
			timestamp          TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_wc_group_msg_conv ON wc_group_messages(conv_id, timestamp)`,

		// ── wc_metrics ──
		`CREATE TABLE IF NOT EXISTS wc_metrics (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			kind       TEXT NOT NULL,
			snapshot   TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_wc_metrics_kind_created ON wc_metrics(kind, created_at)`,

		// ── wc_baseline_* (3 表) ──
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
			run_id      INTEGER NOT NULL REFERENCES wc_baseline_runs(id) ON DELETE CASCADE,
			package     TEXT NOT NULL,
			test_name   TEXT NOT NULL,
			status      TEXT NOT NULL DEFAULT 'pass',
			duration_ms INTEGER NOT NULL DEFAULT 0,
			stdout_hash TEXT NOT NULL DEFAULT '',
			stderr_hash TEXT NOT NULL DEFAULT '',
			source_hash TEXT NOT NULL DEFAULT '',
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
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
