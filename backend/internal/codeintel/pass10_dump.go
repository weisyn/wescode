package codeintel

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DumpToProduction performs the atomic staging.db → code.db promotion.
// Sequence: flush buffer → staging.db → PRAGMA quick_check → atomic rename.
// INV-P1-02: staging.db corruption does NOT affect the production DB.
// preRename callbacks (if any) are invoked just before os.Rename — on Windows
// this is used to close reader/writer connections so the rename can succeed
// (Windows does not allow renaming over an open file).
func DumpToProduction(buf *GraphBuffer, productionPath string, preRename ...func()) error {
	dir := filepath.Dir(productionPath)
	stagingPath := filepath.Join(dir, "staging.db")

	// Remove any leftover staging file from a previous failed attempt.
	os.Remove(stagingPath)
	os.Remove(stagingPath + "-wal")
	os.Remove(stagingPath + "-shm")

	stagingDB, err := sql.Open("sqlite", stagingPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_pragma=mmap_size(0)")
	if err != nil {
		return fmt.Errorf("open staging db: %w", err)
	}
	defer stagingDB.Close()

	if err := createStagingSchema(stagingDB); err != nil {
		return fmt.Errorf("create staging schema: %w", err)
	}

	if err := buf.FlushToStaging(stagingDB); err != nil {
		return fmt.Errorf("flush to staging: %w", err)
	}

	// Force WAL checkpoint before integrity check.
	if _, err := stagingDB.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		slog.Warn("staging wal_checkpoint failed", "err", err)
	}

	// INV-RESIL-05: Verify staging integrity before promoting.
	var checkResult string
	if err := stagingDB.QueryRow("PRAGMA quick_check").Scan(&checkResult); err != nil || checkResult != "ok" {
		slog.Error("staging.db integrity check failed", "result", checkResult, "err", err)
		return fmt.Errorf("staging.db integrity check failed: %s", checkResult)
	}

	stagingDB.Close()

	// Close existing connections before rename (required on Windows where
	// os.Rename cannot overwrite a file held open by another process).
	for _, fn := range preRename {
		fn()
	}

	// Atomic rename: on the same filesystem this is guaranteed atomic.
	// Remove old production DB files first.
	os.Remove(productionPath + "-wal")
	os.Remove(productionPath + "-shm")

	if err := os.Rename(stagingPath, productionPath); err != nil {
		return fmt.Errorf("atomic rename staging→production: %w", err)
	}

	slog.Info("CKG dump complete",
		"nodes", buf.NodeCount(),
		"edges", buf.EdgeCount(),
		"db", productionPath,
	)
	return nil
}
