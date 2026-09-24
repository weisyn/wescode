package codeintel

// 调用图 wire 键的契约测试。
//
// 这些键是 wescode 与 wesui 之间**唯一**的约定：Go 侧的字段名、TS 侧的接口名都可以
// 改，JSON 键不行。而改错它不会报错——`is_focus` 写成 `isFocus` 之后 CallGraph.tsx
// 读到 undefined，于是每个节点都不是焦点、图照样渲染，没有任何一层会说话。
//
// 本文件的存在理由是这组类型刚从 `internal/rpc` 搬到这里（PC-04：工具也要产出图）。
// 搬迁最容易丢的就是 tag——字段重命名时 IDE 会跟着改 tag，而那一步没有编译器看着。
//
// 对应的 TS 类型在 wesui：`GraphNode` / `GraphEdge` / `CallGraphData`。

import (
	"encoding/json"
	"sort"
	"testing"
)

func keysOf(t *testing.T, v any) []string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func assertKeys(t *testing.T, name string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s 键集合大小 = %d，期望 %d\n实得 %v\n期望 %v", name, len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s 键漂移：实得 %v，期望 %v", name, got, want)
			return
		}
	}
}

func TestGraphNode_WireKeys(t *testing.T) {
	// 全字段填充：omitempty 字段留空会让测试漏掉它们，而漏掉的恰好是最容易改错的。
	assertKeys(t, "GraphNode", keysOf(t, GraphNode{
		ID: "a", Name: "Foo", Kind: "func", File: "a.go", Line: 1,
		Signature: "func Foo()", IsFocus: true, IsTest: true,
	}), []string{"file", "id", "is_focus", "is_test", "kind", "line", "name", "signature"})
}

func TestGraphEdge_WireKeys(t *testing.T) {
	assertKeys(t, "GraphEdge", keysOf(t, GraphEdge{
		Source: "a", Target: "b", Kind: "call", Resolved: true,
	}), []string{"kind", "resolved", "source", "target"})
}

func TestImpactSummary_WireKeys(t *testing.T) {
	assertKeys(t, "ImpactSummary", keysOf(t, ImpactSummary{
		DirectCallers: 1, IndirectDependents: 2, AffectedFiles: 3, AffectedTests: 4,
	}), []string{"affected_files", "affected_tests", "direct_callers", "indirect_dependents"})
}

func TestReadinessInfo_WireKeys(t *testing.T) {
	assertKeys(t, "ReadinessInfo", keysOf(t, ReadinessInfo{
		Status: "ready", Completeness: 1, Indexing: false,
	}), []string{"completeness", "indexing", "status"})
}

func TestCallGraphData_WireKeys(t *testing.T) {
	assertKeys(t, "CallGraphData", keysOf(t, CallGraphData{
		Nodes:         []GraphNode{{ID: "a"}},
		Edges:         []GraphEdge{{Source: "a", Target: "b"}},
		ImpactSummary: &ImpactSummary{},
		Readiness:     &ReadinessInfo{},
		Truncated:     true,
	}), []string{"edges", "impact_summary", "nodes", "readiness", "truncated"})
}

// omitempty 的两个指针字段必须真的能被省略：CallGraph.tsx 把它们声明为可选，
// 收到 `"impact_summary": null` 与收不到这个键是两种不同的处理路径。
func TestCallGraphData_OptionalFieldsOmitted(t *testing.T) {
	got := keysOf(t, CallGraphData{Nodes: []GraphNode{}, Edges: []GraphEdge{}})
	for _, k := range got {
		if k == "impact_summary" || k == "readiness" {
			t.Errorf("未设置时 %q 不应出现在 JSON 里，实得键集 %v", k, got)
		}
	}
}
