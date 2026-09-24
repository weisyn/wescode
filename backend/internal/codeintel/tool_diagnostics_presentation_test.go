package codeintel

import (
	"encoding/json"
	"testing"

	"github.com/weisyn/wesgine/tool"
)

func okResult(content string) *tool.ToolResult { return &tool.ToolResult{Content: content} }

// get_diagnostics 是 PC-02 代价最高的一例：它有三条"零结果"分支，其中一条**不是**
// 零结果，而是"还不知道"。
//
//	No diagnostics for <path>                          → empty（这个文件干净）
//	No IDE diagnostics available (IDE may not have…)    → not_ready（IDE 还没上报过）
//	No errors or warnings in the project (last updated…) → empty（项目干净）
//
// 三句话措辞早就不同，但都是 IsError=false 的纯文本——前端分不出来，于是"还不知道"
// 被读成"干净"，而用户据此认为可以提交。

func TestDiagnostics_SeverityLabels(t *testing.T) {
	// 徽标决定用户先处理哪一条。未知值落 "diagnostic" 而不是数字：数字对用户没有
	// 意义，而猜一个级别会让他按错误的优先级处理。
	for _, c := range []struct {
		sev  int
		want string
	}{
		{1, "error"},
		{2, "warning"},
		{4, "info"},
		{8, "hint"},
		{0, "diagnostic"},
		{99, "diagnostic"},
	} {
		if got := severityLabel(c.sev); got != c.want {
			t.Errorf("severityLabel(%d) = %q，要 %q", c.sev, got, c.want)
		}
	}
}

// IDEDiagnostic.Line 来自 VS Code marker 的 startLineNumber，已是 1-based
// （`wescode.contribution.ts` 的 reportDiagnostics 原样传，没有 -1）。
// 所以投影里**不能** +1——同一个包里 CKG 的 LineStart 是 0-based 要 +1，
// 两个来源基准相反，而多加一次不会报错，只会让每条诊断都跳到下一行。
func TestDiagnostics_LineIsAlreadyOneBased(t *testing.T) {
	items := diagnosticListItems([]IDEDiagnostic{{
		Path:     "pkg/h.go",
		Line:     42,
		Severity: 1,
		Message:  "undefined: Foo",
		Source:   "gopls",
		Code:     "UndeclaredName",
	}})

	if len(items) != 1 {
		t.Fatalf("项数 = %d", len(items))
	}
	if items[0].Line != 42 {
		t.Errorf("line = %d，要 42（marker 已是 1-based，不能再 +1）", items[0].Line)
	}
	// label 用消息而不是文件名：诊断列表里用户扫的是"哪里错了"，文件名在位置列已有。
	if items[0].Label != "undefined: Foo" {
		t.Errorf("label = %q，要消息", items[0].Label)
	}
	if items[0].Kind != "error" {
		t.Errorf("kind = %q，严重级要进徽标", items[0].Kind)
	}
	if items[0].Detail != "gopls UndeclaredName" {
		t.Errorf("detail = %q，要 source + code", items[0].Detail)
	}
}

func TestDiagnostics_SourceOnlyWhenNoCode(t *testing.T) {
	items := diagnosticListItems([]IDEDiagnostic{{Message: "m", Source: "eslint"}})
	if items[0].Detail != "eslint" {
		t.Errorf("detail = %q，无 code 时只要 source（不要留个尾随空格）", items[0].Detail)
	}
}

// orphans 的 detail 必须带确定性与理由：这个工具的输出直接导向删代码，而
// name_reachable（0.3）与 isolated（1.0）是完全不同的建议——前者"可能通过反射被调用"，
// 后者"图上确实没人指向它"。只显示名字会让两者同形，而用户逐个删时不会回去读正文。
func TestOrphans_DetailCarriesCertaintyAndReason(t *testing.T) {
	items := orphanListItems([]OrphanResult{
		{Symbol: SymbolEntry{Name: "unusedHelper", Kind: "func", FilePath: "a.go", LineStart: 9},
			Certainty: 1.0, Reason: "no_incoming"},
		{Symbol: SymbolEntry{Name: "maybeUsed", Kind: "func", FilePath: "b.go", LineStart: 20},
			Certainty: 0.3, Reason: "name_reachable"},
	})

	if len(items) != 2 {
		t.Fatalf("项数 = %d", len(items))
	}
	if items[0].Detail == items[1].Detail {
		t.Error("不同确定性的孤儿必须可区分——同形会让用户把 30% 的当 100% 的删")
	}
	if items[0].Line != 10 {
		t.Errorf("line = %d，要 10（CKG LineStart 9 是 0-based）", items[0].Line)
	}
	for _, it := range items {
		if it.Label == "" {
			t.Error("label 必须有，否则 wesui 整体拒收该列表")
		}
	}
}

// 空列表与 nil 的区分在这两个工具上都承重，但理由不同：
//   - diagnostics：空列表 = "这个文件/项目干净"，是用户要的答案
//   - orphans：空列表 = "没有死代码可删"，同样是答案
//
// 落 nil（text）会让两者都变成"这个工具没产出列表"——形状声明，不是答案。
func TestDiagnosticsAndOrphans_CleanMeansEmptyListNotAbsent(t *testing.T) {
	clean := PresentList(nil, false, &ListData{Items: []ListItem{}})
	if clean != nil {
		t.Fatal("前提：res 为 nil 时 PresentList 应 passthrough")
	}

	// 真实形状：有结果对象、空列表
	for _, name := range []string{"diagnostics", "orphans"} {
		t.Run(name, func(t *testing.T) {
			res := PresentList(okResult("clean"), false, &ListData{Items: []ListItem{}})
			var env struct {
				Category string `json:"category"`
				Grade    string `json:"grade"`
			}
			if err := json.Unmarshal(res.StructuredData, &env); err != nil {
				t.Fatal(err)
			}
			if env.Category != string(CategoryList) || env.Grade != string(GradeEmpty) {
				t.Errorf("要 list+empty，得到 %q/%q", env.Category, env.Grade)
			}
		})
	}
}
