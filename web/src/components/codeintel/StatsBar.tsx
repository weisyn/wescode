import { FileCode2, Hash, FolderGit2, Languages } from 'lucide-react'
import { fmtCount, useLocale } from '@wesui'
import { langColor } from './lang-colors'

interface LanguageStat { language: string; file_count: number; symbol_count: number }
interface ProjectStat { root: string; name: string; is_topology_discovered: boolean; file_count: number; symbol_count: number; languages: string[]; language_stats?: LanguageStat[] }
interface DependencyEdge { from: string; to: string; kind: string }

export interface CodeIntelStats {
  status: 'not_initialized' | 'indexing' | 'empty' | 'ready'
  total_files: number; indexed_files: number; stale_files: number; indexing: boolean
  completeness: number; freshness: number; last_updated: string; symbols_count: number
  languages: LanguageStat[]; projects: ProjectStat[]; dependencies: DependencyEdge[]
}

export type { LanguageStat, ProjectStat, DependencyEdge }

// value 收数字而非预格式化字符串：千分位在这里按 locale 渲染一次，
// 调用点各自 .toLocaleString() 会取浏览器 locale（与应用语言无关）。
function StatCard({ icon, label, value, sub, pulse }: {
  icon: React.ReactNode; label: string; value: string | number; sub?: string; pulse?: boolean
}) {
  const { locale } = useLocale()
  return (
    <div className="px-4 py-3 bg-surface rounded-md border border-border relative overflow-hidden group transition-colors hover:border-accent/30">
      {pulse && <div className="absolute inset-0 bg-accent/5 animate-pulse pointer-events-none" />}
      <div className="flex items-center gap-2 mb-1 relative">
        <span className="text-accent transition-transform group-hover:scale-110">{icon}</span>
        <span className="text-caption text-dim">{label}</span>
      </div>
      <p className="text-h2 text-text relative tabular-nums">
        {typeof value === 'number' ? fmtCount(value, locale) : value}
      </p>
      {sub && <p className="text-caption text-dim mt-0.5 relative">{sub}</p>}
    </div>
  )
}

function ProgressBar({ value }: { value: number }) {
  const pct = Math.min(Math.max(value * 100, 0), 100)
  return (
    <div className="h-1.5 bg-surface-2 rounded-full overflow-hidden">
      <div className="h-full bg-gradient-to-r from-accent to-accent-light rounded-full transition-all duration-700 ease-out" style={{ width: `${pct}%` }} />
    </div>
  )
}

function LanguageBar({ languages }: { languages: LanguageStat[] }) {
  const { locale } = useLocale()
  if (!languages || languages.length === 0) return null
  const total = languages.reduce((s, l) => s + l.file_count, 0)
  if (total === 0) return null
  return (
    <div className="flex flex-col gap-2">
      <div className="flex h-2 rounded-full overflow-hidden bg-surface-2">
        {languages.map(l => (
          <div key={l.language} className="h-full transition-all duration-700"
            style={{ width: `${(l.file_count / total) * 100}%`, backgroundColor: langColor(l.language), minWidth: l.file_count > 0 ? 4 : 0 }}
            title={`${l.language}: ${fmtCount(l.file_count, locale)} files · ${fmtCount(l.symbol_count, locale)} symbols`} />
        ))}
      </div>
      <div className="flex flex-wrap gap-x-4 gap-y-1">
        {languages.map(l => (
          <div key={l.language} className="flex items-center gap-1.5 text-caption text-dim">
            <span className="w-2.5 h-2.5 rounded-sm inline-block" style={{ backgroundColor: langColor(l.language) }} />
            <span className="font-medium">{l.language}</span>
            <span className="text-muted tabular-nums">{fmtCount(l.file_count, locale)}</span>
            <span className="text-muted tabular-nums">· {fmtCount(l.symbol_count, locale)} sym</span>
          </div>
        ))}
      </div>
    </div>
  )
}

interface StatsBarProps {
  stats: CodeIntelStats | null
}

export function StatsBar({ stats }: StatsBarProps) {
  const { t, locale } = useLocale()
  if (!stats) return null

  const languages = stats.languages ?? []
  const projects = stats.projects ?? []
  const dependencies = stats.dependencies ?? []
  const langLabel = languages.length > 0 ? languages.map(l => l.language).join(' · ') : '—'

  return (
    <div className="flex flex-col gap-3">
      <div className="grid grid-cols-4 gap-3">
        <StatCard icon={<FileCode2 size={16} />} label={t('codeintel.indexed_files', '已索引文件')} value={stats.indexed_files}
          sub={stats.total_files > 0 ? `/ ${fmtCount(stats.total_files, locale)}` : undefined} pulse={stats.indexing} />
        <StatCard icon={<Hash size={16} />} label={t('codeintel.symbols', '符号')} value={stats.symbols_count} />
        <StatCard icon={<FolderGit2 size={16} />} label={t('codeintel.projects', '项目')} value={projects.length}
          sub={dependencies.length > 0 ? `${fmtCount(dependencies.length, locale)} ${t('codeintel.dependencies', '依赖')}` : undefined} />
        <StatCard icon={<Languages size={16} />} label={t('codeintel.languages', '语言')} value={languages.length} sub={langLabel} />
      </div>

      {stats.completeness < 1 && stats.completeness > 0 && (
        <div className="flex flex-col gap-1">
          <div className="flex items-center justify-between text-caption text-dim">
            <span>{t('codeintel.index_progress', '索引进度')}</span><span className="tabular-nums">{Math.round(stats.completeness * 100)}%</span>
          </div>
          <ProgressBar value={stats.completeness} />
        </div>
      )}

      {languages.length > 0 && <LanguageBar languages={languages} />}
    </div>
  )
}
