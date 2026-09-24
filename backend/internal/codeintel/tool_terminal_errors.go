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

// TerminalError mirrors engine.TerminalError for cross-package access.
type TerminalError struct {
	TerminalID string
	Timestamp  time.Time
	Pattern    string
	Lang       string
	File       string
	Line       int
	Column     int
	Message    string
	Context    string
}

// TerminalDiagProvider is the interface the tool uses to query terminal errors.
type TerminalDiagProvider interface {
	RecentTerminalErrors(within time.Duration) []TerminalError
	AllTerminalErrors() []TerminalError
}

func NewGetTerminalErrorsTool(provider TerminalDiagProvider) tool.Tool {
	return &getTerminalErrorsTool{
		BaseTool: tool.BaseTool{
			Meta: tool.ToolMeta{
				Dimension:    tool.DimensionPerceive,
				Availability: tool.AvailabilityConfigurable,
				ReadOnly:     true,
				Concurrent:   true,
				Timeout:      5 * time.Second,
				Risk:         tool.RiskSafe,
				PolicyFamily: tool.PolicyFamilyOther,
				Enabled:      true,
			},
		},
		provider: provider,
	}
}

type getTerminalErrorsTool struct {
	tool.BaseTool
	provider TerminalDiagProvider
}

func (t *getTerminalErrorsTool) IsBackendConfigured() bool {
	return t.provider != nil
}

func (t *getTerminalErrorsTool) Name() string { return "get_terminal_errors" }

func (t *getTerminalErrorsTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "get_terminal_errors",
		Description: "Get errors detected from terminal output (panics, compile errors, test failures, tracebacks). These are automatically parsed from terminal sessions — you don't need to re-run commands to see what failed. Shows runtime errors that IDE diagnostics can't detect (panics, assertion failures, HTTP errors, etc.).",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"recent_seconds": {
					"type": "integer",
					"description": "Only show errors from the last N seconds. Default: 120 (2 minutes)."
				}
			}
		}`),
	}
}

func (t *getTerminalErrorsTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		RecentSeconds *int `json:"recent_seconds"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}

	within := 120 * time.Second
	if params.RecentSeconds != nil && *params.RecentSeconds > 0 {
		within = time.Duration(*params.RecentSeconds) * time.Second
	}

	errors := t.provider.RecentTerminalErrors(within)
	if len(errors) == 0 {
		list = &ListData{Items: []ListItem{}}
		return &tool.ToolResult{Content: "No terminal errors detected recently."}, nil
	}

	// 时间进 detail：终端错误是流水，"3 秒前"与"20 分钟前"决定它跟当前改动有没有关系。
	// e.Line 直接用——它由错误解析器从终端文本里抓出来，那里的行号本来就是人读的
	// 1-based（编译器输出的行号），不是 CKG 的 0-based。
	items := make([]ListItem, 0, len(errors))
	for _, e := range errors {
		items = append(items, ListItem{
			Label:  e.Message,
			Detail: fmt.Sprintf("%s ago", time.Since(e.Timestamp).Truncate(time.Second)),
			File:   e.File,
			Line:   e.Line,
			Kind:   e.Pattern,
		})
	}
	list = ListOf(items, 0)

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d terminal error(s) detected:\n\n", len(errors))

	for i, e := range errors {
		ago := time.Since(e.Timestamp).Truncate(time.Second)
		fmt.Fprintf(&sb, "%d. [%s] %s (%s ago)\n", i+1, e.Pattern, e.Message, ago)
		if e.File != "" {
			displayFile := e.File
			if tc != nil && projectRoot(tc) != "" {
				if rel, err := filepath.Rel(projectRoot(tc), e.File); err == nil && !strings.HasPrefix(rel, "..") {
					displayFile = rel
				}
			}
			if e.Line > 0 {
				fmt.Fprintf(&sb, "   Location: %s:%d", displayFile, e.Line)
				if e.Column > 0 {
					fmt.Fprintf(&sb, ":%d", e.Column)
				}
				sb.WriteByte('\n')
			} else {
				fmt.Fprintf(&sb, "   File: %s\n", displayFile)
			}
		}
		if e.Context != "" {
			fmt.Fprintf(&sb, "   Context:\n")
			for _, line := range strings.Split(e.Context, "\n") {
				if strings.TrimSpace(line) != "" {
					fmt.Fprintf(&sb, "     %s\n", line)
				}
			}
		}
		sb.WriteByte('\n')
	}

	return &tool.ToolResult{Content: sb.String()}, nil
}
