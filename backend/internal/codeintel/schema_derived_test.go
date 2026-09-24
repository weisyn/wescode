package codeintel

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// 派生表形状变更不得清掉用户数据。
//
// 2026-09-20 给 `doc_references` 加 `doc_line` 列时，第一版做法是 bump schemaVersion
// ——而那会连带 DROP `constraints`（`TeachFromEditRejection` /
// `TeachFromEditModification` 从用户拒绝或改写 AI 编辑里学到的约束，以及他按掉某条
// 约束留下的墓碑）与 `exploration_paths`。那不是索引，是用户行为的沉淀，重建不回来。
//
// 这个测试守的是那条界线：形状变更只重建点名的派生表，别的表一个不动。

func openTestCKG(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "code.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := migrateSchema(db); err != nil {
		t.Fatalf("migrateSchema: %v", err)
	}
	return db, path
}

func TestRebuildDerivedTables_PreservesUserData(t *testing.T) {
	db, _ := openTestCKG(t)

	// 用户数据：一条学到的约束 + 一条运行轨迹。
	if _, err := db.Exec(
		`INSERT INTO constraints (id, rule, kind, priority, status, source, confidence)
		 VALUES ('c-user', '这个文件里不要丢弃 error', 'quality', 1, 'active', 'learned', 0.9)`,
	); err != nil {
		t.Fatalf("seed constraint: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO exploration_paths (run_id, session_id, paths_json) VALUES ('run-1', 's-1', '[]')`,
	); err != nil {
		// 列名不同则跳过这半——约束那半才是关键断言
		t.Logf("exploration_paths seed skipped: %v", err)
	}

	// 模拟"派生表形状过期"：把新列删掉（SQLite 支持 DROP COLUMN）。
	if _, err := db.Exec(`ALTER TABLE doc_references DROP COLUMN doc_line`); err != nil {
		t.Fatalf("模拟旧形状失败：%v", err)
	}
	if has, _ := tableHasColumn(db, "doc_references", "doc_line"); has {
		t.Fatal("前提不成立：doc_line 还在")
	}

	if err := rebuildDerivedTables(db); err != nil {
		t.Fatalf("rebuildDerivedTables: %v", err)
	}

	// 派生表已是新形状。
	if has, _ := tableHasColumn(db, "doc_references", "doc_line"); !has {
		t.Error("doc_line 没被建回来")
	}

	// 用户约束必须还在。这是整个测试的理由。
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM constraints WHERE id = 'c-user'`).Scan(&n); err != nil {
		t.Fatalf("constraints 表不见了：%v", err)
	}
	if n != 1 {
		t.Errorf("用户学到的约束被清掉了（%d 行）——派生表重建不得波及非派生表", n)
	}

	// 运行轨迹同理：它记的是 agent 实际走过的探索路径，重算不回来。
	if err := db.QueryRow(`SELECT COUNT(*) FROM exploration_paths WHERE run_id = 'run-1'`).Scan(&n); err != nil {
		t.Fatalf("exploration_paths 表不见了：%v", err)
	}
	if n != 1 {
		t.Errorf("运行轨迹被清掉了（%d 行）", n)
	}
}

func TestRebuildDerivedTables_IsIdempotent(t *testing.T) {
	db, _ := openTestCKG(t)

	// 已是新形状时重复调用必须是空操作——判据是"列在不在"而不是版本号，
	// 正是为了让它对已迁移的库天然幂等。
	if _, err := db.Exec(
		`INSERT INTO doc_references (doc_file, symbol_name, context, doc_line)
		 VALUES ('a.md', 'Cell', '资源隔离', 42)`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := rebuildDerivedTables(db); err != nil {
			t.Fatalf("第 %d 次 rebuild: %v", i+1, err)
		}
	}

	var line int
	if err := db.QueryRow(`SELECT doc_line FROM doc_references WHERE symbol_name = 'Cell'`).Scan(&line); err != nil {
		t.Fatalf("已是新形状却被重建了：%v", err)
	}
	if line != 42 {
		t.Errorf("doc_line = %d，要 42", line)
	}
}

// schemaVersion 不得为"派生表形状变更"而 bump。
//
// 这条看起来像在测一个常量，但它守的是一次真实的决策：bump 一次就清掉用户约束，
// 而那个动作在 diff 里只是一行数字。把理由钉在测试里，下一个想改它的人会先读到它。
func TestSchemaVersion_NotBumpedForDerivedShapeChanges(t *testing.T) {
	if schemaVersion != 8 {
		t.Errorf("schemaVersion = %d。改它会 DROP `constraints`（用户拒绝/改写 AI 编辑时"+
			"学到的约束 + 墓碑）与 `exploration_paths`。如果这次变更只动派生表，"+
			"请改用 derivedTableShapes；如果确实要全库重建，请在这里说明代价并更新本测试。",
			schemaVersion)
	}
	// 派生表名单必须只含真正可重算的表。
	for _, d := range derivedTableShapes {
		switch d.table {
		case "constraints", "exploration_paths", "symbols", "edges":
			t.Errorf("%q 不是派生表——把它放进 derivedTableShapes 会让形状变更清掉"+
				"重建不回来的数据", d.table)
		}
		if d.source == "" {
			t.Errorf("%q 没写谁能把它填回来——那句话是判断代价的唯一依据", d.table)
		}
	}
}
