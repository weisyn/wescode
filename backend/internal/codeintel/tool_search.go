package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// NewSearchSymbolsTool creates a "search_symbols" tool that searches the code index.
// stateFn provides the current EditorState for language-aware filtering (CI-09).
func NewSearchSymbolsTool(index *CodeIndex, stateFn func() EditorState) tool.Tool {
	return &searchSymbolsTool{
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
		index:         index,
		editorStateFn: stateFn,
	}
}

type searchSymbolsTool struct {
	tool.BaseTool
	index         *CodeIndex
	editorStateFn func() EditorState
}

func (t *searchSymbolsTool) Name() string { return "search_symbols" }

var searchSymbolsContract = tool.PromptContract{
	Summary: "Find where a function, type, method, or variable is defined across the project by name or pattern.",
	WhenToUse: []tool.PromptScenario{
		{Condition: "Need to locate the definition of a symbol (function, type, struct, interface, variable)"},
		{Condition: "Need to find all declarations matching a name pattern across the codebase"},
		{Condition: "Exploring an unfamiliar codebase and need to find entry points by name"},
	},
	WhenNotToUse: []tool.PromptRedirect{
		{Condition: "Need to find where a symbol is used/called (not defined)", AlternativeTool: "find_references", Reason: "find_references traces usage sites"},
		{Condition: "Need to search for arbitrary text patterns in file content", AlternativeTool: "grep", Reason: "grep searches content; search_symbols searches the symbol index"},
		{Condition: "Need symbols within a single already-open file", AlternativeTool: "read_symbols", Reason: "read_symbols is scoped to one file and always available"},
	},
	SideEffects: []tool.SideEffectDecl{
		{Type: "none", Description: "Read-only query against code index", Reversible: true},
	},
	ParamNotes: []tool.ParamNote{
		{Param: "query", Constraint: "Symbol name or partial match; supports function, type, method, variable names"},
		{Param: "limit", Constraint: "Max results to return", Default: "10"},
		{Param: "verbosity", Constraint: "summary | detail | full", Default: "detail"},
	},
}

func (t *searchSymbolsTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "search_symbols",
		Description: tool.ContractToDescription(&searchSymbolsContract),
		Contract:    &searchSymbolsContract,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "Symbol name or search query"},
				"limit": {"type": "integer", "description": "Max results (default 10, max 20)"},
				"verbosity": {"type": "string", "enum": ["summary", "detail", "full"], "description": "Output detail level (default: detail)"}
			},
			"required": ["query"]
		}`),
	}
}

func (t *searchSymbolsTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。not_ready 必须由分支主动说：索引没建完这件事结果本身看不出来
	// （IsError=false、Content 是一句正常的话），而它与"查到 0 个"在前端完全同形。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		Query     string `json:"query"`
		Limit     int    `json:"limit"`
		Verbosity string `json:"verbosity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Query == "" {
		return &tool.ToolResult{Content: "query is required", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Limit <= 0 {
		params.Limit = 10
	}
	if params.Limit > 20 {
		params.Limit = 20
	}

	if t.index == nil {
		notReady = true
		return &tool.ToolResult{Content: "code index is not available"}, nil
	}

	// CI-09: prefer same-language results based on EditorState focus file
	var extFilter []string
	if t.editorStateFn != nil {
		if state := t.editorStateFn(); state.FocusFile != "" {
			if lc := DetectLanguage(state.FocusFile); len(lc.Exts) > 0 {
				extFilter = lc.Exts
			}
		}
	}

	slog.Debug("[search_symbols] request", "query", params.Query, "limit", params.Limit, "ext_filter", extFilter)
	symbols, err := t.index.SearchSymbols(ctx, params.Query, params.Limit, extFilter...)
	if err == nil && len(symbols) == 0 && len(extFilter) > 0 {
		symbols, err = t.index.SearchSymbols(ctx, params.Query, params.Limit)
	}
	if err != nil {
		slog.Warn("[search_symbols] error", "query", params.Query, "err", err)
		return &tool.ToolResult{Content: fmt.Sprintf("search failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}
	slog.Debug("[search_symbols] result", "query", params.Query, "count", len(symbols))

	// L1 full-text search: supplement with file content matches if symbol
	// results are insufficient (CI-12).
	var fileResults []FileEntry
	if len(symbols) < params.Limit {
		remaining := params.Limit - len(symbols)
		fileResults, err = t.index.SearchFiles(ctx, params.Query, remaining, extFilter...)
		if err != nil {
			slog.Warn("[search_symbols] full-text search unavailable", "query", params.Query, "err", err)
			return &tool.ToolResult{Content: fmt.Sprintf("full-text search failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
		}
		if len(fileResults) == 0 && len(extFilter) > 0 {
			fileResults, err = t.index.SearchFiles(ctx, params.Query, remaining)
			if err != nil {
				slog.Warn("[search_symbols] full-text search unavailable", "query", params.Query, "err", err)
				return &tool.ToolResult{Content: fmt.Sprintf("full-text search failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
			}
		}
		symbolPaths := make(map[string]bool, len(symbols))
		for _, s := range symbols {
			symbolPaths[s.FilePath] = true
		}
		var deduped []FileEntry
		for _, f := range fileResults {
			if !symbolPaths[f.FilePath] {
				deduped = append(deduped, f)
			}
		}
		fileResults = deduped
	}

	if len(symbols) == 0 && len(fileResults) == 0 {
		return &tool.ToolResult{Content: fmt.Sprintf("No results found matching %q", params.Query)}, nil
	}

	verbosity := ParseVerbosity(params.Verbosity)
	symbols, symbolTotal := TruncateWithTotal(symbols, verbosity)
	fileResults, fileTotal := TruncateWithTotal(fileResults, verbosity)

	// 两类结果合进一个列表：对用户来说它们是同一个问题的答案（"什么匹配我的查询"），
	// 分成两个列表会让他在两处各扫一遍。kind 徽标区分来源。
	items := make([]ListItem, 0, len(symbols)+len(fileResults))
	for _, sym := range symbols {
		items = append(items, ListItem{
			Label:  sym.Name,
			Detail: sym.Signature,
			File:   sym.FilePath,
			Line:   sym.LineStart + 1, // CKG 存 0-based
			Kind:   sym.Kind,
		})
	}
	for _, f := range fileResults {
		items = append(items, ListItem{
			Label:  filepath.Base(f.FilePath),
			Detail: f.Snippet,
			File:   f.FilePath,
			// 文件内容命中没有行号（FTS 只给文件粒度），所以不填 Line——
			// 填 0 或 1 会让点击跳到文件头并看起来像"命中在第一行"。
			Kind: f.Language,
		})
	}
	list = ListOf(items, symbolTotal+fileTotal)

	var sb strings.Builder
	if len(symbols) > 0 {
		sb.WriteString(fmt.Sprintf("Found %s symbol(s) matching %q:\n\n", CountPhrase(len(symbols), symbolTotal), params.Query))
		for i, sym := range symbols {
			switch verbosity {
			case VerbositySummary:
				sb.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, sym.Kind, sym.Name))
			case VerbosityFull:
				sb.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, sym.Kind, sym.Name))
				sb.WriteString(fmt.Sprintf("   File: %s (lines %d-%d)\n", sym.FilePath, sym.LineStart+1, sym.LineEnd+1))
				if sym.Signature != "" {
					sb.WriteString(fmt.Sprintf("   Signature: %s\n", sym.Signature))
				}
				if sym.Parent != "" {
					sb.WriteString(fmt.Sprintf("   Parent: %s\n", sym.Parent))
				}
				sb.WriteByte('\n')
			default: // VerbosityDetail
				sb.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, sym.Kind, sym.Name))
				sb.WriteString(fmt.Sprintf("   File: %s (lines %d-%d)\n", sym.FilePath, sym.LineStart+1, sym.LineEnd+1))
				if sym.Signature != "" {
					sb.WriteString(fmt.Sprintf("   Signature: %s\n", sym.Signature))
				}
				if sym.Parent != "" {
					sb.WriteString(fmt.Sprintf("   Parent: %s\n", sym.Parent))
				}
				sb.WriteByte('\n')
			}
		}
	}
	if len(fileResults) > 0 {
		sb.WriteString(fmt.Sprintf("Found %s file content match(es):\n\n", CountPhrase(len(fileResults), fileTotal)))
		offset := len(symbols)
		for i, f := range fileResults {
			sb.WriteString(fmt.Sprintf("%d. %s", offset+i+1, f.FilePath))
			if f.Language != "" {
				sb.WriteString(fmt.Sprintf(" (%s)", f.Language))
			}
			sb.WriteByte('\n')
			if verbosity != VerbositySummary && f.Snippet != "" {
				sb.WriteString(fmt.Sprintf("   ...%s...\n", f.Snippet))
			}
			sb.WriteByte('\n')
		}
	}

	// Readiness disclosure is appended by NewConfidenceEnrichHook; calling
	// EnrichResult here as well would append the footer twice.
	return &tool.ToolResult{Content: sb.String()}, nil
}
