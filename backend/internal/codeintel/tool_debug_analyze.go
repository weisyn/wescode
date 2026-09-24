package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/weisyn/wescode/internal/platform"
	"github.com/weisyn/wesgine/tool"
)

// --- analyze_crash (Extended) ---

func NewAnalyzeCrashTool(cfg DebugToolsConfig) tool.Tool {
	return &analyzeCrashTool{
		BaseTool: tool.BaseTool{Meta: tool.ToolMeta{
			Dimension: tool.DimensionPerceive, ReadOnly: true, Concurrent: true,
			Timeout: 30 * time.Second, Risk: tool.RiskSafe, Enabled: true,
			Domain: "debug", DomainToolTier: tool.DomainTierExtended,
		}},
		cfg: cfg,
	}
}

type analyzeCrashTool struct {
	tool.BaseTool
	cfg DebugToolsConfig
}

func (t *analyzeCrashTool) Name() string { return "analyze_crash" }
func (t *analyzeCrashTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "analyze_crash",
		Description: "Analyze a crash or exception by correlating the stack trace with source code. Extracts the crash frames, reads the relevant source lines, and provides a structured analysis. Works with Go panics, Python tracebacks, and JavaScript/TypeScript errors.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"stack_trace":{"type":"string","description":"The full stack trace or crash output to analyze. If empty, uses the last captured crash from debug_context."}}}`),
	}
}

func (t *analyzeCrashTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。本工具不产出列表（输出形态不是可跳转条目），
	// 传 nil 得到 category=text + 分级——分级本身仍是信息（PC-02）。
	var notReady bool
	defer func() { res = PresentList(res, notReady, nil) }()

	var args struct {
		StackTrace string `json:"stack_trace"`
	}
	_ = json.Unmarshal(input, &args)

	if args.StackTrace == "" {
		events := t.cfg.State.DebugEventsRaw(5)
		for _, e := range events {
			if e.Kind == "exception" || e.Kind == "crash" {
				if e.CrashText != "" {
					args.StackTrace = e.CrashText
				} else if e.Exception != nil {
					args.StackTrace = e.Exception.StackTrace
				}
				break
			}
		}
	}

	if args.StackTrace == "" {
		return &tool.ToolResult{Content: "No crash data available. Provide a stack_trace argument or trigger a crash/exception first."}, nil
	}

	var sb strings.Builder
	sb.WriteString("## Crash Analysis\n\n")

	// Extract file:line references from the stack trace
	refs := extractFileLineRefs(args.StackTrace)
	if len(refs) == 0 {
		sb.WriteString("Could not extract file references from the stack trace.\n\n")
		sb.WriteString("```\n")
		if len(args.StackTrace) > 3000 {
			sb.WriteString(args.StackTrace[:3000])
			sb.WriteString("\n... (truncated)")
		} else {
			sb.WriteString(args.StackTrace)
		}
		sb.WriteString("\n```\n")
		return &tool.ToolResult{Content: sb.String()}, nil
	}

	sb.WriteString(fmt.Sprintf("Found %d file references in stack trace.\n\n", len(refs)))

	for i, ref := range refs {
		if i >= 5 {
			sb.WriteString(fmt.Sprintf("\n... and %d more frames\n", len(refs)-5))
			break
		}
		sb.WriteString(fmt.Sprintf("### Frame %d: %s:%d", i+1, filepath.Base(ref.file), ref.line))
		if ref.funcName != "" {
			sb.WriteString(fmt.Sprintf(" (%s)", ref.funcName))
		}
		sb.WriteString("\n\n")

		source := readSourceContext(ref.file, ref.line, 3)
		if source != "" {
			sb.WriteString("```\n")
			sb.WriteString(source)
			sb.WriteString("```\n\n")
		} else {
			sb.WriteString(fmt.Sprintf("(source file not accessible: %s)\n\n", ref.file))
		}
	}

	return &tool.ToolResult{Content: sb.String()}, nil
}

// --- suggest_breakpoint (Extended) ---

func NewSuggestBreakpointTool(cfg DebugToolsConfig) tool.Tool {
	return &suggestBreakpointTool{
		BaseTool: tool.BaseTool{Meta: tool.ToolMeta{
			Dimension: tool.DimensionPerceive, ReadOnly: true, Concurrent: true,
			Timeout: 15 * time.Second, Risk: tool.RiskSafe, Enabled: true,
			Domain: "debug", DomainToolTier: tool.DomainTierExtended,
		}},
		cfg: cfg,
	}
}

type suggestBreakpointTool struct {
	tool.BaseTool
	cfg DebugToolsConfig
}

func (t *suggestBreakpointTool) Name() string { return "suggest_breakpoint" }
func (t *suggestBreakpointTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "suggest_breakpoint",
		Description: "Suggest where to set breakpoints based on a bug description or crash data. Analyzes the last crash/exception stack trace and recommends specific file:line locations with rationale.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"description":{"type":"string","description":"Description of the bug or unexpected behavior"}}}`),
	}
}

func (t *suggestBreakpointTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var args struct {
		Description string `json:"description"`
	}
	_ = json.Unmarshal(input, &args)

	var sb strings.Builder
	sb.WriteString("## Breakpoint Suggestions\n\n")

	events := t.cfg.State.DebugEventsRaw(10)
	var crashFrames []DebugFrameRaw
	for _, e := range events {
		if (e.Kind == "exception" || e.Kind == "crash") && len(e.Frames) > 0 {
			crashFrames = e.Frames
			break
		}
	}

	if len(crashFrames) > 0 {
		sb.WriteString("### Based on last crash/exception\n\n")
		bpItems := make([]ListItem, 0, len(crashFrames))
		for i, f := range crashFrames {
			if i >= 5 {
				break
			}
			if f.Source == "" || strings.Contains(f.Source, "/runtime/") || strings.Contains(f.Source, "node_modules") {
				continue
			}
			var rationale string
			if i == 0 {
				rationale = "crash point — where the error originated"
			} else {
				rationale = fmt.Sprintf("caller %d levels up — trace how we got here", i)
			}
			sb.WriteString(fmt.Sprintf("- **%s:%d** `%s` — %s\n", f.Source, f.Line, f.Name, rationale))
			// 理由进 detail：这个工具的价值不在"列出位置"，而在"为什么在这里下断点"。
			// 崩溃点与上游调用者是两种不同的调查动作，kind 徽标把它们分开。
			// f.Line 来自崩溃栈文本解析，是人读的 1-based，不加。
			kind := "caller"
			if i == 0 {
				kind = "crash point"
			}
			bpItems = append(bpItems, ListItem{
				Label:  f.Name,
				Detail: rationale,
				File:   f.Source,
				Line:   f.Line,
				Kind:   kind,
			})
		}
		list = ListOf(bpItems, 0)
		sb.WriteByte('\n')
	}

	if args.Description != "" {
		sb.WriteString("### Investigation strategy\n\n")
		sb.WriteString("Based on the description, consider:\n")
		sb.WriteString("1. Set a breakpoint at the crash point to inspect variable state\n")
		sb.WriteString("2. Set conditional breakpoints on the callers to catch the bad state earlier\n")
		sb.WriteString("3. Use `evaluate` at each breakpoint to check intermediate values\n\n")
	}

	if len(crashFrames) == 0 && args.Description == "" {
		sb.WriteString("No crash data or bug description provided. Run the failing scenario first, or describe the bug.\n")
	}

	return &tool.ToolResult{Content: sb.String()}, nil
}

// --- helpers ---

type fileLineRef struct {
	file     string
	line     int
	funcName string
}

func extractFileLineRefs(text string) []fileLineRef {
	var refs []fileLineRef
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		ref := tryParseFileLine(trimmed)
		if ref != nil {
			refs = append(refs, *ref)
		}
		if len(refs) >= 20 {
			break
		}
	}
	return refs
}

func tryParseFileLine(line string) *fileLineRef {
	// Go: /path/to/file.go:42 +0x...
	if strings.Contains(line, ".go:") && (strings.Contains(line, "/") || strings.Contains(line, "\\")) {
		if path, lineNum, _, _, ok := platform.ParseFileRef(line); ok && lineNum > 0 && strings.HasSuffix(path, ".go") {
			return &fileLineRef{file: path, line: lineNum}
		}
	}
	// Python: File "/path/to/file.py", line 42
	if strings.HasPrefix(line, "File \"") {
		parts := strings.SplitN(line, "\"", 3)
		if len(parts) >= 2 {
			file := parts[1]
			lineNum := 0
			if idx := strings.Index(line, "line "); idx >= 0 {
				fmt.Sscanf(line[idx:], "line %d", &lineNum)
			}
			funcName := ""
			if idx := strings.LastIndex(line, "in "); idx >= 0 {
				funcName = line[idx+3:]
			}
			if lineNum > 0 {
				return &fileLineRef{file: file, line: lineNum, funcName: funcName}
			}
		}
	}
	// JS/TS: at funcName (/path/to/file.js:42:10)
	if strings.HasPrefix(line, "at ") {
		loc := line[3:]
		parenStart := strings.LastIndex(loc, "(")
		parenEnd := strings.LastIndex(loc, ")")
		funcName := ""
		if parenStart >= 0 && parenEnd > parenStart {
			funcName = strings.TrimSpace(loc[:parenStart])
			loc = loc[parenStart+1 : parenEnd]
		} else {
			loc = strings.TrimSpace(loc)
		}
		parts := strings.Split(loc, ":")
		if len(parts) >= 2 {
			file := parts[0]
			lineNum := 0
			fmt.Sscanf(parts[1], "%d", &lineNum)
			if lineNum > 0 {
				return &fileLineRef{file: file, line: lineNum, funcName: funcName}
			}
		}
	}
	return nil
}

func readSourceContext(file string, line, contextLines int) string {
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	start := line - contextLines - 1
	if start < 0 {
		start = 0
	}
	end := line + contextLines
	if end > len(lines) {
		end = len(lines)
	}

	var sb strings.Builder
	for i := start; i < end; i++ {
		marker := "  "
		if i == line-1 {
			marker = "► "
		}
		sb.WriteString(fmt.Sprintf("%s%4d │ %s\n", marker, i+1, lines[i]))
	}
	return sb.String()
}
