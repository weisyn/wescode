package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// NewCalleesTool creates a "find_callees" tool that returns functions called
// by a given symbol. Architecture: LSP OutgoingCalls first, CKG fallback.
func NewCalleesTool(index *CodeIndex, lsp LSPBridge) tool.Tool {
	return &calleesToolV2{
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

type calleesToolV2 struct {
	tool.BaseTool
	index *CodeIndex
	lsp   LSPBridge
}

func (t *calleesToolV2) Name() string { return "find_callees" }

func (t *calleesToolV2) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "find_callees",
		Description: "Find all functions and methods called by a given symbol. Provide file+line for exact resolution, or just the symbol name to search all definitions.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"symbol": {"type": "string", "description": "Name of the caller function or method"},
				"file": {"type": "string", "description": "File path of the caller (optional, speeds up resolution)"},
				"line": {"type": "integer", "description": "Start line of the caller, 0-based (optional, used with file)"},
				"limit": {"type": "integer", "description": "Max callee results per definition (default 20)"},
				"verbosity": {"type": "string", "enum": ["summary", "detail", "full"], "description": "Output detail level (default: detail)"}
			},
			"required": ["symbol"]
		}`),
	}
}

func (t *calleesToolV2) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。not_ready 必须由分支主动说：索引没建完这件事结果本身看不出来
	// （IsError=false、Content 是一句正常的话），而它与"查到 0 个"在前端完全同形。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		Symbol    string `json:"symbol"`
		File      string `json:"file"`
		Line      int    `json:"line"`
		Limit     int    `json:"limit"`
		Verbosity string `json:"verbosity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Symbol == "" {
		return &tool.ToolResult{Content: "symbol name is required", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if t.index == nil {
		notReady = true
		return &tool.ToolResult{Content: "code index is not available"}, nil
	}
	if params.Limit <= 0 {
		params.Limit = 20
	}

	verbosity := ParseVerbosity(params.Verbosity)

	// Strategy 1: LSP OutgoingCalls (type-precise).
	if lspCallees := t.tryLSPCallees(ctx, params.File, params.Symbol, params.Line, params.Limit); len(lspCallees) > 0 {
		lspCallees, total := TruncateWithTotal(lspCallees, verbosity)
		// callees 只有名字，没有位置——CalleesOf 与 LSP OutgoingCalls 都只返回被调用
		// 者的名字字符串。不填 File 会让整行不可点击，那是诚实的：没有位置就跳不过去，
		// 编一个（比如猜同文件第 1 行）会把用户送到错的地方。
		list = calleeNameList(lspCallees, total)
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Found %s callee(s) of %s:\n\n", CountPhrase(len(lspCallees), total), params.Symbol))
		for i, name := range lspCallees {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, name))
		}
		// INV-UX-01 L-2: source/precision lives in Metadata, not Content.
		return &tool.ToolResult{Content: sb.String(), Metadata: map[string]any{"source": "lsp"}}, nil
	}

	// Strategy 2: CKG fallback.
	if params.File != "" {
		callees, err := t.index.CalleesOf(ctx, params.File, params.Symbol, params.Line)
		if err != nil {
			return &tool.ToolResult{Content: fmt.Sprintf("find callees failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
		}
		if len(callees) == 0 {
			return &tool.ToolResult{Content: fmt.Sprintf("No callees found for %s in %s:%d", params.Symbol, params.File, params.Line)}, nil
		}
		callees, total := TruncateWithTotal(callees, verbosity)
		list = calleeNameList(callees, total)
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Found %s callee(s) of %s (%s:%d):\n\n", CountPhrase(len(callees), total), params.Symbol, params.File, params.Line))
		for i, name := range callees {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, name))
		}
		return &tool.ToolResult{Content: sb.String()}, nil
	}

	// Symbol-only: find definitions first, then callees for each.
	defs, err := t.index.FindSymbol(ctx, params.Symbol)
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("symbol lookup failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}
	if len(defs) == 0 {
		return &tool.ToolResult{Content: fmt.Sprintf("No symbol found matching %q", params.Symbol)}, nil
	}

	var sb strings.Builder
	totalCallees := 0
	// 跨定义合并成一个列表：用户问的是"这个符号调用了什么"，定义分组是实现细节。
	// detail 带上定义名，否则同名 callee 出现在两个定义下就分不清是谁调的。
	var allItems []ListItem
	for _, def := range defs {
		callees, err := t.index.CalleesOf(ctx, def.FilePath, def.Name, def.LineStart)
		if err != nil {
			continue
		}
		if len(callees) == 0 {
			continue
		}
		if len(callees) > params.Limit {
			callees = callees[:params.Limit]
		}
		callees, groupTotal := TruncateWithTotal(callees, verbosity)
		for _, name := range callees {
			allItems = append(allItems, ListItem{
				Label:  name,
				Detail: "called by " + def.Name,
			})
		}

		sb.WriteString(fmt.Sprintf("── %s %s (%s:%d) ──\n", def.Kind, def.Name, def.FilePath, def.LineStart+1))
		for i, name := range callees {
			sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, name))
		}
		if groupTotal > len(callees) {
			sb.WriteString(fmt.Sprintf("  ... and %d more\n", groupTotal-len(callees)))
		}
		sb.WriteByte('\n')
		// 累加截断前的数：此前这里加的是 len(callees)，于是尾部那句
		// "%d total callee(s)" 也跟着变小——组内错和总数错是同一次遗漏的两个面。
		totalCallees += groupTotal
	}

	if totalCallees == 0 {
		return &tool.ToolResult{Content: fmt.Sprintf("No callees found for any definition of %q", params.Symbol)}, nil
	}

	list = ListOf(allItems, totalCallees)
	header := fmt.Sprintf("Callees of %q (%d definition(s), %d total callee(s)):\n\n", params.Symbol, len(defs), totalCallees)
	return &tool.ToolResult{Content: header + sb.String()}, nil
}

// calleeNameList 把只有名字的 callee 列表包成载荷。
//
// 不填 File 是刻意的：`CalleesOf` 与 LSP OutgoingCalls 都只返回被调用者的**名字**，
// 没有位置。整行因此不可点击——那是诚实的，编一个位置（比如猜同文件第 1 行）会把
// 用户送到错的地方，而他不会怀疑是工具编的。
func calleeNameList(names []string, total int) *ListData {
	items := make([]ListItem, 0, len(names))
	for _, n := range names {
		items = append(items, ListItem{Label: n})
	}
	return ListOf(items, total)
}

// tryLSPCallees uses LSP OutgoingCalls to find callees with type precision.
func (t *calleesToolV2) tryLSPCallees(ctx context.Context, file, symbol string, line, limit int) []string {
	if t.lsp == nil {
		return nil
	}
	if _, isNoop := t.lsp.(NoopLSP); isNoop {
		return nil
	}

	// Resolve file+line+col if not provided.
	col := 0
	if file == "" {
		if t.index != nil && t.index.hasReader() {
			t.index.readerMu.RLock()
			t.index.readerDB.QueryRowContext(ctx,
				`SELECT file_path, line_start FROM symbols WHERE name = ? AND kind IN ('function','method') LIMIT 1`,
				symbol).Scan(&file, &line)
			t.index.readerMu.RUnlock()
		}
		if file == "" {
			return nil
		}
	}

	// Use source file to find accurate column position.
	if srcCol := findColumnInSourceFile(file, line, symbol); srcCol >= 0 {
		col = srcCol
	}

	items, err := t.lsp.PrepareCallHierarchy(ctx, file, line, col)
	if err != nil || len(items) == 0 {
		return nil
	}

	outgoing, err := t.lsp.OutgoingCalls(ctx, items[0])
	if err != nil || len(outgoing) == 0 {
		return nil
	}

	var names []string
	for _, call := range outgoing {
		if len(names) >= limit {
			break
		}
		names = append(names, call.To.Name)
	}
	return names
}
