package store

import (
	"context"
	"database/sql"
)

// AuthDBVersion 是 wescode_auth.db 的当前 schema 版本。
const AuthDBVersion = 1

// AuthDBMigrations 返回 wescode_auth.db 的迁移步骤序列。
//
// V1：从 DEV-1 收编。auth 包自带的 Migrate() 使用 CREATE IF NOT EXISTS，
// 这里只追踪版本号使其可感知；实际表由 auth.Service.Migrate() 创建。
func AuthDBMigrations() []MigrationStep {
	return []MigrationStep{
		{Version: 1, Up: authDBV1},
	}
}

// authDBV1：ws_session 表基线。
// auth.Service.Migrate() 也会执行 CREATE IF NOT EXISTS，两者幂等。
func authDBV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS ws_session (
		uid           TEXT PRIMARY KEY DEFAULT 'singleton',
		user_id       TEXT NOT NULL,
		email         TEXT NOT NULL DEFAULT '',
		display_name  TEXT NOT NULL DEFAULT '',
		access_token  TEXT NOT NULL,
		refresh_token TEXT NOT NULL,
		expires_at    TEXT NOT NULL,
		updated_at    TEXT NOT NULL DEFAULT ''
	)`)
	return err
}
