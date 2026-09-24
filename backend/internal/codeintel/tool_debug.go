package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/weisyn/wescode/internal/notify"
	"github.com/weisyn/wesgine/tool"
)

// DebugStateProvider abstracts the debug event store to avoid import cycles.
type DebugStateProvider interface {
	DebugContext() map[string]any
	DebugEventsRaw(n int) []DebugEventRaw
}

// DebugEventRaw is a minimal debug event view for tool consumption.
type DebugEventRaw struct {
	Kind       string             `json:"kind"`
	Timestamp  time.Time          `json:"timestamp"`
	StopReason string             `json:"stop_reason,omitempty"`
	Frames     []DebugFrameRaw    `json:"frames,omitempty"`
	Variables  []DebugVariableRaw `json:"variables,omitempty"`
	Exception  *DebugExceptionRaw `json:"exception,omitempty"`
	CrashText  string             `json:"crash_text,omitempty"`
}

type DebugFrameRaw struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Line   int    `json:"line"`
}

type DebugVariableRaw struct {
	Scope string `json:"scope"`
	Name  string `json:"name"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

type DebugExceptionRaw struct {
	Description string `json:"description"`
	StackTrace  string `json:"stack_trace"`
}

// DebugToolsConfig configures the debug domain tools.
type DebugToolsConfig struct {
	State        DebugStateProvider
	RequestDebug func(ctx context.Context, method notify.Method, params any) (json.RawMessage, error)
}

// --- debug_context (Core) ---

func NewDebugContextTool(cfg DebugToolsConfig) tool.Tool {
	return &debugContextTool{
		BaseTool: tool.BaseTool{Meta: tool.ToolMeta{
			Dimension: tool.DimensionPerceive, ReadOnly: true, Concurrent: true,
			Timeout: 5 * time.Second, Risk: tool.RiskSafe, Enabled: true,
			Domain: "debug", DomainToolTier: tool.DomainTierCore,
		}},
		cfg: cfg,
	}
}

type debugContextTool struct {
	tool.BaseTool
	cfg DebugToolsConfig
}

func (t *debugContextTool) Name() string { return "debug_context" }
func (t *debugContextTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "debug_context",
		Description: "Get the current debug state: active sessions, recent events, last exception. Use to understand what happened at runtime before investigating code.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer","description":"Number of recent debug events to return (default 5, max 20)"}}}`),
	}
}

func (t *debugContextTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。本工具不产出列表（输出形态不是可跳转条目），
	// 传 nil 得到 category=text + 分级——分级本身仍是信息（PC-02）。
	var notReady bool
	defer func() { res = PresentList(res, notReady, nil) }()

	var args struct {
		Count int `json:"count"`
	}
	_ = json.Unmarshal(input, &args)
	if args.Count <= 0 {
		args.Count = 5
	}
	if args.Count > 20 {
		args.Count = 20
	}
	debugCtx := t.cfg.State.DebugContext()
	events := t.cfg.State.DebugEventsRaw(args.Count)

	var sb strings.Builder
	sb.WriteString("## Debug Context\n\n")
	if exc, ok := debugCtx["last_exception"].(map[string]any); ok {
		sb.WriteString(fmt.Sprintf("**Last Exception**: %s at %s:%v\n\n", exc["kind"], exc["file"], exc["line"]))
	}
	sb.WriteString(fmt.Sprintf("Total events: %v\n\n", debugCtx["event_count"]))
	if len(events) > 0 {
		sb.WriteString("### Recent Events\n\n")
		for i, e := range events {
			sb.WriteString(fmt.Sprintf("%d. **%s** [%s]", i+1, e.Kind, e.Timestamp.Format("15:04:05")))
			if len(e.Frames) > 0 {
				sb.WriteString(fmt.Sprintf(" at %s:%d %s", e.Frames[0].Source, e.Frames[0].Line, e.Frames[0].Name))
			}
			if e.CrashText != "" {
				lines := strings.SplitN(e.CrashText, "\n", 3)
				sb.WriteString(fmt.Sprintf("\n   ```\n   %s\n   ```", strings.Join(lines, "\n   ")))
			}
			sb.WriteByte('\n')
		}
	}
	return &tool.ToolResult{Content: sb.String()}, nil
}

// --- debug_stacktrace (Core) ---

func NewDebugStacktraceTool(cfg DebugToolsConfig) tool.Tool {
	return &debugStacktraceTool{
		BaseTool: tool.BaseTool{Meta: tool.ToolMeta{
			Dimension: tool.DimensionPerceive, ReadOnly: true, Concurrent: true,
			Timeout: 5 * time.Second, Risk: tool.RiskSafe, Enabled: true,
			Domain: "debug", DomainToolTier: tool.DomainTierCore,
		}},
		cfg: cfg,
	}
}

type debugStacktraceTool struct {
	tool.BaseTool
	cfg DebugToolsConfig
}

func (t *debugStacktraceTool) Name() string { return "debug_stacktrace" }
func (t *debugStacktraceTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "debug_stacktrace",
		Description: "Get the full call stack and variables from the last breakpoint hit or exception. Shows where execution stopped and what values variables held.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}
}

func (t *debugStacktraceTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	events := t.cfg.State.DebugEventsRaw(1)
	if len(events) == 0 {
		// not_ready 而非 empty：还没有调试事件 ≠ 程序没有栈。
		// 报 empty 会让模型以为"这里没有调用栈"，于是放弃一条本该有的线索。
		notReady = true
		return &tool.ToolResult{Content: "No debug events captured. Start a debug session and hit a breakpoint or trigger an exception first."}, nil
	}
	last := events[0]
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## %s", capitalize(strings.ReplaceAll(last.Kind, "_", " "))))
	if last.StopReason != "" {
		sb.WriteString(fmt.Sprintf(" (reason: %s)", last.StopReason))
	}
	sb.WriteString("\n\n")
	if last.Exception != nil {
		sb.WriteString(fmt.Sprintf("**Exception**: %s\n%s\n\n", last.Exception.Description, last.Exception.StackTrace))
	}
	if last.CrashText != "" {
		sb.WriteString("**Crash Output**:\n```\n" + last.CrashText + "\n```\n\n")
	}
	if len(last.Frames) > 0 {
		// 一项 = 一个栈帧。顺序即调用栈（列表保序），detail 标出栈深——用户看栈是
		// 为了找"哪一帧是我的代码"，而那一帧的行号正是他要跳过去的地方。
		// f.Line 来自 DAP（Debug Adapter Protocol），协议规定 1-based，不加。
		frames := make([]ListItem, 0, len(last.Frames))
		for i, f := range last.Frames {
			kind := "frame"
			if i == 0 {
				kind = "current"
			}
			frames = append(frames, ListItem{
				Label:  f.Name,
				Detail: fmt.Sprintf("depth %d", i),
				File:   f.Source,
				Line:   f.Line,
				Kind:   kind,
			})
		}
		list = ListOf(frames, 0)

		sb.WriteString("### Call Stack\n\n")
		for i, f := range last.Frames {
			marker := "  "
			if i == 0 {
				marker = "► "
			}
			sb.WriteString(fmt.Sprintf("%s%s:%d — %s\n", marker, f.Source, f.Line, f.Name))
		}
		sb.WriteByte('\n')
	}
	if len(last.Variables) > 0 {
		sb.WriteString("### Variables\n\n")
		currentScope := ""
		for _, v := range last.Variables {
			if v.Scope != currentScope {
				currentScope = v.Scope
				sb.WriteString(fmt.Sprintf("**%s**:\n", capitalize(currentScope)))
			}
			sb.WriteString(fmt.Sprintf("  %s: %s = %s\n", v.Type, v.Name, v.Value))
		}
	}
	return &tool.ToolResult{Content: sb.String()}, nil
}

// --- set_breakpoint (Core) ---

func NewSetBreakpointTool(cfg DebugToolsConfig) tool.Tool {
	return &setBreakpointTool{
		BaseTool: tool.BaseTool{Meta: tool.ToolMeta{
			Dimension: tool.DimensionAct, ReadOnly: false, Concurrent: false,
			Timeout: 10 * time.Second, Risk: tool.RiskModerate, Enabled: true,
			Domain: "debug", DomainToolTier: tool.DomainTierCore,
		}},
		cfg: cfg,
	}
}

type setBreakpointTool struct {
	tool.BaseTool
	cfg DebugToolsConfig
}

func (t *setBreakpointTool) Name() string { return "set_breakpoint" }
func (t *setBreakpointTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "set_breakpoint",
		Description: "Set or remove a breakpoint in the IDE debugger. Supports conditional breakpoints.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["set","remove"],"description":"Whether to set or remove a breakpoint"},"file":{"type":"string","description":"Absolute path to the source file"},"line":{"type":"integer","description":"Line number for the breakpoint"},"condition":{"type":"string","description":"Optional condition expression"}},"required":["action","file","line"]}`),
	}
}

func (t *setBreakpointTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。本工具不产出列表（输出形态不是可跳转条目），
	// 传 nil 得到 category=text + 分级——分级本身仍是信息（PC-02）。
	var notReady bool
	defer func() { res = PresentList(res, notReady, nil) }()

	var args struct {
		Action    string `json:"action"`
		File      string `json:"file"`
		Line      int    `json:"line"`
		Condition string `json:"condition"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return &tool.ToolResult{Content: "Invalid arguments: " + err.Error(), IsError: true}, nil
	}
	if t.cfg.RequestDebug == nil {
		notReady = true
		return &tool.ToolResult{Content: "Debug bridge not connected (IDE not available)", IsError: true}, nil
	}
	_, err = t.cfg.RequestDebug(ctx, notify.DebugBreakpoint, map[string]any{
		"action": args.Action, "file": args.File, "line": args.Line, "condition": args.Condition,
	})
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("Failed to %s breakpoint at %s:%d: %s", args.Action, args.File, args.Line, err), IsError: true}, nil
	}
	action := "Set"
	if args.Action == "remove" {
		action = "Removed"
	}
	detail := ""
	if args.Condition != "" {
		detail = fmt.Sprintf(" (condition: %s)", args.Condition)
	}
	return &tool.ToolResult{Content: fmt.Sprintf("%s breakpoint at %s:%d%s", action, args.File, args.Line, detail)}, nil
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
