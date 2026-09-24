package codeintel

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"

	wesgine "github.com/weisyn/wesgine"
	"github.com/weisyn/wesgine/knowledge"
)

// DocReference records a documentation passage that mentions a CKG symbol.
// doc_references is a cross-index between Knowledge (documents) and CKG (code).
type DocReference struct {
	DocFile    string // document file path (relative to workspace)
	SymbolName string // CKG symbol name (e.g. "Cell", "UserService")
	SymbolID   *int   // CKG symbols.id if matched, nil if unresolved
	Context    string // 符号所处的 Markdown 章节标题（不是那一行本身，见 locateSymbols）
	DocLine    int    // 符号首次出现的行（1-based）；0 = 没定位到
}

// DocRefIndex maintains the doc_references table in code.db, linking
// documentation passages to CKG symbols via code_ref entities extracted
// by wesgine's Knowledge enrichment pipeline (understand.ExtractEntities).
type DocRefIndex struct {
	db     *sql.DB // CKG writer DB (code.db)
	cell   *wesgine.Cell
	logger *slog.Logger
}

// NewDocRefIndex creates a DocRefIndex. writerDB must be the CKG code.db writer.
func NewDocRefIndex(writerDB *sql.DB, cell *wesgine.Cell, logger *slog.Logger) *DocRefIndex {
	if logger == nil {
		logger = slog.Default()
	}
	return &DocRefIndex{db: writerDB, cell: cell, logger: logger}
}

// RefreshFile updates doc_references for a single document file. It reads
// code_ref entities from Knowledge (already extracted by wesgine enrichFile),
// matches them against the CKG symbols table, and persists the cross-references.
func (d *DocRefIndex) RefreshFile(ctx context.Context, kbFileID, docPath string) error {
	if d.cell == nil || d.cell.Knowledge() == nil {
		return nil
	}

	entities, err := d.cell.Knowledge().ListFileEntities(ctx, kbFileID)
	if err != nil {
		d.logger.Debug("[doc-ref] list entities failed", "file", docPath, "error", err)
		return nil
	}

	// Filter to code_ref entities only.
	var codeRefs []knowledge.EntityInfo
	for _, e := range entities {
		if e.Type == "code_ref" {
			codeRefs = append(codeRefs, e)
		}
	}

	// Clear stale references for this doc file.
	if _, err := d.db.ExecContext(ctx, `DELETE FROM doc_references WHERE doc_file = ?`, docPath); err != nil {
		return fmt.Errorf("doc_ref delete stale: %w", err)
	}

	if len(codeRefs) == 0 {
		return nil
	}

	// Match each code_ref against CKG symbols and insert.
	stmt, err := d.db.PrepareContext(ctx, `
		INSERT OR IGNORE INTO doc_references (doc_file, symbol_name, symbol_id, context, doc_line)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("doc_ref prepare: %w", err)
	}
	defer stmt.Close()

	// 位置与上下文只能在这里算：wesgine 的 `knowledge.EntityInfo` 只有
	// {Type, Value, Count}——没有位置也没有上下文，所以 `context` 此前写的是空串，
	// 不是有人忘了填，是无可填。自己读一遍文档是唯一的出路。
	//
	// 读失败不致命：条目仍然插入，只是没有位置（doc_line=0）。这个工具的降级形态
	// 就是它此前的全部形态，所以退化回去也不比原来差。
	locs := d.locateSymbols(docPath, codeRefs)

	inserted := 0
	for _, ref := range codeRefs {
		name := strings.TrimSpace(ref.Value)
		if name == "" {
			continue
		}

		// Try to resolve against CKG symbol table.
		var symbolID *int
		var sid int
		err := d.db.QueryRowContext(ctx,
			`SELECT id FROM symbols WHERE name = ? LIMIT 1`, name,
		).Scan(&sid)
		if err == nil {
			symbolID = &sid
		}

		loc := locs[name]
		if _, err := stmt.ExecContext(ctx, docPath, name, symbolID, loc.section, loc.line); err != nil {
			d.logger.Debug("[doc-ref] insert failed", "symbol", name, "error", err)
		} else {
			inserted++
		}
	}

	if inserted > 0 {
		d.logger.Info("[doc-ref] refreshed", "file", docPath, "refs", inserted)
	}
	return nil
}

// docLoc 是一个符号在文档里的位置与它所处的章节。
type docLoc struct {
	line    int    // 1-based；0 表示没定位到
	section string // 最近的上级 Markdown 标题，去掉 # 前缀
}

// locateSymbols 扫一遍文档，给每个 code_ref 找首次出现的行与所处章节。
//
// **context 取章节标题而不是那一行本身**：这个工具的用途是"改代码前理解设计意图"，
// 而意图写在章节里，不在提到符号的那一行。一行 `所有 Cell 共享 ProviderPool` 拿出来
// 看不出它属于「资源隔离」还是「性能优化」那一节，而那两节对同一行代码给出相反的建议。
//
// 匹配用 `strings.Contains` 而非整词：文档里的符号常带反引号、括号、所属包名
// （`cell.Runtime()`、`(*Hypervisor).Start`），要求整词会漏掉大多数真实写法。代价是
// `Cell` 会命中 `CellSpec` 所在的行——可接受，因为这是**定位**而非判定，指错一行的
// 代价远小于指不出任何一行（后者正是此前的状态）。
//
// 只记首次出现：一个符号在一篇文档里出现十次时，用户要的是"这篇文档在哪讲它"，
// 而十个位置里最靠前的那个通常就是定义性的那一段。
func (d *DocRefIndex) locateSymbols(docPath string, refs []knowledge.EntityInfo) map[string]docLoc {
	out := make(map[string]docLoc, len(refs))

	raw, err := os.ReadFile(docPath)
	if err != nil {
		// 文档读不到（已删除 / 权限）——退回无位置，与此前行为一致。
		d.logger.Debug("[doc-ref] read doc failed", "file", docPath, "error", err)
		return out
	}

	pending := make(map[string]bool, len(refs))
	for _, r := range refs {
		if name := strings.TrimSpace(r.Value); name != "" {
			pending[name] = true
		}
	}

	section := ""
	lineNo := 0
	inFence := false
	// bytes.Lines 而非 bufio.Scanner：后者超限时 Scan() 返回 false，与 EOF 同值，
	// 于是"这一行太长"被读成"文件结束了"——设计文档里一张宽表就能触发，而症状是
	// 后半篇文档的符号全部定位不到且没有任何错误（wesgine INV-LINE-01）。
	for line := range bytes.Lines(raw) {
		lineNo++
		text := strings.TrimRight(string(line), "\r\n")

		// 代码围栏必须跟：设计文档里满是 `# 注释` 与 `#!/bin/sh`，把它们当标题会让
		// 章节名变成一句 shell 注释，而 context 的全部价值就在于它是章节名。
		if strings.HasPrefix(strings.TrimSpace(text), "```") {
			inFence = !inFence
			continue
		}
		if !inFence && strings.HasPrefix(text, "#") {
			section = strings.TrimSpace(strings.TrimLeft(text, "# "))
			continue
		}

		if len(pending) == 0 {
			continue
		}
		// 围栏内的符号照样算引用——代码示例正是文档在讲这个符号的证据。
		for name := range pending {
			if strings.Contains(text, name) {
				out[name] = docLoc{line: lineNo, section: section}
				delete(pending, name)
			}
		}
	}
	return out
}

// RefreshAll rebuilds doc_references for all indexed Knowledge files.
func (d *DocRefIndex) RefreshAll(ctx context.Context) error {
	if d.cell == nil || d.cell.Knowledge() == nil {
		return nil
	}

	files, err := d.cell.Knowledge().ListFiles(ctx, knowledge.FileListOptions{Limit: 10000})
	if err != nil {
		return fmt.Errorf("doc_ref list files: %w", err)
	}

	for _, f := range files {
		if err := d.RefreshFile(ctx, f.ID, f.Path); err != nil {
			d.logger.Debug("[doc-ref] refresh failed", "file", f.Path, "error", err)
		}
	}
	return nil
}

// FindBySymbol returns doc references mentioning the given symbol name.
func (d *DocRefIndex) FindBySymbol(ctx context.Context, symbolName string, limit int) ([]DocReference, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := d.db.QueryContext(ctx, `
		SELECT doc_file, symbol_name, symbol_id, context, doc_line FROM doc_references
		WHERE symbol_name = ? ORDER BY doc_file LIMIT ?
	`, symbolName, limit)
	if err != nil {
		return nil, fmt.Errorf("doc_ref find: %w", err)
	}
	defer rows.Close()

	return scanDocRefs(rows)
}

// SearchBySymbolPrefix returns doc references whose symbol name starts with prefix.
func (d *DocRefIndex) SearchBySymbolPrefix(ctx context.Context, prefix string, limit int) ([]DocReference, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := d.db.QueryContext(ctx, `
		SELECT doc_file, symbol_name, symbol_id, context, doc_line FROM doc_references
		WHERE symbol_name LIKE ? ORDER BY symbol_name, doc_file LIMIT ?
	`, prefix+"%", limit)
	if err != nil {
		return nil, fmt.Errorf("doc_ref search: %w", err)
	}
	defer rows.Close()

	return scanDocRefs(rows)
}

func scanDocRefs(rows *sql.Rows) ([]DocReference, error) {
	var refs []DocReference
	for rows.Next() {
		var r DocReference
		var sid sql.NullInt64
		if err := rows.Scan(&r.DocFile, &r.SymbolName, &sid, &r.Context); err != nil {
			return refs, err
		}
		if sid.Valid {
			v := int(sid.Int64)
			r.SymbolID = &v
		}
		refs = append(refs, r)
	}
	return refs, rows.Err()
}
