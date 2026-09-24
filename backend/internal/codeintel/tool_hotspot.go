package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// NewHotspotTool creates a "find_hotspot" tool that identifies functions
// with the highest transitive callee count (complexity hotspots).
func NewHotspotTool(index *CodeIndex) tool.Tool {
	return &hotspotTool{
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

type hotspotTool struct {
	tool.BaseTool
	index *CodeIndex
}

func (t *hotspotTool) Name() string { return "find_hotspot" }

func (t *hotspotTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "find_hotspot",
		Description: "Find complexity hotspots by ranking functions by their transitive callee count (BFS up to depth 5). Functions that transitively call many others are potential complexity risks.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"limit": {"type": "integer", "description": "Max results (default 10)"},
				"scope": {"type": "string", "description": "Optional file path prefix to limit analysis scope"},
				"verbosity": {"type": "string", "enum": ["summary", "detail", "full"], "description": "Output detail level (default: detail)"}
			}
		}`),
	}
}

// hotspotListItems 把热点投影成列表项。
//
// detail 带**两个**计数（transitive 与 direct）而不只是排序用的那个：它们的差值
// 就是复杂度的来源——direct 3 / transitive 200 说的是"这个函数自己很简单，但它
// 拉起了一整棵树"，而 direct 40 / transitive 45 说的是"这个函数本身臃肿"。
// 两种情况的处置完全不同，只显示排序键会让它们同形。
//
// Line 要 +1：hotspotEntry.Line 直接 Scan 自 symbols.line_start（0-based），
// 与文本输出那行 `e.Line+1` 是同一笔账。
func hotspotListItems(entries []hotspotEntry) []ListItem {
	items := make([]ListItem, 0, len(entries))
	for _, e := range entries {
		items = append(items, ListItem{
			Label:  e.Name,
			Detail: fmt.Sprintf("%d transitive · %d direct callees", e.TransitiveCallees, e.DirectCallees),
			File:   e.File,
			Line:   e.Line + 1,
			Kind:   e.Kind,
		})
	}
	return items
}

type hotspotEntry struct {
	Name              string
	Kind              string
	File              string
	Line              int
	DirectCallees     int
	TransitiveCallees int
}

func (t *hotspotTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		Limit     int    `json:"limit"`
		Scope     string `json:"scope"`
		Verbosity string `json:"verbosity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
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
		params.Limit = 10
	}

	db := t.index.readerDB

	// Load function/method symbols.
	var sRows interface {
		Next() bool
		Scan(dest ...any) error
		Close() error
		Err() error
	}
	var queryErr error
	if params.Scope != "" {
		sRows, queryErr = db.QueryContext(ctx,
			`SELECT id, name, file_path, kind, line_start FROM symbols WHERE kind IN ('function','method') AND file_path LIKE ?`,
			IndexPath(params.Scope)+"%")
	} else {
		sRows, queryErr = db.QueryContext(ctx,
			`SELECT id, name, file_path, kind, line_start FROM symbols WHERE kind IN ('function','method')`)
	}
	if queryErr != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("symbol query failed: %v", queryErr), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}
	defer sRows.Close()

	type symNode struct {
		id   int64
		name string
		file string
		kind string
		line int
	}
	var functions []symNode
	funcSet := map[int64]bool{}
	for sRows.Next() {
		var n symNode
		if err := sRows.Scan(&n.id, &n.name, &n.file, &n.kind, &n.line); err != nil {
			continue
		}
		functions = append(functions, n)
		funcSet[n.id] = true
	}
	if err := sRows.Err(); err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("symbol scan error: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}

	if len(functions) == 0 {
		return &tool.ToolResult{Content: "no functions found in scope"}, nil
	}

	// Load call edges.
	eRows, err := db.QueryContext(ctx,
		`SELECT source_id, target_id FROM edges WHERE kind = 'call'`)
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("edge query failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}
	defer eRows.Close()

	adj := map[int64][]int64{}
	directCount := map[int64]int{}
	for eRows.Next() {
		var src, tgt int64
		if err := eRows.Scan(&src, &tgt); err != nil {
			continue
		}
		adj[src] = append(adj[src], tgt)
		if funcSet[src] {
			directCount[src]++
		}
	}
	if err := eRows.Err(); err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("edge scan error: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}

	const maxTransitiveDepth = 5

	// BFS transitive callee count for each function.
	var entries []hotspotEntry
	for _, fn := range functions {
		visited := map[int64]bool{fn.id: true}
		queue := []int64{fn.id}
		transitiveCount := 0
		depth := 0
		for len(queue) > 0 && depth < maxTransitiveDepth {
			nextQueue := []int64{}
			for _, cur := range queue {
				for _, neighbor := range adj[cur] {
					if visited[neighbor] {
						continue
					}
					visited[neighbor] = true
					transitiveCount++
					nextQueue = append(nextQueue, neighbor)
				}
			}
			queue = nextQueue
			depth++
		}
		if transitiveCount > 0 {
			entries = append(entries, hotspotEntry{
				Name:              fn.name,
				Kind:              fn.kind,
				File:              fn.file,
				Line:              fn.line,
				DirectCallees:     directCount[fn.id],
				TransitiveCallees: transitiveCount,
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].TransitiveCallees > entries[j].TransitiveCallees
	})

	if len(entries) == 0 {
		return &tool.ToolResult{Content: "no complexity hotspots found"}, nil
	}

	verbosity := ParseVerbosity(params.Verbosity)
	// 池子大小在两级截断之前取。"top 5" 本身不是谎（top N 就是选前 N），但它说不出
	// 问题规模——"top 5 of 43" 才告诉用户这个代码库有 43 个热点而不是 5 个。
	pool := len(entries)
	if len(entries) > params.Limit {
		entries = entries[:params.Limit]
	}
	entries, _ = TruncateWithTotal(entries, verbosity)
	list = ListOf(hotspotListItems(entries), pool)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Complexity hotspots (top %s by transitive callee count):\n\n", CountPhrase(len(entries), pool)))
	for i, e := range entries {
		switch verbosity {
		case VerbositySummary:
			sb.WriteString(fmt.Sprintf("%d. %s — %d transitive, %d direct\n",
				i+1, e.Name, e.TransitiveCallees, e.DirectCallees))
		default:
			sb.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, e.Kind, e.Name))
			sb.WriteString(fmt.Sprintf("   File: %s:%d\n", e.File, e.Line+1))
			sb.WriteString(fmt.Sprintf("   Direct callees: %d | Transitive callees: %d\n\n",
				e.DirectCallees, e.TransitiveCallees))
		}
	}
	return &tool.ToolResult{Content: sb.String()}, nil
}
