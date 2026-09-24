import { useEffect, useState, useCallback, useReducer } from 'react'
import { ChevronLeft, ChevronRight, Lock, Unlock, Code2, AlertTriangle, ArrowLeft } from 'lucide-react'
import { request } from '../../bridge'
import { CallGraph, type CallGraphData } from './CallGraph'
import { useLocale } from '@wesui'

interface NavEntry { symbol: string; file?: string }
interface NavState { stack: NavEntry[]; cursor: number }

type NavAction =
  | { type: 'push'; entry: NavEntry }
  | { type: 'back' }
  | { type: 'forward' }

function navReducer(state: NavState, action: NavAction): NavState {
  switch (action.type) {
    case 'push': {
      const trimmed = state.stack.slice(0, state.cursor + 1)
      return { stack: [...trimmed, action.entry], cursor: trimmed.length }
    }
    case 'back':
      return state.cursor > 0 ? { ...state, cursor: state.cursor - 1 } : state
    case 'forward':
      return state.cursor < state.stack.length - 1 ? { ...state, cursor: state.cursor + 1 } : state
  }
}

interface FullImpactViewProps {
  symbolName: string
  filePath?: string
  onBack: () => void
  className?: string
}

export function FullImpactView({ symbolName, filePath, onBack, className }: FullImpactViewProps) {
  const { t } = useLocale()
  const [locked, setLocked] = useState(false)
  const [graphData, setGraphData] = useState<CallGraphData | null>(null)
  const [graphLoading, setGraphLoading] = useState(false)

  const [nav, dispatch] = useReducer(navReducer, {
    stack: [{ symbol: symbolName, file: filePath }],
    cursor: 0,
  })
  const currentEntry = nav.cursor >= 0 ? nav.stack[nav.cursor] : null

  const loadCallGraph = useCallback(async (symbol: string, file?: string) => {
    setGraphLoading(true)
    try {
      const data = await request<CallGraphData>('codeintel/callgraph', { symbol, file, depth: 2 })
      setGraphData(data)
    } catch { setGraphData(null) }
    finally { setGraphLoading(false) }
  }, [])

  useEffect(() => {
    if (currentEntry) {
      loadCallGraph(currentEntry.symbol, currentEntry.file)
    }
  }, [currentEntry, loadCallGraph])

  useEffect(() => {
    if (locked) return
    const entryChanged = symbolName !== currentEntry?.symbol || filePath !== currentEntry?.file
    if (entryChanged) {
      dispatch({ type: 'push', entry: { symbol: symbolName, file: filePath } })
    }
  }, [symbolName, filePath, locked, currentEntry?.symbol, currentEntry?.file])

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
          return { ...prev, nodes: [...prev.nodes, ...newNodes], edges: [...prev.edges, ...newEdges], truncated: expanded.truncated ?? prev.truncated }
        })
      }
    } catch { /* expand failed */ }
  }, [])

  const canBack = nav.cursor > 0
  const canForward = nav.cursor < nav.stack.length - 1
  const nodeCount = graphData?.nodes?.length ?? 0
  const edgeCount = graphData?.edges?.length ?? 0
  const affectedTests = graphData?.impact_summary?.affected_tests ?? 0

  return (
    <div className={`flex flex-col overflow-hidden ${className ?? ''}`} style={{ height: '100%' }}>
      <div className="shrink-0 px-4 py-2 border-b border-border flex items-center gap-2">
        <button onClick={onBack}
          className="p-1 rounded hover:bg-surface-3 transition-colors" title={t('codeintel.back_to_symbol', '返回符号详情')}>
          <ArrowLeft size={14} className="text-dim" />
        </button>

        <div className="w-px h-4 bg-border mx-1" />

        <button onClick={() => setLocked(!locked)}
          className="p-1 rounded hover:bg-surface-3 transition-colors" title={locked ? t('codeintel.unlock_follow', '解锁跟随') : t('codeintel.lock_symbol', '锁定当前符号')}>
          {locked ? <Lock size={14} className="text-warning" /> : <Unlock size={14} className="text-dim" />}
        </button>

        <button onClick={() => dispatch({ type: 'back' })} disabled={!canBack}
          className="p-1 rounded hover:bg-surface-3 transition-colors disabled:opacity-50 disabled:pointer-events-none">
          <ChevronLeft size={14} className="text-dim" />
        </button>
        <button onClick={() => dispatch({ type: 'forward' })} disabled={!canForward}
          className="p-1 rounded hover:bg-surface-3 transition-colors disabled:opacity-50 disabled:pointer-events-none">
          <ChevronRight size={14} className="text-dim" />
        </button>

        {currentEntry && (
          <>
            <Code2 size={14} className="text-accent" />
            <span className="text-small text-text font-medium font-mono truncate">{currentEntry.symbol}</span>
          </>
        )}

        {nodeCount > 0 && (
          <span className="text-caption text-muted ml-auto tabular-nums shrink-0">
            {nodeCount} {t('codeintel.nodes_unit', '节点')} · {edgeCount} {t('codeintel.edges_unit', '边')}
          </span>
        )}
      </div>

      <div className="overflow-hidden relative" style={{ height: 450 }}>
        <CallGraph data={graphData} loading={graphLoading} className="w-full h-full" onExpandNode={handleExpandNode} />
      </div>

      {graphData?.impact_summary && (
        <div className="shrink-0 border-t border-border px-4 py-3">
          <div className="flex items-center gap-4 text-small">
            <span className="text-accent tabular-nums">{graphData.impact_summary.direct_callers} {t('codeintel.direct_callers_unit', '直接调用方')}</span>
            <span className="text-dim">·</span>
            <span className="text-warning tabular-nums">{graphData.impact_summary.indirect_dependents} {t('codeintel.indirect_deps_unit', '间接依赖')}</span>
            <span className="text-dim">·</span>
            <span className="text-dim tabular-nums">{graphData.impact_summary.affected_files} {t('codeintel.affected_files_unit', '受影响文件')}</span>
            {affectedTests > 0 && (
              <>
                <span className="text-dim">·</span>
                <span className="flex items-center gap-1 text-info tabular-nums">
                  <AlertTriangle size={12} /> {affectedTests} {t('codeintel.affected_tests_unit', '受影响测试')}
                </span>
              </>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
