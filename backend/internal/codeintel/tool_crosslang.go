package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// NewCrossLanguageTool creates a "cross_language" tool that traces API routes
// across language boundaries (frontend fetch → backend handler and vice versa).
// Queries the edges table (kind='handles' / kind='http_calls') populated by Pipeline pass5.
func NewCrossLanguageTool(codeIndex *CodeIndex) tool.Tool {
	return &crossLanguageTool{
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
		codeIndex: codeIndex,
	}
}

type crossLanguageTool struct {
	tool.BaseTool
	codeIndex *CodeIndex
}

func (t *crossLanguageTool) Name() string { return "cross_language" }

func (t *crossLanguageTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "cross_language",
		Description: "Trace API routes across language boundaries. Find backend handlers for a URL path, or find frontend callers of a route.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"url": {"type": "string", "description": "URL path to trace (e.g. /api/users)"},
				"direction": {"type": "string", "enum": ["handler", "callers", "both"], "description": "handler: find backend handler. callers: find frontend callers. both: find both. Default: both."}
			},
			"required": ["url"]
		}`),
	}
}

func (t *crossLanguageTool) Call(ctx context.Context, input json.RawMessage, _ *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。本工具不产出列表（双向路由追踪分两段输出，摊平会丢掉方向），
	// 传 nil 得到 category=text + 分级——分级本身仍是信息（PC-02）。
	var notReady bool
	defer func() { res = PresentList(res, notReady, nil) }()

	var params struct {
		URL       string `json:"url"`
		Direction string `json:"direction"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "Invalid parameters: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.URL == "" {
		return &tool.ToolResult{Content: "url is required", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Direction == "" {
		params.Direction = "both"
	}

	if t.codeIndex == nil {
		return &tool.ToolResult{Content: "No code index available."}, nil
	}

	db := t.codeIndex.DB()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[Cross-Language Route Trace: %s]\n\n", params.URL))

	if params.Direction == "handler" || params.Direction == "both" {
		// Query edges with kind='handles': route node → handler symbol.
		// Route nodes are stored in symbols with kind='route' and name like "METHOD /path".
		rows, err := db.QueryContext(ctx,
			`SELECT s.file_path, s.name, s.line_start, e.target_name
			 FROM edges e JOIN symbols s ON e.source_id = s.id
			 WHERE e.kind = 'handles' AND s.kind = 'route'`)
		if err == nil {
			defer rows.Close()
			var found bool
			for rows.Next() {
				var filePath, routeName, targetName string
				var line int
				if rows.Scan(&filePath, &routeName, &line, &targetName) != nil {
					continue
				}
				parts := strings.SplitN(routeName, " ", 2)
				if len(parts) != 2 {
					continue
				}
				routePath := parts[1]
				if !pathMatches(routePath, params.URL) {
					continue
				}
				if !found {
					sb.WriteString("Backend Handlers:\n")
					found = true
				}
				handler := targetName
				if handler == "" {
					handler = "(anonymous)"
				}
				sb.WriteString(fmt.Sprintf("  %s %s → %s (%s:%d)\n", parts[0], routePath, handler, filePath, line+1))
			}
			if !found {
				sb.WriteString("No backend handlers found for this URL path.\n")
			}
		} else {
			sb.WriteString("No backend handlers found for this URL path.\n")
		}
		sb.WriteByte('\n')
	}

	if params.Direction == "callers" || params.Direction == "both" {
		// Query edges with kind='http_calls': caller → route node.
		rows, err := db.QueryContext(ctx,
			`SELECT e2.source_id, s_route.name, s_caller.file_path, s_caller.line_start
			 FROM edges e2
			 JOIN symbols s_route ON e2.target_id = s_route.id
			 JOIN symbols s_caller ON e2.source_id = s_caller.id
			 WHERE e2.kind = 'http_calls' AND s_route.kind = 'route'`)
		if err == nil {
			defer rows.Close()
			var found bool
			for rows.Next() {
				var callerID int64
				var routeName, callerFile string
				var callerLine int
				if rows.Scan(&callerID, &routeName, &callerFile, &callerLine) != nil {
					continue
				}
				parts := strings.SplitN(routeName, " ", 2)
				if len(parts) != 2 {
					continue
				}
				routePath := parts[1]
				if !pathMatches(routePath, params.URL) {
					continue
				}
				if !found {
					sb.WriteString("Frontend/Client Callers:\n")
					found = true
				}
				sb.WriteString(fmt.Sprintf("  %s (%s:%d)\n", routeName, callerFile, callerLine+1))
			}
			if !found {
				sb.WriteString("No frontend callers found for this URL path.\n")
			}
		} else {
			sb.WriteString("No frontend callers found for this URL path.\n")
		}
	}

	return &tool.ToolResult{Content: sb.String()}, nil
}
