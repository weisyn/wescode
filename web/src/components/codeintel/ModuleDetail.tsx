import { useEffect, useState } from 'react'
import { FolderGit2, ArrowUpRight, ArrowDownLeft, Code2, Box } from 'lucide-react'
import { request } from '../../bridge'
import { useLocale } from '@wesui'

interface ModuleDep {
  source_dir: string
  target_dir: string
  weight: number
}

interface ModuleDetailProps {
  projectRoot: string
  modulePath: string
  moduleInfo?: { file_count: number; languages: string[]; top_symbols: { name: string; kind: string }[] }
  onSymbolSelect?: (symbolName: string) => void
  className?: string
}

const SYMBOL_ICONS: Record<string, typeof Code2> = {
  function: Code2, method: Code2, type: Box, struct: Box, interface: Box, class: Box,
}

export function ModuleDetail({ projectRoot, modulePath, moduleInfo, onSymbolSelect, className }: ModuleDetailProps) {
  const { t } = useLocale()
  const [outEdges, setOutEdges] = useState<ModuleDep[]>([])
  const [inEdges, setInEdges] = useState<ModuleDep[]>([])

  useEffect(() => {
    let cancelled = false
    request<{ edges: ModuleDep[] }>('codeintel/module-deps', { project_root: projectRoot })
      .then(res => {
        if (cancelled) return
        const edges = res?.edges ?? []
        setOutEdges(
          edges.filter(e => e.source_dir === modulePath)
            .sort((a, b) => b.weight - a.weight)
            .slice(0, 8)
        )
        setInEdges(
          edges.filter(e => e.target_dir === modulePath)
            .sort((a, b) => b.weight - a.weight)
            .slice(0, 8)
        )
      })
      .catch(() => { setOutEdges([]); setInEdges([]) })
    return () => { cancelled = true }
  }, [projectRoot, modulePath])

  const symbols = moduleInfo?.top_symbols ?? []

  return (
    <div className={`flex flex-col gap-4 p-4 overflow-y-auto ${className ?? ''}`} style={{ scrollbarWidth: 'thin' }}>
      <div className="flex items-center gap-2">
        <FolderGit2 size={16} className="text-accent" />
        <h2 className="text-h2 text-text font-mono truncate">{modulePath || '(root)'}</h2>
        {moduleInfo && (
          <span className="text-caption text-muted ml-auto shrink-0 tabular-nums">
            {moduleInfo.file_count} {t('codeintel.files', '文件')} · {moduleInfo.languages.join(', ')}
          </span>
        )}
      </div>

      {(outEdges.length > 0 || inEdges.length > 0) && (
        <div className="grid grid-cols-2 gap-4">
          <div className="flex flex-col gap-1">
            <h3 className="text-caption text-dim flex items-center gap-1 mb-1">
              <ArrowUpRight size={12} /> {t('codeintel.dependencies', '依赖')}
            </h3>
            {outEdges.length === 0 ? (
              <p className="text-caption text-muted">{t('codeintel.no_out_edges', '无出边')}</p>
            ) : (
              outEdges.map((e, i) => (
                <div key={i} className="px-2 py-1.5 rounded hover:bg-surface-3 transition-colors">
                  <span className="text-small text-text font-mono">{e.target_dir || '(root)'}</span>
                  <span className="text-caption text-muted ml-2 tabular-nums">· {e.weight} {t('codeintel.call_edges_unit', '条调用边')}</span>
                </div>
              ))
            )}
          </div>
          <div className="flex flex-col gap-1">
            <h3 className="text-caption text-dim flex items-center gap-1 mb-1">
              <ArrowDownLeft size={12} /> {t('codeintel.depended_by', '被依赖')}
            </h3>
            {inEdges.length === 0 ? (
              <p className="text-caption text-muted">{t('codeintel.no_in_edges', '无入边')}</p>
            ) : (
              inEdges.map((e, i) => (
                <div key={i} className="px-2 py-1.5 rounded hover:bg-surface-3 transition-colors">
                  <span className="text-small text-text font-mono">{e.source_dir || '(root)'}</span>
                  <span className="text-caption text-muted ml-2 tabular-nums">· {e.weight} {t('codeintel.call_edges_unit', '条调用边')}</span>
                </div>
              ))
            )}
          </div>
        </div>
      )}

      {symbols.length > 0 && (
        <div className="flex flex-col gap-1">
          <h3 className="text-h3 text-text mb-1">{t('codeintel.public_symbols', '公开符号')}</h3>
          <div className="flex flex-col gap-0.5">
            {symbols.map((sym, i) => {
              const Icon = SYMBOL_ICONS[sym.kind] ?? Code2
              return (
                <button key={i}
                  onClick={() => onSymbolSelect?.(sym.name)}
                  className="flex items-center gap-2 px-3 py-1.5 rounded hover:bg-surface-3 transition-colors text-left w-full">
                  <Icon size={12} className="text-accent/60 shrink-0" />
                  <span className="text-small text-text font-mono truncate">{sym.name}</span>
                  <span className="text-caption text-muted">{sym.kind}</span>
                </button>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}
