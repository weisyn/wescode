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

func NewLSPRenameTool(lsp LSPBridge) tool.Tool {
	return &lspRenameTool{
		BaseTool: tool.BaseTool{
			Meta: tool.ToolMeta{
				Dimension:    tool.DimensionAct,
				Availability: tool.AvailabilityConfigurable,
				ReadOnly:     false,
				Concurrent:   false,
				Timeout:      20 * time.Second,
				Risk:         tool.RiskModerate,
				PolicyFamily: tool.PolicyFamilyOther,
				Enabled:      true,
			},
		},
		lsp: lsp,
	}
}

type lspRenameTool struct {
	tool.BaseTool
	lsp LSPBridge
}

func (t *lspRenameTool) IsBackendConfigured() bool {
	_, isNoop := t.lsp.(NoopLSP)
	return !isNoop
}

func (t *lspRenameTool) Name() string { return "lsp_rename" }

func (t *lspRenameTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "lsp_rename",
		Description: "Rename a symbol using the language server. This is more precise than grep+edit because the LSP knows all true references (not string literals, comments, or unrelated same-name symbols). Works across multiple files. Use this instead of manual multi-file edits when renaming functions, variables, types, or methods.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "File path where the symbol is defined or used."
				},
				"line": {
					"type": "integer",
					"description": "Line number (0-based) of the symbol to rename."
				},
				"column": {
					"type": "integer",
					"description": "Column number (0-based) within the symbol name."
				},
				"new_name": {
					"type": "string",
					"description": "The new name for the symbol."
				}
			},
			"required": ["path", "line", "column", "new_name"]
		}`),
	}
}

func (t *lspRenameTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。本工具不产出列表（输出形态不是可跳转条目），
	// 传 nil 得到 category=text + 分级——分级本身仍是信息（PC-02）。
	var notReady bool
	defer func() { res = PresentList(res, notReady, nil) }()

	var params struct {
		Path    string `json:"path"`
		Line    int    `json:"line"`
		Column  int    `json:"column"`
		NewName string `json:"new_name"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Path == "" || params.NewName == "" {
		return &tool.ToolResult{Content: "path and new_name are required", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if tc != nil && projectRoot(tc) != "" && !filepath.IsAbs(params.Path) {
		params.Path = filepath.Join(projectRoot(tc), params.Path)
	}

	edits, err := t.lsp.Rename(ctx, params.Path, params.Line, params.Column, params.NewName)
	if err != nil {
		return &tool.ToolResult{Content: "rename failed: " + err.Error() + "\n\nThe symbol may not be renamable at this location. Fallback to manual edit if needed.", IsError: true}, nil
	}
	if len(edits) == 0 {
		return &tool.ToolResult{Content: "Rename produced no edits. The language server may not support rename at this position, or the symbol was not found."}, nil
	}

	var sb strings.Builder
	totalEdits := 0
	for _, fe := range edits {
		totalEdits += len(fe.Edits)
	}
	fmt.Fprintf(&sb, "Renamed to \"%s\": %d replacement(s) across %d file(s).\n\n", params.NewName, totalEdits, len(edits))

	for _, fe := range edits {
		displayPath := fe.Path
		if tc != nil && projectRoot(tc) != "" {
			if rel, err := filepath.Rel(projectRoot(tc), fe.Path); err == nil {
				displayPath = rel
			}
		}
		fmt.Fprintf(&sb, "  %s: %d edit(s)\n", displayPath, len(fe.Edits))
	}
	return &tool.ToolResult{Content: sb.String()}, nil
}
