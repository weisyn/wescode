package codeintel

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// NewFindReferencesTool creates a "find_references" tool that wraps LSPBridge
// to provide definition/references lookup as an Agent-callable tool.
// Domain-scoped ("graph"): only injected into schema when domain is active,
// reducing baseline schema token cost (INV-DOMAIN-09).
// When ckg is non-nil, LSP-empty results fall back to CKG call graph.
func NewFindReferencesTool(lsp LSPBridge, ckg *CodeIndex) tool.Tool {
	return &findReferencesTool{
		BaseTool: tool.BaseTool{
			Meta: tool.ToolMeta{
				Dimension:      tool.DimensionPerceive,
				Availability:   tool.AvailabilityConfigurable,
				ReadOnly:       true,
				Concurrent:     true,
				Timeout:        15 * time.Second,
				Risk:           tool.RiskSafe,
				PolicyFamily:   tool.PolicyFamilyOther,
				Enabled:        true,
				Domain:         "graph",
				DomainToolTier: tool.DomainTierCore,
			},
		},
		lsp: lsp,
		ckg: ckg,
	}
}

type findReferencesTool struct {
	tool.BaseTool
	lsp LSPBridge
	ckg *CodeIndex
}

// IsBackendConfigured implements tool.BackendProbe.
// Returns false when LSP is NoopLSP (no language server available),
// causing the tool to be excluded from the LLM-visible schema.
func (t *findReferencesTool) IsBackendConfigured() bool {
	_, isNoop := t.lsp.(NoopLSP)
	return !isNoop
}

func (t *findReferencesTool) Name() string { return "find_references" }

var findReferencesContract = tool.PromptContract{
	Summary: "Find all references to a symbol at a specific file position, jump to its definition, or find who calls it. Use to trace usages or find API consumers.",
	WhenToUse: []tool.PromptScenario{
		{Condition: "Need to find everywhere a specific symbol (at a known file:line:col) is used"},
		{Condition: "Tracing how an API, function, or type is consumed across the codebase"},
		{Condition: "Need to jump to the definition of a symbol at a given position"},
		{Condition: "Need to find who calls a specific function at a known file:line:col (use kind=callers)"},
	},
	WhenNotToUse: []tool.PromptRedirect{
		{Condition: "Know the symbol name but not its file position", AlternativeTool: "search_symbols", Reason: "search_symbols finds definitions by name without needing file:line:col"},
		{Condition: "Know the symbol name and want callers (no file position needed)", AlternativeTool: "find_callers", Reason: "find_callers uses the call graph by name, supports Type.Method disambiguation"},
		{Condition: "Need full transitive downstream impact", AlternativeTool: "impact_analysis", Reason: "impact_analysis walks the full dependency tree"},
	},
	SideEffects: []tool.SideEffectDecl{
		{Type: "none", Description: "Read-only LSP query", Reversible: true},
	},
	ParamNotes: []tool.ParamNote{
		{Param: "path", Constraint: "File path containing the symbol"},
		{Param: "line", Constraint: "0-based line number of the symbol"},
		{Param: "column", Constraint: "0-based column number of the symbol"},
		{Param: "kind", Constraint: "references | definition | callers", Default: "references"},
		{Param: "verbosity", Constraint: "summary | detail | full", Default: "detail"},
	},
}

func (t *findReferencesTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "find_references",
		Description: tool.ContractToDescription(&findReferencesContract),
		Contract:    &findReferencesContract,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "File path"},
				"line": {"type": "integer", "description": "Line (0-based)"},
				"column": {"type": "integer", "description": "Column (0-based)"},
				"kind": {"type": "string", "enum": ["references", "definition", "callers"], "description": "references=all usages, definition=jump to def, callers=who calls this function (via LSP Call Hierarchy). default: references"},
				"verbosity": {"type": "string", "enum": ["summary", "detail", "full"], "description": "Output detail level (default: detail)"}
			},
			"required": ["path", "line"]
		}`),
	}
}

func (t *findReferencesTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。这个工具没有 not_ready：LSP 不可用时退化到静态分析，那是
	// "降级但有效"而不是"等一会儿再试"，闭域里没有对应的分级，故 source 留在 Metadata。
	var list *ListData
	defer func() { res = PresentList(res, false, list) }()

	var params struct {
		Path      string `json:"path"`
		Line      int    `json:"line"`
		Column    int    `json:"column"`
		Kind      string `json:"kind"`
		Verbosity string `json:"verbosity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Path == "" {
		return &tool.ToolResult{Content: "path is required", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Kind == "" {
		params.Kind = "references"
	}

	slog.Debug("[find_references] request",
		"path", params.Path, "line", params.Line, "col", params.Column,
		"kind", params.Kind, "verbosity", params.Verbosity)

	var locations []DefinitionLocation
	// err 不在此处重新声明：具名返回已占用这个名字（defer 需要它收敛声明）。

	switch params.Kind {
	case "definition":
		locations, err = t.lsp.Definition(ctx, params.Path, params.Line, params.Column)
	case "references":
		locations, err = t.lsp.References(ctx, params.Path, params.Line, params.Column)
	case "callers":
		locations, err = t.lspCallers(ctx, params.Path, params.Line, params.Column)
	default:
		return &tool.ToolResult{
			Content:   fmt.Sprintf("invalid kind %q: must be 'references', 'definition', or 'callers'", params.Kind),
			IsError:   true,
			ErrorKind: tool.ErrorInvocation,
		}, nil
	}
	if err != nil {
		slog.Warn("[find_references] LSP error", "kind", params.Kind, "path", params.Path, "err", err)
		return &tool.ToolResult{Content: fmt.Sprintf("LSP %s failed: %v", params.Kind, err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}
	slog.Debug("[find_references] LSP result", "kind", params.Kind, "path", params.Path, "count", len(locations))

	source := "LSP"
	if len(locations) == 0 && (params.Kind == "references" || params.Kind == "callers") && t.ckg != nil {
		locations = t.ckgFallbackReferences(ctx, params.Path, params.Line)
		if len(locations) > 0 {
			source = "static"
			slog.Info("[find_references] static analysis fallback produced results", "kind", params.Kind, "path", params.Path, "line", params.Line, "count", len(locations))
		}
	}

	if len(locations) == 0 {
		// 空列表而非 nil：让分级落 empty（"查了、没有引用"），而不是 text +
		// "这个工具不产出列表"。前者是答案，后者是形状声明。
		list = &ListData{Items: []ListItem{}}
		return &tool.ToolResult{Content: fmt.Sprintf("No %s found for position %s:%d:%d", params.Kind, params.Path, params.Line, params.Column)}, nil
	}

	verbosity := ParseVerbosity(params.Verbosity)
	locations, total := TruncateWithTotal(locations, verbosity)

	items := make([]ListItem, 0, len(locations))

	var sb strings.Builder
	// INV-UX-01 L-2: no source tags in Content — source goes to Metadata.
	// 数字必须是截断前的：这个工具回答"有多少处引用"，而模型会据此决定能不能改
	// 签名。报 "Found 3" 而实际 30 处，代价是一次编译不过的重构。
	fmt.Fprintf(&sb, "Found %s %s(s):\n\n", CountPhrase(len(locations), total), params.Kind)
	for i, loc := range locations {
		fmt.Fprintf(&sb, "%d. %s:%d:%d", i+1, loc.Path, loc.Line, loc.Column)
		if loc.EndLine > loc.Line {
			fmt.Fprintf(&sb, " – %d:%d", loc.EndLine, loc.EndColumn)
		}
		sb.WriteByte('\n')
		contextLine := readContextLine(loc.Path, loc.Line, tc)
		if verbosity != VerbositySummary && contextLine != "" {
			fmt.Fprintf(&sb, "   %s\n", contextLine)
		}
		sb.WriteByte('\n')

		// wire 的行号是 1-based，而 DefinitionLocation.Line 是 0-based（LSP 协议
		// 与 CKG LineStart 都是，readContextLine 的 `i == line` 可作证）。
		// `Content` 那侧仍打 0-based 是刻意的：这个工具族的 schema 显式声明
		// 0-based 输入，出入一致；UI 只有一套约定，故只在这里转。
		items = append(items, ListItem{
			Label:  filepath.Base(loc.Path),
			Detail: contextLine,
			File:   loc.Path,
			Line:   loc.Line + 1,
			Kind:   params.Kind,
		})
	}
	list = ListOf(items, total)
	return &tool.ToolResult{Content: sb.String(), Metadata: map[string]any{"source": source}}, nil
}

// lspCallers uses LSP Call Hierarchy (prepareCallHierarchy + incomingCalls)
// to find direct callers of the symbol at the given position. More precise than
// textDocument/references for "who calls this function" queries because it only
// returns call sites, not type references or assignments.
func (t *findReferencesTool) lspCallers(ctx context.Context, path string, line, col int) ([]DefinitionLocation, error) {
	items, err := t.lsp.PrepareCallHierarchy(ctx, path, line, col)
	if err != nil {
		return nil, fmt.Errorf("prepareCallHierarchy: %w", err)
	}
	if len(items) == 0 {
		slog.Debug("[find_references] prepareCallHierarchy returned empty", "path", path, "line", line, "col", col)
		return nil, nil
	}

	incoming, err := t.lsp.IncomingCalls(ctx, items[0])
	if err != nil {
		return nil, fmt.Errorf("incomingCalls: %w", err)
	}

	var locs []DefinitionLocation
	for _, call := range incoming {
		locs = append(locs, DefinitionLocation{
			Path:   call.From.Path,
			Line:   call.From.Line,
			Column: call.From.Column,
		})
	}
	slog.Debug("[find_references] callers via LSP CallHierarchy", "path", path, "count", len(locs))
	return locs, nil
}

// ckgFallbackReferences resolves the symbol at (path, line) from the CKG index,
// then queries CallersOf to find call sites. Returns DefinitionLocation slice
// compatible with the LSP result path.
func (t *findReferencesTool) ckgFallbackReferences(ctx context.Context, path string, line int) []DefinitionLocation {
	syms, err := t.ckg.ListFileSymbols(ctx, path)
	if err != nil || len(syms) == 0 {
		return nil
	}

	var target *SymbolEntry
	for i := range syms {
		s := &syms[i]
		if s.LineStart <= line && line <= s.LineEnd {
			if target == nil || s.LineStart > target.LineStart {
				target = s
			}
		}
	}
	if target == nil {
		return nil
	}

	calleeName := target.Name
	if target.Parent != "" {
		calleeName = target.Parent + "." + target.Name
	}
	callers, err := t.ckg.CallersOf(ctx, calleeName, 30)
	if err != nil || len(callers) == 0 {
		return nil
	}

	var locs []DefinitionLocation
	for _, c := range callers {
		locs = append(locs, DefinitionLocation{
			Path:    c.FilePath,
			Line:    c.LineStart,
			Column:  0,
			EndLine: c.LineEnd,
		})
	}
	return locs
}

// readContextLine reads a single line from a file via the Host FileProvider.
// Editor buffer overlays are handled transparently by the FileProvider.
func readContextLine(path string, line int, tc *tool.ToolContext) string {
	if reason := tc.CheckPathAccess(path, false); reason != "" {
		return ""
	}

	fp := tc.Host.Files()
	f, err := fp.Open(context.Background(), path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for i := 0; scanner.Scan(); i++ {
		if i == line {
			return strings.TrimSpace(scanner.Text())
		}
	}
	return ""
}
