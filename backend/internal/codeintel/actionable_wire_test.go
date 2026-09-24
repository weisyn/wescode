package codeintel

import (
	"encoding/json"
	"testing"

	"github.com/weisyn/wesgine/tool"
)

// JSON 键是 Go 与 wesui 之间唯一的契约。消费侧对偶在
// wesui `src/message/__tests__/actionableData.test.ts`。

func TestActionRef_WireKeys(t *testing.T) {
	b, _ := json.Marshal(ActionRef{Label: "应用", Prompt: "Apply X at a.go:1:0", Style: "primary"})
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"label", "prompt", "style"} {
		if _, ok := got[k]; !ok {
			t.Errorf("ActionRef 缺 wire 键 %q：%s", k, b)
		}
	}
	if len(got) != 3 {
		t.Errorf("多出未登记的键：%s", b)
	}
}

func TestActionableItem_ActionsIsAlwaysArray(t *testing.T) {
	// nil actions marshal 成 null，而 wesui 的 asActionableData 要求它是数组 →
	// 整个载荷被拒收。于是**一条没有修复的诊断会让整张列表消失**，
	// 而那条诊断本身是完全合法的数据。
	d := ActionableOf([]ActionableItem{{Label: "undefined: Foo"}}, 0)
	b, _ := json.Marshal(d.Items[0])
	if !jsonHasArray(t, b, "actions") {
		t.Errorf("actions 必须是数组，得到：%s", b)
	}
}

func jsonHasArray(t *testing.T, b []byte, key string) bool {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	raw, ok := m[key]
	if !ok {
		return false
	}
	var arr []any
	return json.Unmarshal(raw, &arr) == nil
}

func TestActionableOf_CapsAndReportsTruncation(t *testing.T) {
	mk := func(n int) []ActionableItem {
		out := make([]ActionableItem, n)
		for i := range out {
			out[i] = ActionableItem{Label: "e", Actions: []ActionRef{}}
		}
		return out
	}

	d := ActionableOf(mk(500), 0)
	if len(d.Items) != maxWireListItems {
		t.Errorf("条目数 = %d，要 %d", len(d.Items), maxWireListItems)
	}
	if !d.Truncated || d.Total != 500 {
		t.Errorf("截断没被报告：truncated=%v total=%d——归一晚于封顶会得到 false/0",
			d.Truncated, d.Total)
	}

	if ok := ActionableOf(mk(3), 0); ok.Truncated {
		t.Error("完整列表被误报成截断")
	}
}

// grade 取**条目数**不是动作数：'有 3 个问题但都没有自动修复' 仍然是 good
// （有答案，只是要手工改）。数动作会把它报成 empty，于是用户以为没事。
func TestPresentActionable_GradeCountsItemsNotActions(t *testing.T) {
	noFixes := ActionableOf([]ActionableItem{
		{Label: "undefined: Foo"},
		{Label: "unused import"},
	}, 0)

	res := PresentActionable(&tool.ToolResult{Content: "2 errors"}, false, noFixes)
	var env struct {
		Category string `json:"category"`
		Grade    string `json:"grade"`
	}
	if err := json.Unmarshal(res.StructuredData, &env); err != nil {
		t.Fatal(err)
	}
	if env.Grade != string(GradeGood) {
		t.Errorf("grade = %q，要 good——有问题但没有自动修复仍然是有答案", env.Grade)
	}
	if env.Category != string(CategoryActionable) {
		t.Errorf("category = %q，要 actionable", env.Category)
	}
}

func TestPresentActionable_EmptyIsEmpty(t *testing.T) {
	res := PresentActionable(&tool.ToolResult{Content: "No errors found."}, false, ActionableOf(nil, 0))
	var env struct {
		Category string `json:"category"`
		Grade    string `json:"grade"`
	}
	if err := json.Unmarshal(res.StructuredData, &env); err != nil {
		t.Fatal(err)
	}
	if env.Grade != string(GradeEmpty) {
		t.Errorf("grade = %q，要 empty", env.Grade)
	}
	// 类别仍是 actionable：wesui 靠它决定画哪种空态，而"项目干净"与"这个工具
	// 不产出待办项"是两件事。
	if env.Category != string(CategoryActionable) {
		t.Errorf("category = %q，空列表仍是 actionable", env.Category)
	}
}

func TestPresentActionable_NilDataIsText(t *testing.T) {
	// 已应用动作的那条路径：动作发生过了，不是待办。
	res := PresentActionable(&tool.ToolResult{Content: "Applied 3 edits"}, false, nil)
	var env struct {
		Category string `json:"category"`
	}
	if err := json.Unmarshal(res.StructuredData, &env); err != nil {
		t.Fatal(err)
	}
	if env.Category != string(CategoryText) {
		t.Errorf("category = %q，无待办项时该是 text", env.Category)
	}
}

// 端到端：dump 出给 wesui 契约测试用的真实字节。
func TestPresentActionable_EnvelopeShape(t *testing.T) {
	d := ActionableOf([]ActionableItem{{
		Label:  "undefined: Foo",
		Detail: "gopls",
		File:   "pkg/h.go",
		Line:   42,
		Kind:   "new error",
		Actions: []ActionRef{{
			Label:  "应用",
			Prompt: `Apply code action "Add import" at pkg/h.go:42:8`,
			Style:  "primary",
		}},
	}}, 0)

	res := PresentActionable(&tool.ToolResult{Content: "1 error(s) to fix"}, false, d)
	t.Logf("ACTIONABLE_ENVELOPE:%s", res.StructuredData)

	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(res.StructuredData, &env); err != nil {
		t.Fatal(err)
	}
	var back ActionableData
	if err := json.Unmarshal(env.Data, &back); err != nil {
		t.Fatalf("载荷往返失败：%v", err)
	}
	if len(back.Items) != 1 || len(back.Items[0].Actions) != 1 {
		t.Fatalf("往返丢东西：%+v", back)
	}
	if back.Items[0].Actions[0].Prompt == "" {
		t.Error("prompt 丢了——空 prompt 的按钮点了什么都不会发生，而它看起来能用")
	}
}

// ActionableOf 不得改写调用方的切片。
//
// 它的名字读起来像构造器，而原实现把 nil→[] 的归一直接写进入参的底层数组。今天
// 没有调用方在之后复用那个切片，所以没有可观测后果——正因如此这条只能靠断言守，
// 靠不上任何现存测试变红。
func TestActionableOf_DoesNotMutateCaller(t *testing.T) {
	in := []ActionableItem{{Label: "a"}, {Label: "b", Actions: []ActionRef{{Label: "fix"}}}}
	_ = ActionableOf(in, 0)

	if in[0].Actions != nil {
		t.Error("ActionableOf 改写了调用方切片里的 Actions（nil 被归一成了空数组）")
	}
	if len(in[1].Actions) != 1 {
		t.Errorf("调用方原有的 Actions 被动了：%v", in[1].Actions)
	}
}
