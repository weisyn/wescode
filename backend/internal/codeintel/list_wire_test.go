package codeintel

import (
	"encoding/json"
	"testing"

	"github.com/weisyn/wesgine/tool"
)

// JSON 键是 Go 与 wesui 之间唯一的契约，而改错它不报错：wesui 的 asListData 会拒收，
// 列表安静地退回 JSON pretty-print。所以键必须被逐个钉住。
// 消费侧的对偶在 wesui `src/message/__tests__/listData.test.ts`（含驼峰化拒收）。

func TestListItem_WireKeys(t *testing.T) {
	b, err := json.Marshal(ListItem{
		Label:  "Handle",
		Detail: "func Handle()",
		File:   "pkg/h.go",
		Line:   10,
		Kind:   "func",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"label", "detail", "file", "line", "kind"} {
		if _, ok := got[k]; !ok {
			t.Errorf("ListItem 缺 wire 键 %q；wesui 读不到它，那一列会是空的：%s", k, b)
		}
	}
	if len(got) != 5 {
		t.Errorf("ListItem 多出未登记的键：%s", b)
	}
}

func TestListItem_OptionalFieldsOmitted(t *testing.T) {
	b, _ := json.Marshal(ListItem{Label: "some-package"})
	if string(b) != `{"label":"some-package"}` {
		t.Errorf("只有 label 时不该发空字段（wesui 靠 file 缺失判断这行不可点）：%s", b)
	}
}

func TestListData_WireKeys(t *testing.T) {
	b, _ := json.Marshal(ListData{
		Items:     []ListItem{{Label: "a"}},
		Total:     137,
		Truncated: true,
	})
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"items", "total", "truncated"} {
		if _, ok := got[k]; !ok {
			t.Errorf("ListData 缺 wire 键 %q：%s", k, b)
		}
	}
}

func TestListData_EmptyItemsIsArrayNotNull(t *testing.T) {
	// nil slice 会 marshal 成 null，而 wesui 的 asListData 要求 items 是数组——
	// null 会让整个载荷被拒收，然后"查了、没有结果"退化成一坨 JSON。
	b, _ := json.Marshal(ListData{Items: []ListItem{}})
	if string(b) != `{"items":[]}` {
		t.Errorf("空列表必须是 []，不是 null：%s", b)
	}
}

func TestListItems_ReportsTruncation(t *testing.T) {
	items := []ListItem{{Label: "a"}, {Label: "b"}}

	t.Run("total 超出已装入的项 → 报截断并带上总数", func(t *testing.T) {
		d := ListItems(items, 137)
		if !d.Truncated || d.Total != 137 {
			t.Errorf("截断没被报告：%+v", d)
		}
	})

	t.Run("total 等于长度 → 不报截断", func(t *testing.T) {
		d := ListItems(items, 2)
		if d.Truncated || d.Total != 0 {
			t.Errorf("完整列表被误报成截断：%+v", d)
		}
	})

	t.Run("total 传 0（调用方不知道总数）→ 不报截断", func(t *testing.T) {
		d := ListItems(items, 0)
		if d.Truncated {
			t.Errorf("total=0 不该被读成截断：%+v", d)
		}
	})

	t.Run("total 小于长度（调用方算错）→ 不报截断，不产出矛盾数字", func(t *testing.T) {
		// "显示 2 / 共 1" 会让用户以为 UI 坏了，比不报截断更糟。
		d := ListItems(items, 1)
		if d.Truncated || d.Total != 0 {
			t.Errorf("矛盾的 total 应被忽略：%+v", d)
		}
	})
}

// ListOf 的封顶保护三十多个工具，所以它自己必须被证明——尤其那个归一顺序：
// 归一在封顶之后会让 500 条封到 200 且报"没截断"，用户看到 200 行并相信那是全部。
func TestListOf_CapsAndReportsTruncation(t *testing.T) {
	mk := func(n int) []ListItem {
		out := make([]ListItem, n)
		for i := range out {
			out[i] = ListItem{Label: "x"}
		}
		return out
	}

	t.Run("超上限时封顶并报出真实总数", func(t *testing.T) {
		d := ListOf(mk(500), 0) // total=0 表示"调用方没截断过"
		if len(d.Items) != maxWireListItems {
			t.Errorf("条目数 = %d，要 %d", len(d.Items), maxWireListItems)
		}
		if !d.Truncated || d.Total != 500 {
			t.Errorf("截断没被报告：truncated=%v total=%d（要 true/500）——"+
				"归一晚于封顶就会得到 false/0，而那正是本轮修掉的那个 bug 类",
				d.Truncated, d.Total)
		}
	})

	t.Run("调用方已截断过时保留它给的总数", func(t *testing.T) {
		d := ListOf(mk(300), 9000)
		if len(d.Items) != maxWireListItems || d.Total != 9000 {
			t.Errorf("要 %d 条 / total 9000，得到 %d / %d", maxWireListItems, len(d.Items), d.Total)
		}
	})

	t.Run("未超上限时不制造截断标记", func(t *testing.T) {
		d := ListOf(mk(3), 0)
		if d.Truncated || d.Total != 0 {
			t.Errorf("完整列表被误报成截断：%+v", d)
		}
	})

	t.Run("恰好等于上限不算截断", func(t *testing.T) {
		d := ListOf(mk(maxWireListItems), 0)
		if d.Truncated {
			t.Error("恰好 200 条不该报截断")
		}
	})

	t.Run("空列表", func(t *testing.T) {
		d := ListOf([]ListItem{}, 0)
		if d.Items == nil || len(d.Items) != 0 || d.Truncated {
			t.Errorf("空列表应是非 nil 的零长切片且不报截断：%+v", d)
		}
	})
}

func TestGradeForList(t *testing.T) {
	if g := GradeForList(ListData{Items: []ListItem{}}); g != GradeEmpty {
		t.Errorf("空列表应为 empty，得到 %q——把'没有结果'报成 good 会让用户照'这里有结果'行动", g)
	}
	if g := GradeForList(ListData{Items: []ListItem{{Label: "a"}}}); g != GradeGood {
		t.Errorf("非空列表应为 good，得到 %q", g)
	}
}

// 端到端：Present 包出来的信封必须是 wesui 侧 asPresentationEnvelope + asListData
// 两道收窄都能通过的形状。这个测试的 dump 是 wesui 跨仓契约 fixture 的来源。
func TestPresent_ListEnvelopeShape(t *testing.T) {
	d := ListItems([]ListItem{
		{Label: "Handle", Detail: "func Handle()", File: "pkg/h.go", Line: 10, Kind: "func"},
		{Label: "Serve", File: "pkg/s.go", Line: 22},
	}, 137)

	res := Present(&tool.ToolResult{Content: "Found 137 reference(s)"}, CategoryList, GradeForList(d), d)
	if res == nil {
		t.Fatal("Present 返回 nil")
	}

	var env struct {
		Category string          `json:"category"`
		Grade    string          `json:"grade"`
		Data     json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(res.StructuredData, &env); err != nil {
		t.Fatalf("信封解不开：%v", err)
	}
	if env.Category != "list" || env.Grade != "good" {
		t.Errorf("category/grade 不对：%s / %s", env.Category, env.Grade)
	}

	var back ListData
	if err := json.Unmarshal(env.Data, &back); err != nil {
		t.Fatalf("载荷解不开：%v", err)
	}
	if len(back.Items) != 2 || back.Items[0].Label != "Handle" || !back.Truncated || back.Total != 137 {
		t.Errorf("载荷往返丢东西：%+v", back)
	}

	t.Logf("LIST_ENVELOPE:%s", res.StructuredData)
}
