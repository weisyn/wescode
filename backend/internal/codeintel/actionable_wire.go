package codeintel

import "github.com/weisyn/wesgine/tool"

// `category: actionable` 的载荷形状。权威定义在 wesui `src/message/actionableData.ts`。
//
// ── 为什么动作是"预填提示"而不是"直接执行" ──
//
// 点「应用此修复」若直接调 RPC 写文件，就绕过了三样东西：`EditEngine` 的检查点
// （可逆性——用户点完发现坏了却回不去）、治理链（DenyPaths / NetworkPolicy）、审计。
// 那不是少了点功能，是把写入挪到了单一 seam 之外（EE-13）。
//
// 所以动作携带的是**一句给 agent 的指令**：点击把它提交进现有对话通道，agent 调工具，
// 写入照常走检查点与治理。代价是多一次模型往返；换来的是「这次修改和别的修改一样
// 可以撤销」。这个代价必须付——不可逆的"一键修复"对中低端用户是净负值。
//
// 因此这里没有 RPC / endpoint / args 字段。要加的人先回答上面那段。

// ActionRef 是一个用户可执行的动作。
type ActionRef struct {
	// Label 是按钮文字，用户看到的唯一说明，所以要说清"点了会发生什么"。
	Label string `json:"label"`
	// Prompt 是提交给 agent 的指令。要具体到不需要模型再猜——
	// `Apply code action "Add import" at pkg/h.go:10`，不是"修一下这个"。
	// 空值会被 wesui 整体拒收：空 prompt 的按钮点了什么都不会发生，
	// 而它看起来与能用的按钮一模一样，用户会反复点。
	Prompt string `json:"prompt"`
	// Style 只给"最可能要点的那一个"填 primary；多个 primary 等于没有 primary。
	Style string `json:"style,omitempty"`
}

// ActionableItem 是一条待处理项。
type ActionableItem struct {
	// Label 是问题本身（"undefined: Foo"），不是文件名。
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	File   string `json:"file,omitempty"`
	// Line 是 1-based，与 ListItem 同约定。
	Line int    `json:"line,omitempty"`
	Kind string `json:"kind,omitempty"`
	// Actions 可以是空数组——"这条没有可用的修复"本身是答案，而 nil 会被 wesui
	// 拒收（它要求 actions 是数组）。构造处一律用 ActionableOf 保证非 nil。
	Actions []ActionRef `json:"actions"`
}

// ActionableData 是 `category: actionable` 的完整载荷。
type ActionableData struct {
	Items     []ActionableItem `json:"items"`
	Total     int              `json:"total,omitempty"`
	Truncated bool             `json:"truncated,omitempty"`
}

// ActionableOf 把条目包成载荷指针，封顶并归一 nil actions。
//
// 归一 Actions 是必要的：nil 切片 marshal 成 `null`，而 wesui 的 `asActionableData`
// 要求它是数组，于是整个载荷被拒收、退回 JSON pretty-print。一条没有修复的诊断
// 因此会让**整张列表**消失，而那条诊断本身是完全合法的数据。
func ActionableOf(items []ActionableItem, total int) *ActionableData {
	if total < len(items) {
		total = len(items)
	}
	if len(items) > maxWireListItems {
		items = items[:maxWireListItems]
	}
	// 拷一份再归一，不原地改 `items`。名字是 `...Of` 的函数读起来像构造器，
	// 而原实现会写进调用方的底层数组——今天每个调用方都是"现造一个传进来、
	// 之后不再用"，所以没有可观测后果；但下一个想在调用 ActionableOf 之后再拿
	// 同一个切片渲染 Content 的人，会拿到被改过的数据且毫无提示。
	out := make([]ActionableItem, len(items))
	copy(out, items)
	for i := range out {
		if out[i].Actions == nil {
			out[i].Actions = []ActionRef{}
		}
	}
	items = out
	d := &ActionableData{Items: items}
	if total > len(items) {
		d.Total = total
		d.Truncated = true
	}
	return d
}

// PresentActionable 是 actionable 类工具的唯一附加点。
//
// hits 取**条目数**而不是动作数：`grade=empty` 说的是"没有待处理项"（干净），
// 而"有 3 个问题但都没有自动修复"仍然是 good——有答案，只是需要手工改。
// 数动作会把后者报成 empty，于是用户以为没事。
func PresentActionable(res *tool.ToolResult, notReady bool, data *ActionableData) *tool.ToolResult {
	if res == nil {
		return nil
	}
	if data == nil {
		return Present(res, CategoryText, GradeFor(res, notReady, 0), nil)
	}
	return Present(res, CategoryActionable, GradeFor(res, notReady, len(data.Items)), *data)
}
