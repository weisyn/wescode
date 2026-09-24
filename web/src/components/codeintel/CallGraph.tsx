import { useEffect, useRef, useState, useCallback } from 'react'
import * as d3 from 'd3'
import { openFile } from '../../bridge'
import { AlertTriangle } from 'lucide-react'
import { useLocale } from '@wesui'

export interface GraphNode {
  id: string
  name: string
  kind: string
  file: string
  line: number
  signature?: string
  is_focus: boolean
  is_test?: boolean
}

export interface GraphEdge {
  source: string
  target: string
  kind: string
  /**
   * False when the far endpoint is a synthetic placeholder — a callee name the
   * backend could not find in the symbol table (unindexed package, dynamic
   * dispatch, external dependency). Rendered dashed + red.
   *
   * Replaces the old `certainty: number`, which only ever arrived as 1.0 or 0.5
   * and was thresholded at 0.8. A two-valued float reads as a confidence scale
   * the backend never computed.
   */
  resolved: boolean
}

export interface ImpactSummary {
  direct_callers: number
  indirect_dependents: number
  affected_files: number
  affected_tests?: number
}

export interface CallGraphData {
  nodes: GraphNode[]
  edges: GraphEdge[]
  impact_summary?: ImpactSummary
  readiness?: { completeness: number; indexing: boolean }
  truncated?: boolean
}

interface CallGraphProps {
  data: CallGraphData | null
  loading?: boolean
  className?: string
  onExpandNode?: (symbol: string, direction: 'callers' | 'callees') => void
}

// Both producers of this graph (codeintel/callgraph, codeintel/expand-node) emit
// exactly one edge kind: `call`. Colors keyed by kinds nothing produces are the
// frontend half of the same dead wiring the Go side carried — `type_ref` and
// `struct_field_type` were declared and filtered on but never emitted, and
// `route` was never an edge kind at all (routes are nodes).
//
// Unknown kinds fall back to grey and to the `default` arrow marker. The
// fallback marker is not decoration: `marker-end` is built from the kind
// string, so a kind absent from this map used to resolve to `url(#arrow-<kind>)`
// with no such element and silently lost its arrowhead.
const EDGE_COLORS: Record<string, string> = {
  call: '#3B82F6',
}
const EDGE_COLOR_FALLBACK = '#6B7280'
const EDGE_MARKER_IDS = [...Object.keys(EDGE_COLORS), 'default']

const edgeColor = (kind: string) => EDGE_COLORS[kind] ?? EDGE_COLOR_FALLBACK
const edgeMarker = (kind: string) => (kind in EDGE_COLORS ? kind : 'default')

const NODE_COLORS: Record<string, string> = {
  function: '#3B82F6',
  method: '#60A5FA',
  type: '#22C55E',
  struct: '#34D399',
  interface: '#A855F7',
  class: '#C084FC',
}

const TEST_COLOR = '#F97316'

function nodeRadius(node: GraphNode, allEdges: GraphEdge[]): number {
  if (node.is_focus) return 18
  const degree = allEdges.filter(e => e.source === node.id || e.target === node.id).length
  return Math.max(8, Math.min(14, 6 + degree * 2))
}

function nodeShape(kind: string, isTest?: boolean): 'circle' | 'diamond' | 'triangle' | 'rect' {
  if (isTest) return 'triangle'
  switch (kind) {
    case 'type': case 'interface': case 'struct': case 'class': return 'diamond'
    default: return 'circle'
  }
}

function isLeafNode(nodeId: string, edges: GraphEdge[]): 'callers' | 'callees' | null {
  const hasIncoming = edges.some(e => e.target === nodeId)
  const hasOutgoing = edges.some(e => e.source === nodeId)
  if (hasIncoming && !hasOutgoing) return 'callees'
  if (!hasIncoming && hasOutgoing) return 'callers'
  return null
}

type SimNode = d3.SimulationNodeDatum & GraphNode
type SimLink = d3.SimulationLinkDatum<SimNode> & { kind: string; resolved: boolean }

export function CallGraph({ data, loading, className, onExpandNode }: CallGraphProps) {
  const { t } = useLocale()
  const svgRef = useRef<SVGSVGElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const [tooltip, setTooltip] = useState<{ x: number; y: number; node: GraphNode } | null>(null)
  const [dimensions, setDimensions] = useState({ width: 800, height: 400 })

  useEffect(() => {
    if (!containerRef.current) return
    const ro = new ResizeObserver(entries => {
      for (const entry of entries) {
        setDimensions({
          width: entry.contentRect.width,
          height: entry.contentRect.height,
        })
      }
    })
    ro.observe(containerRef.current)
    return () => ro.disconnect()
  }, [])

  const renderGraph = useCallback(() => {
    if (!svgRef.current || !data || data.nodes.length === 0) return

    const svg = d3.select(svgRef.current)
    svg.selectAll('*').remove()

    const { width, height } = dimensions
    const g = svg.append('g')

    const zoom = d3.zoom<SVGSVGElement, unknown>()
      .scaleExtent([0.3, 3])
      .on('zoom', (event) => g.attr('transform', event.transform))
    svg.call(zoom)

    const nodes: SimNode[] = data.nodes.map(n => ({ ...n }))
    const nodeById = new Map(nodes.map(n => [n.id, n]))

    const links: SimLink[] = data.edges
      .filter(e => nodeById.has(e.source) && nodeById.has(e.target))
      .map(e => ({
        source: e.source,
        target: e.target,
        kind: e.kind,
        resolved: e.resolved,
      }))

    svg.append('defs').selectAll('marker')
      .data(EDGE_MARKER_IDS)
      .join('marker')
      .attr('id', d => `arrow-${d}`)
      .attr('viewBox', '0 -4 8 8')
      .attr('refX', 20)
      .attr('refY', 0)
      .attr('markerWidth', 6)
      .attr('markerHeight', 6)
      .attr('orient', 'auto')
      .append('path')
      .attr('d', 'M0,-4L8,0L0,4')
      .attr('fill', d => edgeColor(d))
      .attr('opacity', 0.6)

    const simulation = d3.forceSimulation(nodes)
      .force('link', d3.forceLink<SimNode, SimLink>(links).id(d => d.id).distance(100).strength(0.5))
      .force('charge', d3.forceManyBody().strength(-300))
      .force('center', d3.forceCenter(width / 2, height / 2))
      .force('collision', d3.forceCollide().radius(25))

    const linkGroup = g.append('g').attr('class', 'links')
    const link = linkGroup.selectAll('line')
      .data(links)
      .join('line')
      .attr('stroke', d => !d.resolved ? '#EF4444' : edgeColor(d.kind))
      .attr('stroke-width', d => d.resolved ? 1.5 : 1)
      .attr('stroke-dasharray', d => !d.resolved ? '4,3' : null)
      .attr('stroke-opacity', 0.5)
      .attr('marker-end', d => `url(#arrow-${edgeMarker(d.kind)})`)

    const nodeGroup = g.append('g').attr('class', 'nodes')
    const nodeEl = nodeGroup.selectAll<SVGGElement, SimNode>('g')
      .data(nodes)
      .join('g')
      .attr('cursor', 'pointer')
      .call(d3.drag<SVGGElement, SimNode>()
        .on('start', (event, d) => {
          if (!event.active) simulation.alphaTarget(0.3).restart()
          d.fx = d.x
          d.fy = d.y
        })
        .on('drag', (event, d) => {
          d.fx = event.x
          d.fy = event.y
        })
        .on('end', (event, d) => {
          if (!event.active) simulation.alphaTarget(0)
          d.fx = null
          d.fy = null
        })
      )

    nodeEl.each(function (d) {
      const el = d3.select(this)
      const r = nodeRadius(d, data.edges)
      const isTest = d.is_test ?? false
      const color = isTest ? TEST_COLOR : (NODE_COLORS[d.kind] ?? '#6B7280')
      const shape = nodeShape(d.kind, isTest)

      // AI 上下文优先级光晕层（在形状之前绘制，位于下层）
      const glowColor = d.is_focus ? '#F59E0B'
        : links.some(l =>
            ((l.source as SimNode).id === d.id && (l.target as SimNode).is_focus) ||
            ((l.target as SimNode).id === d.id && (l.source as SimNode).is_focus)
          ) ? '#3B82F6'
        : 'var(--color-surface-3, #2a2a3a)'
      const glowRadius = d.is_focus ? r + 5 : links.some(l =>
        ((l.source as SimNode).id === d.id && (l.target as SimNode).is_focus) ||
        ((l.target as SimNode).id === d.id && (l.source as SimNode).is_focus)
      ) ? r + 3 : r + 2
      const glowOpacity = d.is_focus ? 0.3 : links.some(l =>
        ((l.source as SimNode).id === d.id && (l.target as SimNode).is_focus) ||
        ((l.target as SimNode).id === d.id && (l.source as SimNode).is_focus)
      ) ? 0.2 : 0.15
      el.append('circle')
        .attr('r', glowRadius)
        .attr('fill', glowColor)
        .attr('opacity', glowOpacity)
        .attr('class', 'ai-glow')

      if (shape === 'triangle') {
        const h = r * 2
        el.append('path')
          .attr('d', `M0,${-h * 0.6} L${h * 0.5},${h * 0.4} L${-h * 0.5},${h * 0.4} Z`)
          .attr('fill', d.is_focus ? color : 'var(--color-surface-2, #1e1e2e)')
          .attr('stroke', color)
          .attr('stroke-width', d.is_focus ? 2.5 : 1.5)
      } else if (shape === 'diamond') {
        el.append('path')
          .attr('d', `M0,${-r} L${r},0 L0,${r} L${-r},0 Z`)
          .attr('fill', d.is_focus ? color : 'var(--color-surface-2, #1e1e2e)')
          .attr('stroke', color)
          .attr('stroke-width', d.is_focus ? 2.5 : 1.5)
      } else {
        el.append('circle')
          .attr('r', r)
          .attr('fill', d.is_focus ? color : 'var(--color-surface-2, #1e1e2e)')
          .attr('stroke', color)
          .attr('stroke-width', d.is_focus ? 2.5 : 1.5)
      }

      if (d.is_focus) {
        const pulse = el.append('circle')
          .attr('r', r)
          .attr('fill', 'none')
          .attr('stroke', '#F59E0B')
          .attr('stroke-width', 1)
          .attr('opacity', 0.6)

        function animatePulse() {
          pulse
            .attr('r', r)
            .attr('opacity', 0.6)
            .transition()
            .duration(1500)
            .ease(d3.easeQuadOut)
            .attr('r', r + 10)
            .attr('opacity', 0)
            .on('end', animatePulse)
        }
        animatePulse()
      }

      if (!d.is_focus && onExpandNode) {
        const leafDir = isLeafNode(d.id, data.edges)
        if (leafDir) {
          el.append('circle')
            .attr('cx', r + 6)
            .attr('cy', -r + 2)
            .attr('r', 6)
            .attr('fill', 'var(--color-surface, #1a1a2e)')
            .attr('stroke', 'var(--color-border, #3a3a4a)')
            .attr('stroke-width', 1)
            .attr('cursor', 'pointer')
            .on('click', (event) => {
              event.stopPropagation()
              onExpandNode(d.name, leafDir)
            })

          el.append('text')
            .attr('x', r + 6)
            .attr('y', -r + 6)
            .attr('text-anchor', 'middle')
            .attr('fill', 'var(--color-accent, #3B82F6)')
            .attr('font-size', '10px')
            .attr('font-weight', '700')
            .attr('pointer-events', 'none')
            .text('+')
        }
      }

      el.append('text')
        .text(d.name.length > 20 ? d.name.slice(0, 18) + '…' : d.name)
        .attr('dy', r + 14)
        .attr('text-anchor', 'middle')
        .attr('fill', 'var(--color-text-secondary, #a0a0b0)')
        .attr('font-size', d.is_focus ? '12px' : '10px')
        .attr('font-family', 'var(--font-mono, monospace)')
        .attr('font-weight', d.is_focus ? '600' : '400')
    })

    nodeEl
      .on('mouseenter', (event, d) => {
        const [x, y] = d3.pointer(event, containerRef.current)
        setTooltip({ x, y: y - 10, node: d })
        link
          .attr('stroke-opacity', l =>
            (l.source as SimNode).id === d.id || (l.target as SimNode).id === d.id ? 0.9 : 0.15
          )
          .attr('stroke-width', l =>
            (l.source as SimNode).id === d.id || (l.target as SimNode).id === d.id ? 2.5 : 1
          )
        nodeEl.attr('opacity', n =>
          n.id === d.id ||
          links.some(l =>
            ((l.source as SimNode).id === d.id && (l.target as SimNode).id === n.id) ||
            ((l.target as SimNode).id === d.id && (l.source as SimNode).id === n.id)
          ) ? 1 : 0.3
        )
      })
      .on('mouseleave', () => {
        setTooltip(null)
        link.attr('stroke-opacity', 0.5).attr('stroke-width', d => d.resolved ? 1.5 : 1)
        nodeEl.attr('opacity', 1)
      })
      .on('click', (_event, d) => {
        if (d.file) {
          openFile(d.file, d.line + 1)
        }
      })

    simulation.on('tick', () => {
      link
        .attr('x1', d => (d.source as SimNode).x!)
        .attr('y1', d => (d.source as SimNode).y!)
        .attr('x2', d => (d.target as SimNode).x!)
        .attr('y2', d => (d.target as SimNode).y!)

      nodeEl.attr('transform', d => `translate(${d.x},${d.y})`)
    })

    const timers: ReturnType<typeof setTimeout>[] = []
    nodes.forEach(n => {
      if (n.is_focus) {
        n.fx = width / 2
        n.fy = height / 2
        const timer = setTimeout(() => {
          n.fx = null
          n.fy = null
          simulation.alpha(0.3).restart()
        }, 800)
        timers.push(timer)
      }
    })

    return () => {
      simulation.stop()
      timers.forEach(t => clearTimeout(t))
    }
  }, [data, dimensions, onExpandNode])

  useEffect(() => {
    const cleanup = renderGraph()
    return () => { cleanup?.() }
  }, [renderGraph])

  if (!data || data.nodes.length === 0) {
    return (
      <div className={`flex items-center justify-center text-dim text-small ${className ?? ''}`}>
        {loading ? (
          <span className="animate-pulse">{t('codeintel.loading_callgraph', '加载调用图...')}</span>
        ) : (
          <span>{t('codeintel.select_symbol_hint', '选择一个符号查看调用关系')}</span>
        )}
      </div>
    )
  }

  return (
    <div ref={containerRef} className={`relative overflow-hidden ${className ?? ''}`}>
      <svg
        ref={svgRef}
        width={dimensions.width}
        height={dimensions.height}
        className="w-full h-full"
      />

      {data.impact_summary && (
        <div className="absolute top-3 left-3 flex items-center gap-3 px-3 py-1.5 bg-surface/90 backdrop-blur-sm rounded-md border border-border text-caption">
          <span className="text-accent tabular-nums">{data.impact_summary.direct_callers} {t('codeintel.callers_unit', '调用方')}</span>
          <span className="text-dim">·</span>
          <span className="text-warning tabular-nums">{data.impact_summary.indirect_dependents} {t('codeintel.indirect_deps_unit', '间接依赖')}</span>
          <span className="text-dim">·</span>
          <span className="text-dim tabular-nums">{data.impact_summary.affected_files} {t('codeintel.files', '文件')}</span>
          {(data.impact_summary.affected_tests ?? 0) > 0 && (
            <>
              <span className="text-dim">·</span>
              <span className="flex items-center gap-1" style={{ color: TEST_COLOR }}>
                <AlertTriangle size={10} />
                <span className="tabular-nums">{data.impact_summary.affected_tests} {t('codeintel.tests_unit', '测试')}</span>
              </span>
            </>
          )}
        </div>
      )}

      {data.truncated && (
        <div className="absolute top-3 right-3 px-2 py-1 bg-warning-soft rounded text-caption text-warning">
          {t('codeintel.results_truncated', '结果已截断')}
        </div>
      )}

      {!data.truncated && data.readiness && data.readiness.completeness < 0.9 && (
        <div className="absolute top-3 right-3 px-2 py-1 bg-warning-soft rounded text-caption text-warning">
          {t('codeintel.index', '索引')} {Math.round(data.readiness.completeness * 100)}%{data.readiness.indexing ? ` · ${t('codeintel.indexing_ellipsis', '索引中...')}` : ''}
        </div>
      )}

      <div className="absolute bottom-3 left-3 flex flex-col gap-1 px-3 py-1.5 bg-surface/90 backdrop-blur-sm rounded-md border border-border text-caption text-dim">
        <div className="flex items-center gap-3">
          <span className="flex items-center gap-1"><span className="w-3 h-0.5 bg-[#3B82F6] inline-block" />call</span>
          <span className="flex items-center gap-1"><span className="w-3 h-0.5 bg-[#22C55E] inline-block" />type</span>
          <span className="flex items-center gap-1"><span className="w-3 h-0.5 bg-[#A855F7] inline-block" />impl</span>
          <span className="flex items-center gap-1 ml-2">● fn</span>
          <span className="flex items-center gap-1">◆ type</span>
          <span className="flex items-center gap-1" style={{ color: TEST_COLOR }}>▲ test</span>
        </div>
        <div className="flex items-center gap-3 border-t border-border pt-1">
          <span className="flex items-center gap-1" style={{ color: '#F59E0B' }}>● {t('codeintel.ai_must_read', 'AI 必看')}</span>
          <span className="flex items-center gap-1" style={{ color: '#3B82F6' }}>● {t('codeintel.ai_likely_ref', 'AI 大概率参考')}</span>
          <span className="flex items-center gap-1 text-muted">● {t('codeintel.ai_may_truncate', 'AI 可能截断')}</span>
          <span className="flex items-center gap-1" style={{ color: '#EF4444' }}>┄ {t('codeintel.ai_uncertain', 'AI 不确定')}</span>
        </div>
      </div>

      {tooltip && (
        <div
          className="absolute pointer-events-none px-3 py-2 bg-surface border border-border-hi rounded-md shadow-lg z-50 max-w-xs"
          style={{ left: tooltip.x + 12, top: tooltip.y - 40 }}
        >
          <div className="text-small text-text font-medium font-mono">{tooltip.node.name}</div>
          <div className="text-caption text-dim">
            {tooltip.node.kind}
            {tooltip.node.is_test && <span className="ml-2" style={{ color: TEST_COLOR }}>{t('codeintel.badge_test', '测试')}</span>}
          </div>
          {tooltip.node.signature && (
            <div className="text-caption text-muted font-mono mt-1 truncate">{tooltip.node.signature}</div>
          )}
          {tooltip.node.file && (
            <div className="text-caption text-accent mt-1">{tooltip.node.file.split('/').slice(-2).join('/')}:{tooltip.node.line + 1}</div>
          )}
          <div className="text-caption text-muted mt-1">{t('codeintel.click_to_source', '点击跳转源码')}</div>
        </div>
      )}
    </div>
  )
}
