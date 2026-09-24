package codeintel

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/weisyn/wesgine/knowledge"
)

// locateSymbols 是这轮唯一有真实解析逻辑的新代码，也是 find_doc_references 从
// "答不出在哪一行"变成"答得出在哪一节"的那一步。
//
// 缺口原貌：wesgine 的 knowledge.EntityInfo 只有 {Type, Value, Count}——没有位置也
// 没有上下文，所以 doc_references.context 每次写空串（不是有人忘了填，是无可填），
// 而 doc_line 这一列根本不存在。于是这个工具只能答"AGENTS.md 提到了 Cell"，而
// 没有行号的 file 点进去是 3000 行设计文档的第 1 行。

func newLocator(t *testing.T) *DocRefIndex {
	t.Helper()
	return &DocRefIndex{logger: slog.New(slog.DiscardHandler)}
}

func writeDoc(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "design.md")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func refs(names ...string) []knowledge.EntityInfo {
	out := make([]knowledge.EntityInfo, 0, len(names))
	for _, n := range names {
		out = append(out, knowledge.EntityInfo{Type: "code_ref", Value: n})
	}
	return out
}

func TestLocateSymbols_ReportsLineAndSection(t *testing.T) {
	doc := writeDoc(t, `# 架构

## 资源隔离

每个 Cell 独立持有自己的存储。

## 性能优化

所有 Cell 共享 ProviderPool。
`)
	got := newLocator(t).locateSymbols(doc, refs("Cell", "ProviderPool"))

	// section 取**章节标题**而不是那一行本身：这个工具的用途是"改代码前理解设计意图"，
	// 而意图写在章节里。一行"所有 Cell 共享 ProviderPool"拿出来看不出它属于
	// 「资源隔离」还是「性能优化」，而那两节对同一行代码给出相反的建议。
	if got["Cell"].section != "资源隔离" {
		t.Errorf("Cell 的章节 = %q，要「资源隔离」（首次出现所在的节）", got["Cell"].section)
	}
	if got["Cell"].line != 5 {
		t.Errorf("Cell 的行 = %d，要 5（1-based）", got["Cell"].line)
	}
	if got["ProviderPool"].section != "性能优化" {
		t.Errorf("ProviderPool 的章节 = %q，要「性能优化」", got["ProviderPool"].section)
	}
	if got["ProviderPool"].line != 9 {
		t.Errorf("ProviderPool 的行 = %d，要 9", got["ProviderPool"].line)
	}
}

// 代码围栏必须跟：设计文档里满是 `# 注释` 与 `#!/bin/sh`，把它们当标题会让章节名
// 变成一句 shell 注释，而 context 的全部价值就在于它是章节名。
func TestLocateSymbols_FencedCommentsAreNotHeadings(t *testing.T) {
	doc := writeDoc(t, "# 部署\n\n"+
		"```bash\n"+
		"# 这不是标题，是 shell 注释\n"+
		"#!/bin/sh\n"+
		"wesgine serve\n"+
		"```\n\n"+
		"Hypervisor 在这里启动。\n")

	got := newLocator(t).locateSymbols(doc, refs("Hypervisor"))
	if got["Hypervisor"].section != "部署" {
		t.Errorf("章节 = %q，要「部署」——围栏内的 # 被当成标题了", got["Hypervisor"].section)
	}
}

// 围栏内的符号照样算引用：代码示例正是文档在讲这个符号的证据。
func TestLocateSymbols_MatchesInsideFence(t *testing.T) {
	doc := writeDoc(t, "# 用法\n\n```go\ncell.Runtime().Run(ctx, req)\n```\n")
	got := newLocator(t).locateSymbols(doc, refs("Runtime"))
	if got["Runtime"].line == 0 {
		t.Error("围栏内的符号也该定位到——代码示例是引用的证据")
	}
	if got["Runtime"].section != "用法" {
		t.Errorf("章节 = %q，要「用法」", got["Runtime"].section)
	}
}

func TestLocateSymbols_OnlyFirstOccurrence(t *testing.T) {
	// 一个符号出现十次时，用户要的是"这篇文档在哪讲它"，而最靠前的那处通常是
	// 定义性的那一段。
	doc := writeDoc(t, "# 一\n\nCell 首次出现。\n\n# 二\n\nCell 又出现。\n")
	got := newLocator(t).locateSymbols(doc, refs("Cell"))
	if got["Cell"].line != 3 || got["Cell"].section != "一" {
		t.Errorf("要首次出现（第 3 行 / 「一」），得到第 %d 行 / %q",
			got["Cell"].line, got["Cell"].section)
	}
}

func TestLocateSymbols_NoHeadingYieldsEmptySection(t *testing.T) {
	// 符号出现在首个标题之前：section 为空是诚实的，而工具侧会退回文件名做 label
	// （不能留空 label，wesui 会整体拒收该列表）。
	doc := writeDoc(t, "开篇就提到 Cell。\n\n# 后面才有标题\n")
	got := newLocator(t).locateSymbols(doc, refs("Cell"))
	if got["Cell"].line != 1 {
		t.Errorf("行 = %d，要 1", got["Cell"].line)
	}
	if got["Cell"].section != "" {
		t.Errorf("章节 = %q，标题之前该是空", got["Cell"].section)
	}
}

func TestLocateSymbols_MissingFileDegradesQuietly(t *testing.T) {
	// 读不到文档时退回无位置，与此前行为一致——这个工具的降级形态就是它此前的
	// 全部形态，所以退化回去不比原来差。
	got := newLocator(t).locateSymbols(filepath.Join(t.TempDir(), "gone.md"), refs("Cell"))
	if len(got) != 0 {
		t.Errorf("读不到文档应返回空表，得到 %+v", got)
	}
}

func TestLocateSymbols_UnfoundSymbolHasNoEntry(t *testing.T) {
	doc := writeDoc(t, "# 一\n\n这里只讲 Cell。\n")
	got := newLocator(t).locateSymbols(doc, refs("Cell", "Hypervisor"))
	if _, ok := got["Hypervisor"]; ok {
		t.Error("没出现的符号不该有条目——零值 docLoc 会让 doc_line 落 0，那是" +
			"「没定位到」的正确表示，但不该混进" + "已定位" + "的表里")
	}
	if got["Cell"].line == 0 {
		t.Error("出现过的符号必须定位到")
	}
}

// 匹配用 Contains 而非整词是刻意的：文档里的符号常带反引号、括号、包名前缀
// （`cell.Runtime()`、`(*Hypervisor).Start`），要求整词会漏掉大多数真实写法。
func TestLocateSymbols_MatchesQualifiedAndQuotedForms(t *testing.T) {
	doc := writeDoc(t, "# 一\n\n调用 `cell.Runtime()` 之前先看 (*Hypervisor).Start。\n")
	got := newLocator(t).locateSymbols(doc, refs("Runtime", "Hypervisor"))
	if got["Runtime"].line == 0 {
		t.Error("`cell.Runtime()` 这种写法必须能命中 Runtime")
	}
	if got["Hypervisor"].line == 0 {
		t.Error("(*Hypervisor).Start 这种写法必须能命中 Hypervisor")
	}
}
