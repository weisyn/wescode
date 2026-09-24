package codeintel

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// NewCallersTool creates a "find_callers" tool that returns symbols calling a given function.
// Architecture: LSP Call Hierarchy first (type-precise), CKG graph fallback (syntax-level).
func NewCallersTool(index *CodeIndex, lsp LSPBridge) tool.Tool {
	return &callersTool{
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
				DomainToolTier: tool.DomainTierCore,
			},
		},
		index: index,
		lsp:   lsp,
	}
}

type callersTool struct {
	tool.BaseTool
	index *CodeIndex
	lsp   LSPBridge
}

func (t *callersTool) Name() string { return "find_callers" }

// ── 呈现契约（PC-01 ~ PC-04）──────────────────────────────────────────────
//
// find_callers 的结果本来就是一张图，而 CallGraph.tsx 早就能画它——只是对话流里
// 从没接线，于是它落进 GenericResult 的裸 <pre>。下面三样东西把它接上。

// callerRef 是构图所需的最小 caller 信息。
//
// 它存在是因为两条策略产出不同的类型：LSP 路径给 lspCallerEntry（无签名），CKG 路径
// 给 SymbolEntry（有签名、行号语义也不同）。归一到一个形状，图的构建就只有一份。
type callerRef struct {
	Name      string
	Kind      string
	File      string
	Line      int // 0-based，与两个来源一致；转 wire 时 +1
	Signature string
}

func callerRefsFromLSP(entries []lspCallerEntry) []callerRef {
	out := make([]callerRef, 0, len(entries))
	for _, e := range entries {
		out = append(out, callerRef{Name: e.Name, Kind: e.Kind, File: e.File, Line: e.Line})
	}
	return out
}

func callerRefsFromSymbols(entries []SymbolEntry) []callerRef {
	out := make([]callerRef, 0, len(entries))
	for _, e := range entries {
		out = append(out, callerRef{
			Name: e.Name, Kind: e.Kind, File: e.FilePath,
			Line: e.LineStart, Signature: e.Signature,
		})
	}
	return out
}

// graphBuilder 累积节点与边，去重按节点 ID。
//
// 存在的理由是单定义与多定义两条路径共用同一套去重与计数：一个 caller 同时调用两个
// 同名定义时，节点只应出现一次而边应有两条。分头实现会让其中一条漏掉去重，而症状是
// 图上出现两个一模一样的节点——不报错，只是看起来像数据有问题。
type graphBuilder struct {
	nodes   []GraphNode
	edges   []GraphEdge
	seen    map[string]bool
	callers int
}

func newGraphBuilder() *graphBuilder {
	return &graphBuilder{seen: map[string]bool{}}
}

// addFocus 加一个焦点节点并返回它的 ID。line 为 -1 表示位置未知（LSP 路径只回
// 调用方，拿不到焦点自身的位置）。
func (b *graphBuilder) addFocus(name, file string, line int, signature string) string {
	id := "focus:" + name
	if !b.seen[id] {
		b.seen[id] = true
		b.nodes = append(b.nodes, GraphNode{
			ID: id, Name: name, Kind: "symbol",
			File: file, Line: line + 1, Signature: signature, IsFocus: true,
		})
	}
	return id
}

// addCallers 把调用方挂到给定焦点上。边一律 Resolved=true：这些节点来自符号表，
// 不是从 callee 名字铸出的占位节点。
func (b *graphBuilder) addCallers(focusID string, callers []callerRef) {
	for _, c := range callers {
		id := fmt.Sprintf("%s:%d:%s", c.File, c.Line, c.Name)
		if !b.seen[id] {
			b.seen[id] = true
			b.nodes = append(b.nodes, GraphNode{
				ID: id, Name: c.Name, Kind: c.Kind,
				File: c.File, Line: c.Line + 1, Signature: c.Signature,
				IsTest: isTestFile(c.File),
			})
		}
		b.edges = append(b.edges, GraphEdge{Source: id, Target: focusID, Kind: "call", Resolved: true})
		b.callers++
	}
}

// build 产出图。**必须填 ImpactSummary.DirectCallers**——presentCallers 靠它判
// grade=empty，而不是数节点：多定义图里 3 个焦点 0 个调用方也有 3 个节点，用节点数
// 判断会把"谁都没调用"读成 good。
func (b *graphBuilder) build() *CallGraphData {
	return &CallGraphData{
		Nodes:         b.nodes,
		Edges:         b.edges,
		ImpactSummary: &ImpactSummary{DirectCallers: b.callers},
	}
}

// callersGraph 是单定义路径的图：一个焦点 + 它的调用方。
func callersGraph(focusName, focusFile string, focusLine int, callers []callerRef) *CallGraphData {
	b := newGraphBuilder()
	b.addCallers(b.addFocus(focusName, focusFile, focusLine, ""), callers)
	return b.build()
}

// groupedCallersGraph 是多定义路径的图：每个定义一个焦点，各挂自己的调用方。
//
// 把歧义画出来而不是压平成一张图，是因为"同名的三个定义各有谁调用"正是用户在这种
// 情况下唯一想知道的事——压平会让他以为只有一个定义。
func groupedCallersGraph(groups []callerGroup) *CallGraphData {
	b := newGraphBuilder()
	for _, g := range groups {
		focusID := b.addFocus(g.qualifiedName, g.defFile, g.defLine, g.signature)
		b.addCallers(focusID, callerRefsFromSymbols(g.callers))
	}
	return b.build()
}

func isTestFile(path string) bool {
	return strings.HasSuffix(path, "_test.go") ||
		strings.Contains(path, ".test.") || strings.Contains(path, ".spec.")
}

// callersOutcome 记录本次调用的呈现意图。
//
// **零值是安全的**：category 落 text、grade 由结果派生。所以将来有人给 Call 加第八个
// return 分支、忘了填 outcome，产出的也只是"少一张图"，不是一个说谎的声明。这是刻意
// 的——分级本身无法做到防遗忘（只有分支知道语义），能做的是让遗忘的后果退化而非出错。
//
// notReady 是唯一需要分支主动说话的一位：索引没建完时"没找到调用方"这个答案不可信，
// 而这件事结果本身看不出来（IsError=false、Content 是一句正常的话）。
type callersOutcome struct {
	graph    *CallGraphData
	notReady bool
}

// presentCallers 是本工具**唯一**的呈现声明附加点。
//
// 「唯一」是承重的：Call 有 7 个 return，其中 4 个语义各不相同，而分级每个出口都不一样。
// 在 4 处各写一次声明就是 EE-14「判在三个调用方之一」的同一个形状——漏掉的那一处不报错，
// 只是安静地让这个工具退回裸 pre。
func presentCallers(res *tool.ToolResult, o callersOutcome) *tool.ToolResult {
	if res == nil {
		return nil
	}
	category := CategoryText
	if o.graph != nil {
		category = CategoryGraph
	}

	// 有效条目数取**调用方计数**，不是节点数：多定义图里 3 个定义各 0 调用方也有 3 个
	// 节点，数节点会把"谁都没调用"读成 good。分级优先级在 GradeFor 单点。
	hits := 0
	if o.graph != nil && o.graph.ImpactSummary != nil {
		hits = o.graph.ImpactSummary.DirectCallers
	}
	return Present(res, category, GradeFor(res, o.notReady, hits), o.graph)
}

var findCallersContract = tool.PromptContract{
	Summary: "Find all functions and methods that call a given symbol. Use before modifying a function to understand its dependents.",
	WhenToUse: []tool.PromptScenario{
		{Condition: "Need to know who calls a function before changing its signature or behavior"},
		{Condition: "Tracing an execution path upward to find entry points"},
		{Condition: "Assessing blast radius of a bug in a specific function"},
	},
	WhenNotToUse: []tool.PromptRedirect{
		{Condition: "Need full downstream impact including transitive dependents", AlternativeTool: "impact_analysis", Reason: "impact_analysis walks the full dependency tree, not just direct callers"},
		{Condition: "Need to find all usages of a symbol (not just call sites)", AlternativeTool: "find_references", Reason: "find_references includes type references, assignments, and imports"},
		{Condition: "Need to find where the symbol is defined", AlternativeTool: "search_symbols", Reason: "search_symbols locates definitions by name"},
	},
	SideEffects: []tool.SideEffectDecl{
		{Type: "none", Description: "Read-only query against call graph index", Reversible: true},
	},
	ParamNotes: []tool.ParamNote{
		{Param: "symbol", Constraint: "Function or method name. Supports qualified forms: 'Get' (all Get), 'Registry.Get' (only Get on Registry type), 'execution.Registry.Get' (Registry.Get in execution package)"},
		{Param: "limit", Constraint: "Max callers to return", Default: "20"},
		{Param: "verbosity", Constraint: "summary | detail | full", Default: "detail"},
	},
}

func (t *callersTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "find_callers",
		Description: tool.ContractToDescription(&findCallersContract),
		Contract:    &findCallersContract,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"symbol": {"type": "string", "description": "Function/method name. Use qualified form for disambiguation: 'Registry.Get' filters by receiver type, 'execution.Registry.Get' also filters by package"},
				"limit": {"type": "integer", "description": "Max results (default 20)"},
				"verbosity": {"type": "string", "enum": ["summary", "detail", "full"], "description": "Output detail level (default: detail)"}
			},
			"required": ["symbol"]
		}`),
	}
}

func (t *callersTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01/PC-02/PC-03：呈现声明只在这一处附加，覆盖下面每一个 return。
	// 具名返回 + defer 而不是在各分支各写一次——见 presentCallers 的注释。
	var outcome callersOutcome
	defer func() { res = presentCallers(res, outcome) }()

	var params struct {
		Symbol    string `json:"symbol"`
		Limit     int    `json:"limit"`
		Verbosity string `json:"verbosity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if t.index == nil {
		// PC-02：这是 not_ready 而不是一句普通回话。此前它返回 IsError=false 的
		// 纯文本，于是「索引还没建好」与「查到 0 个调用方」在前端完全同形——一个
		// 是"答案不可信"，另一个是"这函数可以删"。
		outcome.notReady = true
		return &tool.ToolResult{Content: "code index is not available"}, nil
	}
	if params.Limit <= 0 {
		params.Limit = 20
	}

	slog.Debug("[find_callers] request", "symbol", params.Symbol, "limit", params.Limit)

	// Strategy 1: LSP Call Hierarchy (type-precise, includes virtual dispatch).
	// This is the ground-truth path — gopls/tsserver resolve types correctly.
	if lspResult := t.tryLSPCallers(ctx, params.Symbol, params.Limit); lspResult != nil {
		slog.Debug("[find_callers] LSP path succeeded", "symbol", params.Symbol, "count", len(lspResult))
		verbosity := ParseVerbosity(params.Verbosity)
		// 焦点的 file/line 这条路径上没有（LSP 只回调用方），所以焦点节点只带名字。
		// 图仍然成立——用户要看的是"谁调用我"，焦点是锚不是内容。
		outcome.graph = callersGraph(params.Symbol, "", -1, callerRefsFromLSP(lspResult))
		return t.formatLSPResult(params.Symbol, lspResult, verbosity), nil
	}

	// Strategy 2: CKG graph fallback (syntax-level, with OVERRIDES expansion).
	slog.Debug("[find_callers] falling back to CKG", "symbol", params.Symbol)

	// Disambiguation: detect multiple definitions across packages and group results.
	groups := t.resolveDisambiguatedCallers(ctx, params.Symbol, params.Limit)
	if groups == nil {
		// Single-definition path (no ambiguity).
		callers, err := t.index.CallersOf(ctx, params.Symbol, params.Limit)
		if err != nil {
			slog.Warn("[find_callers] error", "symbol", params.Symbol, "err", err)
			return &tool.ToolResult{Content: fmt.Sprintf("find callers failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
		}
		if len(callers) == 0 {
			// 图仍然发送：一个只有焦点节点的图就是"没人调用它"这个答案的可视形式，
			// 而 presentCallers 据此给出 grade=empty。
			outcome.graph = callersGraph(params.Symbol, "", -1, nil)
			return &tool.ToolResult{Content: fmt.Sprintf("No callers found for %q", params.Symbol)}, nil
		}
		verbosity := ParseVerbosity(params.Verbosity)
		callers, total := TruncateWithTotal(callers, verbosity)
		// 图必须知道总数，不只是文本：presentCallers 判 grade 看的是
		// ImpactSummary.DirectCallers，而那是从这里传进去的。喂截断后的数会让
		// "还有 15 个调用方"在两个面上同时消失。
		outcome.graph = callersGraph(params.Symbol, "", -1, callerRefsFromSymbols(callers))
		outcome.graph.ImpactSummary.DirectCallers = total
		outcome.graph.Truncated = total > len(callers)
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Found %s caller(s) of %q:\n\n", CountPhrase(len(callers), total), params.Symbol))
		for i, c := range callers {
			sb.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, c.Kind, c.Name))
			sb.WriteString(fmt.Sprintf("   File: %s:%d\n", c.FilePath, c.LineStart+1))
			if c.Signature != "" {
				sb.WriteString(fmt.Sprintf("   Signature: %s\n", c.Signature))
			}
			sb.WriteByte('\n')
		}
		// INV-UX-01 L-2: source/precision lives in Metadata, not Content.
		return &tool.ToolResult{Content: sb.String(), Metadata: map[string]any{"source": "index"}}, nil
	}

	// Multi-definition path: present grouped results.
	outcome.graph = groupedCallersGraph(groups)
	verbosity := ParseVerbosity(params.Verbosity)
	var sb strings.Builder
	totalCallers := 0
	for _, g := range groups {
		totalCallers += len(g.callers)
	}
	sb.WriteString(fmt.Sprintf("Found %d caller(s) of %q across %d definitions [grouped by definition]:\n\n",
		totalCallers, params.Symbol, len(groups)))
	for gi, g := range groups {
		sb.WriteString(fmt.Sprintf("━━━ Definition %d: %s ━━━\n", gi+1, g.qualifiedName))
		sb.WriteString(fmt.Sprintf("    %s:%d  %s\n\n", g.defFile, g.defLine+1, g.signature))
		callers, groupTotal := TruncateWithTotal(g.callers, verbosity)
		for i, c := range callers {
			sb.WriteString(fmt.Sprintf("  %d. %s %s\n", i+1, c.Kind, c.Name))
			sb.WriteString(fmt.Sprintf("     File: %s:%d\n", c.FilePath, c.LineStart+1))
			if verbosity >= VerbosityDetail && c.Signature != "" {
				sb.WriteString(fmt.Sprintf("     Signature: %s\n", c.Signature))
			}
			sb.WriteByte('\n')
		}
		// 组内截断也要说。上面那个 header 的总数是截断前算的（诚实），但组内静默
		// 切掉会让读者以为这个定义只有 5 个调用方——总数对而分布错，比总数错更难查。
		if groupTotal > len(callers) {
			sb.WriteString(fmt.Sprintf("  ... and %d more\n\n", groupTotal-len(callers)))
		}
		if groupTotal == 0 {
			sb.WriteString("  (no callers found)\n\n")
		}
	}
	return &tool.ToolResult{Content: sb.String()}, nil
}

// tryLSPCallers attempts to find callers via LSP Call Hierarchy.
// Returns nil if LSP is unavailable or fails (caller should fallback to CKG).
func (t *callersTool) tryLSPCallers(ctx context.Context, symbol string, limit int) []lspCallerEntry {
	if t.lsp == nil {
		return nil
	}
	if _, isNoop := t.lsp.(NoopLSP); isNoop {
		return nil
	}

	// Resolve symbol to file:line:col via CKG symbol table.
	file, line, col, ok := t.resolveSymbolPosition(ctx, symbol)
	if !ok {
		slog.Debug("[find_callers] LSP: resolveSymbolPosition failed", "symbol", symbol)
		return nil
	}

	// LSP PrepareCallHierarchy → IncomingCalls.
	items, err := t.lsp.PrepareCallHierarchy(ctx, file, line, col)
	if err != nil {
		slog.Debug("[find_callers] LSP: PrepareCallHierarchy error", "file", file, "line", line, "col", col, "err", err)
		return nil
	}
	if len(items) == 0 {
		slog.Debug("[find_callers] LSP: PrepareCallHierarchy returned 0 items", "file", file, "line", line, "col", col)
		return nil
	}

	incoming, err := t.lsp.IncomingCalls(ctx, items[0])
	if err != nil {
		slog.Debug("[find_callers] LSP: IncomingCalls error", "item", items[0].Name, "err", err)
		return nil
	}
	if len(incoming) == 0 {
		slog.Debug("[find_callers] LSP: IncomingCalls returned 0 results", "item", items[0].Name, "file", file)
		return nil
	}

	slog.Debug("[find_callers] LSP path succeeded", "symbol", symbol, "count", len(incoming))
	var results []lspCallerEntry
	for _, call := range incoming {
		if len(results) >= limit {
			break
		}
		results = append(results, lspCallerEntry{
			Name: call.From.Name,
			Kind: symbolKindName(call.From.Kind),
			File: call.From.Path,
			Line: call.From.Line,
		})
	}
	return results
}

// resolveSymbolPosition finds the file:line:col of a symbol name in the CKG.
// Returns the position of the symbol NAME token for LSP PrepareCallHierarchy.
//
// Disambiguation: when multiple symbols match (e.g. 4 different Registry.Get),
// selects the one with the most incoming call edges (most "important" definition).
func (t *callersTool) resolveSymbolPosition(ctx context.Context, symbol string) (string, int, int, bool) {
	if t.index == nil || !t.index.hasReader() {
		return "", 0, 0, false
	}
	// readerDB may be swapped by ReopenAt/Close; hold RLock for the whole
	// query so the handle cannot be closed mid-flight.
	t.index.readerMu.RLock()
	defer t.index.readerMu.RUnlock()
	db := t.index.readerDB

	type candidate struct {
		file, name, signature string
		line, callerCount     int
	}

	var candidates []candidate
	parts := strings.SplitN(symbol, ".", 3)

	var rows *sql.Rows
	var err error
	switch len(parts) {
	case 3:
		rows, err = db.QueryContext(ctx,
			`SELECT s.file_path, s.line_start, s.name, s.signature,
			        (SELECT COUNT(*) FROM edges e WHERE e.target_id = s.id AND e.kind = 'call') as cc
			 FROM symbols s
			 WHERE s.name = ? AND s.parent = ? AND s.file_path LIKE ? AND s.kind IN ('function','method')
			 ORDER BY cc DESC LIMIT 5`,
			parts[2], parts[1], "%/"+parts[0]+"/%")
	case 2:
		rows, err = db.QueryContext(ctx,
			`SELECT s.file_path, s.line_start, s.name, s.signature,
			        (SELECT COUNT(*) FROM edges e WHERE e.target_id = s.id AND e.kind = 'call') as cc
			 FROM symbols s
			 WHERE s.name = ? AND s.parent = ? AND s.kind IN ('function','method')
			 ORDER BY cc DESC LIMIT 5`,
			parts[1], parts[0])
	default:
		rows, err = db.QueryContext(ctx,
			`SELECT s.file_path, s.line_start, s.name, s.signature,
			        (SELECT COUNT(*) FROM edges e WHERE e.target_id = s.id AND e.kind = 'call') as cc
			 FROM symbols s
			 WHERE s.name = ? AND s.kind IN ('function','method')
			 ORDER BY cc DESC LIMIT 5`,
			symbol)
	}
	if err != nil {
		return "", 0, 0, false
	}
	defer rows.Close()
	for rows.Next() {
		var c candidate
		if rows.Scan(&c.file, &c.line, &c.name, &c.signature, &c.callerCount) == nil {
			candidates = append(candidates, c)
		}
	}

	if len(candidates) == 0 {
		return "", 0, 0, false
	}

	best := candidates[0] // already sorted by cc DESC

	// Calculate column: read the actual source line and find the method name token.
	col := findColumnInSourceFile(best.file, best.line, best.name)
	if col < 0 {
		// Fallback: try to find within the stored signature.
		col = 0
		if best.name != "" && best.signature != "" {
			if idx := strings.Index(best.signature, best.name+"("); idx >= 0 {
				col = idx
			} else if idx := strings.Index(best.signature, best.name); idx >= 0 {
				col = idx
			}
		}
	}

	return best.file, best.line, col, true
}

// findColumnInSourceFile reads the source file and finds the byte-column offset
// of the method name on the given line (0-indexed). Returns -1 if not found.
func findColumnInSourceFile(filePath string, lineNum int, name string) int {
	f, err := os.Open(filePath)
	if err != nil {
		return -1
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	currentLine := 0
	for scanner.Scan() {
		if currentLine == lineNum {
			line := scanner.Text()
			// For Go method declarations: "func (r *Registry) Get(name string) ..."
			// The name token appears after "func" and optional receiver.
			idx := strings.Index(line, " "+name+"(")
			if idx >= 0 {
				return idx + 1 // +1 to skip the space
			}
			idx = strings.Index(line, " "+name+" ")
			if idx >= 0 {
				return idx + 1
			}
			idx = strings.Index(line, "."+name+"(")
			if idx >= 0 {
				return idx + 1
			}
			// Generic fallback: bare name anywhere on the line.
			idx = strings.Index(line, name)
			if idx >= 0 {
				return idx
			}
			return -1
		}
		currentLine++
	}
	return -1
}

// callerGroup represents one definition of a method and its callers.
type callerGroup struct {
	qualifiedName string // e.g. "execution.Registry.Get"
	defFile       string
	defLine       int
	signature     string
	callers       []SymbolEntry
}

// resolveDisambiguatedCallers detects multiple definitions for a symbol across different
// packages and returns grouped caller results. Returns nil if there's only one definition
// (caller should fall through to simple CallersOf path).
func (t *callersTool) resolveDisambiguatedCallers(ctx context.Context, symbol string, limit int) []callerGroup {
	if t.index == nil || !t.index.hasReader() {
		return nil
	}
	// readerDB may be swapped by ReopenAt/Close; hold RLock for the whole
	// query so the handle cannot be closed mid-flight.
	t.index.readerMu.RLock()
	defer t.index.readerMu.RUnlock()
	db := t.index.readerDB

	parts := strings.SplitN(symbol, ".", 3)
	if len(parts) == 3 {
		// Already fully qualified — no need to disambiguate.
		return nil
	}

	// Query all matching definitions with file path for package resolution.
	var rows *sql.Rows
	var err error
	switch len(parts) {
	case 2:
		rows, err = db.QueryContext(ctx,
			`SELECT id, file_path, line_start, name, signature, parent
			 FROM symbols
			 WHERE name = ? AND parent = ? AND kind IN ('function','method')`,
			parts[1], parts[0])
	default:
		rows, err = db.QueryContext(ctx,
			`SELECT id, file_path, line_start, name, signature, parent
			 FROM symbols
			 WHERE name = ? AND kind IN ('function','method')`,
			symbol)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()

	type defEntry struct {
		id        int64
		file      string
		line      int
		name      string
		signature string
		parent    string
		pkg       string // derived package path segment
	}

	var defs []defEntry
	for rows.Next() {
		var d defEntry
		if rows.Scan(&d.id, &d.file, &d.line, &d.name, &d.signature, &d.parent) == nil {
			d.pkg = extractPackageHint(d.file)
			defs = append(defs, d)
		}
	}

	if len(defs) <= 1 {
		return nil // Single definition or none — no disambiguation needed.
	}

	// Check if definitions are in different packages.
	pkgSet := make(map[string]bool)
	for _, d := range defs {
		pkgSet[d.pkg] = true
	}
	if len(pkgSet) <= 1 {
		return nil // All in same package — standard path handles this.
	}

	// Group definitions by package and query callers for each group.
	pkgGroups := make(map[string][]defEntry)
	for _, d := range defs {
		pkgGroups[d.pkg] = append(pkgGroups[d.pkg], d)
	}

	var groups []callerGroup
	perGroupLimit := limit
	if len(pkgGroups) > 1 {
		perGroupLimit = limit / len(pkgGroups)
		if perGroupLimit < 5 {
			perGroupLimit = 5
		}
	}

	for pkg, pDefs := range pkgGroups {
		g := callerGroup{
			qualifiedName: pkg + "." + symbol,
			defFile:       pDefs[0].file,
			defLine:       pDefs[0].line,
			signature:     pDefs[0].signature,
		}

		// Collect target IDs for this package group.
		targetIDs := make([]int64, len(pDefs))
		for i, d := range pDefs {
			targetIDs[i] = d.id
		}

		// Query direct callers via the existing index method.
		seen := make(map[int64]bool)
		callers, err := t.index.queryCallersForTargets(ctx, targetIDs, perGroupLimit)
		if err == nil {
			for _, c := range callers {
				if !seen[c.id] {
					seen[c.id] = true
					g.callers = append(g.callers, c.entry)
				}
			}
		}

		// Also expand via OVERRIDES (interface→concrete and concrete→interface paths).
		for _, tid := range targetIDs {
			ifaceIDs, _ := t.index.queryConcreteOverrides(ctx, tid)
			if len(ifaceIDs) > 0 {
				extraCallers, _ := t.index.queryCallersForTargets(ctx, ifaceIDs, perGroupLimit-len(g.callers))
				for _, c := range extraCallers {
					if !seen[c.id] {
						seen[c.id] = true
						g.callers = append(g.callers, c.entry)
					}
				}
			}
			overriddenIDs, _ := t.index.queryOverriddenInterfaces(ctx, tid)
			if len(overriddenIDs) > 0 {
				extraCallers, _ := t.index.queryCallersForTargets(ctx, overriddenIDs, perGroupLimit-len(g.callers))
				for _, c := range extraCallers {
					if !seen[c.id] {
						seen[c.id] = true
						g.callers = append(g.callers, c.entry)
					}
				}
			}
		}

		groups = append(groups, g)
	}

	// Ambiguous edges: callers whose target_id IS NULL because multiple
	// candidates existed. Collect once and append as a separate group so
	// cross-package callers aren't silently lost.
	type symKey struct {
		file string
		line int
	}
	resolvedSeen := make(map[symKey]bool)
	for _, g := range groups {
		for _, c := range g.callers {
			resolvedSeen[symKey{c.FilePath, c.LineStart}] = true
		}
	}
	ambCallers, _ := t.index.queryCallersForAmbiguousTarget(ctx, symbol, limit)
	if len(ambCallers) > 0 {
		ambGroup := callerGroup{
			qualifiedName: symbol + " (ambiguous target)",
		}
		for _, c := range ambCallers {
			k := symKey{c.entry.FilePath, c.entry.LineStart}
			if !resolvedSeen[k] {
				resolvedSeen[k] = true
				ambGroup.callers = append(ambGroup.callers, c.entry)
			}
		}
		if len(ambGroup.callers) > 0 {
			groups = append(groups, ambGroup)
		}
	}

	return groups
}

// extractPackageHint derives a short package identifier from a file path.
// For "/internal/execution/registry.go" → "execution"
// For "/internal/hypervisor/registry/registry.go" → "registry"
func extractPackageHint(filePath string) string {
	dir := filepath.Dir(filePath)
	// Use the last directory segment as the package hint.
	base := filepath.Base(dir)
	if base == "." || base == "/" {
		return "root"
	}
	return base
}

type lspCallerEntry struct {
	Name string
	Kind string
	File string
	Line int
}

func (t *callersTool) formatLSPResult(symbol string, entries []lspCallerEntry, verbosity VerbosityLevel) *tool.ToolResult {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d caller(s) of %q:\n\n", len(entries), symbol))
	for i, e := range entries {
		switch verbosity {
		case VerbositySummary:
			sb.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, e.Kind, e.Name))
		default:
			sb.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, e.Kind, e.Name))
			sb.WriteString(fmt.Sprintf("   File: %s:%d\n", e.File, e.Line+1))
			sb.WriteByte('\n')
		}
	}
	// INV-UX-01 L-2: source/precision lives in Metadata, not Content.
	return &tool.ToolResult{Content: sb.String(), Metadata: map[string]any{"source": "lsp"}}
}

func symbolKindName(kind int) string {
	switch kind {
	case 12:
		return "function"
	case 6:
		return "method"
	case 5:
		return "class"
	case 2:
		return "module"
	default:
		return "symbol"
	}
}

// NewCalleesTool is defined in tool_callees.go (enhanced version with optional file/line).
