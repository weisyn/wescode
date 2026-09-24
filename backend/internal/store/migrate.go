package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
)

// MigrateFunc 执行一个版本步骤的 schema 迁移。
// 接收的 tx 已在事务中；函数只做 DDL / DML，不提交不回滚。
type MigrateFunc func(ctx context.Context, tx *sql.Tx) error

// MigrationStep 描述一个版本步骤。
type MigrationStep struct {
	// Version 是此步骤执行后 PRAGMA user_version 应该达到的值（从 1 开始）。
	Version int
	// Up 执行正向迁移。
	Up MigrateFunc
}

// RunMigrations 读取 db 的 PRAGMA user_version，按序执行未应用的 steps，
// 每步在独立事务中运行并更新 user_version。
//
// steps 必须按 Version 升序排列且从 1 开始连续。
// 迁移成功后 PRAGMA user_version == len(steps)。
//
// 调用方负责迁移失败后的降级策略（归档+重建 / 报错跳过）。
func RunMigrations(ctx context.Context, db *sql.DB, dbName string, steps []MigrationStep) error {
	current, err := readUserVersion(db)
	if err != nil {
		return fmt.Errorf("migrate %s: read user_version: %w", dbName, err)
	}

	target := len(steps)
	if current >= target {
		return nil
	}

	slog.Info("[migrate] starting", "db", dbName, "from", current, "to", target)

	for i := current; i < target; i++ {
		step := steps[i]
		if step.Version != i+1 {
			return fmt.Errorf("migrate %s: step[%d].Version = %d, want %d", dbName, i, step.Version, i+1)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("migrate %s v%d: begin tx: %w", dbName, step.Version, err)
		}

		if err := step.Up(ctx, tx); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrate %s v%d: %w", dbName, step.Version, err)
		}

		// PRAGMA user_version 不支持参数化，需拼字面量；值来自代码常量，安全。
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", step.Version)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrate %s v%d: set user_version: %w", dbName, step.Version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migrate %s v%d: commit: %w", dbName, step.Version, err)
		}

		slog.Info("[migrate] applied", "db", dbName, "version", step.Version)
	}

	return nil
}

func readUserVersion(db *sql.DB) (int, error) {
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}
