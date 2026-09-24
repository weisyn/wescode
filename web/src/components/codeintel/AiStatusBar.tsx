import { useEffect, useState } from 'react'
import { Zap, Loader2 } from 'lucide-react'
import { request } from '../../bridge'
import { type CodeIntelStats } from './StatsBar'
import { fmtCount, useLocale } from '@wesui'

interface CoverageData {
  total_functions: number
  functions_with_edges: number
  edge_coverage: number
  total_interfaces: number
  implements_edges: number
  blind_spots: { name: string; kind: string; file_path: string; line: number }[]
  stale_files: { file_path: string }[]
}

export type { CoverageData }

interface AiStatusBarProps {
  stats: CodeIntelStats | null
  onBlindSpotsClick?: (coverage: CoverageData) => void
  selectedSymbolName?: string
  className?: string
}

export function AiStatusBar({ stats, onBlindSpotsClick, selectedSymbolName, className }: AiStatusBarProps) {
  const { t, locale } = useLocale()
  const [coverage, setCoverage] = useState<CoverageData | null>(null)

  function coverageLevel(cov: number): { label: string; color: string } {
    if (cov > 0.7) return { label: t('codeintel.coverage_high', '高'), color: 'text-success' }
    if (cov >= 0.3) return { label: t('codeintel.coverage_medium', '中'), color: 'text-warning' }
    return { label: t('codeintel.coverage_low', '低'), color: 'text-danger' }
  }

  useEffect(() => {
    if (!stats) return
    if (stats.indexing) {
      setCoverage(null)
      return
    }
    let cancelled = false
    request<CoverageData>('codeintel/coverage', {})
      .then(data => { if (!cancelled) setCoverage(data) })
      .catch(() => {})
    return () => { cancelled = true }
  }, [stats?.indexing])

  if (!stats) return null

  const isIndexing = stats.indexing
  const blindCount = coverage ? (coverage.total_functions - coverage.functions_with_edges) : 0

  return (
    <div className={`h-8 flex items-center gap-3 px-4 bg-surface ${className ?? ''}`}>
      {isIndexing ? (
        <>
          <Loader2 size={12} className="text-accent animate-spin" />
          <span className="text-caption text-dim tabular-nums">
            {t('codeintel.index', '索引')} {stats.indexed_files}/{stats.total_files} · {Math.round(stats.completeness * 100)}%
          </span>
        </>
      ) : coverage ? (
        <>
          <Zap size={12} className="text-accent" />
          <span className="text-caption text-dim">{t('codeintel.ai_coverage_label', 'AI 理解度:')}</span>
          <span className={`text-caption font-medium ${coverageLevel(coverage.edge_coverage).color}`}>
            {coverageLevel(coverage.edge_coverage).label}
          </span>
          <span className="text-caption text-muted tabular-nums">{fmtCount(stats.symbols_count, locale)} {t('codeintel.symbols', '符号')}</span>
          {blindCount > 0 && (
            <>
              <span className="text-caption text-muted">·</span>
              <button
                onClick={() => onBlindSpotsClick?.(coverage)}
                className="text-caption text-warning tabular-nums hover:underline transition-colors cursor-pointer"
              >
                {fmtCount(blindCount, locale)} {t('codeintel.blind_spots', '盲区')}
              </button>
            </>
          )}
        </>
      ) : (
        <>
          <Zap size={12} className="text-dim" />
          <span className="text-caption text-dim">{stats.indexed_files} {t('codeintel.files_indexed', '文件已索引')}</span>
        </>
      )}
      {selectedSymbolName && (
        <>
          <span className="text-caption text-muted ml-auto">|</span>
          <span className="text-caption text-text font-mono">{selectedSymbolName}: {t('codeintel.selected', '已选中')}</span>
        </>
      )}
    </div>
  )
}
