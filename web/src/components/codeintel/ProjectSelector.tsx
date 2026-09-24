import { FolderGit2, ArrowRight } from 'lucide-react'
import type { ProjectStat, DependencyEdge } from './StatsBar'
import { useLocale } from '@wesui'

interface ProjectSelectorProps {
  projects: ProjectStat[]
  dependencies: DependencyEdge[]
  selected: string
  onSelect: (root: string) => void
}

export function ProjectSelector({ projects, dependencies, selected, onSelect }: ProjectSelectorProps) {
  const { t } = useLocale()
  return (
    <div className="flex flex-col gap-0.5">
      <h3 className="text-caption text-dim font-medium px-2 mb-0.5">{t('codeintel.projects', '项目')}</h3>
      {(projects ?? []).map(p => (
        <button key={p.root} onClick={() => onSelect(p.root)}
          className={`flex items-center gap-1.5 px-2 py-1 rounded text-caption text-left w-full transition-colors ${
            selected === p.root ? 'bg-accent/10 text-accent border border-accent/20' : 'text-text hover:bg-surface-3 border border-transparent'
          }`}>
          <FolderGit2 size={12} className={selected === p.root ? 'text-accent shrink-0' : 'text-dim shrink-0'} />
          <span className="truncate">{p.name}</span>
          {p.is_topology_discovered && <span className="text-caption text-muted bg-surface-2 px-1 rounded-sm ml-auto shrink-0">{t('codeintel.discovered', '发现')}</span>}
        </button>
      ))}
      {(dependencies ?? []).length > 0 && (
        <div className="mt-1.5 pt-1.5 border-t border-border">
          <h3 className="text-caption text-dim font-medium px-2 mb-0.5">{t('codeintel.dependencies', '依赖')}</h3>
          {dependencies.map((d, i) => (
            <div key={i} className="flex items-center gap-1 px-2 py-0.5 text-caption text-muted">
              <span>{d.from}</span><ArrowRight size={9} /><span>{d.to}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
