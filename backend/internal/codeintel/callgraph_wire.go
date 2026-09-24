package codeintel

// 调用图的 wire 形状——节点、边、影响摘要、就绪度。
//
// 这组类型此前住在 `internal/rpc/handler_codeintel.go` 且未导出，因为当时只有一个
// 生产者：项目概览页的 `codeintel/callgraph` 与 `codeintel/expand-node` 两个 RPC 端点。
//
// 现在有第二个生产者。`find_callers` / `impact_analysis` / `trace_data_flow` 这些工具
// 的结果本来就是图，而 PC-04 要求它们把结构下发给前端（`CallGraph.tsx` 早就能画，
// 只是对话流里从没接线）。工具住在 `internal/codeintel`，依赖方向是 `rpc → codeintel`，
// 所以工具够不到住在 rpc 里的类型——形状必须搬到两个生产者都能到的地方。
//
// 不在 codeintel 里重新定义一份：两份同形结构会漂移，而漂移的那一半是 snake_case
// 键名——`is_focus` 写成 `isFocus` 不会报错，只是 CallGraph 里每个节点都不是焦点。
//
// 消费侧的对应类型在 wesui：`GraphNode` / `GraphEdge` / `CallGraphData`
// （[web 的 CallGraph](../../../web/src/components/codeintel/CallGraph.tsx) 直接消费）。
// JSON 键是两侧唯一的契约，所以它们必须逐字一致。

// GraphNode 是调用图里的一个符号。
type GraphNode struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Signature string `json:"signature,omitempty"`
	IsFocus   bool   `json:"is_focus"`
	IsTest    bool   `json:"is_test"`
}

// GraphEdge is the wire shape for the call-graph viewer.
//
// Resolved says whether the far endpoint is a symbol we actually indexed, or a
// synthetic placeholder minted from a callee name that FindSymbol could not
// locate. It is a property of the *node*, surfaced on the edge so the renderer
// can dash the line without joining back to the node table.
//
// This field used to be `Certainty float64`, populated with the literals 1.0
// and 0.5 — never read from edges.certainty, and never any value but those two.
// The float invited the frontend to threshold it (`certainty < 0.8`), which
// read as a confidence scale that did not exist. Two states get two states.
type GraphEdge struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Kind     string `json:"kind"`
	Resolved bool   `json:"resolved"`
}

// ImpactSummary 是一次影响分析的计数摘要。
type ImpactSummary struct {
	DirectCallers      int `json:"direct_callers"`
	IndirectDependents int `json:"indirect_dependents"`
	AffectedFiles      int `json:"affected_files"`
	AffectedTests      int `json:"affected_tests"`
}

// ReadinessInfo 说明这张图是在什么索引状态下产出的。
//
// 它是 PC-02 的 `not_ready` 分级在数据层的对应物：索引没建完时图仍然可以画，但
// 「没找到调用方」这个答案不可信，而用户必须能分辨这两件事。
type ReadinessInfo struct {
	Status       string  `json:"status"` // "not_initialized" | "indexing" | "ready"
	Completeness float64 `json:"completeness"`
	Indexing     bool    `json:"indexing"`
}

// CallGraphData 是一张完整的调用图。
//
// 原名 `callgraphResponse`——那个名字假设只有 RPC 响应会用它。现在工具也产出它，
// 名字跟着改成它本来的含义，并与 wesui 侧的 `CallGraphData` 对齐。
type CallGraphData struct {
	Nodes         []GraphNode    `json:"nodes"`
	Edges         []GraphEdge    `json:"edges"`
	ImpactSummary *ImpactSummary `json:"impact_summary,omitempty"`
	Readiness     *ReadinessInfo `json:"readiness,omitempty"`
	Truncated     bool           `json:"truncated"`
}
