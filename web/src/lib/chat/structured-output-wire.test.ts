import { describe, expect, it } from 'vitest'
import { streamEventToWireEvent } from './useStream'
import type { StreamEvent } from '@/bridge'

// PC-03：声明必须投影进流。
//
// 这是管道的最后一环。前三环早就通了：
//
//   wesgine  ToolResult.StructuredData 非 nil → 自动发 EventStructuredOutput
//   wesapp   wire.MapEvent 映射成 wire.StructuredOutput
//   wesui    reduce.ts 收 { type:'structured_output', data } → StructuredOutputPart
//
// wescode 侧断在两处：Go 的 mapEvent 没有这个 case（落 default 丢弃），以及这里的
// streamEventToWireEvent 没有这个 case。两处都补上，`StructuredOutputBlock` 才从
// "看起来是死组件"变成"有数据可渲染"。
//
// 设计见 wescode design/27-presentation-contract.md §四末。

describe('streamEventToWireEvent structured_output', () => {
  it('原样透传 payload，不解释结构', () => {
    const payload = { kind: 'graph', nodes: [{ id: 'main' }], edges: [] }

    const wire = streamEventToWireEvent({
      type: 'structured_output',
      structuredData: payload,
    } as StreamEvent)

    expect(wire).not.toBeNull()
    expect(wire!.type).toBe('structured_output')
    // 同一个对象引用：中间层一旦重塑就成了第三份真相（形状归工具，形态归 wesui）。
    expect(wire!.data).toBe(payload)
  })

  it('数组 payload 同样透传 —— 不假设顶层是对象', () => {
    const payload = [{ path: 'a.go', callers: 3 }]

    const wire = streamEventToWireEvent({
      type: 'structured_output',
      structuredData: payload,
    } as StreamEvent)

    expect(wire!.data).toBe(payload)
  })

  it('无 payload 时丢弃，不产出空事件', () => {
    const wire = streamEventToWireEvent({
      type: 'structured_output',
    } as StreamEvent)

    // wesui 的 reducer 对 undefined/null 会跳过，但让一个无内容的事件穿过整条
    // 链路到最远端再被丢弃，等于把判断推到离原因最远的地方。
    expect(wire).toBeNull()
  })
})
