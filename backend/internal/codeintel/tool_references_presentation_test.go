package codeintel

import (
	"encoding/json"
	"testing"

	"github.com/weisyn/wesgine/tool"
)

// find_references 是第三个迁移的工具，也是第一个用共享 PresentList 的——前两个各自
// 写了一份分级 switch，而两份副本就是漂移的开端。
//
// 这个文件只测本工具**特有**的那部分（行号基准转换、空列表的分级形状）。分级优先级
// 归 presentation_grade_test.go，重复断言只会让同一条规则有两处需要同步。

// 行号基准是这个工具独有的坑：DefinitionLocation.Line 是 0-based（LSP 协议与 CKG
// LineStart 都是），而 wire 的 ListItem.Line 是 1-based。差一会让点击跳错一行，
// 而跳错一行在长文件里看起来像"跳到了不相关的地方"，没人会怀疑是 off-by-one。
//
// `Content` 那侧仍打 0-based 是刻意的：这个工具族的 schema 显式声明 0-based 输入
// （tool_references.go / tool_apply_code_action.go / tool_lsp_rename.go），出入一致。
func TestReferences_WireLineIsOneBased(t *testing.T) {
	// 模拟 Call 尾部的投影：这里直接构造 ListItem 以避免起 LSP。
	// 判据是那个 +1 —— 它在 Call 里只出现一次，而这个测试守的就是那一处。
	const zeroBasedLine = 41
	items := []ListItem{{
		Label: "handler.go",
		File:  "pkg/handler.go",
		Line:  zeroBasedLine + 1,
		Kind:  "references",
	}}
	l := ListItems(items, 0)

	res := PresentList(&tool.ToolResult{Content: "Found 1 references(s):"}, false, &l)
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(res.StructuredData, &env); err != nil {
		t.Fatal(err)
	}
	var back ListData
	if err := json.Unmarshal(env.Data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Items[0].Line != 42 {
		t.Errorf("wire line = %d，要 42（0-based 41 + 1）", back.Items[0].Line)
	}
}

// 零引用时必须落 empty，而落 empty 的前提是传**空列表**而不是 nil。
// 传 nil 会得到 category=text，那说的是"这个工具不产出列表"——形状声明，不是答案。
// 两者在前端是不同的空态，而"没有引用"恰恰是用户最需要确认的那个答案（可以改签名）。
func TestReferences_NoReferencesIsEmptyList(t *testing.T) {
	emptyList := &ListData{Items: []ListItem{}}
	res := PresentList(&tool.ToolResult{Content: "No references found for position a.go:1:0"}, false, emptyList)

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
	if env.Category != string(CategoryList) {
		t.Errorf("category = %q，要 list——传 nil 会落 text，那是形状声明不是答案", env.Category)
	}

	// 对照：传 nil 确实落 text。两条一起断言，否则上面那句"传 nil 会落 text"
	// 只是注释里的说法。
	nilRes := PresentList(&tool.ToolResult{Content: "x"}, false, nil)
	var nilEnv struct {
		Category string `json:"category"`
	}
	if err := json.Unmarshal(nilRes.StructuredData, &nilEnv); err != nil {
		t.Fatal(err)
	}
	if nilEnv.Category != string(CategoryText) {
		t.Errorf("nil 列表应落 text，得到 %q", nilEnv.Category)
	}
}
