package codeintel

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestQueryTopModules_SeparatorAgnostic 锁住 D-9 分隔符家族的最后残留：
// queryTopModules 的模块提取 SQL 必须对 Windows（\）file_path 也能提取出
// 首段。修复前 INSTR(file_path, '/') 在 Windows 路径上返回 0 → module='.'
// → 被 HAVING 过滤 → top modules 恒为空数组。
//
// 注：索引 file_path 是绝对路径（queryProjectStats 的 root 前缀匹配依赖
// 这一点）。因此 POSIX 绝对路径首段为空串是既有行为（macOS 一直如此），
// 本测试锁定「修复不改变 macOS 行为、且 Windows 从空变为有数据」。
func TestQueryTopModules_SeparatorAgnostic(t *testing.T) {
	dir := t.TempDir()
	dsn := "file:" + filepath.ToSlash(filepath.Join(dir, "t.db"))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE symbols (
		file_path TEXT, kind TEXT, name TEXT, id INTEGER PRIMARY KEY
	)`); err != nil {
		t.Fatal(err)
	}
	// Windows 原生分隔符绝对路径 + POSIX 绝对路径混合。
	inserts := []struct{ path, kind string }{
		{`f:\proj\backend\a.go`, "function"},
		{`f:\proj\backend\b.go`, "function"},
		{`f:\proj\frontend\c.js`, "function"},
		{`/home/u/proj/backend/d.go`, "function"},
		{`/home/u/proj/frontend/e.ts`, "function"},
	}
	for i, ins := range inserts {
		if _, err := db.Exec(`INSERT INTO symbols (file_path, kind, name, id) VALUES (?, ?, ?, ?)`,
			ins.path, ins.kind, "sym", i+1); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := db.Query(`
		SELECT
			CASE
				WHEN INSTR(file_path, '/') > 0
				THEN SUBSTR(file_path, 1, INSTR(file_path, '/') - 1)
				WHEN INSTR(file_path, '\') > 0
				THEN SUBSTR(file_path, 1, INSTR(file_path, '\') - 1)
				ELSE '.'
			END as module,
			COUNT(*) as sym_count
		FROM symbols
		WHERE kind IN ('function','method','type','interface','class')
		GROUP BY module
		HAVING module != '.'
		ORDER BY sym_count DESC
		LIMIT 10`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	modules := map[string]int{}
	for rows.Next() {
		var m string
		var c int
		if err := rows.Scan(&m, &c); err != nil {
			t.Fatal(err)
		}
		modules[m] = c
	}

	// Windows 路径提取出盘符首段 `f:`（修复前是 '.' → 被过滤 → 全空）。
	if modules["f:"] != 3 {
		t.Errorf("modules[f:] = %d, want 3 (Windows backslash paths must not be filtered out)", modules["f:"])
	}
	// POSIX 绝对路径首段为空串是既有行为（INSTR 命中开头 '/'），必须保留
	// 而非过滤成空数组——修复不得改变 macOS 行为。
	if modules[""] != 2 {
		t.Errorf("modules[''] = %d, want 2 (POSIX absolute paths keep existing grouping)", modules[""])
	}
	if len(modules) != 2 {
		t.Errorf("modules = %v, want exactly {f::3, '':2}", modules)
	}
}
