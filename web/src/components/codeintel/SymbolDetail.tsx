import { useEffect, useState, useCallback, useMemo } from 'react'
import { Code2, ExternalLink, ArrowRight, ChevronRight, ChevronDown, Home, Zap } from 'lucide-react'
import { request, openFile } from '../../bridge'
import { CallGraph, type CallGraphData } from './CallGraph'
import { useLocale } from '@wesui'

interface SymbolDetailProps {
  symbolName: string
  filePath?: string
  breadcrumb?: string[]
  onOpenFullImpact?: () => void
  onBack?: () => void
  className?: string
}

export function SymbolDetail({ symbolName, filePath, breadcrumb, onOpenFullImpact, onBack, className }: SymbolDetailProps) {
  const { t } = useLocale()
  const [graphData, setGraphData] = useState<CallGraphData | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    request<CallGraphData>('codeintel/callgraph', { symbol: symbolName, file: filePath, depth: 1 })
      .then(data => { if (!cancelled) setGraphData(data) })
      .catch(() => { if (!cancelled) setGraphData(null) })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [symbolName, filePath])

  const handleExpandNode = useCallback(async (symbol: string, direction: 'callers' | 'callees') => {
    try {
      const expanded = await request<CallGraphData>('codeintel/expand-node', { symbol, direction, limit: 10 })
      if (expanded) {
        setGraphData(prev => {
          if (!prev) return expanded
          const existingIds = new Set(prev.nodes.map(n => n.id))
          const newNodes = expanded.nodes.filter(n => !existingIds.has(n.id))
          const existingEdges = new Set(prev.edges.map(e => `${e.source}->${e.target}`))
          const newEdges = expanded.edges.filter(e => !existingEdges.has(`${e.source}->${e.target}`))
          return { ...prev, nodes: [...prev.nodes, ...newNodes], edges: [...prev.edges, ...newEdges] }
        })
      }
    } catch { /* expand failed */ }
  }, [])

  const summary = graphData?.impact_summary
  const focusNode = graphData?.nodes?.find(n => n.is_focus)
  const displayPath = focusNode?.file ?? filePath
  const nodeCount = graphData?.nodes?.length ?? 0

  const [contextOpen, setContextOpen] = useState(false)

  const contextSections = useMemo(() => {
    if (!graphData?.nodes) return []
    const sections: { priority: string; label: string; file: string; tokens: number }[] = []
    const focus = graphData.nodes.find(n => n.is_focus)
    if (focus) {
      sections.push({ priority: 'P0', label: `${focus.name} (${focus.kind})`, file: focus.file, tokens: 150 })
    }
    for (const edge of graphData.edges) {
      const isCallerOfFocus = edge.target === focus?.id
      const isCalleeOfFocus = edge.source === focus?.id
      if (isCallerOfFocus || isCalleeOfFocus) {
        const nodeId = isCallerOfFocus ? edge.source : edge.target
        const node = graphData.nodes.find(n => n.id === nodeId)
        if (node) {
          sections.push({ priority: 'P1', label: `${isCallerOfFocus ? '⬆' : '⬇'} ${node.name}`, file: node.file, tokens: 80 })
        }
      }
    }
    for (const node of graphData.nodes) {
      if (node.is_focus) continue
      const directlyConnected = graphData.edges.some(e =>
        (e.source === focus?.id && e.target === node.id) ||
        (e.target === focus?.id && e.source === node.id)
      )
      if (!directlyConnected) {
        sections.push({ priority: 'P2', label: node.name, file: node.file, tokens: 50 })
      }
    }
    return sections
  }, [graphData])

  return (
    <div className={`flex flex-col overflow-hidden ${className ?? ''}`}>
      {/* 标题区 */}
      <div className="shrink-0 pb-2 border-b border-border">
        <div className="flex items-center gap-1 text-caption text-muted mb-1.5">
          {onBack && (
            <button onClick={onBack} className="flex items-center gap-1 hover:text-accent transition-colors">
              <Home size={10} />
              <span>{t('codeintel.overview', '概览')}</span>
            </button>
          )}
          {breadcrumb && breadcrumb.map((seg, i) => (
            <span key={i} className="flex items-center gap-1">
              <ChevronRight size={10} />
              <span>{seg}</span>
            </span>
          ))}
        </div>
        <div className="flex items-center gap-2">
          <Code2 size={16} className="text-accent shrink-0" />
          <h2 className="text-h2 text-text font-mono truncate">{symbolName}</h2>
          {focusNode && <span className="text-caption text-muted shrink-0">{focusNode.kind}</span>}
        </div>
        {displayPath && (
          <button
            onClick={() => openFile(displayPath, focusNode?.line ? focusNode.line + 1 : undefined)}
            className="text-caption text-accent hover:underline text-left truncate mt-1 block"
          >
            {displayPath.split('/').slice(-3).join('/')}
            {focusNode?.line != null && `:${focusNode.line + 1}`}
          </button>
        )}
      </div>

      {/* 调用图（固定高度，适配居中布局） */}
      <div className="overflow-hidden" style={{ height: 400 }}>
        <CallGraph data={graphData} loading={loading} className="w-full h-full" onExpandNode={handleExpandNode} />
      </div>

      {/* AI 上下文预览折叠区 */}
      {contextSections.length > 0 && (
        <div className="shrink-0 border-t border-border">
          <button onClick={() => setContextOpen(!contextOpen)}
            className="w-full py-2 flex items-center gap-2 text-small text-dim hover:bg-surface-3 transition-colors">
            {contextOpen ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
            <Zap size={12} className="text-accent" />
            <span>{t('codeintel.ai_context_preview', 'AI 上下文预览')}</span>
            <span className="text-caption text-muted ml-auto">
              ~{contextSections.reduce((s, c) => s + c.tokens, 0)} tokens
            </span>
          </button>
          {contextOpen && (
            <div className="pb-3 flex flex-col gap-1">
              {contextSections.map((s, i) => (
                <div key={i} className="flex items-center gap-2 text-caption">
                  <span className={`shrink-0 w-6 text-center font-medium ${
                    s.priority === 'P0' ? 'text-warning' : s.priority === 'P1' ? 'text-accent' : 'text-muted'
                  }`}>{s.priority}</span>
                  <span className="text-text font-mono truncate">{s.label}</span>
                  <span className="text-muted ml-auto shrink-0">{s.tokens} tok</span>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* 底部操作栏 */}
      <div className="shrink-0 py-2 border-t border-border flex items-center gap-2">
        {displayPath && (
          <button
            onClick={() => openFile(displayPath, focusNode?.line ? focusNode.line + 1 : undefined)}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border text-small text-text hover:bg-surface-3 transition-colors"
          >
            <ExternalLink size={12} /> {t('codeintel.open_in_editor', '在编辑器中打开')}
          </button>
        )}
        {onOpenFullImpact && (
          <button
            onClick={onOpenFullImpact}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-accent/30 text-small text-accent hover:bg-surface-3 transition-colors ml-auto"
          >
            {t('codeintel.view_full_impact', '查看完整影响')} <ArrowRight size={12} />
          </button>
        )}
      </div>
    </div>
  )
}
