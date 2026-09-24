package codeintel

import (
	"encoding/json"
	"testing"

	"github.com/weisyn/wesgine/tool"
)

// 第二批迁移（search_symbols / find_similar / find_callees）。
//
// 分级优先级归 presentation_grade_test.go——这里只测各自特有的投影判断，因为那才是
// 每个工具会独立出错的地方。重复断言共享规则只会让同一条规则有多处需要同步。

func listPayload(t *testing.T, res *tool.ToolResult) ListData {
	t.Helper()
	if res == nil || len(res.StructuredData) == 0 {
		t.Fatal("没有信封")
	}
	var env struct {
		Category string          `json:"category"`
		Data     json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(res.StructuredData, &env); err != nil {
		t.Fatal(err)
	}
	if env.Category != string(CategoryList) {
		t.Fatalf("category = %q，要 list", env.Category)
	}
	var l ListData
	if err := json.Unmarshal(env.Data, &l); err != nil {
		t.Fatal(err)
	}
	return l
}

// ListOf 的 nil/空区分是三个工具共用的前提：PresentList 收指针正是为了让
// 「无列表」与「空列表」成为两个不同的空态，而这个区分在 UI 上是两种画法。
func TestListOf_DistinguishesEmptyFromAbsent(t *testing.T) {
	absent := PresentList(&tool.ToolResult{Content: "x"}, false, nil)
	present := PresentList(&tool.ToolResult{Content: "x"}, false, ListOf([]ListItem{}, 0))

	var a, b struct {
		Category string `json:"category"`
		Grade    string `json:"grade"`
	}
	_ = json.Unmarshal(absent.StructuredData, &a)
	_ = json.Unmarshal(present.StructuredData, &b)

	if a.Category != string(CategoryText) {
		t.Errorf("nil 列表应落 text（形状声明），得到 %q", a.Category)
	}
	if b.Category != string(CategoryList) || b.Grade != string(GradeEmpty) {
		t.Errorf("空列表应落 list+empty（是答案），得到 %q/%q", b.Category, b.Grade)
	}
}

// search_symbols 把符号命中与文件内容命中合进一个列表。对用户来说它们是同一个问题的
// 答案（"什么匹配我的查询"），分成两个列表会让他在两处各扫一遍。
func TestSearch_MergesSymbolAndFileHitsIntoOneList(t *testing.T) {
	items := []ListItem{
		{Label: "Handler", Detail: "func Handler()", File: "h.go", Line: 10, Kind: "func"},
		// 文件内容命中：有路径无行号
		{Label: "notes.md", Detail: "...matched text...", File: "docs/notes.md", Kind: "markdown"},
	}
	l := listPayload(t, PresentList(&tool.ToolResult{Content: "x"}, false, ListOf(items, 0)))

	if len(l.Items) != 2 {
		t.Fatalf("两类结果应在同一个列表里，得到 %d 项", len(l.Items))
	}
	// 文件内容命中不填 Line：FTS 只给文件粒度，填 0 或 1 会让点击跳到文件头并
	// 看起来像"命中在第一行"。
	if l.Items[1].Line != 0 {
		t.Errorf("文件命中不该有行号，得到 %d", l.Items[1].Line)
	}
	if l.Items[0].Line != 10 {
		t.Errorf("符号命中应带行号，得到 %d", l.Items[0].Line)
	}
}

// search_symbols 的总数是两类之和：只报符号那一半会让"文件里还有 40 处"消失。
func TestSearch_TotalSpansBothResultKinds(t *testing.T) {
	symbolTotal, fileTotal := 3, 40
	shown := []ListItem{{Label: "a"}, {Label: "b"}}
	l := listPayload(t, PresentList(&tool.ToolResult{Content: "x"},
		false, ListOf(shown, symbolTotal+fileTotal)))

	if l.Total != 43 || !l.Truncated {
		t.Errorf("总数应跨两类求和：total=%d truncated=%v（要 43/true）", l.Total, l.Truncated)
	}
}

// find_callees 只有名字没有位置——CalleesOf 与 LSP OutgoingCalls 都只返回被调用者的
// 名字字符串。不填 File 让整行不可点击，那是诚实的：编一个位置（猜同文件第 1 行）
// 会把用户送到错的地方，而他不会怀疑是工具编的。
func TestCallees_NoPositionMeansNoFakePosition(t *testing.T) {
	l := listPayload(t, PresentList(&tool.ToolResult{Content: "x"},
		false, calleeNameList([]string{"doWork", "validate"}, 0)))

	if len(l.Items) != 2 {
		t.Fatalf("项数 = %d，要 2", len(l.Items))
	}
	for _, it := range l.Items {
		if it.File != "" || it.Line != 0 {
			t.Errorf("callee 不该有位置，得到 %s:%d", it.File, it.Line)
		}
		if it.Label == "" {
			t.Error("label 必须有，否则 wesui 整体拒收该列表")
		}
	}
}

// find_callees 跨定义合并时，detail 必须带定义名：同名 callee 出现在两个定义下
// 就分不清是谁调的，而合并后列表里看不到分组边界。
func TestCallees_CrossDefinitionItemsCarryTheirDefinition(t *testing.T) {
	items := []ListItem{
		{Label: "validate", Detail: "called by Create"},
		{Label: "validate", Detail: "called by Update"},
	}
	l := listPayload(t, PresentList(&tool.ToolResult{Content: "x"}, false, ListOf(items, 0)))

	if l.Items[0].Detail == l.Items[1].Detail {
		t.Error("同名 callee 在不同定义下必须可区分")
	}
}

// find_similar 把相似度放 detail 不放 label：label 是"这是什么"（用户扫名字），
// 百分比是排序依据，读第二眼。
func TestSimilar_ScoreGoesToDetailNotLabel(t *testing.T) {
	items := []ListItem{{
		Label:  "SQLiteStore",
		Detail: "87% similar · type SQLiteStore struct",
		File:   "store/sqlite.go",
		Line:   42,
		Kind:   "struct",
	}}
	l := listPayload(t, PresentList(&tool.ToolResult{Content: "x"}, false, ListOf(items, 0)))

	if l.Items[0].Label != "SQLiteStore" {
		t.Errorf("label 应是名字，得到 %q", l.Items[0].Label)
	}
	if l.Items[0].Detail == "" {
		t.Error("相似度应在 detail 里")
	}
}
