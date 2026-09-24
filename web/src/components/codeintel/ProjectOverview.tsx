import { useEffect, useState } from 'react'
import { StatsBar, type CodeIntelStats, type DependencyEdge } from './StatsBar'
import { ArrowRight, Package, Zap } from 'lucide-react'
import { request } from '../../bridge'
import { fmtCount, useLocale } from '@wesui'
import { LANG_COLORS } from './lang-colors'

interface ModuleDepEdge {
  source_dir: string
  target_dir: string
  weight: number
}

interface ModuleDepResponse {
  edges: ModuleDepEdge[]
}

interface ProjectTreeModule {
  path: string
  file_count: number
  languages: string[]
  top_symbols: { name: string; kind: string }[]
}

interface ProjectTreeResponse {
  modules: ProjectTreeModule[]
}

interface CoreModule {
  path: string
  edgeCount: number
}

interface ProjectOverviewProps {
  stats: CodeIntelStats | null
  selectedProjectRoot?: string
  className?: string
}

// normalizeRoot mirrors Go's normalizeWorkspaceRoot comparison at the RPC
// boundary: trailing separators and case (Windows) must not decide whether a
// project matches the selected root (D-9). Go already returns Clean+Abs+
// Windows-ToLower roots; this is a second line of defence for roots that
// arrive from other sources (window paths, user input).
const normalizeRoot = (p: string) => p.replace(/[\\/]+$/, '').toLowerCase()

export function ProjectOverview({ stats, selectedProjectRoot, className }: ProjectOverviewProps) {
  const { t, locale } = useLocale()
  const [coreModules, setCoreModules] = useState<CoreModule[]>([])
  const [topSymbols, setTopSymbols] = useState<{ name: string; kind: string }[]>([])
  const [langStats, setLangStats] = useState<{ lang: string; files: number }[]>([])

  const project = selectedProjectRoot
    ? stats?.projects?.find(p => normalizeRoot(p.root) === normalizeRoot(selectedProjectRoot))
    : undefined

  const displayName = project?.name ?? t('codeintel.all_projects', '全部项目')
  const fileCount = project?.file_count ?? stats?.total_files ?? 0
  const symbolCount = project?.symbol_count ?? stats?.symbols_count ?? 0
  const languages = project?.languages ?? stats?.languages?.map(l => l.language) ?? []

  const relevantDeps: DependencyEdge[] = (stats?.dependencies ?? []).filter(dep =>
    selectedProjectRoot ? (dep.from === displayName || dep.to === displayName) : true
  )

  useEffect(() => {
    setCoreModules([])
    setTopSymbols([])
    setLangStats([])
    if (!selectedProjectRoot) {
      return
    }
    let cancelled = false
    const root = selectedProjectRoot

    request<ModuleDepResponse>('codeintel/module-deps', { project_root: root })
      .then(data => {
        if (cancelled) return
        const inDegreeMap = new Map<string, number>()
        for (const edge of data.edges) {
          inDegreeMap.set(edge.target_dir, (inDegreeMap.get(edge.target_dir) ?? 0) + edge.weight)
        }
        const sorted = [...inDegreeMap.entries()]
          .filter(([path]) => path !== '.' && path !== '')
          .sort((a, b) => b[1] - a[1])
          .slice(0, 5)
          .map(([path, edgeCount]) => ({ path, edgeCount }))
        setCoreModules(sorted)
      })
      .catch(() => { if (!cancelled) setCoreModules([]) })

    return () => { cancelled = true }
  }, [selectedProjectRoot])

  // Derive topSymbols and langStats from stats (no extra request).
  useEffect(() => {
    if (!selectedProjectRoot || !stats) {
      setTopSymbols([])
      setLangStats([])
      return
    }
    const proj = stats.projects?.find(p => normalizeRoot(p.root) === normalizeRoot(selectedProjectRoot))
    if (proj) {
      setLangStats((proj.languages ?? []).map(lang => ({ lang, files: 1 })))
    } else {
      setLangStats(
        (stats.languages ?? [])
          .sort((a, b) => b.file_count - a.file_count)
          .map(l => ({ lang: l.language, files: l.file_count }))
      )
    }
  }, [selectedProjectRoot, stats])

  const totalLangFiles = langStats.reduce((s, l) => s + l.files, 0)

  return (
    <div className={`flex flex-col gap-4 p-4 overflow-y-auto ${className ?? ''}`} style={{ scrollbarWidth: 'thin' }}>
      {selectedProjectRoot && project ? (
        <div className="rounded-md border border-border bg-surface p-4 flex flex-col gap-2">
          <h2 className="text-h2 text-text">{displayName}</h2>
          <div className="flex items-center gap-3 text-small text-dim">
            <span className="tabular-nums">{fmtCount(fileCount, locale)} {t('codeintel.files', '文件')}</span>
            <span>·</span>
            <span className="tabular-nums">{fmtCount(symbolCount, locale)} {t('codeintel.symbols', '符号')}</span>
            <span>·</span>
            <span>{languages.slice(0, 3).join(' / ')}</span>
          </div>
          {(() => {
            const bars = langStats.length > 0 && totalLangFiles > 0
              ? langStats
              : languages.map(lang => ({ lang, files: 1 }))
            const barTotal = bars.reduce((s, l) => s + l.files, 0)
            if (bars.length === 0 || barTotal === 0) return null
            return (
              <div className="flex flex-col gap-1.5 mt-1">
                <div className="flex h-2 rounded-full overflow-hidden bg-surface-2">
                  {bars.map(l => (
                    <div
                      key={l.lang}
                      className="h-full transition-all duration-700"
                      style={{
                        width: `${(l.files / barTotal) * 100}%`,
                        backgroundColor: LANG_COLORS[l.lang.toLowerCase()] ?? 'var(--color-dim)',
                        minWidth: l.files > 0 ? 4 : 0,
                      }}
                      title={`${l.lang}${langStats.length > 0 ? `: ${l.files} files` : ''}`}
                    />
                  ))}
                </div>
                <div className="flex flex-wrap gap-x-3 gap-y-0.5">
                  {bars.slice(0, 6).map(l => (
                    <div key={l.lang} className="flex items-center gap-1.5 text-caption text-muted">
                      <span className="w-2 h-2 rounded-sm" style={{ backgroundColor: LANG_COLORS[l.lang.toLowerCase()] ?? 'var(--color-dim)' }} />
                      <span>{l.lang}</span>
                      {langStats.length > 0 && <span className="tabular-nums">{l.files}</span>}
                    </div>
                  ))}
                </div>
              </div>
            )
          })()}
        </div>
      ) : (
        <StatsBar stats={stats} />
      )}

      {selectedProjectRoot && coreModules.length > 0 && (
        <div className="flex flex-col gap-2">
          <h3 className="text-h3 text-text">{t('codeintel.core_modules', '核心模块')}</h3>
          <div className="flex flex-col gap-1">
            {coreModules.map((mod, i) => {
              const maxEdges = coreModules[0]?.edgeCount ?? 1
              const barWidth = (mod.edgeCount / maxEdges) * 100
              return (
                <div key={mod.path} className="flex items-center gap-3 px-3 py-2 rounded-md border border-border hover:bg-surface-3 transition-colors relative overflow-hidden">
                  <div className="absolute inset-0 bg-accent/5 transition-all" style={{ width: `${barWidth}%` }} />
                  <span className="text-caption text-muted w-4 shrink-0 relative tabular-nums">{i + 1}</span>
                  <Package size={13} className="text-accent shrink-0 relative" />
                  <span className="text-small text-text font-mono truncate relative">{mod.path}</span>
                  <span className="text-caption text-muted ml-auto shrink-0 relative tabular-nums">{mod.edgeCount} {t('codeintel.calls', '条调用')}</span>
                </div>
              )
            })}
          </div>
        </div>
      )}

      {selectedProjectRoot && topSymbols.length > 0 && (
        <div className="flex flex-col gap-2">
          <h3 className="text-h3 text-text">{t('codeintel.public_symbols', '公开符号')}</h3>
          <div className="flex flex-col gap-1">
            {topSymbols.map((sym, i) => (
              <div key={i} className="flex items-center gap-2 px-3 py-1.5 rounded-md hover:bg-surface-3 transition-colors">
                <Zap size={12} className="text-accent/60 shrink-0" />
                <span className="text-small text-text font-mono truncate">{sym.name}</span>
                <span className="text-caption text-muted ml-auto shrink-0">{sym.kind}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {relevantDeps.length > 0 && (
        <div className="flex flex-col gap-2">
          <h3 className="text-h3 text-text">{t('codeintel.inter_project_deps', '项目间依赖')}</h3>
          <div className="flex flex-wrap gap-2">
            {relevantDeps.map((dep, i) => (
              <div key={i} className="flex items-center gap-1.5 px-2.5 py-1 rounded bg-surface-2 text-caption">
                <span className="text-text font-mono">{dep.from}</span>
                <ArrowRight size={10} className="text-dim" />
                <span className="text-text font-mono">{dep.to}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {!selectedProjectRoot && (
        <p className="text-small text-dim">{t('codeintel.select_project_hint', '选择左侧项目查看结构热点')}</p>
      )}
    </div>
  )
}
