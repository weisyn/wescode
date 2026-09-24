package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// NewCircularDepsTool creates a "detect_circular_dependencies" tool that
// finds strongly connected components (cycles) in the call/import graph
// using Tarjan's algorithm.
func NewCircularDepsTool(index *CodeIndex) tool.Tool {
	return &circularDepsTool{
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

type circularDepsTool struct {
	tool.BaseTool
	index *CodeIndex
}

func (t *circularDepsTool) Name() string { return "detect_circular_dependencies" }

func (t *circularDepsTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "detect_circular_dependencies",
		Description: "Detect circular dependencies in the call/import graph using Tarjan's SCC algorithm. Returns cycles where multiple symbols mutually depend on each other.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"scope": {"type": "string", "description": "Optional file path prefix to limit analysis scope"},
				"limit": {"type": "integer", "description": "Max number of cycles to return (default 5)"}
			}
		}`),
	}
}

func (t *circularDepsTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		Scope string `json:"scope"`
		Limit int    `json:"limit"`
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
		params.Limit = 5
	}

	db := t.index.readerDB

	// Load symbols, optionally filtered by scope.
	var sRows interface {
		Next() bool
		Scan(dest ...any) error
		Close() error
		Err() error
	}
	var queryErr error
	if params.Scope != "" {
		sRows, queryErr = db.QueryContext(ctx,
			`SELECT id, name, file_path, kind FROM symbols WHERE file_path LIKE ?`,
			IndexPath(params.Scope)+"%")
	} else {
		sRows, queryErr = db.QueryContext(ctx,
			`SELECT id, name, file_path, kind FROM symbols`)
	}
	if queryErr != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("symbol query failed: %v", queryErr), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}
	defer sRows.Close()

	type symInfo struct {
		name string
		file string
		kind string
	}
	symMap := map[int64]symInfo{}
	validIDs := map[int64]bool{}
	for sRows.Next() {
		var id int64
		var info symInfo
		if err := sRows.Scan(&id, &info.name, &info.file, &info.kind); err != nil {
			continue
		}
		symMap[id] = info
		validIDs[id] = true
	}
	if err := sRows.Err(); err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("symbol scan error: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}

	if len(symMap) == 0 {
		return &tool.ToolResult{Content: "no symbols found in scope"}, nil
	}

	// Load edges.
	eRows, err := db.QueryContext(ctx,
		`SELECT source_id, target_id FROM edges WHERE kind IN ('call','import')`)
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("edge query failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}
	defer eRows.Close()

	adj := map[int64][]int64{}
	for eRows.Next() {
		var src, tgt int64
		if err := eRows.Scan(&src, &tgt); err != nil {
			continue
		}
		if !validIDs[src] || !validIDs[tgt] {
			continue
		}
		adj[src] = append(adj[src], tgt)
	}
	if err := eRows.Err(); err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("edge scan error: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}

	// Tarjan's SCC.
	cycles := tarjanSCC(validIDs, adj)

	// Filter to cycles (SCC size > 1), sort by size descending.
	var realCycles [][]int64
	for _, scc := range cycles {
		if len(scc) > 1 {
			realCycles = append(realCycles, scc)
		}
	}

	if len(realCycles) == 0 {
		scope := "project"
		if params.Scope != "" {
			scope = params.Scope
		}
		// 空列表而非 nil：落 empty。"没有环"是这个工具最有价值的答案（架构是干净的），
		// 而 nil 会让它显示成"这个工具没产出列表"。
		list = &ListData{Items: []ListItem{}}
		return &tool.ToolResult{Content: fmt.Sprintf("No circular dependencies detected in %s", scope)}, nil
	}

	// 总数在截断前取：这个数字回答"这个代码库有多少个环"，而用户据此判断要不要
	// 动架构。报 "Found 10" 而实际 50，代价是修完 10 个以为没环了。
	cycleTotal := len(realCycles)
	if len(realCycles) > params.Limit {
		realCycles = realCycles[:params.Limit]
	}

	// 一项 = 一个环，不是一个符号。环的成员数与入口是用户判断"这个环好不好拆"的
	// 依据，所以 label 用入口符号、detail 说清环长与参与者——把成员摊平成独立项会让
	// "3 个环" 显示成 "11 个条目"，而用户要数的是环。
	items := make([]ListItem, 0, len(realCycles))
	for _, cycle := range realCycles {
		entry := symMap[cycle[0]]
		names := make([]string, 0, len(cycle))
		for _, nid := range cycle {
			names = append(names, symMap[nid].name)
		}
		items = append(items, ListItem{
			Label:  entry.name,
			Detail: fmt.Sprintf("%d symbols · %s", len(cycle), strings.Join(names, " → ")),
			File:   entry.file,
			Kind:   "cycle",
		})
	}
	list = ListOf(items, cycleTotal)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %s circular dependency cycle(s):\n\n", CountPhrase(len(realCycles), cycleTotal)))
	for i, cycle := range realCycles {
		sb.WriteString(fmt.Sprintf("Cycle %d (%d symbols):\n", i+1, len(cycle)))
		for j, nid := range cycle {
			info := symMap[nid]
			sb.WriteString(fmt.Sprintf("  %d. %s %s  (%s)\n", j+1, info.kind, info.name, info.file))
		}
		first := symMap[cycle[0]]
		sb.WriteString(fmt.Sprintf("  → back to %s\n\n", first.name))
	}
	return &tool.ToolResult{Content: sb.String()}, nil
}

// tarjanSCC runs Tarjan's algorithm and returns all strongly connected components.
func tarjanSCC(nodeIDs map[int64]bool, adj map[int64][]int64) [][]int64 {
	index := map[int64]int{}
	lowlink := map[int64]int{}
	onStack := map[int64]bool{}
	var stack []int64
	counter := 0
	var result [][]int64

	var strongConnect func(v int64)
	strongConnect = func(v int64) {
		index[v] = counter
		lowlink[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true

		for _, w := range adj[v] {
			if _, visited := index[w]; !visited {
				strongConnect(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack[w] {
				if index[w] < lowlink[v] {
					lowlink[v] = index[w]
				}
			}
		}

		if lowlink[v] == index[v] {
			var scc []int64
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			result = append(result, scc)
		}
	}

	for nid := range nodeIDs {
		if _, visited := index[nid]; !visited {
			strongConnect(nid)
		}
	}
	return result
}
