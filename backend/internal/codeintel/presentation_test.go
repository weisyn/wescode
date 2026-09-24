package codeintel

// 呈现契约产出侧的行为测试（PC-01 ~ PC-04）。
//
// 这些测试锁的不是"字段被填了"，而是几个会安静出错的形状：信封走不走 StructuredData
// （走 Metadata 的话到不了前端）、data 有没有被中间层重塑、序列化失败会不会毁掉文本
// 结果、闭域值有没有漂移。

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/weisyn/wesgine/tool"
)

func decodeEnvelope(t *testing.T, res *tool.ToolResult) map[string]any {
	t.Helper()
	if len(res.StructuredData) == 0 {
		t.Fatal("PC-03: 信封没有落在 StructuredData 上——" +
			"engine.ToolResultData 没有 Metadata 字段，走那里永远到不了前端")
	}
	var env map[string]any
	if err := json.Unmarshal(res.StructuredData, &env); err != nil {
		t.Fatalf("信封不是合法 JSON: %v", err)
	}
	return env
}

func TestPresent_EnvelopeCarriesCategoryAndGrade(t *testing.T) {
	res := Present(&tool.ToolResult{Content: "found 3"}, CategoryGraph, GradeGood,
		map[string]any{"nodes": []any{}, "edges": []any{}})

	env := decodeEnvelope(t, res)
	if env["category"] != "graph" {
		t.Errorf("category = %v，期望 graph", env["category"])
	}
	if env["grade"] != "good" {
		t.Errorf("grade = %v，期望 good", env["grade"])
	}
	if res.Content != "found 3" {
		t.Errorf("Content 被改动了: %q", res.Content)
	}
}

// data 必须原样透传。形状归工具、形态归 wesui，中间层一旦重塑就成了第三份真相。
func TestPresent_DataIsNotReshaped(t *testing.T) {
	payload := map[string]any{
		"nodes":          []any{map[string]any{"id": "main", "is_focus": true}},
		"impact_summary": map[string]any{"direct_callers": 3},
	}
	res := Present(&tool.ToolResult{}, CategoryGraph, GradeGood, payload)

	env := decodeEnvelope(t, res)
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data 不是对象: %T", env["data"])
	}
	// snake_case 键必须原样保留——一次驼峰化就会让 wesui 的 CallGraph 收到
	// is_focus=undefined，而它不会报错，只是每个节点都不是焦点。
	nodes := data["nodes"].([]any)
	if nodes[0].(map[string]any)["is_focus"] != true {
		t.Error("PC-04: snake_case 键被改写了；wesui 读 is_focus，不是 isFocus")
	}
	if data["impact_summary"] == nil {
		t.Error("PC-04: impact_summary 丢了")
	}
}

// 没有结构化数据时信封仍要发：`grade: empty` 本身就是答案（"这函数没人调用"），
// 不是"忘了填数据"。
func TestPresent_EnvelopeSentEvenWithoutData(t *testing.T) {
	res := Present(&tool.ToolResult{Content: "no callers"}, CategoryList, GradeEmpty, nil)

	env := decodeEnvelope(t, res)
	if env["grade"] != "empty" {
		t.Errorf("grade = %v，期望 empty", env["grade"])
	}
	if _, has := env["data"]; has {
		t.Errorf("无数据时不该出现 data 键，实得 %v", env["data"])
	}
}

// 载荷序列化失败不得毁掉文本结果——模型仍然要读 Content。但也不能静默：
// 声称是 graph 却没有数据的信封比没有声明更难查。
func TestPresent_UnserializablePayloadDegradesToTextAndSaysSo(t *testing.T) {
	res := Present(&tool.ToolResult{Content: "found 3"}, CategoryGraph, GradeGood,
		map[string]any{"bad": math.Inf(1)})

	env := decodeEnvelope(t, res)
	if env["category"] != "text" {
		t.Errorf("序列化失败后 category = %v，期望降级为 text", env["category"])
	}
	if _, has := env["data"]; has {
		t.Error("序列化失败后不该留下 data 键")
	}
	if res.Content == "found 3" {
		t.Error("降级必须留痕：Content 里应说明结构化载荷被丢弃了")
	}
}

func TestPresent_NilResultIsPassthrough(t *testing.T) {
	if got := Present(nil, CategoryText, GradeGood, nil); got != nil {
		t.Errorf("Present(nil) = %v，期望 nil", got)
	}
}

func TestGradeForCount(t *testing.T) {
	if got := GradeForCount(0); got != GradeEmpty {
		t.Errorf("GradeForCount(0) = %q，期望 %q", got, GradeEmpty)
	}
	for _, n := range []int{1, 20, 1000} {
		if got := GradeForCount(n); got != GradeGood {
			t.Errorf("GradeForCount(%d) = %q，期望 %q", n, got, GradeGood)
		}
	}
}

// 闭域锁定：wesui 侧要按这些字面量分派，任何一个改名都是跨仓破坏。
// 这个测试的作用是让改名在 Go 侧先红，而不是在用户屏幕上变成裸 pre。
func TestPresentationDomainsAreStable(t *testing.T) {
	// 四值：`stream` 于 2026-09-20 删除（零生产方，见 presentation.go 的说明）。
	// 删除时这个测试先在**编译期**红了——闭域测试正是为此存在的。
	categories := map[PresentationCategory]string{
		CategoryText: "text", CategoryList: "list", CategoryGraph: "graph",
		CategoryActionable: "actionable",
	}
	for got, want := range categories {
		if string(got) != want {
			t.Errorf("category 字面量漂移: %q，期望 %q", got, want)
		}
	}
	if len(categories) != 4 {
		t.Errorf("类别闭域应为 4 值，实得 %d", len(categories))
	}

	grades := map[ResultGrade]string{
		GradeGood: "good", GradeBad: "bad", GradeEmpty: "empty", GradeNotReady: "not_ready",
	}
	for got, want := range grades {
		if string(got) != want {
			t.Errorf("grade 字面量漂移: %q，期望 %q", got, want)
		}
	}
	if len(grades) != 4 {
		t.Errorf("分级闭域应为 4 值，实得 %d", len(grades))
	}
}
