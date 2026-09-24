import { describe, expect, it } from 'vitest'
import { asCallGraphData } from './CallGraphPart'

// PC-01/PC-04：对话流里的调用图。
//
// 这里只测收窄逻辑，不测 D3 渲染——后者要真实 DOM 尺寸（CallGraph 用 ResizeObserver
// 从容器取宽高），在 jsdom 里量出来的是 0，测出来的东西跟屏幕上的不是一回事。
//
// 收窄才是会安静出错的那一半：载荷来自 Go 侧的 codeintel.CallGraphData，两侧靠 JSON
// 键对齐，而键写错不会报错。`as CallGraphData` 会让那种载荷一路走进 D3，然后在一个跟
// 原因无关的地方崩掉，或者更糟——静默画出一张空图。

describe('asCallGraphData', () => {
  it('收下最小合法图', () => {
    const graph = { nodes: [{ id: 'a', is_focus: true }], edges: [] }
    expect(asCallGraphData(graph)).toBe(graph)
  })

  it('收下带 impact_summary / readiness 的完整图', () => {
    const graph = {
      nodes: [{ id: 'a', is_focus: true }, { id: 'b' }],
      edges: [{ source: 'b', target: 'a', kind: 'call', resolved: true }],
      impact_summary: { direct_callers: 1 },
      readiness: { status: 'ready', completeness: 1, indexing: false },
    }
    expect(asCallGraphData(graph)).toBe(graph)
  })

  it.each([
    ['null', null],
    ['字符串', 'nodes'],
    ['数组', [{ id: 'a' }]],
    ['缺 nodes', { edges: [] }],
    ['缺 edges', { nodes: [] }],
    ['nodes 不是数组', { nodes: { id: 'a' }, edges: [] }],
    ['edges 不是数组', { nodes: [], edges: 'none' }],
    // 驼峰化过的载荷：Go 侧发的是 snake_case，一旦中间层重塑就该被拒而不是画空图
    ['驼峰键（中间层重塑过）', { Nodes: [], Edges: [] }],
  ])('拒收 %s', (_name, input) => {
    expect(asCallGraphData(input)).toBeNull()
  })
})
