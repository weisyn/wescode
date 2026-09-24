package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

func NewAPIConsumersTool(index *CodeIndex) tool.Tool {
	return &apiConsumersTool{
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

type apiConsumersTool struct {
	tool.BaseTool
	index *CodeIndex
}

func (t *apiConsumersTool) Name() string { return "trace_api_consumers" }

func (t *apiConsumersTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "trace_api_consumers",
		Description: "Trace which code consumes a given HTTP API endpoint. Finds route handlers and HTTP callers using the code knowledge graph.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"endpoint": {"type": "string", "description": "HTTP endpoint path to trace consumers for (e.g. '/api/users')"},
				"method": {"type": "string", "description": "HTTP method filter (e.g. 'GET', 'POST'). Optional."},
				"limit": {"type": "integer", "description": "Max results (default 20)"},
				"verbosity": {"type": "string", "enum": ["summary", "detail", "full"], "description": "Output detail level (default: detail)"}
			},
			"required": ["endpoint"]
		}`),
	}
}

type apiSymbol struct {
	FilePath  string
	Kind      string
	Name      string
	Signature string
	Parent    string
	LineStart int
	LineEnd   int
}

func (t *apiConsumersTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		Endpoint  string `json:"endpoint"`
		Method    string `json:"method"`
		Limit     int    `json:"limit"`
		Verbosity string `json:"verbosity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.Endpoint == "" {
		return &tool.ToolResult{Content: "endpoint is required", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if t.index == nil || t.index.readerDB == nil {
		notReady = true
		return &tool.ToolResult{Content: "code index is not available"}, nil
	}
	// readerDB may be swapped by ReopenAt/Close; hold RLock for the whole
	// query so the handle cannot be closed mid-flight.
	t.index.readerMu.RLock()
	defer t.index.readerMu.RUnlock()
	if params.Limit <= 0 {
		params.Limit = 20
	}

	verbosity := ParseVerbosity(params.Verbosity)
	db := t.index.readerDB

	// Find route symbols matching the endpoint.
	routePattern := "%" + params.Endpoint + "%"
	routeRows, err := db.QueryContext(ctx,
		`SELECT id, name FROM symbols WHERE kind = 'route' AND name LIKE ?`,
		routePattern)
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("route query failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}
	defer routeRows.Close()

	type routeInfo struct {
		id   int64
		name string
	}
	var routes []routeInfo
	for routeRows.Next() {
		var r routeInfo
		if err := routeRows.Scan(&r.id, &r.name); err != nil {
			continue
		}
		if params.Method != "" && !strings.Contains(strings.ToUpper(r.name), strings.ToUpper(params.Method)) {
			continue
		}
		routes = append(routes, r)
	}
	if err := routeRows.Err(); err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("route scan error: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}

	if len(routes) == 0 {
		return &tool.ToolResult{Content: fmt.Sprintf("No route found matching %q", params.Endpoint)}, nil
	}

	var handlers []apiSymbol
	var callers []apiSymbol

	for _, route := range routes {
		// Find handlers (who handles this route).
		hRows, err := db.QueryContext(ctx, `
			SELECT s.file_path, s.kind, s.name, s.signature, s.parent, s.line_start, s.line_end
			FROM edges e
			JOIN symbols s ON e.source_id = s.id
			WHERE e.kind = 'handles' AND e.target_id = ?
			LIMIT ?
		`, route.id, params.Limit)
		if err == nil {
			for hRows.Next() {
				var sym apiSymbol
				if err := hRows.Scan(&sym.FilePath, &sym.Kind, &sym.Name, &sym.Signature, &sym.Parent, &sym.LineStart, &sym.LineEnd); err != nil {
					continue
				}
				handlers = append(handlers, sym)
			}
			hRows.Close()
		}

		// Find HTTP callers (who calls this route).
		cRows, err := db.QueryContext(ctx, `
			SELECT s.file_path, s.kind, s.name, s.signature, s.parent, s.line_start, s.line_end
			FROM edges e
			JOIN symbols s ON e.source_id = s.id
			WHERE e.kind IN ('http_calls', 'cross_http_calls') AND e.target_id = ?
			LIMIT ?
		`, route.id, params.Limit)
		if err == nil {
			for cRows.Next() {
				var sym apiSymbol
				if err := cRows.Scan(&sym.FilePath, &sym.Kind, &sym.Name, &sym.Signature, &sym.Parent, &sym.LineStart, &sym.LineEnd); err != nil {
					continue
				}
				callers = append(callers, sym)
			}
			cRows.Close()
		}
	}

	if len(handlers) == 0 && len(callers) == 0 {
		list = &ListData{Items: []ListItem{}}
		return &tool.ToolResult{Content: fmt.Sprintf("No handlers or callers found for endpoint %q", params.Endpoint)}, nil
	}

	handlers, handlerTotal := TruncateWithTotal(handlers, verbosity)
	callers, callerTotal := TruncateWithTotal(callers, verbosity)

	// handler 与 caller 合进一个列表：用户问的是"谁在用这个 API"，而 handler（实现它的）
	// 与 caller（调它的）是同一个问题的两半。kind 徽标区分角色——两者的处置不同：
	// 改 API 要动 handler，删 API 要先看 caller。
	items := make([]ListItem, 0, len(handlers)+len(callers))
	for _, h := range handlers {
		items = append(items, ListItem{
			Label: h.Name, Detail: h.Parent, File: h.FilePath,
			Line: h.LineStart + 1, Kind: "handler",
		})
	}
	for _, c := range callers {
		items = append(items, ListItem{
			Label: c.Name, Detail: c.Parent, File: c.FilePath,
			Line: c.LineStart + 1, Kind: "caller",
		})
	}
	list = ListOf(items, handlerTotal+callerTotal)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("API consumers for %q (%d route(s) matched):\n\n", params.Endpoint, len(routes)))

	if len(handlers) > 0 {
		sb.WriteString(fmt.Sprintf("── Handlers (%s) ──\n", CountPhrase(len(handlers), handlerTotal)))
		for i, h := range handlers {
			switch verbosity {
			case VerbositySummary:
				sb.WriteString(fmt.Sprintf("  %d. %s (%s)\n", i+1, h.Name, h.FilePath))
			default:
				sb.WriteString(fmt.Sprintf("  %d. %s %s\n", i+1, h.Kind, h.Name))
				sb.WriteString(fmt.Sprintf("     File: %s:%d\n", h.FilePath, h.LineStart+1))
				if h.Parent != "" {
					sb.WriteString(fmt.Sprintf("     Parent: %s\n", h.Parent))
				}
			}
		}
		sb.WriteByte('\n')
	}

	if len(callers) > 0 {
		sb.WriteString(fmt.Sprintf("── Callers (%s) ──\n", CountPhrase(len(callers), callerTotal)))
		for i, c := range callers {
			switch verbosity {
			case VerbositySummary:
				sb.WriteString(fmt.Sprintf("  %d. %s (%s)\n", i+1, c.Name, c.FilePath))
			default:
				sb.WriteString(fmt.Sprintf("  %d. %s %s\n", i+1, c.Kind, c.Name))
				sb.WriteString(fmt.Sprintf("     File: %s:%d\n", c.FilePath, c.LineStart+1))
				if c.Parent != "" {
					sb.WriteString(fmt.Sprintf("     Parent: %s\n", c.Parent))
				}
			}
		}
	}

	return &tool.ToolResult{Content: sb.String()}, nil
}
