package codeintel

// `category: list` 的载荷形状。权威定义在 wesui `src/message/listData.ts`——
// 渲染器服务三个产品，所以形状归它；这里是生产侧的 Go 对偶。
//
// 两侧靠 JSON 键对齐，而键写错不报错：wesui 侧 `asListData` 会拒收，然后列表安静地
// 退回 JSON pretty-print，没有任何一层会说话。所以键由
// `list_wire_test.go` 锁死，并由 wesui 的跨仓契约测试消费实际 dump 的字节。
//
// 字段与 `GraphNode` 刻意同构（扁平 File / Line）——同一族契约里两种命名会让生产侧
// 每次都要回头查用的是哪一套。

// ListItem 是列表里的一行。
type ListItem struct {
	// Label 是主文本。缺它这一行没有意义，wesui 侧会整体拒收该列表。
	Label string `json:"label"`
	// Detail 是同行压暗的次要文本：代码片段 / 签名 / 摘要。
	Detail string `json:"detail,omitempty"`
	// File 有值时整行可点击跳转。没有位置的列表只能看不能用。
	File string `json:"file,omitempty"`
	// Line 是 1-based。CKG 与 LSP 都是 0-based，转换在构造处做一次。
	Line int `json:"line,omitempty"`
	// Kind 是类型徽标（func / type / doc / test）。纯展示，无行为依赖它。
	Kind string `json:"kind,omitempty"`
}

// ListData 是 `category: list` 的完整载荷。
type ListData struct {
	Items []ListItem `json:"items"`
	// Total 是实际总数，与 len(Items) 不等即为截断。
	//
	// 与 Truncated 并存不是冗余：截断了但不知道总数时（上限 N 的检索）只能填
	// Truncated。被截断的列表读成完整的，代价是用户照"只有这些"行动。
	Total int `json:"total,omitempty"`
	// Truncated 在不知道 Total 时仍然要告诉用户"这不是全部"。
	Truncated bool `json:"truncated,omitempty"`
}

// ListItems 把切片包成载荷，并在被截断时如实报告。
//
// total <= len(items) 表示没有截断（传 0 即可）。这个 helper 存在的理由是截断信号
// 最容易被漏填——构造点通常正忙着切片，而漏填不报错，只让列表看起来是完整的。
func ListItems(items []ListItem, total int) ListData {
	d := ListData{Items: items}
	if total > len(items) {
		d.Total = total
		d.Truncated = true
	}
	return d
}

// maxWireListItems 是进信封的条目上限。
//
// 它不是传输限制（`client.MaxEventBytes` 是 32MB），是**可用性**限制：一个要靠滚动
// 读的列表超过两百行之后，用户的动作不再是"扫一遍"而是"放弃"，那时他需要的是过滤或
// 分组，不是更多行。上限在这里而不在各工具里，理由与 INV-EXEC-PROC-01 同：三十多个
// 工具各自"记得"截断，等于其中几个不会记得。
const maxWireListItems = 200

// ListOf 把条目包成载荷指针，并在超过 maxWireListItems 时如实报告截断。
//
// `PresentList` 收指针是为了区分「无列表」（nil → category text）与「空列表」
// （非 nil 且 len 0 → category list + grade empty），那是两个不同的空态。
//
// total 是**调用方已知的**截断前总数；传 0 表示"没截断过"。归一必须发生在封顶
// **之前**：否则 total=0 + 500 条封到 200 会让 `ListItems` 以为没截断（0 > 200 为假），
// 于是用户看到 200 行并相信那是全部——正是本轮修掉的那个 bug 类，只是换到了这里。
func ListOf(items []ListItem, total int) *ListData {
	if total < len(items) {
		total = len(items)
	}
	if len(items) > maxWireListItems {
		items = items[:maxWireListItems]
	}
	l := ListItems(items, total)
	return &l
}

// GradeForList 按列表长度派生语义分级。空列表是 `empty`，不是 `good`——
// "查了、没有结果"与"这里有结果"是用户要分辨的两件事。
func GradeForList(d ListData) ResultGrade {
	return GradeForCount(len(d.Items))
}
