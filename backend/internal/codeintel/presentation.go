package codeintel

// 呈现契约的产出侧（PC-01 ~ PC-04）。设计见 design/27-presentation-contract.md。
//
// 这个文件解决一件事：**让工具告诉前端「这个结果是什么形状、这次是好消息还是坏
// 消息」**。此前 51 个 Layer C 工具里 49 个落进 `GenericResult` 的裸 `<pre>`，不是
// 因为前端偷懒——前端拿到的是一个字符串，无从得知它是调用树还是排名列表，也无从
// 得知这次查到了还是没查到。
//
// 载体只有一个信封，走 `tool.ToolResult.StructuredData`：
//
//	{ "category": "graph", "grade": "good", "data": { … } }
//
// **不走 `ToolResult.Metadata`**：`engine.ToolResultData` 没有 Metadata 字段，它在
// 引擎内部被消费后就止步，永远到不了前端。用它等于「声明了、到不了、而产出点看
// 起来完全正确」——PC-03 就是为这个失败形状写的。

import (
	"encoding/json"
	"fmt"

	"github.com/weisyn/wesgine/tool"
)

// PresentationCategory 是工具结果的**形状**（PC-01）。闭域五值。
//
// 它回答的是「用户看完这个要做什么」，不是「这是什么数据类型」。按数据类型分类会
// 得出「这是 JSON 所以给个 JSON viewer」，而那正是 `StructuredOutputBlock` 被造出来
// 又没有生产方的原因。
type PresentationCategory string

const (
	// CategoryText 无结构文本。它是缺省值，但是**声明的**缺省而非渲染侧的推断——
	// 前者可以在产出点被测试发现，后者只能在用户屏幕上被发现。
	CategoryText PresentationCategory = "text"
	// CategoryList 若干条目，可排序、每条可点。49 个落裸 pre 的工具里最大的一类。
	CategoryList PresentationCategory = "list"
	// CategoryGraph 节点与边。`find_callers` / `impact_analysis` / `trace_data_flow`
	// 都是这一类，而 CallGraph.tsx 早就能画它们了——只是对话流里从没接线。
	CategoryGraph PresentationCategory = "graph"
	// CategoryActionable 每条对应一个可执行动作（`fix_diagnostics` 那一族）。
	CategoryActionable PresentationCategory = "actionable"
)

// `stream` 曾在这个闭域里，已删除（2026-09-20）。
//
// 它的注释举的例子是 `watch_logs` / `ci_status`，而这两个工具**一次性返回**：
// 51 个 Layer C 工具没有任何一个在 Call 期间增量推送，wescode 的流式发生在 agent 层
// （`chat/stream`），不在单个工具上。所以它零生产方，而给它做渲染器就是反模式 323
// （留着没有写入方的接线）——那个渲染器会让下一个人以为某处正在推流，然后花时间
// 找它在哪。
//
// 要把它加回来的条件是具体的：出现一个在 `Call` 返回之前就能发出增量事件的工具。
// 那需要引擎侧给工具一条推送通道（`tool.ToolContext` 现在没有），属迭代 B。
// 加回来时两侧闭域各加一行，代价远小于留一个空渲染器的认知负担。

// ResultGrade 是这次结果对用户的**含义**（PC-02）。闭域四值。
//
// 它存在的理由：`status` 是传输层的成败，不回答语义。`find_callers` 返回 0 个调用方、
// `check_constraints` 返回 FAIL、CKG 索引没建完——三者 `status` 都是 `done`，而含义
// 分别是「这函数没人用，可以删」「你违反了约束」「我还没准备好，答案不可信」。
type ResultGrade string

const (
	// GradeGood 查到了 / 通过了。
	GradeGood ResultGrade = "good"
	// GradeBad 答案可信，内容不妙。
	GradeBad ResultGrade = "bad"
	// GradeEmpty 正常执行，结果为空。**不是错误**——`find_callers` 返回 0 个可能
	// 正是用户想听的答案（这函数可以删）。
	GradeEmpty ResultGrade = "empty"
	// GradeNotReady 前置条件不满足，**答案不可信**。
	//
	// 必须单独成级、不得并入 GradeBad：它是唯一一个说「别相信这个答案」的级别，
	// 而 bad 说的是「答案可信，内容不妙」。混在一起会让用户把「我还没准备好」读成
	// 「你的代码有问题」，然后去查一个不存在的问题。
	GradeNotReady ResultGrade = "not_ready"
)

// presentationEnvelope 是下发给前端的信封。字段名 snake_case 与 wesui 的
// ContentPart 约定一致（`reduce.ts` 直接透传 data，不做键名转换）。
type presentationEnvelope struct {
	Category PresentationCategory `json:"category"`
	Grade    ResultGrade          `json:"grade"`
	// Data 是结构化载荷，形状由各工具定义。用 json.RawMessage 让这一层不解释它——
	// 形状归工具、形态归 wesui，中间层一旦重塑就成了第三份真相。
	Data json.RawMessage `json:"data,omitempty"`
}

// Present 把呈现声明附到工具结果上，**这是唯一的附加点**。
//
// 「唯一」是承重的，不是风格偏好。`find_callers` 的 Call 有 7 个 return，其中 4 个
// 语义各不同（LSP 成功 / 无调用方 / CKG 单定义 / CKG 分组），而分级每个出口都不一样。
// 在 4 处各写一次声明，就是 EE-14「判在三个调用方之一」的同一个形状——漏掉的那一处
// 不报错，只是安静地让一个工具退回裸 pre。所以每个工具的 Call 必须收敛到一个出口
// 调用本函数。
//
// res 为 nil 时原样返回 nil：调用方不必为了附加声明而先判空。
// data 为 nil 时信封仍然发送——类别与分级本身就是信息（`grade: empty` 恰恰是
// 「没有数据」这个答案，而不是「忘了填数据」）。
func Present(res *tool.ToolResult, category PresentationCategory, grade ResultGrade, data any) *tool.ToolResult {
	if res == nil {
		return nil
	}

	env := presentationEnvelope{Category: category, Grade: grade}
	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			// 结构化载荷序列化失败不该毁掉文本结果——模型仍然要读 Content。
			// 但也不能静默：降级为 text 并说明，否则前端会收到一个声称是 graph
			// 却没有数据的信封，而那比没有声明更难查。
			env.Category = CategoryText
			env.Data = nil
			res.Content += fmt.Sprintf("\n\n[presentation: structured payload dropped: %v]", err)
		} else {
			env.Data = raw
		}
	}

	raw, err := json.Marshal(env)
	if err != nil {
		// 信封本身序列化不了只可能是 category/grade 含非法字节，属编程错误。
		// 不附加即可，文本结果照常返回。
		return res
	}
	res.StructuredData = raw
	return res
}

// GradeFor 派生语义分级。四个出口的**优先级**在这一处定义，不在每个工具里重复。
//
// 优先级不是风格问题，每一对都有一个会让用户做错事的读法：
//   - not_ready 压过 bad：索引没建完不是"答案不好"，是"答案不可信"。报 bad 会让
//     用户去查自己的输入，而他该做的是等索引。
//   - bad 压过 empty：查询出错时说"没有结果"，用户会读成"这里确实没有"。
//   - empty 不是 bad：`hits == 0` 往往正是用户想听的答案（这函数没人调用，可以删）。
//
// hits 是**有效条目数**，由调用方给——它不一定等于载荷长度。多定义调用图里 3 个定义
// 各 0 调用方也有 3 个节点，数节点会把"谁都没调用"读成 good（PC-02 的原始缺陷）。
func GradeFor(res *tool.ToolResult, notReady bool, hits int) ResultGrade {
	switch {
	case notReady:
		return GradeNotReady
	case res != nil && res.IsError:
		return GradeBad
	case hits == 0:
		return GradeEmpty
	default:
		return GradeGood
	}
}

// PresentList 是列表类工具的唯一附加点。三十多个工具共用它，所以每个工具只需要做
// 两件事：在索引不可用的分支置 notReady，在成功分支构造 list。
//
// notReady 必须由分支主动说——索引没建完这件事结果本身看不出来（`IsError=false`、
// `Content` 是一句正常的话），而它与"查到 0 个结果"在前端完全同形。
//
// list 为 nil 时仍然发信封（`category: text`）：分级本身就是信息，而 not_ready
// 这句话恰恰是最需要被说出来的那一句（PC-02）。
func PresentList(res *tool.ToolResult, notReady bool, list *ListData) *tool.ToolResult {
	if res == nil {
		return nil
	}
	if list == nil {
		return Present(res, CategoryText, GradeFor(res, notReady, 0), nil)
	}
	return Present(res, CategoryList, GradeFor(res, notReady, len(list.Items)), *list)
}

// GradeForCount 由结果条数派生分级，覆盖「查了、可能查到也可能没查到」这一最常见
// 的形状。
//
// 它存在是为了让分级**尽量不靠人记得填**：查询类工具的分级几乎总是 count 的函数，
// 而手写 `if len(x) == 0 { empty } else { good }` 在 49 个工具里会写 49 遍，其中总有
// 一遍写反。不适用的工具（`check_constraints` 的 FAIL 是 bad 不是 good）自己传常量。
func GradeForCount(n int) ResultGrade {
	if n == 0 {
		return GradeEmpty
	}
	return GradeGood
}
