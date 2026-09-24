package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/weisyn/wescode/internal/treesitter"
	"github.com/weisyn/wesgine/tool"
)

// NewControlFlowTool creates a "control_flow" tool that analyzes the control
// flow structure of a function and identifies which execution paths pass
// through a given line.
func NewControlFlowTool(ts *treesitter.ParserPool, fp tool.FileProvider) tool.Tool {
	return &controlFlowTool{
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
				Domain:         "analysis",
				DomainToolTier: tool.DomainTierExtended,
			},
		},
		ts: ts,
		fp: fp,
	}
}

type controlFlowTool struct {
	tool.BaseTool
	ts *treesitter.ParserPool
	fp tool.FileProvider
}

func (t *controlFlowTool) Name() string { return "control_flow" }

func (t *controlFlowTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "control_flow",
		Description: "Analyze a function's control flow: branches, loops, error paths, return points. Optionally show which paths pass through a specific line.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"file": {"type": "string", "description": "Absolute path to the file"},
				"line": {"type": "integer", "description": "0-based line number within the function"},
				"focus_line": {"type": "integer", "description": "Optional: 0-based line to highlight in the flow (shows which branches contain it)"}
			},
			"required": ["file", "line"]
		}`),
	}
}

func (t *controlFlowTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。这个工具**故意不产出列表**：输出是带缩进的控制流树，
	// 摊平成条目会丢掉嵌套，而那个缩进本身就是信息（哪个分支在哪个循环里）。
	// 传 nil 得到 category=text + 分级——分级仍然有价值（解析失败 vs 该行没有函数
	// 是两件事，而前端此前分不出来）。
	var notReady bool
	defer func() { res = PresentList(res, notReady, nil) }()

	var params struct {
		File      string `json:"file"`
		Line      int    `json:"line"`
		FocusLine *int   `json:"focus_line"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "Invalid parameters: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.File == "" {
		return &tool.ToolResult{Content: "file is required", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}

	content, err := t.readFile(params.File)
	if err != nil {
		return &tool.ToolResult{Content: "Failed to read file: " + err.Error(), IsError: true}, nil
	}

	lang, ok := treesitter.DetectLang(params.File)
	if !ok {
		return &tool.ToolResult{Content: "Unsupported language for " + params.File, IsError: true}, nil
	}

	tree, err := t.ts.Parse(lang, content, nil)
	if err != nil {
		return &tool.ToolResult{Content: "Parse failed: " + err.Error(), IsError: true}, nil
	}
	defer tree.Close()

	syms := treesitter.ExtractSymbols(lang, tree, content)
	fn := treesitter.FindFunctionAt(syms, params.Line)
	if fn == nil {
		return &tool.ToolResult{Content: fmt.Sprintf("No function found at line %d in %s", params.Line, params.File)}, nil
	}

	fnContent := content[fn.StartByte:fn.EndByte]
	entries := treesitter.ExtractControlFlow(lang, t.ts, fnContent)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[Control Flow: %s in %s (lines %d-%d)]\n\n",
		fn.Name, params.File, fn.StartLine+1, fn.EndLine+1))

	focusLine := -1
	if params.FocusLine != nil {
		focusLine = *params.FocusLine
	}

	errorPaths := 0
	returns := 0
	defers := 0

	for _, e := range entries {
		absLine := fn.StartLine + e.Line + 1
		indent := strings.Repeat("  ", e.Depth)

		marker := " "
		if absLine == focusLine+1 {
			marker = ">"
		}

		switch e.Kind {
		case treesitter.CFNodeIf:
			errTag := ""
			if e.IsError {
				errTag = " [ERROR PATH]"
				errorPaths++
			}
			sb.WriteString(fmt.Sprintf("%s%sL%d: if %s%s\n", marker, indent, absLine, e.Condition, errTag))
		case treesitter.CFNodeElse:
			sb.WriteString(fmt.Sprintf("%s%sL%d: else\n", marker, indent, absLine))
		case treesitter.CFNodeElseIf:
			sb.WriteString(fmt.Sprintf("%s%sL%d: else if\n", marker, indent, absLine))
		case treesitter.CFNodeFor:
			sb.WriteString(fmt.Sprintf("%s%sL%d: for %s\n", marker, indent, absLine, e.Condition))
		case treesitter.CFNodeSwitch:
			sb.WriteString(fmt.Sprintf("%s%sL%d: switch %s\n", marker, indent, absLine, e.Condition))
		case treesitter.CFNodeCase:
			sb.WriteString(fmt.Sprintf("%s%sL%d: case\n", marker, indent, absLine))
		case treesitter.CFNodeDefault:
			sb.WriteString(fmt.Sprintf("%s%sL%d: default\n", marker, indent, absLine))
		case treesitter.CFNodeReturn:
			sb.WriteString(fmt.Sprintf("%s%sL%d: return\n", marker, indent, absLine))
			returns++
		case treesitter.CFNodeDefer:
			sb.WriteString(fmt.Sprintf("%s%sL%d: defer\n", marker, indent, absLine))
			defers++
		case treesitter.CFNodePanic:
			sb.WriteString(fmt.Sprintf("%s%sL%d: panic/fatal [TERMINAL]\n", marker, indent, absLine))
		case treesitter.CFNodeTry:
			sb.WriteString(fmt.Sprintf("%s%sL%d: try\n", marker, indent, absLine))
		case treesitter.CFNodeCatch:
			sb.WriteString(fmt.Sprintf("%s%sL%d: catch [ERROR PATH]\n", marker, indent, absLine))
			errorPaths++
		case treesitter.CFNodeFinally:
			sb.WriteString(fmt.Sprintf("%s%sL%d: finally\n", marker, indent, absLine))
		case treesitter.CFNodeSelect:
			sb.WriteString(fmt.Sprintf("%s%sL%d: select\n", marker, indent, absLine))
		case treesitter.CFNodeBreak:
			sb.WriteString(fmt.Sprintf("%s%sL%d: break\n", marker, indent, absLine))
		case treesitter.CFNodeContinue:
			sb.WriteString(fmt.Sprintf("%s%sL%d: continue\n", marker, indent, absLine))
		case treesitter.CFNodeGoto:
			sb.WriteString(fmt.Sprintf("%s%sL%d: goto\n", marker, indent, absLine))
		}
	}

	sb.WriteString(fmt.Sprintf("\nComplexity: %d branches, %d return points, %d error paths, %d defers\n",
		len(entries), returns, errorPaths, defers))

	return &tool.ToolResult{Content: sb.String()}, nil
}

func (t *controlFlowTool) readFile(path string) ([]byte, error) {
	if t.fp != nil {
		return t.fp.ReadFile(context.Background(), path)
	}
	return os.ReadFile(path)
}
