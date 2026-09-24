package codeintel

import (
	"fmt"
	"strings"
	"testing"
)

// 第三批迁移（impact_analysis / check_constraints / find_hotspot /
// detect_circular_dependencies）。只测各自特有的投影判断。
//
// 行号基准在这批里出过一次错：我先给 find_hotspot 写了"Line 已归一，不能再 +1"的
// 注释，核实之后发现 fn.line 是直接 Scan 自 symbols.line_start（0-based），而文本
// 输出那行打的是 e.Line+1。注释写反了而代码照注释写，结果每个热点都会跳到上一行。
// 所以每个带位置的工具都要有一条行号断言——基准错不会报错，只会跳错一行。

func TestHotspot_LineIsZeroBasedAndNeedsPlusOne(t *testing.T) {
	items := hotspotListItems([]hotspotEntry{{
		Name:              "assembleContext",
		Kind:              "func",
		File:              "ctx.go",
		Line:              120, // 直接来自 symbols.line_start，0-based
		DirectCallees:     3,
		TransitiveCallees: 200,
	}})

	if items[0].Line != 121 {
		t.Errorf("line = %d，要 121（0-based 120 + 1，与文本输出的 e.Line+1 同源）", items[0].Line)
	}
}

// detail 带两个计数而不只是排序键：差值就是复杂度的来源。
// direct 3 / transitive 200 = "这函数自己简单，但拉起了一整棵树"；
// direct 40 / transitive 45 = "这函数本身臃肿"。两种处置完全不同。
func TestHotspot_DetailCarriesBothCounts(t *testing.T) {
	shallow := hotspotListItems([]hotspotEntry{{Name: "a", DirectCallees: 3, TransitiveCallees: 200}})
	fat := hotspotListItems([]hotspotEntry{{Name: "b", DirectCallees: 40, TransitiveCallees: 45}})

	if shallow[0].Detail == fat[0].Detail {
		t.Fatal("两种复杂度来源必须可区分")
	}
	for _, d := range []string{shallow[0].Detail, fat[0].Detail} {
		if !strings.Contains(d, "transitive") || !strings.Contains(d, "direct") {
			t.Errorf("detail 缺一个计数：%q", d)
		}
	}
}

func TestImpact_DepthLabelMatchesTextOutput(t *testing.T) {
	// 与文本输出用同一套词：两处用不同说法会让用户以为列表和正文讲的是两件事。
	for _, c := range []struct {
		depth int
		want  string
	}{
		{1, "direct"},
		{2, "indirect"},
		{3, "depth-3"},
		{7, "depth-7"},
	} {
		if got := impactDepthLabel(c.depth); got != c.want {
			t.Errorf("impactDepthLabel(%d) = %q，要 %q", c.depth, got, c.want)
		}
	}
}

// 深度进 kind 徽标（第一眼可见，是筛选依据），边类型进 detail。
// depth 1 与 depth 3 是不同的风险等级，边类型决定是不是编译期硬依赖——
// 只显示名字会让整张列表看起来同等重要，而用户要的恰恰是"先看哪几个"。
func TestImpact_DepthInKindEdgeInDetail(t *testing.T) {
	items := impactListItems([]ImpactNode{
		{Symbol: SymbolEntry{Name: "Handler", Kind: "func", FilePath: "h.go", LineStart: 9},
			Depth: 1, EdgeKind: "call"},
		{Symbol: SymbolEntry{Name: "Config", Kind: "struct", FilePath: "c.go", LineStart: 3},
			Depth: 3, EdgeKind: "import"},
	})

	if items[0].Kind != "direct" || items[1].Kind != "depth-3" {
		t.Errorf("深度要进 kind：%q / %q", items[0].Kind, items[1].Kind)
	}
	if !strings.Contains(items[0].Detail, "call") || !strings.Contains(items[1].Detail, "import") {
		t.Errorf("边类型要进 detail：%q / %q", items[0].Detail, items[1].Detail)
	}
	if items[0].Line != 10 || items[1].Line != 4 {
		t.Errorf("CKG LineStart 是 0-based 要 +1：%d / %d", items[0].Line, items[1].Line)
	}
}

// 环列表：一项 = 一个环，不是一个符号。把成员摊平成独立项会让"3 个环"显示成
// "11 个条目"，而用户要数的是环。
func TestCircularDeps_OneItemPerCycleNotPerSymbol(t *testing.T) {
	// 模拟 Call 里的投影（symMap 查表 + 成员串联）
	type sym struct{ name, file string }
	symMap := map[int]sym{1: {"A", "a.go"}, 2: {"B", "b.go"}, 3: {"C", "c.go"}}
	cycles := [][]int{{1, 2, 3}, {2, 3}}

	items := make([]ListItem, 0, len(cycles))
	for _, cycle := range cycles {
		entry := symMap[cycle[0]]
		names := make([]string, 0, len(cycle))
		for _, nid := range cycle {
			names = append(names, symMap[nid].name)
		}
		items = append(items, ListItem{
			Label:  entry.name,
			Detail: fmt.Sprintf("%d symbols · %s", len(cycle), strings.Join(names, " → ")),
			File:   entry.file,
			Kind:   "cycle",
		})
	}

	if len(items) != 2 {
		t.Fatalf("两个环应是 2 项，得到 %d——摊平成员会让用户数错环数", len(items))
	}
	// 环长必须在 detail 里：它是"这个环好不好拆"的第一判据
	if !strings.Contains(items[0].Detail, "3 symbols") {
		t.Errorf("detail 缺环长：%q", items[0].Detail)
	}
	// 参与者串联必须在：只给入口符号说不出这是个什么环
	if !strings.Contains(items[0].Detail, "A → B → C") {
		t.Errorf("detail 缺参与者链：%q", items[0].Detail)
	}
	// 环没有单一行号（它跨多个符号），所以不填 Line——编一个会指向入口而不是环
	if items[0].Line != 0 {
		t.Errorf("环不该有行号，得到 %d", items[0].Line)
	}
}
