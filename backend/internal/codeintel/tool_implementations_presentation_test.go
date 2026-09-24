package codeintel

import (
	"encoding/json"
	"testing"

	"github.com/weisyn/wesgine/tool"
)

// PC-01/PC-02：find_implementations 的每个语义出口都必须产出正确的分级。
// "唯一附加点"只有覆盖每个出口才算证明——漏掉的那处不报错，只是安静地退回裸文本。

func envOf(t *testing.T, res *tool.ToolResult) (category, grade string, data json.RawMessage) {
	t.Helper()
	if res == nil {
		t.Fatal("结果为 nil")
	}
	if len(res.StructuredData) == 0 {
		t.Fatal("没有信封：这个出口漏了声明，工具会退回裸文本")
	}
	var env struct {
		Category string          `json:"category"`
		Grade    string          `json:"grade"`
		Data     json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(res.StructuredData, &env); err != nil {
		t.Fatalf("信封解不开：%v", err)
	}
	return env.Category, env.Grade, env.Data
}

func TestPresentImpls_IndexUnavailableIsNotReady(t *testing.T) {
	// 此前这个出口是 IsError=false 的纯文本，于是"索引还没建好"与"查到 0 个实现"
	// 在前端完全同形。后者会让用户删掉一个其实有实现方的接口。
	res := presentImpls(&tool.ToolResult{Content: "code index is not available"},
		implsOutcome{notReady: true})

	cat, grade, _ := envOf(t, res)
	if grade != string(GradeNotReady) {
		t.Errorf("grade = %q，要 not_ready", grade)
	}
	if cat != string(CategoryText) {
		t.Errorf("category = %q，无载荷时该是 text", cat)
	}
}

func TestPresentImpls_ErrorIsBad(t *testing.T) {
	res := presentImpls(&tool.ToolResult{Content: "query failed", IsError: true}, implsOutcome{})
	if _, grade, _ := envOf(t, res); grade != string(GradeBad) {
		t.Errorf("grade = %q，要 bad", grade)
	}
}

func TestPresentImpls_NotReadyOutranksError(t *testing.T) {
	// 两者同时为真时必须报 not_ready：用户需要先知道"答案不可信"，
	// 而"出错了"会让他去查自己的输入。
	res := presentImpls(&tool.ToolResult{IsError: true}, implsOutcome{notReady: true})
	if _, grade, _ := envOf(t, res); grade != string(GradeNotReady) {
		t.Errorf("grade = %q，not_ready 必须压过 bad", grade)
	}
}

func TestPresentImpls_NoImplementationsIsEmpty(t *testing.T) {
	res := presentImpls(&tool.ToolResult{Content: `No implementations found for interface "Foo"`},
		implsOutcome{})
	if _, grade, _ := envOf(t, res); grade != string(GradeEmpty) {
		t.Errorf("grade = %q，要 empty——把'没人实现'报成 good 会让用户以为有结果没显示", grade)
	}
}

func TestPresentImpls_ResultsAreAList(t *testing.T) {
	out := implsOutcome{hasList: true, list: ListItems(implListItems([]implEntry{
		{Name: "SQLiteStore", Signature: "type SQLiteStore struct", FilePath: "store/sqlite.go", LineStart: 41, Kind: "struct"},
		{Name: "MemStore", FilePath: "store/mem.go", LineStart: 9, Kind: "struct"},
	}), 0)}

	res := presentImpls(&tool.ToolResult{Content: "Implementations of \"Store\" (2 found):"}, out)
	cat, grade, data := envOf(t, res)
	if cat != string(CategoryList) {
		t.Errorf("category = %q，要 list", cat)
	}
	if grade != string(GradeGood) {
		t.Errorf("grade = %q，要 good", grade)
	}

	var list ListData
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("载荷解不开：%v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("项数 = %d，要 2", len(list.Items))
	}
	if list.Items[0].Label != "SQLiteStore" || list.Items[0].Detail != "type SQLiteStore struct" {
		t.Errorf("首项投影不对：%+v", list.Items[0])
	}
	// LineStart 是 0-based，wire 是 1-based。差一会让点击跳错一行，而跳错一行在
	// 长文件里看起来像"跳到了不相关的地方"，没人会怀疑是 off-by-one。
	if list.Items[0].Line != 42 {
		t.Errorf("line = %d，要 42（LineStart 41 是 0-based）", list.Items[0].Line)
	}
	if list.Items[1].Line != 10 {
		t.Errorf("line = %d，要 10", list.Items[1].Line)
	}
	if list.Truncated {
		t.Error("完整列表被误报成截断")
	}
}

// 本次修的真 bug：截断器静默切片，而 18 个调用点里 8 个在截断之后用 len() 报数。
// summary 档（上限 5）下 20 个实现输出成 "(5 found)"，模型和用户都读成"就这 5 个"，
// 然后改接口时漏掉 15 个实现方。
//
// 两侧都要断言：信封给人看，`Content` 给模型看，而模型正是那个会基于"只有 5 个实现"
// 去改接口的角色。只修一侧等于只修了一半用户。
func TestImplementations_TruncationIsReportedOnBothSides(t *testing.T) {
	entries := make([]implEntry, 20)
	for i := range entries {
		entries[i] = implEntry{Name: "Impl", FilePath: "a.go", LineStart: i}
	}
	shown, total := TruncateWithTotal(entries, VerbositySummary)
	if len(shown) != 5 || total != 20 {
		t.Fatalf("前提不成立：summary 档应截到 5/20，得到 %d/%d", len(shown), total)
	}

	t.Run("信封侧（给人看）", func(t *testing.T) {
		out := implsOutcome{hasList: true, list: ListItems(implListItems(shown), total)}
		_, _, data := envOf(t, presentImpls(&tool.ToolResult{Content: "x"}, out))

		var list ListData
		if err := json.Unmarshal(data, &list); err != nil {
			t.Fatal(err)
		}
		if !list.Truncated || list.Total != 20 {
			t.Errorf("截断没被报告：truncated=%v total=%d", list.Truncated, list.Total)
		}
	})

	t.Run("Content 侧（给模型看）", func(t *testing.T) {
		if got := CountPhrase(len(shown), total); got != "5 of 20" {
			t.Errorf("CountPhrase = %q，没说出截断", got)
		}
	})
}

func TestPresentImpls_NilResultIsPassthrough(t *testing.T) {
	if presentImpls(nil, implsOutcome{hasList: true}) != nil {
		t.Error("nil 结果应原样返回 nil")
	}
}
