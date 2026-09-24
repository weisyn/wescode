package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// NewVerifyCallersTool creates a tool that verifies CKG-reported callers
// against the LSP Call Hierarchy for type-level precision.
func NewVerifyCallersTool(index *CodeIndex, lsp LSPBridge) tool.Tool {
	return &verifyCallersTool{
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
				DomainToolTier: tool.DomainTierExtended,
			},
		},
		index: index,
		lsp:   lsp,
	}
}

type verifyCallersTool struct {
	tool.BaseTool
	index *CodeIndex
	lsp   LSPBridge
}

func (t *verifyCallersTool) IsBackendConfigured() bool {
	_, isNoop := t.lsp.(NoopLSP)
	return !isNoop && t.index != nil
}

func (t *verifyCallersTool) Name() string { return "verify_callers" }

func (t *verifyCallersTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "verify_callers",
		Description: "Confirm the real callers of a function or method before rename or delete. Prefer when the symbol may share a name with unrelated methods on other types.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "File where the symbol is defined."
				},
				"line": {
					"type": "integer",
					"description": "Line (0-based) of the symbol definition."
				},
				"column": {
					"type": "integer",
					"description": "Column (0-based) of the symbol name."
				},
				"symbol": {
					"type": "string",
					"description": "Symbol name (alternative to path/line/column — will be resolved via code index)."
				}
			}
		}`),
	}
}

func (t *verifyCallersTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		Path   string `json:"path"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
		Symbol string `json:"symbol"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}

	// Resolve symbol to location if only name given
	if params.Path == "" && params.Symbol != "" {
		results, err := t.index.FindSymbol(ctx, params.Symbol)
		if err != nil || len(results) == 0 {
			return &tool.ToolResult{Content: fmt.Sprintf("Symbol %q not found in index.", params.Symbol)}, nil
		}
		params.Path = results[0].FilePath
		params.Line = results[0].LineStart
		params.Column = 0
	}

	if params.Path == "" {
		return &tool.ToolResult{Content: "either path+line or symbol is required", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if tc != nil && projectRoot(tc) != "" && !filepath.IsAbs(params.Path) {
		params.Path = filepath.Join(projectRoot(tc), params.Path)
	}

	// Step 1: Get CKG callers (syntax-level, may have false positives)
	symbolName := params.Symbol
	if symbolName == "" {
		symbolName = filepath.Base(params.Path) + ":" + fmt.Sprintf("L%d", params.Line)
	}

	// Step 2: LSP Call Hierarchy (type-precise)
	items, err := t.lsp.PrepareCallHierarchy(ctx, params.Path, params.Line, params.Column)
	if err != nil || len(items) == 0 {
		// LSP unavailable — fall back to CKG only
		r, nr, e := t.ckgOnlyResult(ctx, params.Symbol, tc)
		notReady = nr
		return r, e
	}

	incoming, err := t.lsp.IncomingCalls(ctx, items[0])
	if err != nil {
		r, nr, e := t.ckgOnlyResult(ctx, params.Symbol, tc)
		notReady = nr
		return r, e
	}

	// Format verified results
	var sb strings.Builder
	fmt.Fprintf(&sb, "Verified callers of %q:\n\n", items[0].Name)

	if len(incoming) == 0 {
		// 空列表而非 nil：这个工具存在的理由就是核实"真的没人调用"——它是 LSP 验证
		// 过的答案，比 CKG 的推断可信，而那正是用户敢删代码的依据。
		list = &ListData{Items: []ListItem{}}
		sb.WriteString("  No callers found (this symbol may be unused).\n")
	} else {
		verified := make([]ListItem, 0, len(incoming))
		fmt.Fprintf(&sb, "%d verified caller(s):\n\n", len(incoming))
		for i, call := range incoming {
			displayPath := call.From.Path
			if tc != nil && projectRoot(tc) != "" {
				if rel, err := filepath.Rel(projectRoot(tc), call.From.Path); err == nil && !strings.HasPrefix(rel, "..") {
					displayPath = rel
				}
			}
			fmt.Fprintf(&sb, "  %d. %s (at %s:%d)\n", i+1, call.From.Name, displayPath, call.From.Line+1)
			// File 用绝对路径而非 displayPath：后者是给人读的相对形式，而点击跳转
			// 要的是能被 openFile 解析的路径。Line 与文本同源（+1）。
			verified = append(verified, ListItem{
				Label: call.From.Name,
				File:  call.From.Path,
				Line:  call.From.Line + 1,
				Kind:  "verified",
			})
		}
		list = ListOf(verified, 0)
	}

	meta := map[string]any{"source": "lsp", "verified": true}
	// Compare with index count if symbol name available — diagnostics go to Metadata (INV-UX-01 L-2).
	if params.Symbol != "" && t.index != nil {
		ckgCallers, _ := t.index.CallersOf(ctx, params.Symbol, 50)
		if len(ckgCallers) > 0 {
			ckgCount := len(ckgCallers)
			lspCount := len(incoming)
			if ckgCount > lspCount {
				meta["index_caller_count"] = ckgCount
				meta["verified_caller_count"] = lspCount
				meta["note"] = fmt.Sprintf("%d index callers were not confirmed; likely same-name methods on other types", ckgCount-lspCount)
			}
		}
	}

	return &tool.ToolResult{Content: sb.String(), Metadata: meta}, nil
}

// ckgOnlyResult 是 LSP 不可用时的退路：只靠 CKG 回答"谁调了它"。
//
// notReady 回给调用方而不是自己塞进结果：这个函数不持有信封（信封在 Call 的
// defer 里装），而两种失败原因的语义不同，必须分开传回去。
func (t *verifyCallersTool) ckgOnlyResult(ctx context.Context, symbol string, tc *tool.ToolContext) (res *tool.ToolResult, notReady bool, err error) {
	// 两种原因原先合成一句"No symbol name provided and ... is unavailable"，而它们
	// 对用户的下一步相反：没给符号名是**调用方**该补参数（empty），索引缺失是
	// **产品**还没准备好（not_ready，去建索引）。合起来说等于两边都答不了。
	if t.index == nil {
		return &tool.ToolResult{
			Content: "Precise caller verification not available — the code index is not ready.",
		}, true, nil
	}
	if symbol == "" {
		return &tool.ToolResult{Content: "No symbol name provided."}, false, nil
	}
	callers, err := t.index.CallersOf(ctx, symbol, 30)
	if err != nil || len(callers) == 0 {
		return &tool.ToolResult{Content: fmt.Sprintf("No callers found for %q.", symbol)}, false, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d caller(s) of %q:\n\n", len(callers), symbol)
	for i, c := range callers {
		displayPath := c.FilePath
		if tc != nil && projectRoot(tc) != "" {
			if rel, err := filepath.Rel(projectRoot(tc), c.FilePath); err == nil && !strings.HasPrefix(rel, "..") {
				displayPath = rel
			}
		}
		fmt.Fprintf(&sb, "  %d. %s %s (at %s:%d)\n", i+1, c.Kind, c.Name, displayPath, c.LineStart+1)
	}
	// INV-UX-01 L-2: unverified/source notes belong in Metadata, not Content.
	return &tool.ToolResult{
		Content: sb.String(),
		Metadata: map[string]any{
			"source":   "index",
			"verified": false,
			"note":     "results may include same-name methods on unrelated types",
		},
	}, false, nil
}
