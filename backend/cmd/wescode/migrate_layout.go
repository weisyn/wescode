package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/weisyn/wescode/internal/engine"
	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Windows legacy-layout migration (D-8 后遗, A-8).
//
// 背景：D-8 版本的打包产物（main.js 缺 win32 分支）在 Windows 上把引擎数据
// 写到了 Linux XDG 风格路径：
//
//	~/.local/share/wescode/           ← 真数据（hypervisor.db + cells/ + 日志）
//	~/.config/wescode/config.yaml     ← 若用户保存过 LLM 配置（ConfigDir 双胞胎）
//
// 而 Go xdg 的 Windows 默认是：
//
//	%LOCALAPPDATA%\wescode\           ← canonical（hypervisor.db / cells / db / logs）
//	%APPDATA%\wescode\config.yaml     ← canonical config
//
// 修复后首次启动，若 canonical 尚无 hypervisor.db 而 leftover 有，说明 leftover
// 是旧包写出的真数据 → 一次性迁入 canonical。判定锚点是 hypervisor.db（Cell
// 注册表），不是「目录为空」也不是 auth.db 的 mtime。
// ---------------------------------------------------------------------------

// legacyDataDirXDG returns the Linux-XDG-style data dir that D-8 packaged
// builds wrote on Windows (and that a Linux→macOS/Windows migration could
// also leave behind): ~/.local/share/wescode.
func legacyDataDirXDG() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "wescode")
}

// legacyConfigDirXDG returns the Linux-XDG-style config dir that D-8
// packaged builds wrote on Windows (ConfigDir 双胞胎):
// ~/.config/wescode. Checked on every OS — the paths are Linux-XDG
// semantics, not Windows-specific.
func legacyConfigDirXDG() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "wescode")
}

// runLegacyLayoutMigration performs a one-shot boot-time migration of Windows
// legacy data written by D-8 builds. It runs BEFORE initAuth / StartEngine so
// no SQLite lock exists on either tree.
//
// 触发条件（全部满足才迁移）：
//  1. 用户未显式设置 WESCODE_DATA_DIR / WESCODE_CONFIG / WESCODE_CONFIG_DIR
//     （显式设置 = 有意覆盖，绝不迁移）；
//  2. leftover（~/.local/share/wescode）存在 hypervisor.db；
//  3. canonical（xdg DataDir）不存在 hypervisor.db；
//  4. leftover != canonical（非 Windows 或 xdg 本就指向该目录时跳过）。
//
// 两处都有 hypervisor.db（异常但可能）→ 只打 ERROR，不覆盖，原样保留。
func runLegacyLayoutMigration() {
	if strings.TrimSpace(os.Getenv("WESCODE_DATA_DIR")) != "" {
		return // 用户显式覆盖 data dir，不做任何迁移
	}

	canonical := engine.DefaultDataDir()
	leftover := legacyDataDirXDG()

	doMigrate, reason := shouldMigrateLayout(leftover, canonical)
	if !doMigrate {
		if reason != "" {
			if reason == "legacy tree has no hypervisor.db" && legacyDirNonEmpty(leftover) {
				// Leftover exists but lost its anchor — likely an interrupted
				// migration (hypervisor.db already moved). Do NOT auto-migrate;
				// surface it so an operator can recover the remaining files.
				slog.Warn("[migrate] legacy data dir exists without hypervisor.db — possible incomplete migration; files are NOT auto-migrated",
					"legacy", leftover, "canonical", canonical)
			} else {
				slog.Debug("[migrate] Windows legacy layout migration skipped", "reason", reason,
					"legacy", leftover, "canonical", canonical)
			}
		}
		return
	}

	// shouldMigrateLayout above is an unlocked fast path so the common
	// no-migration boot never touches a lock file. migrateDataDir re-decides
	// under the lock; that verdict is the authoritative one.
	migrated, err := migrateDataDir(leftover, canonical)
	if err != nil {
		slog.Error("[migrate] Windows legacy data dir migration failed",
			"from", leftover, "to", canonical, "err", err)
		return
	}
	if !migrated {
		// A concurrent process won. The marker rename and config-twin
		// migration below belong to the winner: renaming leftover here could
		// land mid-flight in the winner's item loop, and migrateConfigFile
		// would archive the config the winner just placed.
		return
	}
	slog.Info("[migrate] Windows legacy data dir migrated",
		"from", leftover, "to", canonical)

	// Completion marker: rename the emptied legacy tree to
	// leftover.migrated-<ts> so future boots see no leftover at all
	// (idempotent skip) and forensic evidence is preserved. Rename is
	// atomic and non-fatal on failure (e.g. dir still open on Windows).
	marker := leftover + ".migrated-" + time.Now().UTC().Format("20060102-150405")
	if err := os.Rename(leftover, marker); err != nil {
		slog.Warn("[migrate] cannot rename completed legacy dir (non-fatal)",
			"from", leftover, "to", marker, "err", err)
	} else {
		slog.Info("[migrate] legacy dir archived", "archive", marker)
	}

	// ConfigDir 双胞胎：~/.config/wescode/config.yaml → %APPDATA%\wescode\
	// （只在 DataDir 迁移成功后才做；config 与 data 是两棵树）
	if err := migrateConfigFile(); err != nil {
		slog.Error("[migrate] Windows legacy config file migration failed", "err", err)
	}
}

// legacyDirNonEmpty reports whether dir exists and contains at least one
// entry. Used to distinguish "no legacy data at all" from "leftover exists
// but lost its anchor — possible interrupted migration".
func legacyDirNonEmpty(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	return len(entries) > 0
}

// shouldMigrateLayout decides whether a one-shot legacy-data migration is
// warranted. Anchor = hypervisor.db (the Cell registry), NOT "directory is
// empty" and NOT auth.db mtime: a legacy tree whose engine was alive carries
// hypervisor.db; a crashed make-run only ever produced db/wescode_auth.db.
//
//	leftover == canonical                → same tree (Linux) → no-op
//	no hypervisor.db in leftover         → no legacy data → no-op
//	hypervisor.db in BOTH                → ambiguous → refuse, keep both
func shouldMigrateLayout(leftover, canonical string) (bool, string) {
	if filepath.Clean(leftover) == filepath.Clean(canonical) {
		return false, "leftover and canonical are the same directory"
	}
	if _, err := os.Stat(filepath.Join(leftover, "hypervisor.db")); err != nil {
		return false, "legacy tree has no hypervisor.db"
	}
	if _, err := os.Stat(filepath.Join(canonical, "hypervisor.db")); err == nil {
		return false, "canonical tree already has hypervisor.db (refusing to overwrite)"
	}
	return true, ""
}

// migrationLockName is the exclusive-lock file that serializes migration
// across processes. It lives in canonical, not leftover: leftover is renamed
// to leftover.migrated-<ts> on completion, which would yank the lock out from
// under a waiting peer.
//
// Never unlinked, not even after a successful migration. flock keys on the
// inode: unlinking while a peer polls leaves that peer waiting on a ghost
// inode, so a third process creates a fresh file and locks it instantly —
// two holders, mutual exclusion gone. A 0-byte anchor is cheaper than that
// class of bug, and it matches .cell.lock / .im-channel.lock, which persist
// for the same reason.
const migrationLockName = ".migrate.lock"

// migrationLockTimeout bounds how long we wait for a peer's migration to
// finish. Exceeding it is fail-closed (migration refused, legacy tree left
// intact for the next boot) — proceeding unlocked is the data-loss path.
// Overridden by tests.
var migrationLockTimeout = 30 * time.Second

// acquireMigrationLock takes an exclusive cross-process lock on
// canonical/.migrate.lock, waiting up to migrationLockTimeout.
//
// Blocking (not skip-on-contention like acquireChannelBootLock) is required:
// a loser that skipped would return to boot and let initAuth/StartEngine open
// a half-migrated canonical, creating a fresh hypervisor.db that the winner's
// migrateItem would then archive out from under the live engine.
func acquireMigrationLock(canonical string) (func(), error) {
	if err := os.MkdirAll(canonical, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir canonical %s: %w", canonical, err)
	}
	lockPath := filepath.Join(canonical, migrationLockName)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open migration lock %s: %w", lockPath, err)
	}

	deadline := time.Now().Add(migrationLockTimeout)
	for {
		locked, err := tryLockExclusive(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("lock %s: %w", lockPath, err)
		}
		if locked {
			return func() { unlockFile(f); f.Close() }, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("migration lock %s held by another process for >%s", lockPath, migrationLockTimeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// migrateDataDir moves the legacy data tree into canonical, item by item.
// Order matters: hypervisor.db group first (the anchor), then bulk dirs.
// Any pre-existing canonical file/dir is archived (never overwritten).
//
// Returns (false, nil) when a concurrent process already completed the
// migration. Callers MUST NOT treat that as "I migrated": the completion
// marker rename and config-twin migration belong to the winner alone.
//
// The whole body runs under an exclusive lock. Per-item locking would not
// help — migrateItem's Lstat(dst) → Rename(dst, archive) → Rename(src, dst)
// is a TOCTOU window, so an unsynchronized peer that lands between the Lstat
// and the archive will file the winner's freshly-migrated tree away as
// cells.legacy-<ts>, leaving canonical/cells absent. The user sees zero Cells.
func migrateDataDir(leftover, canonical string) (bool, error) {
	release, err := acquireMigrationLock(canonical)
	if err != nil {
		return false, err
	}
	defer release()

	// Re-decide UNDER the lock. shouldMigrateLayout ran before the lock was
	// held, so a peer may have completed the whole migration since. Every
	// false reason here means "not ours to do" — anchor already moved, or
	// canonical already anchored.
	if ok, reason := shouldMigrateLayout(leftover, canonical); !ok {
		slog.Info("[migrate] migration already completed by a concurrent process",
			"reason", reason, "legacy", leftover, "canonical", canonical)
		return false, nil
	}

	// 0. Validate the source hypervisor DB BEFORE moving anything — a corrupt
	//    legacy DB must not be transplanted over a healthy (empty) canonical.
	hypDB := filepath.Join(leftover, "hypervisor.db")
	if err := checkSQLiteIntegrity(hypDB); err != nil {
		return false, fmt.Errorf("legacy hypervisor.db integrity check failed: %w", err)
	}

	// 1. hypervisor.db group (-wal/-shm travel with the main DB).
	for _, name := range []string{"hypervisor.db", "hypervisor.db-wal", "hypervisor.db-shm"} {
		if err := migrateItem(filepath.Join(leftover, name), filepath.Join(canonical, name)); err != nil {
			return false, fmt.Errorf("migrate %s: %w", name, err)
		}
	}

	// 2. Bulk directories / files. logs/: migrateItem archives any existing
	//    canonical/logs (e.g. the stub initLogger just created for THIS boot,
	//    or older real logs from previous dev/Config-Mode sessions) as
	//    logs.legacy-<ts> — never deleted, so concurrent backends and prior
	//    sessions cannot lose log data (review Must-Fix #1).
	for _, name := range []string{"cells", "logs", "runtime", "crashes", ".im-channel.lock", "config.yaml"} {
		if err := migrateItem(filepath.Join(leftover, name), filepath.Join(canonical, name)); err != nil {
			return false, fmt.Errorf("migrate %s: %w", name, err)
		}
	}

	// 3. db/ — auth.db travels WITH the hypervisor (the legacy tree's engine
	//    was alive; the canonical db/ holds only a crashed make-run leftover).
	if err := migrateItem(filepath.Join(leftover, "db"), filepath.Join(canonical, "db")); err != nil {
		return false, fmt.Errorf("migrate db/: %w", err)
	}

	return true, nil
}

// migrateItem moves src to dst. If dst exists, it is archived as
// <dst>.legacy-<ts> first (a leftover from a crashed dev run — never merge).
// Cross-device moves fall back to copy + remove (atomic rename is impossible
// when %LOCALAPPDATA% and %USERPROFILE% live on different volumes).
func migrateItem(src, dst string) error {
	if _, err := os.Lstat(src); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // item absent on the legacy side — nothing to do
		}
		return err
	}

	if _, err := os.Lstat(dst); err == nil {
		archive := dst + ".legacy-" + time.Now().UTC().Format("20060102-150405")
		if err := os.Rename(dst, archive); err != nil {
			return fmt.Errorf("archive existing %s → %s: %w", dst, archive, err)
		}
		slog.Info("[migrate] archived pre-existing canonical item",
			"path", dst, "archive", archive)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("mkdir parent %s: %w", filepath.Dir(dst), err)
	}

	return movePath(src, dst)
}

// movePath moves src to dst, falling back to a cross-device copy+verify+remove
// when an atomic rename is impossible (EXDEV / EINVAL — e.g. %LOCALAPPDATA%
// redirected to a different volume than %USERPROFILE%). Shared by migrateItem
// and migrateConfigFile so the config twin gets the same semantics.
func movePath(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) && !errors.Is(err, syscall.EINVAL) {
		return fmt.Errorf("rename %s → %s: %w", src, dst, err)
	}

	// Cross-device: copy, then verify, then remove source.
	if err := copyPath(src, dst); err != nil {
		return fmt.Errorf("cross-device copy %s → %s: %w", src, dst, err)
	}
	if err := os.RemoveAll(src); err != nil {
		return fmt.Errorf("cross-device remove source %s: %w", src, err)
	}
	slog.Info("[migrate] cross-device copy (non-atomic)", "from", src, "to", dst)
	return nil
}

// checkSQLiteIntegrity runs PRAGMA quick_check on a (static, unlocked) DB.
func checkSQLiteIntegrity(path string) error {
	dsn := "file:" + filepath.ToSlash(path) + "?mode=ro&_pragma=busy_timeout(3000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	var result string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("quick_check = %q", result)
	}
	return nil
}

// migrateConfigFile moves ~/.config/wescode/config.yaml → xdg ConfigDir
// (Windows: %APPDATA%\wescode\) when the canonical side has none. Same anchor
// rule as the data dir: only when the user did not override config paths.
func migrateConfigFile() error {
	if strings.TrimSpace(os.Getenv("WESCODE_CONFIG")) != "" {
		return nil
	}
	if strings.TrimSpace(os.Getenv("WESCODE_CONFIG_DIR")) != "" {
		return nil
	}

	canonicalPath := engine.DefaultConfigPath()
	canonicalDir := filepath.Dir(canonicalPath)
	legacyPath := filepath.Join(legacyConfigDirXDG(), "config.yaml")

	if filepath.Clean(legacyPath) == filepath.Clean(canonicalPath) {
		return nil
	}
	if _, err := os.Stat(legacyPath); err != nil {
		return nil // no legacy config
	}
	if _, err := os.Stat(canonicalPath); err == nil {
		return nil // canonical already has config — never overwrite
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(canonicalDir, 0o755); err != nil {
		return err
	}
	if err := movePath(legacyPath, canonicalPath); err != nil {
		return err
	}
	slog.Info("[migrate] Windows legacy config migrated", "from", legacyPath, "to", canonicalPath)
	return nil
}

// --- copy helpers (cross-device fallback) ---

func copyPath(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	}
	if info.IsDir() {
		return copyDir(src, dst)
	}
	return copyFile(src, dst, info)
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(path, target, info)
	})
}

func copyFile(src, dst string, info fs.FileInfo) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
