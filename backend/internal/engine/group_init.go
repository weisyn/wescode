package engine

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/weisyn/wesapp/group"
	"github.com/weisyn/wescode/internal/store"
	wesgine "github.com/weisyn/wesgine"
)

// openAppDB opens the per-workspace application SQLite database.
//
// v1.0 P1 follow-up: wescode's workspace Cell owns cell.db exclusively
// (wesgine decision B: 1 DB = 1 Cell, no cell_id column inside).  Application
// tables — groups, baselines, metrics, user agents — get their own file next
// to the cell.db so that:
//
//   - Cell.db schema stays clean and portable across Storage backends
//   - Uninstalling an application feature does not touch engine data
//   - Backup / migrate operates on a self-contained sqlite file
//
// Opening is split out from initGroupService because the two callers need it
// at different times: the agent mirror must be readable before RPCs are
// served (Initialize), while the group service can finish assembling in
// postInitialize. One file, one handle, opened once at the earlier point.
// appDBPragmas is the DSN suffix every opener of the application DB must use.
// busy_timeout in particular is load-bearing under concurrent writes: without
// it SQLite returns SQLITE_BUSY immediately instead of retrying, and callers
// that discard the write error then observe a missing row rather than a failure.
// Tests open the same file, so they must open it the same way — a fixture
// without these pragmas passes or fails on lock-acquisition timing, which
// differs per platform.
const appDBPragmas = "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"

func openAppDB(
	ctx context.Context,
	dataDir, cellID string,
	logger *slog.Logger,
) (*sql.DB, error) {
	if dataDir == "" || cellID == "" {
		return nil, fmt.Errorf("openAppDB: dataDir and cellID required")
	}
	cellRoot := filepath.Join(dataDir, "cells", cellID)
	if err := os.MkdirAll(cellRoot, 0o755); err != nil {
		return nil, fmt.Errorf("openAppDB: mkdir cell root: %w", err)
	}
	appDBPath := filepath.Join(cellRoot, "wescode-app.db")

	if _, statErr := os.Stat(appDBPath); os.IsNotExist(statErr) {
		entries, _ := os.ReadDir(filepath.Join(dataDir, "cells"))
		for _, e := range entries {
			if e.IsDir() && len(e.Name()) > len(cellID) && e.Name()[:len(cellID)] == cellID && e.Name()[len(cellID)] == '.' {
				oldDB := filepath.Join(dataDir, "cells", e.Name(), "wescode-app.db")
				if _, err := os.Stat(oldDB); err == nil {
					_ = copyFile(oldDB, appDBPath)
					logger.Info("[engine] recovered wescode-app.db from renamed cell dir",
						"source", e.Name(), "target", cellID)
					break
				}
			}
		}
	}
	db, err := sql.Open("sqlite", appDBPath+appDBPragmas)
	if err != nil {
		return nil, fmt.Errorf("openAppDB: open sqlite: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("openAppDB: ping sqlite: %w", err)
	}

	// Run PRAGMA user_version migration chain. On failure, archive the
	// corrupted file and let the caller create a fresh DB — matching
	// wesgine INV-RESIL-01 (boot always succeeds, degradation is legal).
	if err := store.RunMigrations(ctx, db, "wescode-app.db", store.AppDBMigrations()); err != nil {
		logger.Warn("[engine] wescode-app.db migration failed, archiving and recreating",
			"error", err, "path", appDBPath)
		_ = db.Close()
		archivePath := appDBPath + ".migration-failed"
		_ = os.Rename(appDBPath, archivePath)
		_ = os.Remove(appDBPath + "-wal")
		_ = os.Remove(appDBPath + "-shm")
		db2, err2 := sql.Open("sqlite", appDBPath+appDBPragmas)
		if err2 != nil {
			return nil, fmt.Errorf("openAppDB: reopen after archive: %w", err2)
		}
		if err2 = db2.PingContext(ctx); err2 != nil {
			_ = db2.Close()
			return nil, fmt.Errorf("openAppDB: ping after archive: %w", err2)
		}
		if err2 = store.RunMigrations(ctx, db2, "wescode-app.db", store.AppDBMigrations()); err2 != nil {
			_ = db2.Close()
			return nil, fmt.Errorf("openAppDB: migration on fresh db: %w", err2)
		}
		db = db2
	}

	return db, nil
}

// initGroupService migrates the group tables in the already-open application
// database and returns a fully-wired *group.Service.
//
// The Cell object is threaded in for the SessionCleaner hook so that
// group-delete propagates to the underlying wesgine sessions / memory.
func initGroupService(
	ctx context.Context,
	db *sql.DB,
	cell *wesgine.Cell,
	logger *slog.Logger,
) (*group.Service, error) {
	if db == nil {
		return nil, fmt.Errorf("initGroupService: nil app db")
	}
	// Group table prefix "wc_group_" keeps them namespaced within
	// wescode-app.db so other application tables can coexist.
	store := group.NewSQLiteStore(db, "wc_group_")
	if err := store.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("initGroupService: migrate: %w", err)
	}

	cleaner := &groupSessionCleaner{cell: cell}
	return group.NewService(store, cleaner, logger), nil
}

// groupMemberIDs resolves a group's member agent ids for RunParams.Scope.
//
// The member list is read server-side rather than accepted from the client so
// a stale frontend cannot run a group against membership it misremembers —
// the Run's participants and the group the user sees in the picker are the
// same row. Failures are returned, never swallowed into an empty Scope: an
// empty Scope means "all registered agents" to the engine's router, so a
// silent lookup failure would turn one group into the whole roster.
func (s *Service) groupMemberIDs(ctx context.Context, groupID string) ([]string, error) {
	svc := s.GroupService()
	if svc == nil {
		return nil, fmt.Errorf("group %q: group service not available", groupID)
	}
	g, err := svc.Get(ctx, s.CellID(), "local", groupID)
	if err != nil {
		return nil, fmt.Errorf("group %q: %w", groupID, err)
	}
	if g == nil || len(g.AgentIDs) == 0 {
		return nil, fmt.Errorf("group %q has no members", groupID)
	}
	return append([]string(nil), g.AgentIDs...), nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
