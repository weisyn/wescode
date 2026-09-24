package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/weisyn/wescode/internal/treesitter"
	"github.com/weisyn/wesgine/tool"
)

// ── trace_variable (local intra-procedural) ─────────────────────────────────

// NewTraceVariableTool creates a "trace_variable" tool that traces a variable's
// data flow within a function (def-use chain) and across files (LSP References).
func NewTraceVariableTool(ts *treesitter.ParserPool, lsp LSPBridge, fp tool.FileProvider) tool.Tool {
	return &traceVariableTool{
		BaseTool: tool.BaseTool{
			Meta: tool.ToolMeta{
				Dimension:      tool.DimensionPerceive,
				Availability:   tool.AvailabilityConfigurable,
				ReadOnly:       true,
				Concurrent:     true,
				Timeout:        5 * time.Second,
				Risk:           tool.RiskSafe,
				PolicyFamily:   tool.PolicyFamilyOther,
				Enabled:        true,
				Domain:         "analysis",
				DomainToolTier: tool.DomainTierCore,
			},
		},
		ts:  ts,
		lsp: lsp,
		fp:  fp,
	}
}

type traceVariableTool struct {
	tool.BaseTool
	ts  *treesitter.ParserPool
	lsp LSPBridge
	fp  tool.FileProvider
}

func (t *traceVariableTool) Name() string { return "trace_variable" }

func (t *traceVariableTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "trace_variable",
		Description: "Trace a variable's data flow: where it's defined, assigned, used, and returned within a function, plus cross-file references via LSP.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"file": {"type": "string", "description": "Absolute path to the file containing the variable"},
				"line": {"type": "integer", "description": "0-based line number where the variable appears"},
				"variable": {"type": "string", "description": "Name of the variable to trace"}
			},
			"required": ["file", "line", "variable"]
		}`),
	}
}

func (t *traceVariableTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		File     string `json:"file"`
		Line     int    `json:"line"`
		Variable string `json:"variable"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "Invalid parameters: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.File == "" || params.Variable == "" {
		return &tool.ToolResult{Content: "file and variable are required", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}

	content, err := t.readFile(params.File)
	if err != nil {
		return &tool.ToolResult{Content: "Failed to read file: " + err.Error(), IsError: true}, nil
	}

	lang, ok := treesitter.DetectLang(params.File)
	if !ok {
		return &tool.ToolResult{Content: "Unsupported language for " + params.File, IsError: true}, nil
	}

	tree, err := t.ts.Parse(lang, content, nil)
	if err != nil {
		return &tool.ToolResult{Content: "Parse failed: " + err.Error(), IsError: true}, nil
	}
	defer tree.Close()

	syms := treesitter.ExtractSymbols(lang, tree, content)
	fn := treesitter.FindFunctionAt(syms, params.Line)
	if fn == nil {
		return &tool.ToolResult{Content: fmt.Sprintf("No function found at line %d in %s", params.Line, params.File)}, nil
	}

	fnContent := content[fn.StartByte:fn.EndByte]
	flow := treesitter.ExtractVarFlow(lang, t.ts, fnContent)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[Data Flow: `%s` in %s (function %s, lines %d-%d)]\n\n",
		params.Variable, params.File, fn.Name, fn.StartLine+1, fn.EndLine+1))

	// 一项 = 一次定义/赋值/使用。顺序即数据流（列表保序），kind 徽标区分事件类型——
	// DEF 与 USE 的处置完全不同（前者是"值从哪来"，后者是"值被谁读"）。
	flowItems := make([]ListItem, 0, len(flow))
	count := 0
	for _, e := range flow {
		if e.Name != params.Variable {
			continue
		}
		absLine := fn.StartLine + e.Line + 1
		var evt, detail string
		switch e.Kind {
		case treesitter.VarFlowDef:
			evt, detail = "DEF", e.RHSExpr
			sb.WriteString(fmt.Sprintf("DEF    line %d: %s := %s\n", absLine, e.Name, e.RHSExpr))
		case treesitter.VarFlowDefUse:
			evt, detail = "ASSIGN", e.RHSExpr
			sb.WriteString(fmt.Sprintf("ASSIGN line %d: %s = %s\n", absLine, e.Name, e.RHSExpr))
		case treesitter.VarFlowReturn:
			evt = "RETURN"
			sb.WriteString(fmt.Sprintf("RETURN line %d: return %s\n", absLine, e.Name))
		case treesitter.VarFlowParam:
			evt, detail = "PARAM", "function parameter"
			sb.WriteString(fmt.Sprintf("PARAM  line %d: %s (function parameter)\n", absLine, e.Name))
		case treesitter.VarFlowUse:
			evt = "USE"
			sb.WriteString(fmt.Sprintf("USE    line %d: %s\n", absLine, e.Name))
		}
		// absLine 已含 +1（`fn.StartLine + e.Line + 1`），是文件内的 1-based 绝对行，
		// 与文本输出同源。这里不能再加。
		flowItems = append(flowItems, ListItem{
			Label:  e.Name,
			Detail: detail,
			File:   params.File,
			Line:   absLine,
			Kind:   evt,
		})
		count++
	}

	if count == 0 {
		// 空列表而非 nil：落 empty（"这个变量在这个函数里没有流动"）。
		list = &ListData{Items: []ListItem{}}
		sb.WriteString(fmt.Sprintf("No data flow entries found for `%s` in this function.\n", params.Variable))
	} else {
		list = ListOf(flowItems, 0)
	}

	// INV-DF-03: Cross-file references via LSP (fallback: skip if unavailable)
	_, isNoop := t.lsp.(NoopLSP)
	if !isNoop {
		refs, lspErr := t.lsp.References(ctx, params.File, params.Line, 0)
		if lspErr == nil && len(refs) > 0 {
			sb.WriteString("\n[Cross-file References]\n")
			shown := 0
			for _, ref := range refs {
				if ref.Path == params.File {
					continue
				}
				if shown >= 10 {
					sb.WriteString(fmt.Sprintf("... and %d more references\n", len(refs)-shown))
					break
				}
				sb.WriteString(fmt.Sprintf("REF    %s:%d\n", ref.Path, ref.Line+1))
				shown++
			}
		}
	}

	return &tool.ToolResult{Content: sb.String()}, nil
}

func (t *traceVariableTool) readFile(path string) ([]byte, error) {
	if t.fp != nil {
		return t.fp.ReadFile(context.Background(), path)
	}
	return os.ReadFile(path)
}

// ── trace_data_flow (inter-procedural via CKG DATA_FLOWS_TO edges) ──────────

// NewDataFlowTool creates a "trace_data_flow" tool that traces data flow
// forward or backward from a symbol using CKG DATA_FLOWS_TO edges.
func NewDataFlowTool(index *CodeIndex) tool.Tool {
	return &dataFlowTool{
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
				Domain:         "analysis",
				DomainToolTier: tool.DomainTierExtended,
			},
		},
		index: index,
	}
}

type dataFlowTool struct {
	tool.BaseTool
	index *CodeIndex
}

func (t *dataFlowTool) Name() string { return "trace_data_flow" }

func (t *dataFlowTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "trace_data_flow",
		Description: "Trace inter-procedural data flow forward or backward from a symbol using code graph edges. Shows where values flow through function parameters across call boundaries. Argument→parameter pairing is positional and read from signatures, so it is most reliable in statically typed files and weakest in untyped Python.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"symbol": {"type": "string", "description": "Symbol to trace data flow from"},
				"direction": {"type": "string", "enum": ["forward", "backward"], "description": "Trace direction: forward (where does this value go?) or backward (where does this value come from?)"},
				"max_depth": {"type": "integer", "description": "Max hops (default 3)"},
				"verbosity": {"type": "string", "enum": ["summary", "detail", "full"], "description": "Output detail level"}
			},
			"required": ["symbol"]
		}`),
	}
}

func (t *dataFlowTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。本工具不产出列表（跨符号数据流树，缩进表达深度），
	// 传 nil 得到 category=text + 分级——分级本身仍是信息（PC-02）。
	var notReady bool
	defer func() { res = PresentList(res, notReady, nil) }()

	var params struct {
		Symbol    string `json:"symbol"`
		Direction string `json:"direction"`
		MaxDepth  int    `json:"max_depth"`
		Verbosity string `json:"verbosity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "Invalid parameters: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Symbol == "" {
		return &tool.ToolResult{Content: "symbol is required", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Direction == "" {
		params.Direction = "forward"
	}
	if params.MaxDepth <= 0 || params.MaxDepth > 10 {
		params.MaxDepth = 3
	}
	if params.Verbosity == "" {
		params.Verbosity = "detail"
	}

	rdb := t.index.DB()
	if rdb == nil {
		notReady = true
		notReady = true
		return &tool.ToolResult{Content: "Code index not available"}, nil
	}

	// Find the symbol(s) matching the query.
	rows, err := rdb.QueryContext(ctx, `SELECT id, file_path, kind, name, signature, line_start
		FROM symbols WHERE name = ? LIMIT 10`, params.Symbol)
	if err != nil {
		return &tool.ToolResult{Content: "Query failed: " + err.Error(), IsError: true}, nil
	}

	type symRef struct {
		id        int64
		filePath  string
		kind      string
		name      string
		signature string
		lineStart int
	}
	var roots []symRef
	for rows.Next() {
		var s symRef
		if err := rows.Scan(&s.id, &s.filePath, &s.kind, &s.name, &s.signature, &s.lineStart); err != nil {
			continue
		}
		roots = append(roots, s)
	}
	rows.Close()

	if len(roots) == 0 {
		return &tool.ToolResult{Content: fmt.Sprintf("No symbol found matching `%s`", params.Symbol)}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[Data Flow Trace: `%s` (%s, max_depth=%d)]\n\n",
		params.Symbol, params.Direction, params.MaxDepth))

	for _, root := range roots {
		sb.WriteString(fmt.Sprintf("Root: %s %s @ %s:%d\n", root.kind, root.name, root.filePath, root.lineStart+1))

		visited := make(map[int64]bool)
		visited[root.id] = true

		type bfsItem struct {
			id    int64
			depth int
			path  string
		}
		queue := []bfsItem{{id: root.id, depth: 0, path: root.name}}

		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			if cur.depth >= params.MaxDepth {
				continue
			}

			edges := t.queryEdges(ctx, cur.id, params.Direction == "forward")

			for _, edge := range edges {
				if visited[edge.targetID] {
					continue
				}
				visited[edge.targetID] = true

				indent := strings.Repeat("  ", cur.depth+1)
				arrow := "→"
				if params.Direction == "backward" {
					arrow = "←"
				}

				switch params.Verbosity {
				case "summary":
					sb.WriteString(fmt.Sprintf("%s%s %s (param[%d])\n",
						indent, arrow, edge.targetName, edge.paramIdx))
				case "full":
					sb.WriteString(fmt.Sprintf("%s%s %s %s @ %s:%d  [%s param[%d]]\n",
						indent, arrow, edge.targetKind, edge.targetName,
						edge.targetFile, edge.targetLine+1,
						edge.flowType, edge.paramIdx))
				default: // detail
					sb.WriteString(fmt.Sprintf("%s%s %s %s @ %s:%d  [%s]\n",
						indent, arrow, edge.targetKind, edge.targetName,
						edge.targetFile, edge.targetLine+1, edge.flowType))
				}

				queue = append(queue, bfsItem{
					id:    edge.targetID,
					depth: cur.depth + 1,
					path:  cur.path + " " + arrow + " " + edge.targetName,
				})
			}
		}
		sb.WriteString("\n")
	}

	return &tool.ToolResult{Content: sb.String()}, nil
}

type dataFlowEdge struct {
	targetID   int64
	targetName string
	targetKind string
	targetFile string
	targetLine int
	flowType   string
	paramIdx   int
}

func (t *dataFlowTool) queryEdges(ctx context.Context, nodeID int64, forward bool) []dataFlowEdge {
	db := t.index.DB()
	if db == nil {
		return nil
	}

	var query string
	if forward {
		query = `SELECT s.id, s.name, s.kind, s.file_path, s.line_start, e.flow_type, e.param_idx
			FROM edges e JOIN symbols s ON s.id = e.target_id
			WHERE e.source_id = ? AND e.kind = 'data_flows_to'
			LIMIT 50`
	} else {
		query = `SELECT s.id, s.name, s.kind, s.file_path, s.line_start, e.flow_type, e.param_idx
			FROM edges e JOIN symbols s ON s.id = e.source_id
			WHERE e.target_id = ? AND e.kind = 'data_flows_to'
			LIMIT 50`
	}

	rows, err := db.QueryContext(ctx, query, nodeID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var result []dataFlowEdge
	for rows.Next() {
		var edge dataFlowEdge
		if err := rows.Scan(&edge.targetID, &edge.targetName, &edge.targetKind,
			&edge.targetFile, &edge.targetLine, &edge.flowType, &edge.paramIdx); err != nil {
			continue
		}
		result = append(result, edge)
	}
	return result
}
