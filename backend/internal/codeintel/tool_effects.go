package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// NewEffectsTool creates a "get_effects" tool that queries function side effects.
func NewEffectsTool(index *CodeIndex) tool.Tool {
	return &effectsTool{
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
				Domain:         "analysis",
				DomainToolTier: tool.DomainTierCore,
			},
		},
		index: index,
	}
}

type effectsTool struct {
	tool.BaseTool
	index *CodeIndex
}

func (t *effectsTool) Name() string { return "get_effects" }

func (t *effectsTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "get_effects",
		Description: "Query the side effects of a function or method. Returns whether it is pure, reads/writes DB, does network IO, filesystem operations, etc. Useful for understanding safety of refactoring and identifying impure functions.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"symbol": {"type": "string", "description": "Name of the function or method to query effects for"},
				"verbosity": {"type": "string", "enum": ["summary", "detail", "full"], "description": "Output detail level (default: detail)"}
			},
			"required": ["symbol"]
		}`),
	}
}

func (t *effectsTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		Symbol    string `json:"symbol"`
		Verbosity string `json:"verbosity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if t.index == nil {
		notReady = true
		return &tool.ToolResult{Content: "code index is not available"}, nil
	}
	if params.Symbol == "" {
		return &tool.ToolResult{Content: "symbol parameter is required", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}

	results, err := t.index.SymbolEffects(ctx, params.Symbol)
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("query effects failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}

	if len(results) == 0 {
		list = &ListData{Items: []ListItem{}}
		return &tool.ToolResult{Content: fmt.Sprintf("No effects data found for %q. The symbol may not be indexed or not a function/method.", params.Symbol)}, nil
	}

	// 风险等级进 kind 徽标：这个工具回答"调它会发生什么副作用"，而 risk 是用户决定
	// 要不要继续读的第一判据。effects 串进 detail。
	//
	// LineStart 要 +1（CKG 存 0-based）。文本输出那侧也已统一加 1（2026-09-20 普查：
	// CKG 族 15 处里 3 处漏加，这是其中一处）——同一个符号在两处显示不同行号会让
	// 用户以为工具在猜。
	items := make([]ListItem, 0, len(results))
	for _, r := range results {
		items = append(items, ListItem{
			Label:  r.Name,
			Detail: r.Effects,
			File:   r.FilePath,
			Line:   r.LineStart + 1,
			Kind:   classifyRisk(r.Effects),
		})
	}
	list = ListOf(items, 0)

	verbosity := ParseVerbosity(params.Verbosity)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Side effects for %q (%d match(es)):\n\n", params.Symbol, len(results)))

	for i, r := range results {
		risk := classifyRisk(r.Effects)

		switch verbosity {
		case VerbositySummary:
			sb.WriteString(fmt.Sprintf("%d. %s — %s [%s]\n", i+1, r.Name, r.Effects, risk))
		case VerbosityFull:
			sb.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, r.Kind, r.Name))
			sb.WriteString(fmt.Sprintf("   File: %s:%d\n", r.FilePath, r.LineStart+1))
			sb.WriteString(fmt.Sprintf("   Effects: %s\n", r.Effects))
			sb.WriteString(fmt.Sprintf("   Risk: %s\n", risk))
			if r.Signature != "" {
				sb.WriteString(fmt.Sprintf("   Signature: %s\n", r.Signature))
			}
			sb.WriteByte('\n')
		default:
			sb.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, r.Kind, r.Name))
			sb.WriteString(fmt.Sprintf("   Effects: %s\n", r.Effects))
			sb.WriteString(fmt.Sprintf("   Risk: %s\n", risk))
			sb.WriteByte('\n')
		}
	}
	return &tool.ToolResult{Content: sb.String()}, nil
}

// EffectResult is a symbol with its propagated effects.
type EffectResult struct {
	FilePath  string
	Kind      string
	Name      string
	Signature string
	LineStart int
	Effects   string // comma-separated effect kinds
}

// SymbolEffects queries the effects column for symbols matching the given name.
func (ci *CodeIndex) SymbolEffects(ctx context.Context, name string) ([]EffectResult, error) {
	// readerDB may be swapped by ReopenAt/Close; hold RLock for the whole
	// query so the handle cannot be closed mid-flight.
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()
	rows, err := ci.readerDB.QueryContext(ctx,
		`SELECT file_path, kind, name, signature, line_start, effects
		 FROM symbols
		 WHERE name = ? AND kind IN ('function', 'method')
		 ORDER BY file_path, line_start
		 LIMIT 50`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []EffectResult
	for rows.Next() {
		var r EffectResult
		var effectsRaw string
		if err := rows.Scan(&r.FilePath, &r.Kind, &r.Name, &r.Signature, &r.LineStart, &effectsRaw); err != nil {
			continue
		}
		r.Effects = parseEffectsDisplay(effectsRaw)
		results = append(results, r)
	}
	return results, rows.Err()
}

// parseEffectsDisplay converts the stored JSON or raw string into a display string.
func parseEffectsDisplay(raw string) string {
	if raw == "" {
		return "unknown"
	}
	var es EffectSet
	if err := json.Unmarshal([]byte(raw), &es); err == nil && len(es.Effects) > 0 {
		return es.String()
	}
	return raw
}

// classifyRisk maps an effects string to a risk level.
func classifyRisk(effects string) string {
	if effects == "pure" {
		return "safe"
	}
	if effects == "unknown" {
		return "unknown"
	}
	for _, high := range []string{"writes_db", "fs_write", "network_io", "panics"} {
		if strings.Contains(effects, high) {
			return "high"
		}
	}
	for _, med := range []string{"reads_db", "fs_read", "mutates_global", "spawns_goroutine"} {
		if strings.Contains(effects, med) {
			return "medium"
		}
	}
	return "low"
}
