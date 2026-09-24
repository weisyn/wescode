package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// NewTaintPathsTool creates "find_taint_paths" — traces paths from taint source to dangerous sink
// via BFS along DATA_FLOWS_TO edges.
func NewTaintPathsTool(index *CodeIndex) tool.Tool {
	return &taintPathsTool{
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
				Domain:         "analysis",
				DomainToolTier: tool.DomainTierCore,
			},
		},
		index: index,
	}
}

type taintPathsTool struct {
	tool.BaseTool
	index *CodeIndex
}

func (t *taintPathsTool) Name() string { return "find_taint_paths" }

func (t *taintPathsTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "find_taint_paths",
		Description: "Traces data flow paths from taint sources (user_input, config, db_result, external_api) to dangerous sinks (e.g. Exec, Query). Uses BFS on DATA_FLOWS_TO edges in the code knowledge graph.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"source_kind": {"type": "string", "enum": ["user_input","config","db_result","external_api"], "description": "Taint origin category to trace from"},
				"sink_pattern": {"type": "string", "description": "Target function name pattern to match sinks (e.g. 'Exec', 'Query')"},
				"max_depth": {"type": "integer", "description": "Max BFS hops (default 5)"},
				"verbosity": {"type": "string", "enum": ["summary","detail","full"], "description": "Output detail level (default: detail)"}
			},
			"required": ["source_kind", "sink_pattern"]
		}`),
	}
}

func (t *taintPathsTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		SourceKind  string `json:"source_kind"`
		SinkPattern string `json:"sink_pattern"`
		MaxDepth    int    `json:"max_depth"`
		Verbosity   string `json:"verbosity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if t.index == nil {
		notReady = true
		return &tool.ToolResult{Content: "code index is not available"}, nil
	}
	if params.SourceKind == "" || params.SinkPattern == "" {
		return &tool.ToolResult{Content: "source_kind and sink_pattern are required", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if params.MaxDepth <= 0 {
		params.MaxDepth = 5
	}
	if params.MaxDepth > 15 {
		params.MaxDepth = 15
	}

	paths, err := t.index.FindTaintPaths(ctx, TaintKind(params.SourceKind), params.SinkPattern, params.MaxDepth)
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("taint analysis failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}

	if len(paths) == 0 {
		// 空列表而非 nil：落 empty。"没有污点路径"是这个工具最有价值的答案——
		// 它意味着这条数据流是干净的。
		list = &ListData{Items: []ListItem{}}
		return &tool.ToolResult{Content: fmt.Sprintf("No taint paths found from %s sources to sinks matching %q within %d hops.",
			params.SourceKind, params.SinkPattern, params.MaxDepth)}, nil
	}

	verbosity := ParseVerbosity(params.Verbosity)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d taint path(s) from %s → %q:\n\n", len(paths), params.SourceKind, params.SinkPattern))

	limit := 20
	if verbosity == VerbositySummary {
		limit = 5
	} else if verbosity == VerbosityFull {
		limit = 50
	}
	pathTotal := len(paths)
	if len(paths) > limit {
		paths = paths[:limit]
	}

	// 一项 = 一条污点路径，不是一个节点。label 用 source→sink（用户判断"这条要不要
	// 管"看的是两端），detail 带跳数与完整链路。位置取**源头**：修污点要从入口改，
	// 而链路中段的节点通常是无辜的传递者。
	//
	// TaintStep.Line 来自 `SELECT s.line_start`（0-based），所以要 +1。文本输出那侧
	// 也已统一（2026-09-20 普查）——同一个节点在列表与正文显示不同行号会让用户
	// 以为工具在猜。
	items := make([]ListItem, 0, len(paths))
	for _, tp := range paths {
		var srcFile string
		var srcLine int
		if len(tp.Steps) > 0 {
			srcFile, srcLine = tp.Steps[0].File, tp.Steps[0].Line+1
		}
		items = append(items, ListItem{
			Label:  fmt.Sprintf("%s → %s", tp.Source, tp.Sink),
			Detail: fmt.Sprintf("%d hops · %s", tp.Hops, strings.Join(tp.StepNames(), " → ")),
			File:   srcFile,
			Line:   srcLine,
			Kind:   "taint",
		})
	}
	list = ListOf(items, pathTotal)

	for i, p := range paths {
		switch verbosity {
		case VerbositySummary:
			sb.WriteString(fmt.Sprintf("%d. %s → ... → %s (%d hops)\n", i+1, p.Source, p.Sink, p.Hops))
		case VerbosityFull:
			sb.WriteString(fmt.Sprintf("%d. Path (%d hops):\n", i+1, p.Hops))
			for j, step := range p.Steps {
				sb.WriteString(fmt.Sprintf("   [%d] %s (%s:%d)\n", j, step.Name, step.File, step.Line+1))
			}
			sb.WriteByte('\n')
		default:
			sb.WriteString(fmt.Sprintf("%d. %s → %s (%d hops)\n", i+1, p.Source, p.Sink, p.Hops))
			sb.WriteString(fmt.Sprintf("   Path: %s\n\n", strings.Join(p.StepNames(), " → ")))
		}
	}

	if len(paths) == limit {
		sb.WriteString(fmt.Sprintf("\n(showing first %d paths, increase verbosity for more)\n", limit))
	}

	return &tool.ToolResult{Content: sb.String()}, nil
}

// TaintPath represents a single source-to-sink data flow path.
type TaintPath struct {
	Source string
	Sink   string
	Hops   int
	Steps  []TaintStep
}

// StepNames returns the list of node names along the path.
func (tp TaintPath) StepNames() []string {
	names := make([]string, len(tp.Steps))
	for i, s := range tp.Steps {
		names[i] = s.Name
	}
	return names
}

// TaintStep is a single node in a taint path.
type TaintStep struct {
	QName string
	Name  string
	File  string
	Line  int
}

// FindTaintPaths performs BFS from tainted source nodes to sink-matching nodes
// along DATA_FLOWS_TO edges.
func (ci *CodeIndex) FindTaintPaths(ctx context.Context, sourceKind TaintKind, sinkPattern string, maxDepth int) ([]TaintPath, error) {
	// readerDB may be swapped by ReopenAt/Close; hold RLock for the whole
	// query so the handle cannot be closed mid-flight.
	ci.readerMu.RLock()
	defer ci.readerMu.RUnlock()
	// Load tainted source nodes (from DB, matching the requested kind).
	sourceRows, err := ci.readerDB.QueryContext(ctx,
		`SELECT s.id, s.name, s.file_path, s.line_start
		 FROM symbols s
		 WHERE s.kind IN ('function','method')
		 LIMIT 5000`)
	if err != nil {
		return nil, fmt.Errorf("query sources: %w", err)
	}

	type nodeInfo struct {
		id   int64
		name string
		file string
		line int
	}
	allNodes := make(map[int64]nodeInfo)
	for sourceRows.Next() {
		var ni nodeInfo
		if err := sourceRows.Scan(&ni.id, &ni.name, &ni.file, &ni.line); err != nil {
			continue
		}
		allNodes[ni.id] = ni
	}
	sourceRows.Close()

	// Identify source nodes by taint pattern matching.
	var sourceIDs []int64
	for id, ni := range allNodes {
		for pattern, kind := range taintSources {
			if kind == sourceKind && strings.Contains(ni.name, pattern) {
				sourceIDs = append(sourceIDs, id)
				break
			}
		}
	}

	if len(sourceIDs) == 0 {
		return nil, nil
	}

	// Build forward adjacency from DATA_FLOWS_TO edges.
	edgeRows, err := ci.readerDB.QueryContext(ctx,
		`SELECT source_id, target_id FROM edges WHERE kind = 'data_flows_to' AND target_id IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}

	forwardAdj := make(map[int64][]int64)
	for edgeRows.Next() {
		var src, tgt int64
		if err := edgeRows.Scan(&src, &tgt); err != nil {
			continue
		}
		forwardAdj[src] = append(forwardAdj[src], tgt)
	}
	edgeRows.Close()

	// BFS from each source, recording paths to nodes whose name matches sinkPattern.
	sinkPatternLower := strings.ToLower(sinkPattern)
	var results []TaintPath

	for _, startID := range sourceIDs {
		if ctx.Err() != nil {
			break
		}

		type bfsEntry struct {
			id   int64
			path []int64
		}

		visited := map[int64]bool{startID: true}
		queue := []bfsEntry{{id: startID, path: []int64{startID}}}

		for len(queue) > 0 && len(results) < 200 {
			cur := queue[0]
			queue = queue[1:]

			if len(cur.path)-1 >= maxDepth {
				continue
			}

			for _, next := range forwardAdj[cur.id] {
				if visited[next] {
					continue
				}
				visited[next] = true

				newPath := make([]int64, len(cur.path)+1)
				copy(newPath, cur.path)
				newPath[len(cur.path)] = next

				ni, ok := allNodes[next]
				if ok && strings.Contains(strings.ToLower(ni.name), sinkPatternLower) {
					tp := TaintPath{
						Source: allNodes[startID].name,
						Sink:   ni.name,
						Hops:   len(newPath) - 1,
					}
					for _, pid := range newPath {
						if pni, ok := allNodes[pid]; ok {
							tp.Steps = append(tp.Steps, TaintStep{
								Name: pni.name,
								File: pni.file,
								Line: pni.line,
							})
						}
					}
					results = append(results, tp)
				}

				queue = append(queue, bfsEntry{id: next, path: newPath})
			}
		}
	}

	return results, nil
}
