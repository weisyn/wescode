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

func NewApplyCodeActionTool(lsp LSPBridge) tool.Tool {
	return &applyCodeActionTool{
		BaseTool: tool.BaseTool{
			Meta: tool.ToolMeta{
				Dimension:    tool.DimensionAct,
				Availability: tool.AvailabilityConfigurable,
				ReadOnly:     false,
				Concurrent:   false,
				Timeout:      15 * time.Second,
				Risk:         tool.RiskModerate,
				PolicyFamily: tool.PolicyFamilyOther,
				Enabled:      true,
			},
		},
		lsp: lsp,
	}
}

type applyCodeActionTool struct {
	tool.BaseTool
	lsp LSPBridge
}

func (t *applyCodeActionTool) IsBackendConfigured() bool {
	_, isNoop := t.lsp.(NoopLSP)
	return !isNoop
}

func (t *applyCodeActionTool) Name() string { return "apply_code_action" }

func (t *applyCodeActionTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "apply_code_action",
		Description: "Apply a code action (quick fix, refactoring) from the language server at a specific location. This uses the IDE's built-in fixes (like auto-import, remove unused variable, etc.) which are more precise than manual editing. Use `get_diagnostics` first to identify errors, then this tool to apply LSP-provided fixes.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "File path where the code action should be applied."
				},
				"line": {
					"type": "integer",
					"description": "Line number (0-based) of the diagnostic/location."
				},
				"column": {
					"type": "integer",
					"description": "Column number (0-based) of the diagnostic/location. Default: 0."
				},
				"end_line": {
					"type": "integer",
					"description": "End line of the range (0-based). Defaults to same as line."
				},
				"end_column": {
					"type": "integer",
					"description": "End column of the range (0-based). Defaults to end of line."
				},
				"action_title": {
					"type": "string",
					"description": "Exact title of the code action to apply. Use without this parameter to list available actions."
				}
			},
			"required": ["path", "line"]
		}`),
	}
}

func (t *applyCodeActionTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。这个工具有两种形态：
	//   无 action_title → **列出**可用动作（actionable：每项一个可点的修复）
	//   有 action_title → **已经应用**了（text：那是结果确认，不是待办）
	// 第二种不该是 actionable——动作已经发生了，再画一个按钮等于请用户重做一次。
	var actions *ActionableData
	var notReady bool
	defer func() { res = PresentActionable(res, notReady, actions) }()

	var params struct {
		Path        string `json:"path"`
		Line        int    `json:"line"`
		Column      int    `json:"column"`
		EndLine     *int   `json:"end_line"`
		EndColumn   *int   `json:"end_column"`
		ActionTitle string `json:"action_title"`
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

	endLine := params.Line
	if params.EndLine != nil {
		endLine = *params.EndLine
	}
	endCol := params.Column + 1
	if params.EndColumn != nil {
		endCol = *params.EndColumn
	}

	if params.ActionTitle == "" {
		available, aerr := t.lsp.CodeActions(ctx, params.Path, params.Line, params.Column, endLine, endCol)
		if aerr != nil {
			return &tool.ToolResult{Content: "failed to get code actions: " + aerr.Error(), IsError: true}, nil
		}
		if len(available) == 0 {
			// 空列表而非 nil：落 empty（"这里没有可用的自动修复"是答案，
			// 用户据此知道要手工改）。
			actions = ActionableOf(nil, 0)
			return &tool.ToolResult{Content: "No code actions available at this location."}, nil
		}

		// 一项 = 一个可用动作。prompt 带上精确标题与位置——它会被原样提交回对话，
		// 而 LSP 靠标题精确匹配，说得含糊模型就会挑错一个。
		items := make([]ActionableItem, 0, len(available))
		for _, a := range available {
			style := ""
			if a.IsPreferred {
				style = "primary"
			}
			items = append(items, ActionableItem{
				Label:  a.Title,
				Detail: a.Kind,
				File:   params.Path,
				// LSP 族 schema 声明 0-based 入参，而 wire 统一 1-based。
				Line: params.Line + 1,
				Kind: "code action",
				Actions: []ActionRef{{
					Label: "应用",
					Prompt: fmt.Sprintf("Apply code action %q at %s:%d:%d",
						a.Title, params.Path, params.Line, params.Column),
					Style: style,
				}},
			})
		}
		actions = ActionableOf(items, 0)

		var sb strings.Builder
		fmt.Fprintf(&sb, "%d code action(s) available:\n\n", len(available))
		for _, a := range available {
			preferred := ""
			if a.IsPreferred {
				preferred = " [preferred]"
			}
			kind := ""
			if a.Kind != "" {
				kind = fmt.Sprintf(" (%s)", a.Kind)
			}
			fmt.Fprintf(&sb, "  • \"%s\"%s%s\n", a.Title, kind, preferred)
		}
		sb.WriteString("\nUse the `action_title` parameter with the exact title to apply one.")
		return &tool.ToolResult{Content: sb.String()}, nil
	}

	edits, err := t.lsp.ApplyCodeAction(ctx, params.Path, params.Line, params.Column, endLine, endCol, params.ActionTitle)
	if err != nil {
		return &tool.ToolResult{Content: "failed to apply code action: " + err.Error(), IsError: true}, nil
	}
	if len(edits) == 0 {
		return &tool.ToolResult{Content: fmt.Sprintf("Code action \"%s\" produced no edits (action may not be available at this location).", params.ActionTitle)}, nil
	}

	var sb strings.Builder
	totalEdits := 0
	for _, fe := range edits {
		totalEdits += len(fe.Edits)
	}
	fmt.Fprintf(&sb, "Applied \"%s\": %d edit(s) across %d file(s).\n", params.ActionTitle, totalEdits, len(edits))
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
