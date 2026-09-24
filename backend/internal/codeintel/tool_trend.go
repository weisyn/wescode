package codeintel

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// NewGetTrendTool creates a "get_trend" tool that shows historical metrics for a symbol.
func NewGetTrendTool(dbFn func() *sql.DB) tool.Tool {
	return &getTrendTool{
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
				Domain:         "temporal",
				DomainToolTier: tool.DomainTierCore,
			},
		},
		dbFn: dbFn,
	}
}

type getTrendTool struct {
	tool.BaseTool
	dbFn func() *sql.DB
}

func (t *getTrendTool) Name() string { return "get_trend" }

func (t *getTrendTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "get_trend",
		Description: "Show historical metrics (line count, callers, callees, effects) for a symbol over time. Useful for understanding how code is evolving.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"symbol": {"type": "string", "description": "Symbol qualified name or name substring to search"},
				"limit": {"type": "integer", "description": "Max snapshots to return (default 10)"},
				"verbosity": {"type": "string", "enum": ["summary","detail","full"], "description": "Output detail level (default: detail)"}
			},
			"required": ["symbol"]
		}`),
	}
}

func (t *getTrendTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。本工具不产出列表（时间序列按时间分节，顺序与分节都是信息），
	// 传 nil 得到 category=text + 分级——分级本身仍是信息（PC-02）。
	var notReady bool
	defer func() { res = PresentList(res, notReady, nil) }()

	var params struct {
		Symbol    string `json:"symbol"`
		Limit     int    `json:"limit"`
		Verbosity string `json:"verbosity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid params: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Limit <= 0 {
		params.Limit = 10
	}
	if params.Verbosity == "" {
		params.Verbosity = "detail"
	}

	db := t.dbFn()
	if db == nil {
		notReady = true
		notReady = true
		notReady = true
		return &tool.ToolResult{Content: "Temporal database not available."}, nil
	}

	rows, err := db.QueryContext(ctx, `
		SELECT symbol_name, file_path, snapshot_at, complexity, caller_count, callee_count, line_count, effects
		FROM node_history WHERE symbol_name LIKE ?
		ORDER BY snapshot_at DESC LIMIT ?
	`, "%"+params.Symbol+"%", params.Limit)
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("query failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}
	defer rows.Close()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Trend for '%s':\n\n", params.Symbol))

	count := 0
	for rows.Next() {
		var s NodeSnapshot
		var snapAt string
		if err := rows.Scan(&s.SymbolName, &s.FilePath, &snapAt, &s.Complexity, &s.CallerCount, &s.CalleeCount, &s.LineCount, &s.Effects); err != nil {
			continue
		}
		count++
		switch params.Verbosity {
		case "summary":
			sb.WriteString(fmt.Sprintf("  %s: %d lines, %d callers\n", snapAt[:10], s.LineCount, s.CallerCount))
		case "detail":
			sb.WriteString(fmt.Sprintf("  [%s] %s — %d lines, %d callers, %d callees\n",
				snapAt[:19], s.SymbolName, s.LineCount, s.CallerCount, s.CalleeCount))
		case "full":
			sb.WriteString(fmt.Sprintf("  [%s]\n    Symbol: %s\n    File: %s\n    Lines: %d | Callers: %d | Callees: %d\n    Effects: %s\n\n",
				snapAt[:19], s.SymbolName, s.FilePath, s.LineCount, s.CallerCount, s.CalleeCount, s.Effects))
		}
	}

	if count == 0 {
		return &tool.ToolResult{Content: fmt.Sprintf("No history found for symbol matching '%s'.", params.Symbol)}, nil
	}

	return &tool.ToolResult{Content: sb.String()}, nil
}

// NewDetectDriftTool creates a "detect_drift" tool that analyzes architectural drift.
func NewDetectDriftTool(dbFn func() *sql.DB) tool.Tool {
	return &detectDriftTool{
		BaseTool: tool.BaseTool{
			Meta: tool.ToolMeta{
				Dimension:      tool.DimensionPerceive,
				Availability:   tool.AvailabilityConfigurable,
				ReadOnly:       true,
				Concurrent:     true,
				Timeout:        15 * time.Second,
				Risk:           tool.RiskSafe,
				PolicyFamily:   tool.PolicyFamilyOther,
				Enabled:        true,
				Domain:         "temporal",
				DomainToolTier: tool.DomainTierCore,
			},
		},
		dbFn: dbFn,
	}
}

type detectDriftTool struct {
	tool.BaseTool
	dbFn func() *sql.DB
}

func (t *detectDriftTool) Name() string { return "detect_drift" }

func (t *detectDriftTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "detect_drift",
		Description: "Detect architectural drift patterns: complexity spikes, interface instability, and dependency cycle formation. Based on historical snapshots.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"verbosity": {"type": "string", "enum": ["summary","detail","full"], "description": "Output detail level (default: detail)"}
			}
		}`),
	}
}

func (t *detectDriftTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		Verbosity string `json:"verbosity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid params: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Verbosity == "" {
		params.Verbosity = "detail"
	}

	db := t.dbFn()
	if db == nil {
		notReady = true
		notReady = true
		notReady = true
		return &tool.ToolResult{Content: "Temporal database not available."}, nil
	}

	alerts, err := DetectDrifts(db)
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("drift detection failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}

	if len(alerts) == 0 {
		return &tool.ToolResult{Content: "No architectural drift detected. Codebase metrics are stable."}, nil
	}

	var sb strings.Builder
	// 严重级进 kind 徽标：它决定先看哪条。漂移是跨时间的统计结论，没有单一位置。
	driftItems := make([]ListItem, 0, len(alerts))
	for _, a := range alerts {
		driftItems = append(driftItems, ListItem{
			Label:  a.Symbol,
			Detail: fmt.Sprintf("%s · %s", a.Kind, a.Description),
			Kind:   a.Severity,
		})
	}
	list = ListOf(driftItems, 0)

	sb.WriteString(fmt.Sprintf("Detected %d drift alert(s):\n\n", len(alerts)))

	for _, a := range alerts {
		switch params.Verbosity {
		case "summary":
			sb.WriteString(fmt.Sprintf("• [%s] %s: %s\n", a.Severity, a.Kind, a.Symbol))
		case "detail":
			sb.WriteString(fmt.Sprintf("## [%s] %s\n  %s\n\n", a.Severity, a.Kind, a.Description))
		case "full":
			sb.WriteString(fmt.Sprintf("## [%s] %s\n  Symbol: %s\n  Description: %s\n  Metric: %.1f\n\n",
				a.Severity, a.Kind, a.Symbol, a.Description, a.Metric))
		}
	}

	return &tool.ToolResult{Content: sb.String()}, nil
}

// NewPredictCoChangesTool creates a "predict_co_changes" tool that predicts which files tend to change together.
func NewPredictCoChangesTool(dbFn func() *sql.DB) tool.Tool {
	return &predictCoChangesTool{
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
				Domain:         "temporal",
				DomainToolTier: tool.DomainTierCore,
			},
		},
		dbFn: dbFn,
	}
}

type predictCoChangesTool struct {
	tool.BaseTool
	dbFn func() *sql.DB
}

func (t *predictCoChangesTool) Name() string { return "predict_co_changes" }

func (t *predictCoChangesTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "predict_co_changes",
		Description: "Predict which files are likely to need changes when a given file is modified, based on historical co-change patterns in temporal snapshots.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"file": {"type": "string", "description": "File path to analyze co-change partners for"},
				"limit": {"type": "integer", "description": "Max results (default 10)"},
				"verbosity": {"type": "string", "enum": ["summary","detail","full"], "description": "Output detail level (default: detail)"}
			},
			"required": ["file"]
		}`),
	}
}

func (t *predictCoChangesTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		File      string `json:"file"`
		Limit     int    `json:"limit"`
		Verbosity string `json:"verbosity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid params: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Limit <= 0 {
		params.Limit = 10
	}
	if params.Verbosity == "" {
		params.Verbosity = "detail"
	}

	db := t.dbFn()
	if db == nil {
		notReady = true
		notReady = true
		notReady = true
		return &tool.ToolResult{Content: "Temporal database not available."}, nil
	}

	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT nh2.file_path, COUNT(*) as co_count
		FROM node_history nh1
		JOIN node_history nh2 ON nh1.snapshot_at = nh2.snapshot_at AND nh1.file_path != nh2.file_path
		WHERE nh1.file_path LIKE ?
		GROUP BY nh2.file_path
		ORDER BY co_count DESC
		LIMIT ?
	`, "%"+IndexPath(params.File)+"%", params.Limit)
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("query failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}
	defer rows.Close()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Co-change predictions for '%s':\n\n", params.File))

	// 共变次数进 detail：它是"这个预测可信吗"的判据。文件粒度无行号。
	predItems := make([]ListItem, 0, 16)
	count := 0
	for rows.Next() {
		var filePath string
		var coCount int
		if err := rows.Scan(&filePath, &coCount); err != nil {
			continue
		}
		count++
		predItems = append(predItems, ListItem{
			Label:  filepath.Base(filePath),
			Detail: fmt.Sprintf("co-changed %d times", coCount),
			File:   filePath,
			Kind:   "predicted",
		})
		switch params.Verbosity {
		case "summary":
			sb.WriteString(fmt.Sprintf("  %s (%d)\n", filePath, coCount))
		case "detail", "full":
			sb.WriteString(fmt.Sprintf("  %s — co-changed %d times\n", filePath, coCount))
		}
	}

	list = ListOf(predItems, 0)

	if count == 0 {
		return &tool.ToolResult{Content: fmt.Sprintf("No co-change data found for '%s'. Need multiple temporal snapshots.", params.File)}, nil
	}

	return &tool.ToolResult{Content: sb.String()}, nil
}
