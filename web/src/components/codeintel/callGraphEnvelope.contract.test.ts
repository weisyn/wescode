import { describe, expect, it } from 'vitest'
import { asPresentationEnvelope } from '@wesui/message'
import { asCallGraphData } from './CallGraphPart'

/**
 * 跨仓契约测试：Go 侧真实产出必须通过 TS 侧的两道收窄。
 *
 * 下面这个 fixture 不是手写的，是从 Go 侧**实际打印出来**的字节：
 *
 *   presentCallers(&tool.ToolResult{…}, callersOutcome{graph: callersGraph(
 *       "Foo", "foo.go", 41, []callerRef{{…Handle…}, {…TestHandle…}})})
 *
 * 手写 fixture 会让这个测试变成"我以为 Go 会发什么"的自证——而这条链路上唯一真正
 * 危险的失败正是"两侧以为的形状不一样"：JSON 键是唯一契约，改错它不报错，只会让
 * `asPresentationEnvelope` 或 `asCallGraphData` 返回 null，然后图安静地退回 JSON
 * pretty-print。没有任何一层会说话。
 *
 * 产出侧的对应测试在 wescode `internal/codeintel/callgraph_wire_test.go`（锁 JSON 键）
 * 与 `tool_callgraph_presentation_test.go`（锁四个语义出口）。
 *
 * fixture 过期的处置：重新从 Go 侧 dump 一份，**不要**手改这里的键名去迁就 TS——
 * 那等于把契约违约改写成契约。
 */
const GO_ENVELOPE = JSON.parse(
  `{"category":"graph","grade":"good","data":{"nodes":[{"id":"focus:Foo","name":"Foo","kind":"symbol","file":"foo.go","line":42,"is_focus":true,"is_test":false},{"id":"h.go:9:Handle","name":"Handle","kind":"func","file":"h.go","line":10,"signature":"func Handle()","is_focus":false,"is_test":false},{"id":"h_test.go:3:TestHandle","name":"TestHandle","kind":"func","file":"h_test.go","line":4,"is_focus":false,"is_test":true}],"edges":[{"source":"h.go:9:Handle","target":"focus:Foo","kind":"call","resolved":true},{"source":"h_test.go:3:TestHandle","target":"focus:Foo","kind":"call","resolved":true}],"impact_summary":{"direct_callers":2,"indirect_dependents":0,"affected_files":0,"affected_tests":0},"truncated":false}}`,
) as unknown

describe('Go 信封 → TS 收窄（跨仓契约）', () => {
  it('信封被识别，category/grade 落在闭域里', () => {
    const env = asPresentationEnvelope(GO_ENVELOPE)
    expect(env, 'Go 发的信封被 TS 拒收——category/grade 字面量漂移了').not.toBeNull()
    expect(env!.category).toBe('graph')
    expect(env!.grade).toBe('good')
  })

  it('载荷被识别为调用图', () => {
    const env = asPresentationEnvelope(GO_ENVELOPE)!
    const graph = asCallGraphData(env.data)
    expect(graph, 'Go 发的图被 TS 拒收——nodes/edges 键漂移了').not.toBeNull()
  })

  it('渲染器真正读的那几个键都在（snake_case 未被重塑）', () => {
    const graph = asCallGraphData(asPresentationEnvelope(GO_ENVELOPE)!.data)!

    // is_focus 决定哪个节点是焦点。写成 isFocus 不会报错，只是每个节点都不是焦点，
    // 而图照样渲染——这是这条链路上最安静的一种失败。
    const focus = graph.nodes.filter(n => n.is_focus)
    expect(focus, '没有焦点节点：is_focus 键可能被驼峰化了').toHaveLength(1)
    expect(focus[0].name).toBe('Foo')

    // is_test 让"只有测试在调用它"与"生产代码在调用它"成为两个可分辨的结论。
    expect(graph.nodes.filter(n => n.is_test)).toHaveLength(1)

    // resolved 决定边画实线还是虚线；它曾是 certainty: number 被前端当置信度阈值。
    expect(graph.edges.every(e => e.resolved === true)).toBe(true)

    // direct_callers 是 CallGraphPart 的摘要数字，也是 Go 侧判 grade=empty 的依据。
    expect(graph.impact_summary?.direct_callers).toBe(2)
  })

  it('行号是 1-based：两个数据源都是 0-based，差一会让点击跳错一行', () => {
    const graph = asCallGraphData(asPresentationEnvelope(GO_ENVELOPE)!.data)!
    // Go 侧 focusLine=41 / callerRef.Line=9 → wire 42 / 10
    expect(graph.nodes.find(n => n.is_focus)!.line).toBe(42)
    expect(graph.nodes.find(n => n.name === 'Handle')!.line).toBe(10)
  })
})
