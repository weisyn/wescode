import { useState } from 'react'
import { Network, ChevronDown, ChevronRight } from 'lucide-react'
import { GRADE_STYLES, type ResultGrade } from '@wesui/message'
import { CallGraph, type CallGraphData } from './CallGraph'

/**
 * 对话流里的调用图（PC-01 `category: 'graph'`）。
 *
 * 为什么这个组件在 wescode 而不在 wesui：`CallGraph` 是 D3 实现，依赖 CKG 的数据形状，
 * 而 wesui 服务三个产品——wesclaw / wescraft 没有 CKG。所以它走 wesui 已有的
 * `MessageBubble.renderPart` 扩展点，而不是往 wesui 里塞一个两个产品用不上的富组件。
 *
 * ── 为什么要一个容器，而不是直接把图丢进气泡 ──
 *
 * 对话流的视觉锚点是 composer，上面的东西越少越好。一个不限高的 D3 画布会把用户的
 * 问题顶出屏幕，而他滚不回去。所以这里定两条：高度有上限，摘要在 header（header 永远
 * 可见，图可以被折起来）。
 */

interface Props {
  data: CallGraphData
  grade: ResultGrade
}

/** 对话流里富组件的高度上限。够看清一层调用关系，又不至于顶掉上下文。 */
const GRAPH_HEIGHT = 260

export function CallGraphPart({ data, grade }: Props) {
  const callers = data.impact_summary?.direct_callers ?? Math.max(0, data.nodes.length - 1)
  // empty 默认收起：只有焦点节点的图没什么可看，而"没人调用它"这句话在 header 里。
  const [open, setOpen] = useState(grade !== 'empty')
  const style = GRADE_STYLES[grade]

  const focus = data.nodes.find(n => n.is_focus)
  const summary = callers === 0
    ? `${focus?.name ?? ''} 没有调用方`
    : `${focus?.name ?? ''} 有 ${callers} 个调用方`

  return (
    <div className={`rounded border overflow-hidden ${style.container}`}>
      <button
        type="button"
        onClick={() => setOpen(v => !v)}
        className="flex items-center gap-2 h-8 w-full text-left px-2 hover:bg-card-2 transition-colors duration-fast"
      >
        <Network size={13} className={`shrink-0 ${style.icon}`} />
        <span className={`text-[10px] px-1.5 py-0.5 rounded shrink-0 ${style.badge}`}>GRAPH</span>
        <span className="text-caption text-text-secondary flex-1 min-w-0 truncate">{summary}</span>
        <span className="text-muted-foreground shrink-0">
          {open ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
        </span>
      </button>

      {open && (
        // 高度必须落在容器上：CallGraph 用 ResizeObserver 从父元素取尺寸，
        // 不给高度它会长成内容的高度，而那正是"把用户的问题顶出屏幕"。
        <div
          className="mx-2 mb-2 mt-1 rounded border border-border bg-tool-card-bg overflow-hidden"
          style={{ height: GRAPH_HEIGHT }}
        >
          <CallGraph data={data} className="w-full h-full" />
        </div>
      )}
    </div>
  )
}

/**
 * 收窄，不是断言。
 *
 * 载荷来自 Go 侧的 `codeintel.CallGraphData`，两侧靠 JSON 键对齐——而键写错不会报错，
 * 只会让渲染器读到 undefined。`as CallGraphData` 会让那种信封一路走到 D3 里，然后在
 * 一个跟原因无关的地方崩掉（或者更糟：静默画出一张空图）。
 *
 * 只验最小必要结构：nodes / edges 是数组。字段级校验留给渲染器自己——它对缺失字段
 * 已有兜底（未知 edge kind 走灰色 + default 箭头）。
 */
export function asCallGraphData(v: unknown): CallGraphData | null {
  if (typeof v !== 'object' || v === null) return null
  const o = v as Record<string, unknown>
  if (!Array.isArray(o.nodes) || !Array.isArray(o.edges)) return null
  return o as unknown as CallGraphData
}
