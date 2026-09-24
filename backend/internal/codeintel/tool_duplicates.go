package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// NewDuplicatesTool creates a "find_similar" tool that finds code similar
// to a given symbol using name, signature, and callee set comparison.
func NewDuplicatesTool(index *CodeIndex) tool.Tool {
	return &duplicatesTool{
		BaseTool: tool.BaseTool{
			Meta: tool.ToolMeta{
				Dimension:      tool.DimensionPerceive,
				Availability:   tool.AvailabilityConfigurable,
				ReadOnly:       true,
				Concurrent:     true,
				Timeout:        10 * time.Second,
				Risk:           tool.RiskSafe,
				PolicyFamily:   tool.PolicyFamilyOther,
				Enabled:        true,
				Domain:         "graph",
				DomainToolTier: tool.DomainTierExtended,
			},
		},
		index: index,
	}
}

type duplicatesTool struct {
	tool.BaseTool
	index *CodeIndex
}

func (t *duplicatesTool) Name() string { return "find_similar" }

func (t *duplicatesTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "find_similar",
		Description: "Find functions and methods similar to a given symbol. Compares by name, signature pattern, and shared callees to prevent code duplication.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {"type": "string", "description": "Function or method name to find similar code for"},
				"signature": {"type": "string", "description": "Optional function signature for structural comparison"},
				"limit": {"type": "integer", "description": "Max results (default 10)"},
				"verbosity": {"type": "string", "enum": ["summary", "detail", "full"], "description": "Output detail level (default: detail)"}
			},
			"required": ["name"]
		}`),
	}
}

func (t *duplicatesTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。not_ready 必须由分支主动说：索引没建完这件事结果本身看不出来
	// （IsError=false、Content 是一句正常的话），而它与"查到 0 个"在前端完全同形。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		Name      string `json:"name"`
		Signature string `json:"signature"`
		Limit     int    `json:"limit"`
		Verbosity string `json:"verbosity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Name == "" {
		return &tool.ToolResult{Content: "name is required", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if t.index == nil {
		notReady = true
		return &tool.ToolResult{Content: "code index is not available"}, nil
	}

	opts := FindSimilarOpts{
		Signature: params.Signature,
		Limit:     params.Limit,
		MinScore:  0.3,
	}

	results, err := t.index.FindSimilar(ctx, params.Name, opts)
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("similarity search failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}

	if len(results) == 0 {
		return &tool.ToolResult{Content: fmt.Sprintf("No similar symbols found for %q", params.Name)}, nil
	}

	verbosity := ParseVerbosity(params.Verbosity)
	results, total := TruncateWithTotal(results, verbosity)

	items := make([]ListItem, 0, len(results))
	for _, r := range results {
		items = append(items, ListItem{
			Label: r.Symbol.Name,
			// 相似度进 detail 而不是 label：label 是"这是什么"，用户扫的是名字；
			// 百分比是排序依据，读第二眼。
			Detail: fmt.Sprintf("%.0f%% similar · %s", r.Score*100, r.Symbol.Signature),
			File:   r.Symbol.FilePath,
			Line:   r.Symbol.LineStart + 1, // CKG 存 0-based
			Kind:   r.Symbol.Kind,
		})
	}
	list = ListOf(items, total)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %s symbol(s) similar to %q:\n\n", CountPhrase(len(results), total), params.Name))
	for i, r := range results {
		switch verbosity {
		case VerbositySummary:
			sb.WriteString(fmt.Sprintf("%d. %s %s (%.0f%% similar)\n", i+1, r.Symbol.Kind, r.Symbol.Name, r.Score*100))
		default:
			sb.WriteString(fmt.Sprintf("%d. %s %s (%.0f%% similar, match: %s)\n", i+1, r.Symbol.Kind, r.Symbol.Name, r.Score*100, r.MatchType))
			sb.WriteString(fmt.Sprintf("   File: %s:%d\n", r.Symbol.FilePath, r.Symbol.LineStart+1))
			if r.Symbol.Signature != "" {
				sb.WriteString(fmt.Sprintf("   Signature: %s\n", r.Symbol.Signature))
			}
			sb.WriteByte('\n')
		}
	}
	return &tool.ToolResult{Content: sb.String()}, nil
}
