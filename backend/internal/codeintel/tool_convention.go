package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// NewListConventionsTool creates a "list_conventions" tool that lists discovered project conventions.
func NewListConventionsTool(conventionsFn func() []Convention) tool.Tool {
	return &listConventionsTool{
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
				Domain:         "constraint",
				DomainToolTier: tool.DomainTierCore,
			},
		},
		conventionsFn: conventionsFn,
	}
}

type listConventionsTool struct {
	tool.BaseTool
	conventionsFn func() []Convention
}

func (t *listConventionsTool) Name() string { return "list_conventions" }

func (t *listConventionsTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "list_conventions",
		Description: "List automatically discovered project conventions (patterns that ≥80% of a community follows). Use to understand implicit coding standards.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"community_id": {"type": "integer", "description": "Filter by Leiden community ID (optional)"},
				"dimension": {"type": "string", "enum": ["error","param","return","entry","exit"], "description": "Filter by pattern dimension (optional)"},
				"verbosity": {"type": "string", "enum": ["summary","detail","full"], "description": "Output detail level (default: detail)"}
			}
		}`),
	}
}

func (t *listConventionsTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		CommunityID *int   `json:"community_id"`
		Dimension   string `json:"dimension"`
		Verbosity   string `json:"verbosity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid params: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Verbosity == "" {
		params.Verbosity = "detail"
	}

	conventions := t.conventionsFn()
	if conventions == nil {
		// not_ready 而非 empty：nil 表示惯例推导还没跑过（要先建索引），不是
		// "这个项目没有惯例"。两者对用户的下一步不同——前者去等/去建索引，
		// 后者去接受"这里没规矩"。PC-03。
		notReady = true
		return &tool.ToolResult{Content: "Conventions not available yet — run indexing first."}, nil
	}

	var filtered []Convention
	for _, c := range conventions {
		if params.CommunityID != nil && c.CommunityID != *params.CommunityID {
			continue
		}
		if params.Dimension != "" && c.Dimension != params.Dimension {
			continue
		}
		filtered = append(filtered, c)
	}

	if len(filtered) == 0 {
		list = &ListData{Items: []ListItem{}}
		return &tool.ToolResult{Content: "No conventions match the filter criteria."}, nil
	}

	// 覆盖率进 detail：它是"这条惯例可信吗"的判据（95% 覆盖是惯例，40% 是巧合）。
	// 维度进 kind 徽标。惯例是跨文件的统计事实，没有单一位置。
	convItems := make([]ListItem, 0, len(filtered))
	for _, c := range filtered {
		convItems = append(convItems, ListItem{
			Label:  c.Pattern,
			Detail: fmt.Sprintf("%.0f%% coverage", c.Coverage*100),
			Kind:   c.Dimension,
		})
	}
	list = ListOf(convItems, 0)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Discovered %d convention(s):\n\n", len(filtered)))

	for _, c := range filtered {
		switch params.Verbosity {
		case "summary":
			sb.WriteString(fmt.Sprintf("• %s: %s (%.0f%%)\n", c.Dimension, c.Pattern, c.Coverage*100))
		case "detail":
			sb.WriteString(fmt.Sprintf("## %s\n", c.Name))
			sb.WriteString(fmt.Sprintf("  %s\n", c.Description))
			if len(c.Outliers) > 0 {
				sb.WriteString(fmt.Sprintf("  Outliers: %s\n", strings.Join(c.Outliers, ", ")))
			}
			sb.WriteString("\n")
		case "full":
			sb.WriteString(fmt.Sprintf("## %s\n", c.Name))
			sb.WriteString(fmt.Sprintf("  Description: %s\n", c.Description))
			sb.WriteString(fmt.Sprintf("  Community: %d | Dimension: %s\n", c.CommunityID, c.Dimension))
			sb.WriteString(fmt.Sprintf("  Pattern: %s | Coverage: %.1f%%\n", c.Pattern, c.Coverage*100))
			sb.WriteString(fmt.Sprintf("  Exemplars: %s\n", strings.Join(c.Exemplars, ", ")))
			if len(c.Outliers) > 0 {
				sb.WriteString(fmt.Sprintf("  Outliers (info-level): %s\n", strings.Join(c.Outliers, ", ")))
			}
			sb.WriteString("\n")
		}
	}

	return &tool.ToolResult{Content: sb.String()}, nil
}

// NewCheckConventionTool creates a "check_convention" tool that checks if a function follows community conventions.
func NewCheckConventionTool(conventionsFn func() []Convention) tool.Tool {
	return &checkConventionTool{
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
				Domain:         "constraint",
				DomainToolTier: tool.DomainTierCore,
			},
		},
		conventionsFn: conventionsFn,
	}
}

type checkConventionTool struct {
	tool.BaseTool
	conventionsFn func() []Convention
}

func (t *checkConventionTool) Name() string { return "check_convention" }

func (t *checkConventionTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "check_convention",
		Description: "Check if a function follows its community's discovered conventions. Returns deviations as info-level suggestions (not violations).",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"function_name": {"type": "string", "description": "Name of the function to check"},
				"signature": {"type": "string", "description": "Function signature to analyze"},
				"community_id": {"type": "integer", "description": "Community to check against (required)"},
				"verbosity": {"type": "string", "enum": ["summary","detail","full"], "description": "Output detail level (default: detail)"}
			},
			"required": ["function_name", "signature", "community_id"]
		}`),
	}
}

func (t *checkConventionTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		FunctionName string `json:"function_name"`
		Signature    string `json:"signature"`
		CommunityID  int    `json:"community_id"`
		Verbosity    string `json:"verbosity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid params: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Verbosity == "" {
		params.Verbosity = "detail"
	}

	conventions := t.conventionsFn()
	if conventions == nil {
		return &tool.ToolResult{Content: "No conventions discovered yet."}, nil
	}

	fp := ExtractFingerprint(params.Signature)

	var deviations []string
	var matches []string

	for _, conv := range conventions {
		if conv.CommunityID != params.CommunityID {
			continue
		}
		var actual string
		switch conv.Dimension {
		case "error":
			actual = fp.ErrorPattern
		case "param":
			actual = fp.ParamPattern
		case "return":
			actual = fp.ReturnPattern
		case "entry":
			actual = fp.EntryPattern
		case "exit":
			actual = fp.ExitPattern
		}
		if actual == conv.Pattern {
			matches = append(matches, fmt.Sprintf("%s: matches '%s'", conv.Dimension, conv.Pattern))
		} else if actual != "" && actual != "unknown" {
			deviations = append(deviations, fmt.Sprintf("[info] %s: expected '%s', got '%s' (%.0f%% convention)",
				conv.Dimension, conv.Pattern, actual, conv.Coverage*100))
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Convention check for '%s' (community %d):\n\n", params.FunctionName, params.CommunityID))

	if len(matches) > 0 {
		sb.WriteString("✓ Follows conventions:\n")
		for _, m := range matches {
			sb.WriteString("  " + m + "\n")
		}
	}

	if len(deviations) > 0 {
		sb.WriteString("\n⚠ Deviations (info-level, not violations — INV-P6-07):\n")
		for _, d := range deviations {
			sb.WriteString("  " + d + "\n")
		}
	}

	if len(matches) == 0 && len(deviations) == 0 {
		sb.WriteString("No conventions found for this community.")
	}

	return &tool.ToolResult{Content: sb.String()}, nil
}
