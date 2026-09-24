package codeintel

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// NewTracePathTool creates a "trace_path" tool that finds the shortest path
// between two symbols in the call/type/implements graph using BFS.
func NewTracePathTool(index *CodeIndex) tool.Tool {
	return &tracePathTool{
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

type tracePathTool struct {
	tool.BaseTool
	index *CodeIndex
}

func (t *tracePathTool) Name() string { return "trace_path" }

func (t *tracePathTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "trace_path",
		Description: "Find the shortest dependency path between two symbols through call and implements edges. Useful for understanding how two distant symbols are connected.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"from": {"type": "string", "description": "Source symbol name"},
				"to": {"type": "string", "description": "Target symbol name"},
				"max_depth": {"type": "integer", "description": "Max search depth (default 10, max 20)"}
			},
			"required": ["from", "to"]
		}`),
	}
}

type tpNode struct {
	id   int64
	name string
	file string
	line int
	kind string
}

func (t *tracePathTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		From     string `json:"from"`
		To       string `json:"to"`
		MaxDepth int    `json:"max_depth"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.From == "" || params.To == "" {
		return &tool.ToolResult{Content: "both 'from' and 'to' are required", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if t.index == nil || t.index.readerDB == nil {
		notReady = true
		return &tool.ToolResult{Content: "code index is not available"}, nil
	}
	// readerDB may be swapped by ReopenAt/Close; hold RLock for the whole
	// query so the handle cannot be closed mid-flight.
	t.index.readerMu.RLock()
	defer t.index.readerMu.RUnlock()
	if params.MaxDepth <= 0 {
		params.MaxDepth = 10
	}
	if params.MaxDepth > 20 {
		params.MaxDepth = 20
	}

	db := t.index.readerDB

	// Resolve source and target symbol IDs.
	sourceIDs, err := resolveSymbolIDs(ctx, db, params.From)
	if err != nil || len(sourceIDs) == 0 {
		return &tool.ToolResult{Content: fmt.Sprintf("source symbol %q not found", params.From)}, nil
	}
	targetIDs, err := resolveSymbolIDs(ctx, db, params.To)
	if err != nil || len(targetIDs) == 0 {
		return &tool.ToolResult{Content: fmt.Sprintf("target symbol %q not found", params.To)}, nil
	}
	targetSet := make(map[int64]bool, len(targetIDs))
	for _, id := range targetIDs {
		targetSet[id] = true
	}

	// Build adjacency list from edges.
	adj, nodeInfo, err := buildTraceAdjacency(ctx, db)
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("failed to build graph: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}

	// BFS from all source IDs.
	type bfsEntry struct {
		id   int64
		path []int64
	}
	visited := map[int64]bool{}
	var queue []bfsEntry
	for _, sid := range sourceIDs {
		queue = append(queue, bfsEntry{id: sid, path: []int64{sid}})
		visited[sid] = true
	}

	var foundPath []int64
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if targetSet[cur.id] && len(cur.path) > 1 {
			foundPath = cur.path
			break
		}
		if len(cur.path) > params.MaxDepth {
			continue
		}
		for _, neighbor := range adj[cur.id] {
			if visited[neighbor] {
				continue
			}
			visited[neighbor] = true
			newPath := make([]int64, len(cur.path)+1)
			copy(newPath, cur.path)
			newPath[len(cur.path)] = neighbor
			queue = append(queue, bfsEntry{id: neighbor, path: newPath})
		}
	}

	if foundPath == nil {
		return &tool.ToolResult{Content: fmt.Sprintf("No path found from %q to %q within depth %d", params.From, params.To, params.MaxDepth)}, nil
	}

	var sb strings.Builder
	// 一项 = 路径上的一步。顺序即路径（列表保序），detail 标出它是第几跳——
	// 用户要读的是"从哪里怎么走到哪里"，而中间任一步都可能是他要改的那一处。
	pathItems := make([]ListItem, 0, len(foundPath))
	for i, nid := range foundPath {
		info := nodeInfo[nid]
		pathItems = append(pathItems, ListItem{
			Label:  info.name,
			Detail: fmt.Sprintf("hop %d", i),
			File:   info.file,
			Line:   info.line + 1, // CKG 存 0-based，与文本的 info.line+1 同源
			Kind:   info.kind,
		})
	}
	list = ListOf(pathItems, 0)

	sb.WriteString(fmt.Sprintf("Path from %q to %q (%d hop(s)):\n\n", params.From, params.To, len(foundPath)-1))
	for i, nid := range foundPath {
		info := nodeInfo[nid]
		arrow := "  "
		if i > 0 {
			arrow = "→ "
		}
		sb.WriteString(fmt.Sprintf("%s%s %s  (%s:%d)\n", arrow, info.kind, info.name, info.file, info.line+1))
	}
	return &tool.ToolResult{Content: sb.String()}, nil
}

func resolveSymbolIDs(ctx context.Context, db *sql.DB, name string) ([]int64, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id FROM symbols WHERE name = ? LIMIT 10`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func buildTraceAdjacency(ctx context.Context, db *sql.DB) (map[int64][]int64, map[int64]tpNode, error) {
	// Load symbols.
	sRows, err := db.QueryContext(ctx,
		`SELECT id, name, file_path, kind, line_start FROM symbols`)
	if err != nil {
		return nil, nil, err
	}
	defer sRows.Close()
	nodeInfo := map[int64]tpNode{}
	for sRows.Next() {
		var n tpNode
		if err := sRows.Scan(&n.id, &n.name, &n.file, &n.kind, &n.line); err != nil {
			continue
		}
		nodeInfo[n.id] = n
	}
	if err := sRows.Err(); err != nil {
		return nil, nil, err
	}

	// Load edges. Unresolved rows (target_id IS NULL) carry no traversable
	// endpoint; filtering them in SQL rather than letting Scan fail keeps the
	// swallowed-error path from hiding a real scan bug.
	eRows, err := db.QueryContext(ctx,
		`SELECT source_id, target_id FROM edges
		 WHERE kind IN ('call','implements') AND target_id IS NOT NULL`)
	if err != nil {
		return nil, nil, err
	}
	defer eRows.Close()
	adj := map[int64][]int64{}
	for eRows.Next() {
		var src, tgt int64
		if err := eRows.Scan(&src, &tgt); err != nil {
			continue
		}
		adj[src] = append(adj[src], tgt)
	}
	return adj, nodeInfo, eRows.Err()
}
