package codeintel

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

func NewImplementationsTool(index *CodeIndex, lsp LSPBridge) tool.Tool {
	return &implementationsTool{
		BaseTool: tool.BaseTool{
			Meta: tool.ToolMeta{
				Dimension:      tool.DimensionPerceive,
				Availability:   tool.AvailabilityConfigurable,
				ReadOnly:       true,
				Concurrent:     true,
				Timeout:        10 * time.Second,
				Risk:           tool.RiskSafe,
				PolicyFamily:   tool.PolicyFamilyOther,
				Enabled:        true,
				Domain:         "graph",
				DomainToolTier: tool.DomainTierExtended,
			},
		},
		index: index,
		lsp:   lsp,
	}
}

type implementationsTool struct {
	tool.BaseTool
	index *CodeIndex
	lsp   LSPBridge
}

func (t *implementationsTool) Name() string { return "find_implementations" }

func (t *implementationsTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "find_implementations",
		Description: "Find types that implement a given interface using 'implements' edges in the code knowledge graph.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"interface": {"type": "string", "description": "Name of the interface to find implementations for"},
				"limit": {"type": "integer", "description": "Max results (default 20)"},
				"verbosity": {"type": "string", "enum": ["summary", "detail", "full"], "description": "Output detail level (default: detail)"}
			},
			"required": ["interface"]
		}`),
	}
}

type implEntry struct {
	FilePath   string
	Kind       string
	Name       string
	Signature  string
	Parent     string
	LineStart  int
	LineEnd    int
	Exported   bool
	Visibility string
}

// implsOutcome 是 PC-01 声明的载体。零值产出 `text` + 由结果派生的分级，所以将来
// 有人加第八个 return 分支忘了填，后果是"少一个列表"而不是"一个说谎的声明"。
//
// notReady 必须由分支主动说：索引没建完这件事结果本身看不出来（IsError=false、
// Content 是一句正常的话），而它与"查到 0 个实现"在前端完全同形——一个是"答案不可信"，
// 另一个是"这个接口没人实现，可以删"。
type implsOutcome struct {
	list     ListData
	hasList  bool
	notReady bool
}

func (t *implementationsTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// 唯一附加点：7 个 return 里 4 个语义各不相同，在每个分支各写一次声明就是
	// INV-QUOTA-05「判在三个调用方之一」的同一个形状——漏掉的那处不报错，只是
	// 安静地让这个工具退回裸文本。
	var out implsOutcome
	defer func() { res = presentImpls(res, out) }()

	var params struct {
		Interface string `json:"interface"`
		Limit     int    `json:"limit"`
		Verbosity string `json:"verbosity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Interface == "" {
		return &tool.ToolResult{Content: "interface name is required", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if t.index == nil || t.index.readerDB == nil {
		out.notReady = true
		return &tool.ToolResult{Content: "code index is not available"}, nil
	}
	// readerDB may be swapped by ReopenAt/Close; hold RLock for the whole
	// query (incl. tryLSPImplementations' readerDB lookups) so the handle
	// cannot be closed mid-flight.
	t.index.readerMu.RLock()
	defer t.index.readerMu.RUnlock()
	if params.Limit <= 0 {
		params.Limit = 20
	}

	verbosity := ParseVerbosity(params.Verbosity)

	// Strategy 1: LSP textDocument/implementation (type-precise).
	if lspResults := t.tryLSPImplementations(ctx, params.Interface, params.Limit); len(lspResults) > 0 {
		// TruncateWithTotal 的第二个返回值是截断前的总数。此前这里打印的是截断
		// 之后的长度，于是 summary 档（上限 5）下 20 个实现输出成 "(5 found)"，
		// 模型和用户都读成"就这 5 个"，然后改接口时漏掉 15 个实现方。
		lspResults, total := TruncateWithTotal(lspResults, verbosity)
		out.list = ListItems(implListItems(lspResults), total)
		out.hasList = true
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Implementations of %q (%s found):\n\n", params.Interface, CountPhrase(len(lspResults), total)))
		for i, e := range lspResults {
			sb.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, e.Kind, e.Name))
			sb.WriteString(fmt.Sprintf("   File: %s:%d-%d\n", e.FilePath, e.LineStart+1, e.LineEnd+1))
			if e.Signature != "" {
				sb.WriteString(fmt.Sprintf("   Signature: %s\n", e.Signature))
			}
			sb.WriteByte('\n')
		}
		// INV-UX-01 L-2: source/precision lives in Metadata, not Content.
		return &tool.ToolResult{Content: sb.String(), Metadata: map[string]any{"source": "lsp"}}, nil
	}

	// Strategy 2: CKG implements edges (syntax-level fallback).
	db := t.index.readerDB

	// First resolve the interface's symbol ID for precise edge lookup.
	var ifaceID int64
	db.QueryRowContext(ctx,
		`SELECT id FROM symbols WHERE name = ? AND kind = 'interface' LIMIT 1`,
		params.Interface).Scan(&ifaceID)

	// err 不在此处重新声明：具名返回已占用这个名字（defer 需要它来收敛声明）。
	var rows *sql.Rows
	if ifaceID > 0 {
		rows, err = db.QueryContext(ctx, `
			SELECT s.file_path, s.kind, s.name, s.signature, s.parent,
			       s.line_start, s.line_end, s.exported, s.visibility
			FROM edges e
			JOIN symbols s ON e.source_id = s.id
			WHERE e.kind = 'implements' AND e.target_id = ?
			LIMIT ?
		`, ifaceID, params.Limit)
	} else {
		// Fallback to target_name when interface symbol not indexed.
		rows, err = db.QueryContext(ctx, `
			SELECT s.file_path, s.kind, s.name, s.signature, s.parent,
			       s.line_start, s.line_end, s.exported, s.visibility
			FROM edges e
			JOIN symbols s ON e.source_id = s.id
			WHERE e.kind = 'implements' AND e.target_name = ?
			LIMIT ?
		`, params.Interface, params.Limit)
	}
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("query failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}
	defer rows.Close()

	var results []implEntry
	for rows.Next() {
		var e implEntry
		if err := rows.Scan(&e.FilePath, &e.Kind, &e.Name, &e.Signature, &e.Parent,
			&e.LineStart, &e.LineEnd, &e.Exported, &e.Visibility); err != nil {
			continue
		}
		results = append(results, e)
	}
	if err := rows.Err(); err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("query error: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}

	if len(results) == 0 {
		results = t.heuristicFallback(ctx, params.Interface, params.Limit)
	}

	if len(results) == 0 {
		return &tool.ToolResult{Content: fmt.Sprintf("No implementations found for interface %q", params.Interface)}, nil
	}

	results, total := TruncateWithTotal(results, verbosity)
	out.list = ListItems(implListItems(results), total)
	out.hasList = true

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Implementations of %q (%s found):\n\n", params.Interface, CountPhrase(len(results), total)))
	for i, e := range results {
		switch verbosity {
		case VerbositySummary:
			sb.WriteString(fmt.Sprintf("%d. %s (%s)\n", i+1, e.Name, e.FilePath))
		default:
			sb.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, e.Kind, e.Name))
			sb.WriteString(fmt.Sprintf("   File: %s:%d-%d\n", e.FilePath, e.LineStart+1, e.LineEnd+1))
			if e.Signature != "" {
				sb.WriteString(fmt.Sprintf("   Signature: %s\n", e.Signature))
			}
			if e.Parent != "" {
				sb.WriteString(fmt.Sprintf("   Parent: %s\n", e.Parent))
			}
			sb.WriteString(fmt.Sprintf("   Exported: %v | Visibility: %s\n\n", e.Exported, e.Visibility))
		}
	}
	return &tool.ToolResult{Content: sb.String()}, nil
}

// implListItems 把实现条目投影成 PC-01 的列表载荷。
//
// LineStart 是 0-based（CKG 与 LSP 都是），wire 是 1-based——转换只在这一处做，
// 与文本输出里那个 `e.LineStart+1` 是同一笔账。差一会让点击跳错一行，而跳错一行
// 在长文件里看起来像"跳到了不相关的地方"，没人会怀疑是 off-by-one。
func implListItems(entries []implEntry) []ListItem {
	items := make([]ListItem, 0, len(entries))
	for _, e := range entries {
		items = append(items, ListItem{
			Label:  e.Name,
			Detail: e.Signature,
			File:   e.FilePath,
			Line:   e.LineStart + 1,
			Kind:   e.Kind,
		})
	}
	return items
}

// presentImpls 把 outcome 交给共用的 PresentList——分级优先级与载体形状都在那里单点定义。
func presentImpls(res *tool.ToolResult, out implsOutcome) *tool.ToolResult {
	if !out.hasList {
		return PresentList(res, out.notReady, nil)
	}
	return PresentList(res, out.notReady, &out.list)
}

func (t *implementationsTool) heuristicFallback(ctx context.Context, ifaceName string, limit int) []implEntry {
	db := t.index.readerDB

	// Find methods declared by the interface.
	methodRows, err := db.QueryContext(ctx, `
		SELECT name FROM symbols
		WHERE kind = 'method' AND parent = ?
	`, ifaceName)
	if err != nil {
		return nil
	}
	defer methodRows.Close()

	var ifaceMethods []string
	for methodRows.Next() {
		var m string
		if err := methodRows.Scan(&m); err != nil {
			continue
		}
		ifaceMethods = append(ifaceMethods, m)
	}
	if len(ifaceMethods) == 0 {
		return nil
	}

	// Find types that have all the interface methods as children.
	placeholders := make([]string, len(ifaceMethods))
	args := make([]any, len(ifaceMethods))
	for i, m := range ifaceMethods {
		placeholders[i] = "?"
		args[i] = m
	}

	query := fmt.Sprintf(`
		SELECT s.file_path, s.kind, s.name, s.signature, s.parent,
		       s.line_start, s.line_end, s.exported, s.visibility
		FROM symbols s
		WHERE s.kind IN ('struct', 'type', 'class')
		  AND (SELECT COUNT(DISTINCT m.name) FROM symbols m
		       WHERE m.kind = 'method' AND m.parent = s.name
		         AND m.name IN (%s)) = ?
		LIMIT ?
	`, strings.Join(placeholders, ","))
	args = append(args, len(ifaceMethods), limit)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var results []implEntry
	for rows.Next() {
		var e implEntry
		if err := rows.Scan(&e.FilePath, &e.Kind, &e.Name, &e.Signature, &e.Parent,
			&e.LineStart, &e.LineEnd, &e.Exported, &e.Visibility); err != nil {
			continue
		}
		results = append(results, e)
	}
	return results
}

// tryLSPImplementations queries LSP textDocument/implementation for the given interface.
func (t *implementationsTool) tryLSPImplementations(ctx context.Context, ifaceName string, limit int) []implEntry {
	if t.lsp == nil {
		return nil
	}
	if _, isNoop := t.lsp.(NoopLSP); isNoop {
		return nil
	}
	if t.index == nil || t.index.readerDB == nil {
		return nil
	}

	// Find the interface definition position in CKG.
	var file string
	var line int
	t.index.readerDB.QueryRowContext(ctx,
		`SELECT file_path, line_start FROM symbols WHERE name = ? AND kind = 'interface' LIMIT 1`,
		ifaceName).Scan(&file, &line)
	if file == "" {
		return nil
	}

	// Use source file for accurate column.
	col := 0
	if srcCol := findColumnInSourceFile(file, line, ifaceName); srcCol >= 0 {
		col = srcCol
	}

	// LSP textDocument/implementation on the interface definition.
	impls, err := t.lsp.Implementation(ctx, file, line, col)
	if err != nil || len(impls) == 0 {
		return nil
	}

	var results []implEntry
	for _, loc := range impls {
		if len(results) >= limit {
			break
		}
		var e implEntry
		t.index.readerDB.QueryRowContext(ctx,
			`SELECT file_path, kind, name, signature, parent, line_start, line_end, exported, visibility
			 FROM symbols WHERE file_path = ? AND line_start = ? LIMIT 1`,
			IndexPath(loc.Path), loc.Line).Scan(&e.FilePath, &e.Kind, &e.Name, &e.Signature, &e.Parent,
			&e.LineStart, &e.LineEnd, &e.Exported, &e.Visibility)
		if e.Name != "" {
			results = append(results, e)
		}
	}
	return results
}
