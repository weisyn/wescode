import { useState } from 'react'
import { FileWarning, Clock, Home, ChevronRight } from 'lucide-react'
import { useLocale } from '@wesui'
import { type CodeIntelStats } from './StatsBar'
import { type CoverageData } from './AiStatusBar'
import { ProjectOverview } from './ProjectOverview'
import { ModuleDetail } from './ModuleDetail'
import { SymbolDetail } from './SymbolDetail'
import { FullImpactView } from './FullImpactView'

export type SelectionLevel =
  | { level: 'none' }
  | { level: 'project'; root: string }
  | { level: 'module'; projectRoot: string; modulePath: string; moduleInfo?: { file_count: number; languages: string[]; top_symbols: { name: string; kind: string }[] } }
  | { level: 'symbol'; symbolName: string; filePath?: string; breadcrumb?: string[] }
  | { level: 'coverage'; data: CoverageData }

interface DetailPanelProps {
  selection: SelectionLevel
  stats: CodeIntelStats | null
  selectedProjectRoot?: string
  onSymbolSelect?: (name: string) => void
  onBackToOverview?: () => void
  className?: string
}

export function DetailPanel({ selection, stats, selectedProjectRoot, onSymbolSelect, onBackToOverview, className }: DetailPanelProps) {
  const { t } = useLocale()
  const [fullImpact, setFullImpact] = useState<{ symbol: string; file?: string } | null>(null)

  if (fullImpact) {
    return (
      <FullImpactView
        symbolName={fullImpact.symbol}
        filePath={fullImpact.file}
        onBack={() => setFullImpact(null)}
        className={className}
      />
    )
  }

  switch (selection.level) {
    case 'none':
    case 'project':
      return <ProjectOverview stats={stats} selectedProjectRoot={selectedProjectRoot} className={className} />

    case 'module':
      return (
        <ModuleDetail
          projectRoot={selection.projectRoot}
          modulePath={selection.modulePath}
          moduleInfo={selection.moduleInfo}
          onSymbolSelect={onSymbolSelect}
          className={className}
        />
      )

    case 'symbol':
      return (
        <SymbolDetail
          symbolName={selection.symbolName}
          filePath={selection.filePath}
          breadcrumb={selection.breadcrumb}
          onOpenFullImpact={() => setFullImpact({ symbol: selection.symbolName, file: selection.filePath })}
          onBack={onBackToOverview}
          className={className}
        />
      )

    case 'coverage':
      return (
        <div className={`flex flex-col h-full overflow-hidden ${className ?? ''}`}>
          <div className="shrink-0 px-4 pt-3 pb-2 border-b border-border">
            <div className="flex items-center gap-1 text-caption text-muted mb-1.5">
              {onBackToOverview && (
                <button onClick={onBackToOverview} className="flex items-center gap-1 hover:text-accent transition-colors">
                  <Home size={10} /><span>{t('codeintel.overview', '概览')}</span>
                </button>
              )}
              <ChevronRight size={10} />
              <span>{t('codeintel.ai_coverage', 'AI 理解度')}</span>
            </div>
            <h2 className="text-h2 text-text">{t('codeintel.blind_spot_analysis', 'CKG 盲区分析')}</h2>
            <div className="flex items-center gap-4 mt-1 text-caption text-dim">
              <span>{selection.data.total_functions} {t('codeintel.functions_unit', '函数')}</span>
              <span>{selection.data.functions_with_edges} {t('codeintel.with_edges', '有调用边')}</span>
              <span>{t('codeintel.coverage_label', '覆盖率')} {Math.round(selection.data.edge_coverage * 100)}%</span>
              <span className="text-warning">{selection.data.blind_spots?.length ?? 0} {t('codeintel.blind_spots', '盲区')}</span>
            </div>
          </div>
          <div className="flex-1 overflow-y-auto p-4" style={{ scrollbarWidth: 'thin' }}>
            {(selection.data.blind_spots?.length ?? 0) > 0 && (
              <div className="flex flex-col gap-2 mb-6">
                <div className="flex items-center gap-2">
                  <FileWarning size={14} className="text-warning" />
                  <h3 className="text-h3 text-text">{t('codeintel.blind_spots_title', '盲区（无调用边的函数）')}</h3>
                </div>
                <div className="border border-border rounded-md overflow-hidden">
                  <table className="w-full text-small">
                    <thead>
                      <tr className="bg-surface-2 text-caption text-dim">
                        <th className="text-left px-3 py-2 font-medium">{t('codeintel.col_file', '文件')}</th>
                        <th className="text-left px-3 py-2 font-medium">{t('codeintel.col_symbol', '符号')}</th>
                        <th className="text-left px-3 py-2 font-medium">{t('codeintel.col_kind', '类型')}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {selection.data.blind_spots.map((spot, i) => (
                        <tr key={i} className="border-t border-border hover:bg-surface-3 transition-colors">
                          <td className="px-3 py-2 text-text font-mono truncate max-w-[200px]">{spot.file_path.split('/').slice(-2).join('/')}</td>
                          <td className="px-3 py-2 text-dim font-mono">{spot.name}</td>
                          <td className="px-3 py-2 text-muted">{spot.kind}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            )}
            {(selection.data.stale_files?.length ?? 0) > 0 && (
              <div className="flex flex-col gap-2">
                <div className="flex items-center gap-2">
                  <Clock size={14} className="text-dim" />
                  <h3 className="text-h3 text-text">{t('codeintel.stale_files_title', '待重索引文件')}</h3>
                </div>
                <div className="border border-border rounded-md overflow-hidden">
                  <table className="w-full text-small">
                    <thead>
                      <tr className="bg-surface-2 text-caption text-dim">
                        <th className="text-left px-3 py-2 font-medium">{t('codeintel.col_file', '文件')}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {selection.data.stale_files.map((f, i) => (
                        <tr key={i} className="border-t border-border hover:bg-surface-3 transition-colors">
                          <td className="px-3 py-2 text-text font-mono truncate">{f.file_path.split('/').slice(-2).join('/')}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            )}
          </div>
        </div>
      )
  }
}
