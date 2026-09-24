package codeintel

import (
	"encoding/json"
	"testing"

	"github.com/weisyn/wesgine/tool"
)

// GradeFor 是全部工具共用的分级单点。优先级不是风格问题——每一对都有一个会让用户
// 做错事的读法，所以四条各自断言，而且要**成对**断言（同时为真时谁赢）。
//
// 此前这个 switch 有两份近乎相同的副本（presentCallers / presentImpls），而两份
// 副本就是漂移的开端：修一处不同步另一处，症状是同一种语义在两个工具上分级不同。

func TestGradeFor_Priority(t *testing.T) {
	errRes := &tool.ToolResult{IsError: true}
	okRes := &tool.ToolResult{}

	for _, c := range []struct {
		name     string
		res      *tool.ToolResult
		notReady bool
		hits     int
		want     ResultGrade
		why      string
	}{
		{"索引未就绪", okRes, true, 0, GradeNotReady, ""},
		{"出错", errRes, false, 0, GradeBad, ""},
		{"零结果", okRes, false, 0, GradeEmpty, ""},
		{"有结果", okRes, false, 3, GradeGood, ""},

		{
			"not_ready 压过 bad", errRes, true, 0, GradeNotReady,
			"报 bad 会让用户去查自己的输入，而他该做的是等索引建完",
		},
		{
			"not_ready 压过有结果", okRes, true, 5, GradeNotReady,
			"索引没建完时拿到的 5 条不代表只有 5 条——答案不可信，不是答案不好",
		},
		{
			"bad 压过零结果", errRes, false, 0, GradeBad,
			"查询出错时说'没有结果'，用户会读成'这里确实没有'",
		},
		{
			"出错但有部分结果仍是 bad", errRes, false, 3, GradeBad,
			"部分结果配 good 会让用户以为查询完成了",
		},

		{"res 为 nil 时不 panic，按非错误处理", nil, false, 2, GradeGood, ""},
		{"res 为 nil 且零结果", nil, false, 0, GradeEmpty, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := GradeFor(c.res, c.notReady, c.hits)
			if got != c.want {
				msg := ""
				if c.why != "" {
					msg = "——" + c.why
				}
				t.Errorf("GradeFor = %q，要 %q%s", got, c.want, msg)
			}
		})
	}
}

func TestPresentList_AttachesEnvelope(t *testing.T) {
	list := ListItems([]ListItem{
		{Label: "Foo", File: "a.go", Line: 10},
		{Label: "Bar", File: "b.go", Line: 3},
	}, 0)

	res := PresentList(&tool.ToolResult{Content: "found 2"}, false, &list)
	if res == nil || len(res.StructuredData) == 0 {
		t.Fatal("没有信封")
	}

	var env struct {
		Category string          `json:"category"`
		Grade    string          `json:"grade"`
		Data     json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(res.StructuredData, &env); err != nil {
		t.Fatal(err)
	}
	if env.Category != string(CategoryList) {
		t.Errorf("category = %q，要 list", env.Category)
	}
	if env.Grade != string(GradeGood) {
		t.Errorf("grade = %q，要 good", env.Grade)
	}

	var back ListData
	if err := json.Unmarshal(env.Data, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Items) != 2 || back.Items[0].Label != "Foo" {
		t.Errorf("载荷往返丢东西：%+v", back)
	}
}

func TestPresentList_NilListStillSendsEnvelope(t *testing.T) {
	// 无载荷仍然发信封：分级本身就是信息，而 not_ready 这句话恰恰是最需要被说出来
	// 的那一句——它与"查到 0 个"在前端完全同形，一个是"答案不可信"，另一个是
	// "这里确实没有"。
	res := PresentList(&tool.ToolResult{Content: "code index is not available"}, true, nil)
	if res == nil || len(res.StructuredData) == 0 {
		t.Fatal("无载荷时也必须发信封，否则 not_ready 说不出去")
	}

	var env struct {
		Category string `json:"category"`
		Grade    string `json:"grade"`
	}
	if err := json.Unmarshal(res.StructuredData, &env); err != nil {
		t.Fatal(err)
	}
	if env.Category != string(CategoryText) {
		t.Errorf("category = %q，无载荷时该是 text", env.Category)
	}
	if env.Grade != string(GradeNotReady) {
		t.Errorf("grade = %q，要 not_ready", env.Grade)
	}
}

func TestPresentList_EmptyListIsEmptyNotGood(t *testing.T) {
	empty := ListItems([]ListItem{}, 0)
	res := PresentList(&tool.ToolResult{Content: "no results"}, false, &empty)

	var env struct {
		Category string `json:"category"`
		Grade    string `json:"grade"`
	}
	if err := json.Unmarshal(res.StructuredData, &env); err != nil {
		t.Fatal(err)
	}
	if env.Grade != string(GradeEmpty) {
		t.Errorf("grade = %q，空列表要 empty——报 good 会让用户以为有结果没显示出来", env.Grade)
	}
	// 类别仍是 list：空列表是"查了、没有结果"，与"这个工具不产出列表"是两件事，
	// 而 wesui 靠 category 决定画哪种空态。
	if env.Category != string(CategoryList) {
		t.Errorf("category = %q，空列表仍是 list", env.Category)
	}
}

func TestPresentList_NilResultIsPassthrough(t *testing.T) {
	list := ListItems([]ListItem{{Label: "x"}}, 0)
	if PresentList(nil, false, &list) != nil {
		t.Error("nil 结果应原样返回 nil")
	}
}
