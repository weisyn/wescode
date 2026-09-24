package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// NewImpactTool creates an "impact_analysis" tool that traces downstream
// symbols affected by changing a given symbol.
func NewImpactTool(index *CodeIndex) tool.Tool {
	return &impactTool{
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
				DomainToolTier: tool.DomainTierCore,
			},
		},
		index: index,
	}
}

type impactTool struct {
	tool.BaseTool
	index *CodeIndex
}

func (t *impactTool) Name() string { return "impact_analysis" }

var impactAnalysisContract = tool.PromptContract{
	// The CKG has no type-reference edge (no extractor emits one), so a symbol
	// used only as a parameter or field type will not appear here. Promising
	// "type references" made the model plan around a capability whose empty
	// result reads identically to "nothing depends on this"; the WhenNotToUse
	// redirect to find_references (LSP-backed, which does see type positions)
	// is the honest route for that question.
	Summary: "Analyze the full downstream impact of changing a symbol. Walks every inbound code index edge kind transitively — calls, interface implementations, method overrides, dataflow, co-change history. Does NOT cover type-only references (parameter/field types).",
	WhenToUse: []tool.PromptScenario{
		{Condition: "About to modify a function/type and need to know everything that could break"},
		{Condition: "Planning a refactor and need the complete blast radius before starting"},
		{Condition: "Assessing risk of a proposed API change across the codebase"},
	},
	WhenNotToUse: []tool.PromptRedirect{
		{Condition: "Only need direct callers (one level up)", AlternativeTool: "find_callers", Reason: "find_callers is faster for single-hop caller lookup"},
		{Condition: "Need all usage sites including non-call references", AlternativeTool: "find_references", Reason: "find_references covers imports, type annotations, assignments — not just call chains"},
		{Condition: "Need to find the symbol's definition first", AlternativeTool: "search_symbols", Reason: "search_symbols locates where a symbol is defined"},
	},
	SideEffects: []tool.SideEffectDecl{
		{Type: "none", Description: "Read-only BFS traversal of dependency graph", Reversible: true},
	},
	ParamNotes: []tool.ParamNote{
		{Param: "symbol", Constraint: "Function, type, or method name to analyze impact for"},
		{Param: "depth", Constraint: "Max BFS traversal depth", Default: "3"},
		{Param: "verbosity", Constraint: "summary | detail | full", Default: "detail"},
	},
}

func (t *impactTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "impact_analysis",
		Description: tool.ContractToDescription(&impactAnalysisContract),
		Contract:    &impactAnalysisContract,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"symbol": {"type": "string", "description": "Name of the symbol to analyze (function, type, method)"},
				"depth": {"type": "integer", "description": "Max BFS depth (default 3, max 5)"},
				"verbosity": {"type": "string", "enum": ["summary", "detail", "full"], "description": "Output detail level (default: detail)"}
			},
			"required": ["symbol"]
		}`),
	}
}

// impactListItems 把影响面节点投影成列表项。
//
// detail 带**深度与边类型**：这个工具回答"改它会波及谁"，而 depth 1（直接调用）与
// depth 3（三跳之外）是完全不同的风险等级，边类型（call / import / extends）又决定
// 这次波及是不是编译期硬依赖。只显示名字会让整张列表看起来同等重要，而用户要的
// 恰恰是"先看哪几个"。
//
// 深度进 kind 徽标而不是 detail：它是排序与筛选依据，第一眼就要看见。
func impactListItems(nodes []ImpactNode) []ListItem {
	items := make([]ListItem, 0, len(nodes))
	for _, n := range nodes {
		items = append(items, ListItem{
			Label:  n.Symbol.Name,
			Detail: fmt.Sprintf("%s · %s edge", n.Symbol.Kind, n.EdgeKind),
			File:   n.Symbol.FilePath,
			Line:   n.Symbol.LineStart + 1, // CKG 存 0-based
			Kind:   impactDepthLabel(n.Depth),
		})
	}
	return items
}

// impactDepthLabel 与文本输出用同一套词（direct / indirect / depth-N）——两处用不同
// 说法会让用户以为列表和正文讲的是两件事。
func impactDepthLabel(depth int) string {
	switch depth {
	case 1:
		return "direct"
	case 2:
		return "indirect"
	default:
		return fmt.Sprintf("depth-%d", depth)
	}
}

func (t *impactTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		Symbol    string `json:"symbol"`
		Depth     int    `json:"depth"`
		Verbosity string `json:"verbosity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Symbol == "" {
		return &tool.ToolResult{Content: "symbol name is required", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if t.index == nil {
		notReady = true
		return &tool.ToolResult{Content: "code index is not available"}, nil
	}
	if params.Depth <= 0 {
		params.Depth = 3
	}
	if params.Depth > 5 {
		params.Depth = 5
	}

	slog.Debug("[impact_analysis] request", "symbol", params.Symbol, "depth", params.Depth)
	// Readiness disclosure is appended by NewConfidenceEnrichHook for every
	// result branch. This tool used to disclose only on the empty branch, so
	// the branch that actually listed possibly-incomplete rows stayed silent.
	nodes, _, err := t.index.ImpactAnalysis(ctx, params.Symbol, params.Depth)
	if err != nil {
		slog.Warn("[impact_analysis] error", "symbol", params.Symbol, "err", err)
		return &tool.ToolResult{Content: fmt.Sprintf("impact analysis failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}
	slog.Debug("[impact_analysis] result", "symbol", params.Symbol, "nodes", len(nodes))

	if len(nodes) == 0 {
		// 空列表而非 nil：落 empty（"查了、没有下游依赖"）。对这个工具这是最有价值的
		// 答案——它意味着改这个符号不会波及别处。
		list = &ListData{Items: []ListItem{}}
		return &tool.ToolResult{Content: fmt.Sprintf("No downstream dependencies found for %q", params.Symbol)}, nil
	}

	// 信封按**全量**节点构造，不跟着文本走 per-layer 截断：两者服务不同的消费方，
	// 文本受 token 预算约束（模型读），列表可滚动（人读）。上限由 ListOf 统一封顶。
	list = ListOf(impactListItems(nodes), 0)

	// Group by depth for readability.
	byDepth := map[int][]ImpactNode{}
	affectedFiles := map[string]bool{}
	for _, n := range nodes {
		byDepth[n.Depth] = append(byDepth[n.Depth], n)
		affectedFiles[n.Symbol.FilePath] = true
	}

	verbosity := ParseVerbosity(params.Verbosity)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Impact analysis for %q: %d downstream symbol(s) across %d file(s)\n\n",
		params.Symbol, len(nodes), len(affectedFiles)))

	if verbosity == VerbositySummary {
		for depth := 1; depth <= params.Depth; depth++ {
			layer := byDepth[depth]
			if len(layer) == 0 {
				continue
			}
			label := "direct"
			if depth == 2 {
				label = "indirect"
			} else if depth > 2 {
				label = fmt.Sprintf("depth-%d", depth)
			}
			sb.WriteString(fmt.Sprintf("── %s (depth %d): %d symbol(s) ──\n", label, depth, len(layer)))
			truncated, layerTotal := TruncateWithTotal(layer, verbosity)
			for _, n := range truncated {
				sb.WriteString(fmt.Sprintf("  %s %s\n", n.Symbol.Kind, n.Symbol.Name))
			}
			if layerTotal > len(truncated) {
				sb.WriteString(fmt.Sprintf("  ... and %d more\n", layerTotal-len(truncated)))
			}
		}
	} else {
		for depth := 1; depth <= params.Depth; depth++ {
			layer := byDepth[depth]
			if len(layer) == 0 {
				continue
			}
			label := "direct"
			if depth == 2 {
				label = "indirect"
			} else if depth > 2 {
				label = fmt.Sprintf("depth-%d", depth)
			}
			sb.WriteString(fmt.Sprintf("── %s dependents (depth %d): %d ──\n", label, depth, len(layer)))
			truncated, layerTotal := TruncateWithTotal(layer, verbosity)
			for _, n := range truncated {
				sb.WriteString(fmt.Sprintf("  %s %s  ← %s edge\n", n.Symbol.Kind, n.Symbol.Name, n.EdgeKind))
				sb.WriteString(fmt.Sprintf("    %s:%d\n", n.Symbol.FilePath, n.Symbol.LineStart+1))
			}
			if layerTotal > len(truncated) {
				sb.WriteString(fmt.Sprintf("  ... and %d more\n", layerTotal-len(truncated)))
			}
			sb.WriteByte('\n')
		}

		sb.WriteString("Affected files:\n")
		for f := range affectedFiles {
			sb.WriteString(fmt.Sprintf("  %s\n", f))
		}
	}
	return &tool.ToolResult{Content: sb.String()}, nil
}
