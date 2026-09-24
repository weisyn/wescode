package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/weisyn/wesgine/tool"
)

func NewLSPOrganizeImportsTool(lsp LSPBridge) tool.Tool {
	return &lspOrganizeImportsTool{
		BaseTool: tool.BaseTool{
			Meta: tool.ToolMeta{
				Dimension:    tool.DimensionAct,
				Availability: tool.AvailabilityConfigurable,
				ReadOnly:     false,
				Concurrent:   false,
				Timeout:      10 * time.Second,
				Risk:         tool.RiskSafe,
				PolicyFamily: tool.PolicyFamilyOther,
				Enabled:      true,
			},
		},
		lsp: lsp,
	}
}

type lspOrganizeImportsTool struct {
	tool.BaseTool
	lsp LSPBridge
}

func (t *lspOrganizeImportsTool) IsBackendConfigured() bool {
	_, isNoop := t.lsp.(NoopLSP)
	return !isNoop
}

func (t *lspOrganizeImportsTool) Name() string { return "lsp_organize_imports" }

func (t *lspOrganizeImportsTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "lsp_organize_imports",
		Description: "Organize imports in a file using the language server (add missing, remove unused, sort). This is the equivalent of VSCode's 'Organize Imports' command. Use after editing a file to ensure imports are correct — much more reliable than manually writing import statements.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "File path to organize imports for."
				}
			},
			"required": ["path"]
		}`),
	}
}

func (t *lspOrganizeImportsTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。本工具不产出列表（输出形态不是可跳转条目），
	// 传 nil 得到 category=text + 分级——分级本身仍是信息（PC-02）。
	var notReady bool
	defer func() { res = PresentList(res, notReady, nil) }()

	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Path == "" {
		return &tool.ToolResult{Content: "path is required", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if tc != nil && projectRoot(tc) != "" && !filepath.IsAbs(params.Path) {
		params.Path = filepath.Join(projectRoot(tc), params.Path)
	}

	edits, err := t.lsp.OrganizeImports(ctx, params.Path)
	if err != nil {
		return &tool.ToolResult{Content: "organize imports failed: " + err.Error()}, nil
	}
	if len(edits) == 0 {
		return &tool.ToolResult{Content: "Imports are already organized (no changes needed)."}, nil
	}

	totalEdits := 0
	for _, fe := range edits {
		totalEdits += len(fe.Edits)
	}

	displayPath := params.Path
	if tc != nil && projectRoot(tc) != "" {
		if rel, err := filepath.Rel(projectRoot(tc), params.Path); err == nil {
			displayPath = rel
		}
	}

	return &tool.ToolResult{Content: fmt.Sprintf("Organized imports in %s: %d edit(s) applied.", displayPath, totalEdits)}, nil
}
