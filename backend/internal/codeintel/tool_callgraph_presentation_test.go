package codeintel

// find_callers 的呈现契约测试（PC-01 ~ PC-04）。
//
// Call 有 7 个 return，其中 4 个语义各不相同，而分级每个出口都不一样。这些测试逐个
// 覆盖那 4 个语义出口——因为「唯一附加点」这件事只有覆盖每个出口才算证明：漏掉的那
// 一处不报错，只是安静地让这个工具退回裸 pre。

import (
	"encoding/json"
	"testing"

	"github.com/weisyn/wesgine/tool"
)

func envelopeOf(t *testing.T, res *tool.ToolResult) (category string, grade string, data map[string]any) {
	t.Helper()
	if res == nil {
		t.Fatal("结果为 nil")
	}
	if len(res.StructuredData) == 0 {
		t.Fatal("PC-03: 没有呈现信封——工具会落回 GenericResult 的裸 pre")
	}
	var env struct {
		Category string          `json:"category"`
		Grade    string          `json:"grade"`
		Data     json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(res.StructuredData, &env); err != nil {
		t.Fatalf("信封不是合法 JSON: %v", err)
	}
	if len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, &data); err != nil {
			t.Fatalf("data 不是对象: %v", err)
		}
	}
	return env.Category, env.Grade, data
}

// 出口一：索引不可用。此前返回 IsError=false 的纯文本，于是「索引还没建好」与
// 「查到 0 个调用方」在前端完全同形——一个是"答案不可信"，另一个是"这函数可以删"。
func TestPresentCallers_IndexUnavailableIsNotReady(t *testing.T) {
	res := presentCallers(
		&tool.ToolResult{Content: "code index is not available"},
		callersOutcome{notReady: true},
	)
	category, grade, _ := envelopeOf(t, res)
	if grade != "not_ready" {
		t.Errorf("PC-02: grade = %q，期望 not_ready", grade)
	}
	if category != "text" {
		t.Errorf("没有图时 category = %q，期望 text", category)
	}
}

// not_ready 必须优先于 IsError：索引未就绪不是"答案不好"，是"答案不可信"。
func TestPresentCallers_NotReadyOutranksError(t *testing.T) {
	res := presentCallers(
		&tool.ToolResult{Content: "boom", IsError: true},
		callersOutcome{notReady: true},
	)
	if _, grade, _ := envelopeOf(t, res); grade != "not_ready" {
		t.Errorf("grade = %q，期望 not_ready 压过 bad", grade)
	}
}

// 出口二：查询出错。
func TestPresentCallers_QueryErrorIsBad(t *testing.T) {
	res := presentCallers(
		&tool.ToolResult{Content: "find callers failed", IsError: true, ErrorKind: tool.ErrorExecution},
		callersOutcome{},
	)
	if _, grade, _ := envelopeOf(t, res); grade != "bad" {
		t.Errorf("grade = %q，期望 bad", grade)
	}
}

// 出口三：0 个调用方。图仍然发送（只有焦点节点），分级 empty——
// 「这函数没人调用」往往正是用户想听的答案，不是错误。
func TestPresentCallers_NoCallersIsEmptyButStillAGraph(t *testing.T) {
	res := presentCallers(
		&tool.ToolResult{Content: `No callers found for "Foo"`},
		callersOutcome{graph: callersGraph("Foo", "", -1, nil)},
	)
	category, grade, data := envelopeOf(t, res)
	if grade != "empty" {
		t.Errorf("PC-02: grade = %q，期望 empty", grade)
	}
	if category != "graph" {
		t.Errorf("PC-01: category = %q，期望 graph", category)
	}
	nodes, _ := data["nodes"].([]any)
	if len(nodes) != 1 {
		t.Errorf("只有焦点节点时 nodes = %d，期望 1", len(nodes))
	}
}

// 出口四：有结果。
func TestPresentCallers_WithCallersIsGoodGraph(t *testing.T) {
	callers := []callerRef{
		{Name: "Handle", Kind: "func", File: "h.go", Line: 9, Signature: "func Handle()"},
		{Name: "TestHandle", Kind: "func", File: "h_test.go", Line: 3},
	}
	res := presentCallers(
		&tool.ToolResult{Content: "Found 2 caller(s)"},
		callersOutcome{graph: callersGraph("Foo", "foo.go", 41, callers)},
	)
	category, grade, data := envelopeOf(t, res)
	if grade != "good" || category != "graph" {
		t.Fatalf("category/grade = %q/%q，期望 graph/good", category, grade)
	}

	nodes, _ := data["nodes"].([]any)
	if len(nodes) != 3 {
		t.Fatalf("nodes = %d，期望 3（1 焦点 + 2 调用方）", len(nodes))
	}
	focus := nodes[0].(map[string]any)
	if focus["is_focus"] != true {
		t.Error("PC-04: 焦点节点必须排在第 0 位且 is_focus=true")
	}
	// 行号 +1：两个数据源都是 0-based，wire 是 1-based。差一会让用户点开跳错一行。
	if focus["line"].(float64) != 42 {
		t.Errorf("焦点 line = %v，期望 42（0-based 41 + 1）", focus["line"])
	}
	// 测试文件必须被标出来：「只有测试在调用它」和「生产代码在调用它」是两个结论。
	if nodes[2].(map[string]any)["is_test"] != true {
		t.Error("h_test.go 应被标记 is_test")
	}
	edges, _ := data["edges"].([]any)
	if len(edges) != 2 {
		t.Errorf("edges = %d，期望 2", len(edges))
	}
	if s := data["impact_summary"].(map[string]any)["direct_callers"]; s.(float64) != 2 {
		t.Errorf("direct_callers = %v，期望 2", s)
	}
}

// 多定义图：每个定义一个焦点。判 empty 必须看调用方计数而不是节点数——
// 3 个定义各 0 调用方也有 3 个节点，数节点会把"谁都没调用"读成 good。
func TestPresentCallers_GroupedGraphEmptyJudgedByCallerCount(t *testing.T) {
	groups := []callerGroup{
		{qualifiedName: "a.Get", defFile: "a.go", defLine: 1},
		{qualifiedName: "b.Get", defFile: "b.go", defLine: 2},
		{qualifiedName: "c.Get", defFile: "c.go", defLine: 3},
	}
	res := presentCallers(&tool.ToolResult{Content: "grouped"},
		callersOutcome{graph: groupedCallersGraph(groups)})

	_, grade, data := envelopeOf(t, res)
	nodes, _ := data["nodes"].([]any)
	if len(nodes) != 3 {
		t.Fatalf("nodes = %d，期望 3 个焦点", len(nodes))
	}
	if grade != "empty" {
		t.Errorf("PC-02: 3 个定义 0 调用方时 grade = %q，期望 empty（判据是调用方计数，不是节点数）", grade)
	}
}

// 同一个调用方调用两个同名定义时，节点去重但边不去重。
// 分头实现两条路径会让其中一条漏掉去重，症状是图上出现两个一模一样的节点。
func TestGroupedCallersGraph_DedupesNodesNotEdges(t *testing.T) {
	shared := []SymbolEntry{{Name: "Caller", Kind: "func", FilePath: "x.go", LineStart: 5}}
	graph := groupedCallersGraph([]callerGroup{
		{qualifiedName: "a.Get", defFile: "a.go", callers: shared},
		{qualifiedName: "b.Get", defFile: "b.go", callers: shared},
	})

	// 2 个焦点 + 1 个去重后的调用方
	if len(graph.Nodes) != 3 {
		t.Errorf("nodes = %d，期望 3（调用方应去重）", len(graph.Nodes))
	}
	// 但它确实调用了两个定义，所以两条边
	if len(graph.Edges) != 2 {
		t.Errorf("edges = %d，期望 2（边不去重）", len(graph.Edges))
	}
	if graph.ImpactSummary.DirectCallers != 2 {
		t.Errorf("direct_callers = %d，期望 2", graph.ImpactSummary.DirectCallers)
	}
}

// 零值 outcome 必须安全：将来有人加第八个 return 分支忘了填，产出的应是"少一张图"
// 而不是一个说谎的声明。
func TestPresentCallers_ZeroOutcomeDegradesSafely(t *testing.T) {
	res := presentCallers(&tool.ToolResult{Content: "whatever"}, callersOutcome{})
	category, grade, _ := envelopeOf(t, res)
	if category != "text" {
		t.Errorf("零值 outcome 的 category = %q，期望 text", category)
	}
	if grade != "empty" {
		t.Errorf("零值 outcome 的 grade = %q，期望 empty（无图即无结果）", grade)
	}
}
