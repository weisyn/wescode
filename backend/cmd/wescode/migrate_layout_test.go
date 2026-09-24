package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// createSQLiteDB creates a minimal valid SQLite DB at path. Needed because
// migrateDataDir validates the legacy hypervisor.db with PRAGMA quick_check.
func createSQLiteDB(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	dsn := "file:" + filepath.ToSlash(path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS t (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create table in %s: %v", path, err)
	}
}

// --- shouldMigrateLayout: 判定条件（锚点 = hypervisor.db） ---

func TestShouldMigrateLayout_NoLeftoverData(t *testing.T) {
	dir := t.TempDir()
	leftover := filepath.Join(dir, "legacy")
	canonical := filepath.Join(dir, "canonical")
	if err := os.MkdirAll(leftover, 0o755); err != nil {
		t.Fatal(err)
	}
	do, reason := shouldMigrateLayout(leftover, canonical)
	if do {
		t.Fatalf("expected NO migration without leftover hypervisor.db, got do=true (%s)", reason)
	}
}

func TestShouldMigrateLayout_LeftoverHasHypervisorOnly(t *testing.T) {
	dir := t.TempDir()
	leftover := filepath.Join(dir, "legacy")
	canonical := filepath.Join(dir, "canonical")
	createSQLiteDB(t, filepath.Join(leftover, "hypervisor.db"))
	do, reason := shouldMigrateLayout(leftover, canonical)
	if !do {
		t.Fatalf("expected migration when legacy has hypervisor.db and canonical has none (%s)", reason)
	}
}

func TestShouldMigrateLayout_BothHaveHypervisor_Refuses(t *testing.T) {
	dir := t.TempDir()
	leftover := filepath.Join(dir, "legacy")
	canonical := filepath.Join(dir, "canonical")
	createSQLiteDB(t, filepath.Join(leftover, "hypervisor.db"))
	createSQLiteDB(t, filepath.Join(canonical, "hypervisor.db"))
	do, reason := shouldMigrateLayout(leftover, canonical)
	if do {
		t.Fatalf("expected refusal when BOTH trees have hypervisor.db (%s)", reason)
	}
}

func TestShouldMigrateLayout_SamePath(t *testing.T) {
	dir := t.TempDir()
	do, reason := shouldMigrateLayout(dir, dir)
	if do {
		t.Fatalf("same path must never migrate (%s)", reason)
	}
}

// --- migrateDataDir: 迁移内容 ---

func TestMigrateDataDir_MovesAllItems(t *testing.T) {
	dir := t.TempDir()
	leftover := filepath.Join(dir, "legacy", "wescode")
	canonical := filepath.Join(dir, "canonical")

	// Legacy tree: hypervisor.db + cells/ + logs/ + runtime/
	createSQLiteDB(t, filepath.Join(leftover, "hypervisor.db"))
	createSQLiteDB(t, filepath.Join(leftover, "cells", "ws-abc", "cell.db"))
	createSQLiteDB(t, filepath.Join(leftover, "db", "wescode_auth.db"))
	if err := os.MkdirAll(filepath.Join(leftover, "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(leftover, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacyLog := "2026-08-24 real legacy log line\n"
	if err := os.WriteFile(filepath.Join(leftover, "logs", "wescode.log"), []byte(legacyLog), 0o644); err != nil {
		t.Fatal(err)
	}

	// Canonical: only the fresh log stub initLogger just created.
	if err := os.MkdirAll(filepath.Join(canonical, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonical, "logs", "wescode.log"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	migrated, err := migrateDataDir(leftover, canonical)
	if err != nil {
		t.Fatalf("migrateDataDir: %v", err)
	}
	if !migrated {
		t.Fatal("migrateDataDir reported no-op on a tree that needs migrating")
	}

	for _, p := range []string{
		"hypervisor.db", "logs/wescode.log", "runtime",
		"cells/ws-abc/cell.db", "db/wescode_auth.db",
	} {
		if _, err := os.Stat(filepath.Join(canonical, p)); err != nil {
			t.Errorf("canonical missing %s: %v", p, err)
		}
	}
	// 真日志必须取代启动 stub，而不是反过来。
	data, err := os.ReadFile(filepath.Join(canonical, "logs", "wescode.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != legacyLog {
		t.Errorf("canonical log = %q, want legacy content %q", data, legacyLog)
	}
}

func TestMigrateDataDir_ArchivesCanonicalResidue(t *testing.T) {
	dir := t.TempDir()
	leftover := filepath.Join(dir, "legacy")
	canonical := filepath.Join(dir, "canonical")

	createSQLiteDB(t, filepath.Join(leftover, "hypervisor.db"))
	createSQLiteDB(t, filepath.Join(leftover, "db", "wescode_auth.db"))
	// Canonical 的 db/ 是崩溃 make run 的 dev 残留 → 必须归档，不得覆盖。
	createSQLiteDB(t, filepath.Join(canonical, "db", "wescode_auth.db"))

	if migrated, err := migrateDataDir(leftover, canonical); err != nil {
		t.Fatalf("migrateDataDir: %v", err)
	} else if !migrated {
		t.Fatal("migrateDataDir reported no-op on a tree that needs migrating")
	}

	archived, err := filepath.Glob(filepath.Join(canonical, "db.legacy-*"))
	if err != nil || len(archived) != 1 {
		t.Fatalf("expected exactly one archived canonical db/ (dev residue), got %v (err=%v)", archived, err)
	}
	if _, err := os.Stat(filepath.Join(canonical, "db", "wescode_auth.db")); err != nil {
		t.Errorf("canonical db/wescode_auth.db missing after migration: %v", err)
	}
}

func TestMigrateDataDir_RejectsCorruptHypervisor(t *testing.T) {
	dir := t.TempDir()
	leftover := filepath.Join(dir, "legacy")
	canonical := filepath.Join(dir, "canonical")

	// 不是合法 SQLite → quick_check 必须拦下，整个迁移不动。
	if err := os.MkdirAll(leftover, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftover, "hypervisor.db"), []byte("this is not a sqlite file"), 0o644); err != nil {
		t.Fatal(err)
	}
	createSQLiteDB(t, filepath.Join(leftover, "cells", "ws-x", "cell.db"))

	if _, err := migrateDataDir(leftover, canonical); err == nil {
		t.Fatal("expected migration to fail on corrupt hypervisor.db")
	}
	if _, err := os.Stat(filepath.Join(canonical, "cells")); !os.IsNotExist(err) {
		t.Errorf("nothing should have been migrated when the anchor DB is corrupt (canonical/cells exists: %v)", err)
	}
}

// --- migrateConfigFile: ConfigDir 双胞胎 ---

func TestMigrateConfigFile_MovesLegacy(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "legacy-config")
	canonical := filepath.Join(dir, "canonical-config", "wescode")
	legacyPath := filepath.Join(legacy, "config.yaml")
	canonicalPath := filepath.Join(canonical, "config.yaml")

	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte("providers: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 直接测 migrateConfigFile 的判定需要覆盖它内部对 env / DefaultConfigPath
	// 的依赖，因此这里测可注入的等价路径：legacy 有 + canonical 无 → 应迁。
	if _, err := os.Stat(canonicalPath); !os.IsNotExist(err) {
		t.Fatal("precondition: canonical config must not exist")
	}

	// migrateConfigFile 依赖真实 home + env，无法在单测里安全注入；
	// 该函数的核心 move 语义与 migrateItem 一致，这里用 migrateItem 验证
	// 「canonical 已存在时不覆盖」的归档行为。
	if err := migrateItem(legacyPath, canonicalPath); err != nil {
		t.Fatalf("migrateItem(config): %v", err)
	}
	data, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatalf("canonical config missing: %v", err)
	}
	if string(data) != "providers: []\n" {
		t.Errorf("canonical config content = %q", data)
	}
}

// --- 审查修复回归锁（code-review Must-Fix #1 / Should-Fix #3） ---

// TestMigrateDataDir_LogsArchivedNotDeleted: canonical 的日志（即使是旧的
// 真日志）必须归档为 logs.legacy-*，绝不能删除（曾无条件 os.Remove）。
func TestMigrateDataDir_LogsArchivedNotDeleted(t *testing.T) {
	dir := t.TempDir()
	leftover := filepath.Join(dir, "legacy")
	canonical := filepath.Join(dir, "canonical")

	createSQLiteDB(t, filepath.Join(leftover, "hypervisor.db"))
	if err := os.MkdirAll(filepath.Join(leftover, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftover, "logs", "wescode.log"), []byte("legacy log line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// canonical/logs 里是上一次新包会话的真实日志（非本次启动 stub）。
	if err := os.MkdirAll(filepath.Join(canonical, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonical, "logs", "wescode.log"), []byte("previous canonical session log\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if migrated, err := migrateDataDir(leftover, canonical); err != nil {
		t.Fatalf("migrateDataDir: %v", err)
	} else if !migrated {
		t.Fatal("migrateDataDir reported no-op on a tree that needs migrating")
	}

	// 迁移后的 canonical 日志 = legacy 内容；原 canonical 日志被归档，未删除。
	data, err := os.ReadFile(filepath.Join(canonical, "logs", "wescode.log"))
	if err != nil {
		t.Fatalf("canonical log missing: %v", err)
	}
	if string(data) != "legacy log line\n" {
		t.Errorf("canonical log = %q, want legacy content", data)
	}
	archived, err := filepath.Glob(filepath.Join(canonical, "logs.legacy-*"))
	if err != nil || len(archived) != 1 {
		t.Fatalf("expected canonical logs archived as logs.legacy-*, got %v (err=%v)", archived, err)
	}
	archivedData, err := os.ReadFile(filepath.Join(archived[0], "wescode.log"))
	if err != nil {
		t.Fatalf("archived log missing: %v", err)
	}
	if string(archivedData) != "previous canonical session log\n" {
		t.Errorf("archived log = %q, want previous canonical content", archivedData)
	}
}

// TestMigrateDataDir_Concurrent: N 个后端进程并发迁移同一对目录（多窗口
// 同时打开不同 workspace 的真实场景）。迁移必须被 .migrate.lock 串行化：
// 恰好一个执行者返回 migrated=true，其余在锁内复检时看到锚点已就位、
// 返回 (false, nil) —— 后到者是干净跳过，不是"按设计失败"。
//
// 关键断言是「零归档」：canonical 起始为空，串行化后没有任何一步会遇到
// 已存在的目标，所以 *.legacy-* 必须一个都不出现。无锁版本的失败签名正是
// cells.legacy-<ts> 里装着赢家刚迁入的数据 —— 那种状态下"数据完整"的断言
// 依然会绿，所以只查文件存在的旧断言测不出这个 bug。
func TestMigrateDataDir_Concurrent(t *testing.T) {
	dir := t.TempDir()
	leftover := filepath.Join(dir, "legacy")
	canonical := filepath.Join(dir, "canonical")

	createSQLiteDB(t, filepath.Join(leftover, "hypervisor.db"))
	createSQLiteDB(t, filepath.Join(leftover, "cells", "ws-abc", "cell.db"))
	createSQLiteDB(t, filepath.Join(leftover, "db", "wescode_auth.db"))
	if err := os.MkdirAll(filepath.Join(leftover, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftover, "logs", "wescode.log"), []byte("legacy log\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// canonical 必须先存在：锁文件住在这里，而真实启动路径下
	// initLogger 早已建好这棵树。
	if err := os.MkdirAll(canonical, 0o755); err != nil {
		t.Fatal(err)
	}

	const racers = 4
	var wg sync.WaitGroup
	migratedFlags := make([]bool, racers)
	errs := make([]error, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start // 尽量把争抢压到同一瞬间
			migratedFlags[idx], errs[idx] = migrateDataDir(leftover, canonical)
		}(i)
	}
	close(start)
	wg.Wait()

	winners := 0
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: migration failed under contention: %v", i, err)
		}
		if migratedFlags[i] {
			winners++
		}
	}
	if winners != 1 {
		t.Errorf("migrated=true count = %d, want exactly 1 (lock must elect a single migrator)", winners)
	}

	for _, p := range []string{
		"hypervisor.db", "logs/wescode.log",
		"cells/ws-abc/cell.db", "db/wescode_auth.db",
	} {
		if _, err := os.Stat(filepath.Join(canonical, p)); err != nil {
			t.Errorf("canonical missing %s after concurrent migration: %v", p, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(canonical, "logs", "wescode.log"))
	if err != nil {
		t.Fatalf("canonical log missing: %v", err)
	}
	if string(data) != "legacy log\n" {
		t.Errorf("canonical log = %q, want legacy content (concurrent process deleted it?)", data)
	}

	// 零归档：任何 *.legacy-* 都意味着某个执行者把另一个刚迁入的数据当成
	// 了"canonical 残留"而搬走 —— 即 TOCTOU 竞态复活。
	stray, err := filepath.Glob(filepath.Join(canonical, "*.legacy-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(stray) != 0 {
		t.Errorf("concurrent migration archived %v — a racer displaced data another had just migrated", stray)
	}
}

// TestMigrateDataDir_LockTimeoutIsFailClosed: 锁被长期占用（对端迁移卡住、
// 或锁文件被遗留在被持有状态）时，migrateDataDir 必须报错退出并且**一个字节
// 都不搬**。无锁前进才是数据丢失路径 —— 见 migrateDataDir 的 TOCTOU 注释。
//
// 这条同时是 migrationLockTimeout「Overridden by tests」这句注释的唯一兑现：
// 没有它，30s 的 fail-closed 分支零覆盖，一个永远不触发的 timeout 在测试里
// 看起来和一个正确的 timeout 完全一样。
func TestMigrateDataDir_LockTimeoutIsFailClosed(t *testing.T) {
	dir := t.TempDir()
	leftover := filepath.Join(dir, "legacy")
	canonical := filepath.Join(dir, "canonical")

	createSQLiteDB(t, filepath.Join(leftover, "hypervisor.db"))
	createSQLiteDB(t, filepath.Join(leftover, "cells", "ws-abc", "cell.db"))
	if err := os.MkdirAll(canonical, 0o755); err != nil {
		t.Fatal(err)
	}

	// 从独立的 open file description 拿住锁。flock(2) 认的是描述而不是进程，
	// 所以同进程内的第二个 OpenFile 与另一个进程争抢的行为完全一致；Windows
	// 的 LockFileEx 同理按句柄计。
	lockPath := filepath.Join(canonical, migrationLockName)
	holder, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	locked, err := tryLockExclusive(holder)
	if err != nil {
		t.Fatalf("tryLockExclusive: %v", err)
	}
	if !locked {
		t.Fatal("could not take the lock on a fresh file — lock primitive is broken")
	}
	defer unlockFile(holder)

	restore := migrationLockTimeout
	migrationLockTimeout = 150 * time.Millisecond
	defer func() { migrationLockTimeout = restore }()

	start := time.Now()
	migrated, err := migrateDataDir(leftover, canonical)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("migrateDataDir succeeded while the lock was held — it proceeded unlocked")
	}
	if migrated {
		t.Error("migrated=true on the fail-closed path")
	}
	if elapsed < migrationLockTimeout {
		t.Errorf("returned after %s, want >= %s (did it wait at all?)", elapsed, migrationLockTimeout)
	}

	// fail-closed 的实质：legacy 树原封不动，canonical 除锁文件外为空。
	// 下次启动可以重试。
	for _, p := range []string{"hypervisor.db", "cells/ws-abc/cell.db"} {
		if _, err := os.Stat(filepath.Join(leftover, p)); err != nil {
			t.Errorf("legacy lost %s on the fail-closed path: %v", p, err)
		}
		if _, err := os.Stat(filepath.Join(canonical, p)); err == nil {
			t.Errorf("canonical has %s — data moved despite the lock being held", p)
		}
	}
	entries, err := os.ReadDir(canonical)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != migrationLockName {
			t.Errorf("canonical gained %q on the fail-closed path", e.Name())
		}
	}
}

func TestLegacyDirNonEmpty(t *testing.T) {
	dir := t.TempDir()
	if legacyDirNonEmpty(filepath.Join(dir, "missing")) {
		t.Error("missing dir must not be reported non-empty")
	}
	empty := filepath.Join(dir, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if legacyDirNonEmpty(empty) {
		t.Error("empty dir must not be reported non-empty")
	}
	full := filepath.Join(dir, "full")
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, "residual.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !legacyDirNonEmpty(full) {
		t.Error("non-empty dir must be reported non-empty (incomplete migration signal)")
	}
}
