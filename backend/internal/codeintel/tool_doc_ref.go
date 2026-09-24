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

// NewDocReferencesTool creates a "find_doc_references" tool that finds
// project documentation passages mentioning a given code symbol. This bridges
// the CKG (code knowledge) with Knowledge (document knowledge) — the agent
// can discover which design docs, AGENTS.md sections, or READMEs describe
// constraints or requirements for a specific symbol.
func NewDocReferencesTool(docRef *DocRefIndex) tool.Tool {
	return &docReferencesTool{
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
				Domain:         "knowledge",
				DomainToolTier: tool.DomainTierExtended,
			},
		},
		docRef: docRef,
	}
}

type docReferencesTool struct {
	tool.BaseTool
	docRef *DocRefIndex
}

func (t *docReferencesTool) Name() string { return "find_doc_references" }

func (t *docReferencesTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "find_doc_references",
		Description: "Find project documentation passages that mention a code symbol. Returns design docs, AGENTS.md sections, and other indexed documents that describe, constrain, or reference the given symbol. Use this to understand design intent, invariants, or architectural context before modifying code.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["symbol"],
			"properties": {
				"symbol": {"type": "string", "description": "Symbol name to search for in documentation (e.g. 'Cell', 'UserService', 'enrichFile')"},
				"limit": {"type": "integer", "description": "Maximum number of references to return (default: 20)"}
			}
		}`),
	}
}

func (t *docReferencesTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		Symbol string `json:"symbol"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Symbol == "" {
		return &tool.ToolResult{Content: `{"error": "symbol is required"}`, IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Limit <= 0 {
		params.Limit = 20
	}

	refs, err := t.docRef.FindBySymbol(ctx, params.Symbol, params.Limit)
	if err != nil {
		return nil, fmt.Errorf("find doc refs: %w", err)
	}

	if len(refs) == 0 {
		refs, err = t.docRef.SearchBySymbolPrefix(ctx, params.Symbol, params.Limit)
		if err != nil {
			return nil, fmt.Errorf("search doc refs: %w", err)
		}
	}

	if len(refs) == 0 {
		// 空列表而非 nil：落 empty（"没有文档提到它"是答案——意味着改它不必先读设计）。
		list = &ListData{Items: []ListItem{}}
		return &tool.ToolResult{
			Content: fmt.Sprintf(`{"symbol": %q, "references": [], "note": "No documentation references found for this symbol."}`, params.Symbol),
		}, nil
	}

	// 一项 = 一处文档引用。label 用**章节标题**而不是文件名：这个工具回答"改代码前
	// 要读哪段设计"，而章节名才说得出那段在讲什么；文件名在位置列已有。
	//
	// Context 为空是可能的（符号出现在首个标题之前，或文档读不到），那时退回文件名——
	// 不能留空 label，wesui 会整体拒收该列表。
	items := make([]ListItem, 0, len(refs))
	for _, r := range refs {
		label := r.Context
		if label == "" {
			label = filepath.Base(r.DocFile)
		}
		kind := "unresolved"
		if r.SymbolID != nil {
			kind = "matched"
		}
		items = append(items, ListItem{
			Label:  label,
			Detail: r.SymbolName,
			File:   r.DocFile,
			// DocLine 已是 1-based（locateSymbols 从 1 开始计），0 表示没定位到——
			// 那时不填，让整行仍可点击但跳到文件头，而不是假装命中在第 1 行。
			Line: r.DocLine,
			Kind: kind,
		})
	}
	list = ListOf(items, 0)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`{"symbol": %q, "references": [`, params.Symbol))
	for i, r := range refs {
		if i > 0 {
			sb.WriteByte(',')
		}
		resolved := "unresolved"
		if r.SymbolID != nil {
			resolved = "matched"
		}
		// 位置与章节也进 Content：模型正是那个要决定"该不该先读这段设计"的角色，
		// 而"AGENTS.md 提到了 Cell"这句话不足以让它做那个决定。
		sb.WriteString(fmt.Sprintf(`{"doc_file": %q, "doc_line": %d, "section": %q, "symbol_name": %q, "resolution": %q}`,
			r.DocFile, r.DocLine, r.Context, r.SymbolName, resolved))
	}
	sb.WriteString("]}")
	return &tool.ToolResult{Content: sb.String()}, nil
}
