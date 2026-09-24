package codeintel

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
)

// schemaVersion gates the entire CKG schema. A mismatch drops every table and
// rebuilds from ckgSchemaDDL (DEV-1): there is no ALTER path, no column
// probing, and no reader for a retired shape. Bump this when edge semantics,
// resolution encoding, or symbol columns change.
//
// v4: one authoritative DDL shared by production and staging. The two copies
// had drifted — production's literal DDL lacked `metadata` and the three
// partial indexes, and rebuilt the symbols table one pragma_table_info probe +
// ALTER at a time — so one table's definition lived in three mechanisms whose
// agreement nothing checked.
//
// v5: `certainty REAL` split into `resolution` (closed enum) + `score`
// (nullable, measured kinds only), and target_id demoted instead of cascaded.
// The old column meant three things at once, and 1.0 was both "same-package
// exact hit" and "no candidate found at all" — 43k unresolved call edges sat at
// 1.0 and were invisible to the one diagnostic that read the column.
// v6: `file_path` is stored slash-normalized on every platform. The column held
// native separators, so on Windows every `LIKE '%/pkg/%'` package-path
// predicate could never match: the resolution fallback silently degraded to
// bare-name matching (which INV-CKG-EDGE-01 forbids, since it either mis-binds
// cross-package homonyms or loses the ambiguous group), and the `%/e2e/%`
// exclusions let test and bench code into retrieval results. Measured edge
// coverage on Windows was 30.3%. A mixed DB is the failure this bump prevents:
// a backslash row and a slash row for the same file are two rows to SQL.
//
// v7: `reachability_class` + `structural_tag` on symbols (INV-CKG-SINGLE-CLASS).
// Every consumer — queryCoverageSummary, handleCodeIntelCoverage, FindOrphans,
// Agent tool chain — reads the same column. No consumer derives reachability
// from edges independently; that is the bypass this version eliminates.
// 留在 8：`doc_references.doc_line`（2026-09-20）**没有**bump 它。那次变更只动了一张
// 派生表，而 bump 会连带 DROP `constraints`（用户拒绝/改写 AI 编辑时学到的约束 + 他
// 按掉某条约束的墓碑）与 `exploration_paths`。派生表形状变更走 rebuildDerivedTables。
const schemaVersion = 8

// sqlInList renders a Go slice as a SQL `IN (...)` tuple with single-quoted
// members. Single quotes inside values are escaped to ” (SQL standard).
// An empty slice produces "(NULL)" so the caller never builds a syntax error.
//
// Primary consumers: schema CHECK constraints and reader predicates generated
// from boundResolutions / scoredEdgeKinds (compile-time constants), plus
// collectReachabilityNeighbors and ClassifyReachabilityForFiles which pass
// runtime file paths.
func sqlInList[T ~string](members []T) string {
	if len(members) == 0 {
		return "(NULL)"
	}
	quoted := make([]string, len(members))
	for i, m := range members {
		quoted[i] = "'" + strings.ReplaceAll(string(m), "'", "''") + "'"
	}
	return "(" + strings.Join(quoted, ",") + ")"
}

// The SQL halves of the Go predicates in types.go. A reader that wants bound
// edges, or a query that must exclude the two scored kinds, uses these instead
// of respelling the members.
var (
	resolutionBoundSQL  = "resolution IN " + sqlInList(boundResolutions)
	resolutionDomainSQL = "resolution IN " + sqlInList([]Resolution{
		ResolutionExact, ResolutionInferred, ResolutionAmbiguous, ResolutionUnresolved,
	})
	scoredKindSQL = "kind IN " + sqlInList(scoredEdgeKinds)
)

// ckgSchemaDDL is the only place the CKG schema exists. The production DB
// (migrateSchema) and the pipeline staging DB (createStagingSchema) run exactly
// these statements, so promoting staging.db over code.db can never land a shape
// the reader does not expect.
var ckgSchemaDDL = []string{
	`CREATE TABLE IF NOT EXISTS symbols (
		id                INTEGER PRIMARY KEY AUTOINCREMENT,
		file_path         TEXT NOT NULL,
		kind              TEXT NOT NULL,
		name              TEXT NOT NULL,
		qualified         TEXT NOT NULL DEFAULT '',
		signature         TEXT NOT NULL DEFAULT '',
		doc               TEXT NOT NULL DEFAULT '',
		parent            TEXT NOT NULL DEFAULT '',
		line_start        INTEGER NOT NULL,
		line_end          INTEGER NOT NULL,
		byte_start        INTEGER NOT NULL DEFAULT 0,
		byte_end          INTEGER NOT NULL DEFAULT 0,
		content_hash      TEXT NOT NULL DEFAULT '',
		exported          INTEGER NOT NULL DEFAULT 0,
		visibility        TEXT NOT NULL DEFAULT '',
		package_path      TEXT NOT NULL DEFAULT '',
		community_id      INTEGER NOT NULL DEFAULT 0,
		effects           TEXT NOT NULL DEFAULT '',
		summary           TEXT NOT NULL DEFAULT '',
		summary_hash      TEXT NOT NULL DEFAULT '',
		runtime_frequency INTEGER NOT NULL DEFAULT 0,
		coverage          REAL NOT NULL DEFAULT -1,
		cpu_pct           REAL NOT NULL DEFAULT -1,
		reachability_class TEXT NOT NULL DEFAULT 'connected',
		structural_tag     TEXT
	)`,
	`CREATE INDEX IF NOT EXISTS idx_symbols_file ON symbols(file_path)`,
	`CREATE INDEX IF NOT EXISTS idx_symbols_name ON symbols(name)`,
	`CREATE INDEX IF NOT EXISTS idx_symbols_exported ON symbols(exported) WHERE exported = 1`,
	`CREATE INDEX IF NOT EXISTS idx_symbols_pkg_name ON symbols(package_path, name)`,
	`CREATE INDEX IF NOT EXISTS idx_symbols_reachability ON symbols(reachability_class) WHERE kind IN ('function','method','type','interface','class')`,
	`CREATE VIRTUAL TABLE IF NOT EXISTS symbol_fts USING fts5(
		name, qualified, signature, doc, summary, file_path,
		content='symbols',
		content_rowid='id'
	)`,
	`CREATE TRIGGER IF NOT EXISTS symbols_ai AFTER INSERT ON symbols BEGIN
		INSERT INTO symbol_fts(rowid, name, qualified, signature, doc, summary, file_path)
		VALUES (new.id, new.name, new.qualified, new.signature, new.doc, new.summary, new.file_path);
	END`,
	`CREATE TRIGGER IF NOT EXISTS symbols_ad AFTER DELETE ON symbols BEGIN
		INSERT INTO symbol_fts(symbol_fts, rowid, name, qualified, signature, doc, summary, file_path)
		VALUES ('delete', old.id, old.name, old.qualified, old.signature, old.doc, old.summary, old.file_path);
	END`,
	`CREATE TABLE IF NOT EXISTS file_hashes (
		file_path    TEXT PRIMARY KEY,
		content_hash TEXT NOT NULL,
		indexed_at   TEXT NOT NULL DEFAULT (datetime('now')),
		mtime_ns     INTEGER NOT NULL DEFAULT 0
	)`,
	// target_id is SET NULL, not CASCADE. Incremental re-indexing deletes and
	// reinserts one file's symbols, and it only rebuilds that file's *outbound*
	// edges — so cascading on target_id silently deleted every other file's
	// edges into this one, with no error and no threshold, on every save. The
	// edge survives as unresolved with target_name intact, which is exactly what
	// the read-side name fallback already consumes.
	//
	// The three CHECKs make the states this table can hold the states the reader
	// expects, rather than a convention writers must remember:
	//   1. resolution is a closed domain — a typo fails the insert instead of
	//      becoming a value no consumer's switch covers.
	//   2. bound-ness is biconditional. 'exact'/'inferred' with a NULL target is
	//      a claim about a target nobody can follow; 'unresolved' with a bound
	//      target hides a real answer from every query that filters on it.
	//   3. score belongs only to the two kinds that measure something. The other
	//      kinds used to write hardcoded constants into that float, which let a
	//      reader rank an inference constant against a cosine distance.
	fmt.Sprintf(`CREATE TABLE IF NOT EXISTS edges (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		source_id   INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
		target_id   INTEGER REFERENCES symbols(id) ON DELETE SET NULL,
		target_name TEXT NOT NULL DEFAULT '',
		kind        TEXT NOT NULL,
		resolution  TEXT NOT NULL DEFAULT '%s',
		score       REAL,
		source      TEXT NOT NULL DEFAULT 'tree-sitter',
		flow_type   TEXT NOT NULL DEFAULT '',
		param_idx   INTEGER NOT NULL DEFAULT -1,
		metadata    TEXT NOT NULL DEFAULT '',
		CHECK (%s),
		CHECK ((%s) = (target_id IS NOT NULL)),
		CHECK (score IS NULL OR %s)
	)`, ResolutionUnresolved, resolutionDomainSQL, resolutionBoundSQL, scoredKindSQL),
	// The pairing CHECK above and ON DELETE SET NULL are mutually exclusive on
	// their own: the FK action moves target_id without touching resolution, so
	// the row SQLite writes is always invalid and the DELETE fails outright.
	// This trigger moves both columns in one statement before the FK action
	// runs, so the demotion is atomic and no intermediate state exists. It is
	// the mechanism that makes "symbol deleted" degrade to "unresolved" instead
	// of erroring — deleting the trigger reintroduces the failure as a hard
	// error on every incremental re-index, not as silent data loss.
	fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS edges_unbind_before_symbol_delete
		BEFORE DELETE ON symbols
	BEGIN
		UPDATE edges SET target_id = NULL, resolution = '%s'
		 WHERE target_id = old.id;
	END`, ResolutionUnresolved),
	`CREATE INDEX IF NOT EXISTS idx_edges_source ON edges(source_id)`,
	`CREATE INDEX IF NOT EXISTS idx_edges_target ON edges(target_id)`,
	`CREATE INDEX IF NOT EXISTS idx_edges_target_name ON edges(target_name)`,
	`CREATE INDEX IF NOT EXISTS idx_edges_kind ON edges(kind)`,
	// The resolver and every name-fallback read scan exactly the unbound rows.
	`CREATE INDEX IF NOT EXISTS idx_edges_unresolved ON edges(target_name) WHERE target_id IS NULL`,
	// Polymorphic dispatch queries (Pass 8/9) probe one kind at a time.
	`CREATE INDEX IF NOT EXISTS idx_edges_target_implements ON edges(target_id, kind) WHERE kind = 'implements'`,
	`CREATE INDEX IF NOT EXISTS idx_edges_source_overrides ON edges(source_id, kind) WHERE kind = 'overrides'`,
	`CREATE INDEX IF NOT EXISTS idx_edges_target_overrides ON edges(target_id, kind) WHERE kind = 'overrides'`,
	`CREATE TABLE IF NOT EXISTS mind_map (
		workspace_root TEXT PRIMARY KEY,
		json_data      TEXT NOT NULL,
		generated_at   TEXT NOT NULL,
		index_version  INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS node_history (
		id           INTEGER PRIMARY KEY,
		symbol_name  TEXT NOT NULL,
		file_path    TEXT NOT NULL,
		snapshot_at  TEXT NOT NULL,
		complexity   INTEGER NOT NULL DEFAULT 0,
		caller_count INTEGER NOT NULL DEFAULT 0,
		callee_count INTEGER NOT NULL DEFAULT 0,
		line_count   INTEGER NOT NULL DEFAULT 0,
		effects      TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX IF NOT EXISTS idx_history_sym ON node_history(symbol_name, snapshot_at)`,
	`CREATE TABLE IF NOT EXISTS constraints (
		id          TEXT PRIMARY KEY,
		rule        TEXT NOT NULL,
		kind        TEXT NOT NULL,
		priority    INTEGER NOT NULL,
		status      TEXT NOT NULL DEFAULT 'candidate',
		confidence  REAL NOT NULL DEFAULT 0.5,
		source      TEXT NOT NULL,
		scope       TEXT NOT NULL DEFAULT '',
		ttl_days    INTEGER NOT NULL DEFAULT 30,
		usage_count INTEGER NOT NULL DEFAULT 0,
		last_used   INTEGER NOT NULL DEFAULT 0,
		created_at  INTEGER NOT NULL DEFAULT 0,
		data_json   TEXT NOT NULL DEFAULT '{}'
	)`,
	`CREATE INDEX IF NOT EXISTS idx_constraints_status ON constraints(status)`,
	`CREATE INDEX IF NOT EXISTS idx_constraints_source ON constraints(source)`,
	`CREATE TABLE IF NOT EXISTS exploration_paths (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id     TEXT NOT NULL,
		session_id TEXT NOT NULL,
		paths_json TEXT NOT NULL,
		created_at INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS idx_exploration_run ON exploration_paths(run_id)`,

	// Doc↔Code cross-references: links Knowledge document passages to CKG symbols
	// via code_ref entities extracted by wesgine understand.ExtractEntities.
	`CREATE TABLE IF NOT EXISTS doc_references (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		doc_file    TEXT NOT NULL,
		symbol_name TEXT NOT NULL,
		symbol_id   INTEGER,
		context     TEXT NOT NULL DEFAULT '',
		-- doc_line 是符号在文档里首次出现的行（1-based）。此前这一列不存在，
		-- 而 DocReference.DocLine 是个永不被填也永不落盘的字段，于是这个工具只能
		-- 答"AGENTS.md 提到了 Cell"，答不出在哪一行——而没有行号的 file 点进去是
		-- 3000 行设计文档的第 1 行。0 表示没定位到。
		doc_line    INTEGER NOT NULL DEFAULT 0,
		UNIQUE(doc_file, symbol_name)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_doc_refs_symbol ON doc_references(symbol_name)`,
	`CREATE INDEX IF NOT EXISTS idx_doc_refs_file ON doc_references(doc_file)`,
}

// ckgTables is every table a rebuild must remove: the current set plus retired
// names (`files`/`file_fts` from the pre-schemaVersion era, `call_edges` from
// before the unified edge table). Retired names stay listed so a rebuild
// purges them instead of leaving orphan tables the next reader might find.
// Children come before parents: DROP TABLE runs an implicit DELETE FROM that
// fires foreign key actions, so dropping `edges` first means nothing references
// `symbols` by the time it goes. Triggers are dropped with their table.
var ckgTables = []string{
	"doc_references",
	"edges", "call_edges",
	"symbols", "symbol_fts", "file_hashes",
	"mind_map", "node_history", "constraints", "exploration_paths",
	"files", "file_fts",
}

// migrateSchema brings the production DB to schemaVersion, rebuilding from
// scratch when it is not already there.
//
// **全量重建不是无代价的**，尽管这个 DB 常被说成"可重建数据"：`ckgTables` 里的
// `constraints` 装着 `TeachFromEditRejection` / `TeachFromEditModification` 学到的
// 东西——用户拒绝或改写某次 AI 编辑时沉淀下来的约束，以及他按掉某条约束留下的墓碑
// （"别再提这条"）。那不是索引，是用户行为的沉淀，重建不回来。`exploration_paths`
// 同理（运行轨迹）。
//
// 所以纯粹是**派生表形状变了**的时候，走 rebuildDerivedTables 而不是 bump
// schemaVersion。2026-09-20 给 `doc_references` 加 `doc_line` 列时先 bump 了版本，
// 那会连带清掉上面两张表——而那次变更只需要重算 doc_references 一张表。
func migrateSchema(db *sql.DB) error {
	if schemaOutdated(db) {
		// Best effort: a missing table is the outcome we want anyway.
		for _, tbl := range ckgTables {
			db.Exec("DROP TABLE IF EXISTS " + tbl)
		}
	}
	if err := applySchema(db); err != nil {
		return err
	}
	return rebuildDerivedTables(db)
}

// derivedTableShapes 是**纯派生**表的当前形状：整表可以随时删掉重算，不丢用户数据。
//
// 每项是 (表名, 判据列)。判据列在表里不存在时整表重建——比 ALTER TABLE 简单且
// 幂等，代价只是下一次 Knowledge 刷新前这张表是空的。
//
// **只有确实可重算的表能进这张表**。`constraints` 与 `exploration_paths` 不在其中，
// 理由见 migrateSchema。
var derivedTableShapes = []struct {
	table  string
	column string
	source string // 谁能把它填回来（写进错误信息与注释，供下一个人判断代价）
}{
	{"doc_references", "doc_line", "DocRefIndex.RefreshAll（从 Knowledge 实体 + 文档重算）"},
}

// rebuildDerivedTables 把形状过期的派生表删掉重建。
//
// 判据是"该列在不在"，不是版本号：版本号是全库一档的，而派生表的形状各自独立演进。
// 用列做判据还让它对已经是新形状的库天然幂等。
func rebuildDerivedTables(db *sql.DB) error {
	for _, d := range derivedTableShapes {
		has, err := tableHasColumn(db, d.table, d.column)
		if err != nil {
			return fmt.Errorf("ckg derived %s: %w", d.table, err)
		}
		if has {
			continue
		}
		if _, err := db.Exec("DROP TABLE IF EXISTS " + d.table); err != nil {
			return fmt.Errorf("ckg derived drop %s: %w", d.table, err)
		}
		slog.Info("[ckg] rebuilding derived table (shape changed)",
			"table", d.table, "missing_column", d.column, "refilled_by", d.source)
		// 重新执行权威 DDL 把它建回来。applySchema 全是 IF NOT EXISTS，所以对其它
		// 表是空操作——这里不能只建这一张，否则 DDL 就有了第二份副本。
		if err := applySchema(db); err != nil {
			return err
		}
	}
	return nil
}

// tableHasColumn 回答"这张表有没有这一列"，并把"表不存在"与"列不存在"合并成 false
// ——两者的处置相同（建它），而分开只会让调用方写两个分支。
func tableHasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query("SELECT 1 FROM pragma_table_info(?) WHERE name = ?", table, column)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	return rows.Next(), rows.Err()
}

// createStagingSchema builds the schema in staging.db for the pipeline dump.
// The file is fresh each run, so there is nothing to drop.
func createStagingSchema(db *sql.DB) error {
	return applySchema(db)
}

// applySchema runs the authoritative DDL and stamps the version. The stamp is
// load-bearing on staging too: DumpToProduction promotes staging.db to code.db
// by rename, and an unstamped DB would look outdated on the next startup and
// get wiped right after being built.
func applySchema(db *sql.DB) error {
	for _, stmt := range ckgSchemaDDL {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("ckg schema %q: %w", stmt[:min(len(stmt), 60)], err)
		}
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("ckg schema user_version: %w", err)
	}
	return nil
}

// schemaOutdated reports whether the DB holds a shape other than schemaVersion.
// user_version=0 is ambiguous — it is both "brand new file" and "written before
// versioning existed" — so it is resolved by looking for the tables themselves.
func schemaOutdated(db *sql.DB) bool {
	var version int
	db.QueryRow("PRAGMA user_version").Scan(&version)
	if version == schemaVersion {
		return false
	}
	if version != 0 {
		return true
	}
	for _, tbl := range []string{"symbols", "edges", "files"} {
		var n int
		db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&n)
		if n > 0 {
			return true
		}
	}
	return false
}
