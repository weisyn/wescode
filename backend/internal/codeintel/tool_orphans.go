package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// NewOrphansTool creates a "find_orphans" tool that detects exported symbols
// with no incoming references in the code knowledge graph.
func NewOrphansTool(index *CodeIndex) tool.Tool {
	return &orphansTool{
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

type orphansTool struct {
	tool.BaseTool
	index *CodeIndex
}

func (t *orphansTool) Name() string { return "find_orphans" }

// CKGBias marks this tool as absence-based: an unindexed reference is
// indistinguishable from no reference, so a short index inflates the answer
// instead of trimming it. The generic "matches may be missing" disclosure would
// aim the model's doubt at the one part of this result that is trustworthy.
func (t *orphansTool) CKGBias() CKGBias { return BiasOverReport }

func (t *orphansTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "find_orphans",
		Description: "Find exported symbols (functions, types, classes) with no callers or references. Useful for discovering dead code or reusable symbols before writing new code. Results are grouped by certainty level: high (definite dead code), medium (likely dead), low (uncertain — may be called cross-language or via reflection).",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"directory": {"type": "string", "description": "Only check symbols under this directory path prefix"},
				"kind": {"type": "string", "description": "Filter by symbol kind: function, method, type, interface, class"},
				"limit": {"type": "integer", "description": "Max results (default 20)"},
				"include_tests": {"type": "boolean", "description": "Include test files (default: exclude)"},
				"min_certainty": {"type": "number", "description": "Minimum certainty threshold (0.0-1.0). Default 0 returns all. Set to 0.7 for likely+ dead code only, 1.0 for definite dead code only."},
				"verbosity": {"type": "string", "enum": ["summary", "detail", "full"], "description": "Output detail level (default: detail)"}
			}
		}`),
	}
}

func (t *orphansTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。这个工具的用户动作是**删代码**，所以 not_ready 与 empty 的
	// 区分在这里代价最高：索引没建完时的"没有孤儿"会让他以为清干净了。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		Directory    string  `json:"directory"`
		Kind         string  `json:"kind"`
		Limit        int     `json:"limit"`
		IncludeTests bool    `json:"include_tests"`
		MinCertainty float64 `json:"min_certainty"`
		Verbosity    string  `json:"verbosity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if t.index == nil {
		notReady = true
		return &tool.ToolResult{Content: "code index is not available"}, nil
	}

	opts := FindOrphanOpts{
		FileFilter:       params.Directory,
		ExcludeMain:      true,
		ExcludeTests:     !params.IncludeTests,
		ExcludeBuildTags: true,
		MinCertainty:     params.MinCertainty,
		Limit:            params.Limit,
	}
	if params.Kind != "" {
		opts.KindFilter = []string{params.Kind}
	}

	// Readiness disclosure is appended by NewConfidenceEnrichHook for every
	// result branch, so this tool does not carry the report itself.
	orphans, _, err := t.index.FindOrphans(ctx, opts)
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("orphan detection failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}

	if len(orphans) == 0 {
		// 空列表而非 nil：落 empty（"查了、没有孤儿"）而不是 text。
		// 对这个工具这是最有价值的那个答案——它意味着没有死代码可删。
		list = &ListData{Items: []ListItem{}}
		msg := "No orphan symbols found"
		if params.Directory != "" {
			msg += fmt.Sprintf(" under %s", params.Directory)
		}
		return &tool.ToolResult{Content: msg}, nil
	}

	verbosity := ParseVerbosity(params.Verbosity)
	orphans, total := TruncateWithTotal(orphans, verbosity)
	list = ListOf(orphanListItems(orphans), total)

	return &tool.ToolResult{Content: formatOrphansByGroup(orphans, total, verbosity)}, nil
}

// formatOrphansByGroup renders orphan results grouped by certainty level.
// High-certainty results show full context; low-certainty results are summarized
// to optimize AI token consumption (CI-23).
// orphanListItems 把孤儿投影成列表项。
//
// detail 带上**确定性与理由**，不只是签名：这个工具的输出直接导向删代码，而
// `name_reachable`（0.3 确定性）与 `isolated`（1.0）是完全不同的建议——前者是
// "可能通过反射/接口被调用"，后者是"图上确实没人指向它"。只显示名字会让两者同形，
// 而用户对着列表逐个删的时候不会回去读正文。
func orphanListItems(orphans []OrphanResult) []ListItem {
	items := make([]ListItem, 0, len(orphans))
	for _, o := range orphans {
		items = append(items, ListItem{
			Label:  o.Symbol.Name,
			Detail: fmt.Sprintf("%.0f%% certain · %s", o.Certainty*100, o.Reason),
			File:   o.Symbol.FilePath,
			Line:   o.Symbol.LineStart + 1, // CKG 存 0-based
			Kind:   o.Symbol.Kind,
		})
	}
	return items
}

// total 是截断前的孤儿总数。它必须单独传进来：此前这个函数报的是 len(orphans)，
// 而入参已经被截断过，于是"Found 5 orphan symbol(s)"在实际 40 个时照样打印。
// 这个工具的用户动作是**删代码**——报少了会让他清理完 5 个就以为干净了。
func formatOrphansByGroup(orphans []OrphanResult, total int, verbosity VerbosityLevel) string {
	type group struct {
		label   string
		minCert float64
		maxCert float64
		items   []OrphanResult
	}
	groups := []group{
		{"Definite dead code (certainty >= 80%)", 0.8, 1.01, nil},
		{"Likely dead code (certainty 50-80%)", 0.5, 0.8, nil},
		{"Uncertain (certainty < 50%)", 0.0, 0.5, nil},
	}

	for _, o := range orphans {
		for i := range groups {
			if o.Certainty >= groups[i].minCert && o.Certainty < groups[i].maxCert {
				groups[i].items = append(groups[i].items, o)
				break
			}
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %s orphan symbol(s) with no incoming references:\n", CountPhrase(len(orphans), total)))

	idx := 1
	for gi, g := range groups {
		if len(g.items) == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("\n=== %s ===\n", g.label))

		// Low-certainty group: only show count, not details (save AI tokens).
		if gi == 2 {
			sb.WriteString(fmt.Sprintf("  %d symbol(s) with low certainty (possibly called via reflection/interface/cross-language)\n", len(g.items)))
			for _, o := range g.items {
				sb.WriteString(fmt.Sprintf("  - %s %s (%s)\n", o.Symbol.Kind, o.Symbol.Name, o.Suggestion))
			}
			continue
		}

		for _, o := range g.items {
			switch verbosity {
			case VerbositySummary:
				sb.WriteString(fmt.Sprintf("%d. %s %s (%.0f%%)\n", idx, o.Symbol.Kind, o.Symbol.Name, o.Certainty*100))
			default:
				sb.WriteString(fmt.Sprintf("%d. %s %s\n", idx, o.Symbol.Kind, o.Symbol.Name))
				sb.WriteString(fmt.Sprintf("   File: %s:%d\n", o.Symbol.FilePath, o.Symbol.LineStart+1))
				if o.Symbol.Signature != "" {
					sb.WriteString(fmt.Sprintf("   Signature: %s\n", o.Symbol.Signature))
				}
				sb.WriteString(fmt.Sprintf("   Certainty: %.0f%% — %s\n", o.Certainty*100, o.Reason))
				if o.Suggestion != "" {
					sb.WriteString(fmt.Sprintf("   Suggestion: %s\n", o.Suggestion))
				}
				if verbosity == VerbosityFull {
					if len(o.KnownBlindSpots) > 0 {
						sb.WriteString(fmt.Sprintf("   Blind spots: %s\n", strings.Join(o.KnownBlindSpots, ", ")))
					}
					if len(o.SimilarNames) > 0 {
						sb.WriteString(fmt.Sprintf("   Similar names found: %s\n", strings.Join(o.SimilarNames, ", ")))
					}
				}
			}
			idx++
		}
	}
	return sb.String()
}
