package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/weisyn/wescode/internal/notify"
	"github.com/weisyn/wesgine/tool"
)

// --- evaluate (Extended) ---

func NewEvaluateTool(cfg DebugToolsConfig) tool.Tool {
	return &evaluateTool{
		BaseTool: tool.BaseTool{Meta: tool.ToolMeta{
			Dimension: tool.DimensionAct, ReadOnly: true, Concurrent: false,
			Timeout: 5 * time.Second, Risk: tool.RiskModerate, Enabled: true,
			Domain: "debug", DomainToolTier: tool.DomainTierExtended,
		}},
		cfg: cfg,
	}
}

type evaluateTool struct {
	tool.BaseTool
	cfg DebugToolsConfig
}

func (t *evaluateTool) Name() string { return "evaluate" }
func (t *evaluateTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "evaluate",
		Description: "Evaluate an expression in the context of the current debug session's stopped frame. Only works when the debugger is paused at a breakpoint or exception (5s timeout to prevent deadlock expressions).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"expression":{"type":"string","description":"The expression to evaluate in the current stack frame"},"frame_index":{"type":"integer","description":"Stack frame index (0 = top frame, default)"}},"required":["expression"]}`),
	}
}

func (t *evaluateTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。本工具不产出列表（输出形态不是可跳转条目），
	// 传 nil 得到 category=text + 分级——分级本身仍是信息（PC-02）。
	var notReady bool
	defer func() { res = PresentList(res, notReady, nil) }()

	var args struct {
		Expression string `json:"expression"`
		FrameIndex int    `json:"frame_index"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return &tool.ToolResult{Content: "Invalid arguments: " + err.Error(), IsError: true}, nil
	}
	if args.Expression == "" {
		return &tool.ToolResult{Content: "expression is required", IsError: true}, nil
	}
	if t.cfg.RequestDebug == nil {
		notReady = true
		return &tool.ToolResult{Content: "Debug bridge not connected (IDE not available)", IsError: true}, nil
	}

	resp, err := t.cfg.RequestDebug(ctx, notify.DebugEvaluate, map[string]any{
		"expression":  args.Expression,
		"frame_index": args.FrameIndex,
	})
	if err != nil {
		return &tool.ToolResult{
			Content: fmt.Sprintf("Evaluate failed: %s\n\nMake sure the debugger is paused at a breakpoint.", err),
			IsError: true,
		}, nil
	}

	var result struct {
		Value string `json:"result"`
		Type  string `json:"type"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return &tool.ToolResult{Content: string(resp)}, nil
	}

	content := fmt.Sprintf("**%s** = `%s`", args.Expression, result.Value)
	if result.Type != "" {
		content += fmt.Sprintf(" (type: %s)", result.Type)
	}
	return &tool.ToolResult{Content: content}, nil
}

// --- debug_continue (Extended) ---

func NewDebugContinueTool(cfg DebugToolsConfig) tool.Tool {
	return &debugContinueTool{
		BaseTool: tool.BaseTool{Meta: tool.ToolMeta{
			Dimension: tool.DimensionAct, ReadOnly: false, Concurrent: false,
			Timeout: 5 * time.Second, Risk: tool.RiskModerate, Enabled: true,
			Domain: "debug", DomainToolTier: tool.DomainTierExtended,
		}},
		cfg: cfg,
	}
}

type debugContinueTool struct {
	tool.BaseTool
	cfg DebugToolsConfig
}

func (t *debugContinueTool) Name() string { return "debug_continue" }
func (t *debugContinueTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "debug_continue",
		Description: "Control the debug session: continue execution, step over, step into, or step out. Only works when the debugger is paused.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["continue","step_over","step_into","step_out"],"description":"Debug action to perform"}},"required":["action"]}`),
	}
}

func (t *debugContinueTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。本工具不产出列表（输出形态不是可跳转条目），
	// 传 nil 得到 category=text + 分级——分级本身仍是信息（PC-02）。
	var notReady bool
	defer func() { res = PresentList(res, notReady, nil) }()

	var args struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return &tool.ToolResult{Content: "Invalid arguments: " + err.Error(), IsError: true}, nil
	}
	if t.cfg.RequestDebug == nil {
		notReady = true
		return &tool.ToolResult{Content: "Debug bridge not connected (IDE not available)", IsError: true}, nil
	}

	_, err = t.cfg.RequestDebug(ctx, notify.DebugContinue, map[string]any{
		"action": args.Action,
	})
	if err != nil {
		return &tool.ToolResult{
			Content: fmt.Sprintf("Debug %s failed: %s", args.Action, err),
			IsError: true,
		}, nil
	}

	actionLabels := map[string]string{
		"continue":  "Resumed execution",
		"step_over": "Stepped over to next line",
		"step_into": "Stepped into function call",
		"step_out":  "Stepped out to caller",
	}
	label := actionLabels[args.Action]
	if label == "" {
		label = args.Action
	}
	return &tool.ToolResult{Content: label + ". Use debug_context to see where execution stopped next."}, nil
}
