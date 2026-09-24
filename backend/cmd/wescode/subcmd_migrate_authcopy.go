package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// copyAuthTable transplants the {@code ws_session} table from a legacy
// {@code wescode.db} to a freshly-created {@code wescode_auth.db}.  Preserves
// user sessions across the v0.2→v1.0 boundary so operators don't have to
// force a global logout during the migration.
//
// The auth service (wesclaw/auth) applies its own schema migration on
// boot, so we only need to materialise the raw ws_session rows here —
// column additions on the auth side will backfill safely.
func copyAuthTable(legacyPath, newPath string) error {
	ctx := context.Background()
	src, err := sql.Open("sqlite",
		fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&mode=ro", filepath.ToSlash(legacyPath)))
	if err != nil {
		return fmt.Errorf("open legacy: %w", err)
	}
	defer src.Close()

	// Verify the source has the auth table before we bother creating the dest.
	var tableName string
	if err := src.QueryRowContext(ctx,
		"SELECT name FROM sqlite_master WHERE type='table' AND name='ws_session'").Scan(&tableName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// No auth rows to preserve; treat as success (dest stays empty).
			return nil
		}
		return fmt.Errorf("probe ws_session: %w", err)
	}

	dst, err := sql.Open("sqlite",
		fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", filepath.ToSlash(newPath)))
	if err != nil {
		return fmt.Errorf("open new auth db: %w", err)
	}
	defer dst.Close()

	// Read all rows via SELECT * (schema drift-tolerant).  wesclaw/auth's
	// Migrate() will add any missing columns on the first boot after this.
	cols, err := discoverColumns(ctx, src, "ws_session")
	if err != nil {
		return err
	}
	if len(cols) == 0 {
		return nil // empty table, nothing to copy
	}

	// Create dest table with the same column set (all TEXT — auth svc
	// will re-migrate to canonical types on boot).
	createSQL := "CREATE TABLE IF NOT EXISTS ws_session ("
	for i, c := range cols {
		if i > 0 {
			createSQL += ", "
		}
		createSQL += c + " TEXT"
	}
	createSQL += ")"
	if _, err := dst.ExecContext(ctx, createSQL); err != nil {
		return fmt.Errorf("create dst ws_session: %w", err)
	}

	// Bulk copy.  For an auth session table this is at most a few dozen
	// rows so a single-tx INSERT is fine.
	selectSQL := "SELECT "
	for i, c := range cols {
		if i > 0 {
			selectSQL += ", "
		}
		selectSQL += c
	}
	selectSQL += " FROM ws_session"
	rows, err := src.QueryContext(ctx, selectSQL)
	if err != nil {
		return fmt.Errorf("select ws_session: %w", err)
	}
	defer rows.Close()

	placeholders := ""
	for i := range cols {
		if i > 0 {
			placeholders += ", "
		}
		placeholders += "?"
	}
	insertSQL := fmt.Sprintf("INSERT INTO ws_session VALUES (%s)", placeholders)

	tx, err := dst.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin dst tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, insertSQL)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return fmt.Errorf("scan row: %w", err)
		}
		if _, err := stmt.ExecContext(ctx, vals...); err != nil {
			return fmt.Errorf("insert row: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iter ws_session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func discoverColumns(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, fmt.Errorf("pragma table_info: %w", err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     string
			notnull   int
			dfltValue any
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return nil, fmt.Errorf("scan table_info: %w", err)
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}
