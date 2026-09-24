import { useState } from 'react'
import { ChevronRight, ChevronDown } from 'lucide-react'
import { openFile } from '../../bridge'
import { fmtCount, useLocale } from '@wesui'
import { LANG_COLORS, langColor, SYMBOL_STYLE, DEFAULT_SYMBOL_STYLE } from './lang-colors'

interface SymbolBrief { name: string; kind: string }

export interface ModuleInfo {
  path: string
  file_count: number
  languages: string[]
  top_symbols: SymbolBrief[]
}

interface ModuleListProps {
  modules: ModuleInfo[]
  selectedModule?: string
  onModuleSelect?: (modulePath: string) => void
  selectedSymbol?: string
  onSymbolClick?: (symbolName: string, filePath?: string) => void
}

export function ModuleList({ modules, selectedModule, onModuleSelect, selectedSymbol, onSymbolClick }: ModuleListProps) {
  const { t, locale } = useLocale()
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  const handleModuleClick = (path: string) => {
    setExpanded(prev => { const next = new Set(prev); next.has(path) ? next.delete(path) : next.add(path); return next })
    onModuleSelect?.(path)
  }

  if (!modules || modules.length === 0) return <div className="text-dim text-caption p-3">{t('codeintel.no_module_data', '暂无模块数据')}</div>

  return (
    <div className="flex flex-col">
      {modules.map(m => {
        const isOpen = expanded.has(m.path)
        const isSelected = selectedModule === m.path
        const hasChildren = (m.top_symbols ?? []).length > 0
        return (
          <div key={m.path}>
            <button onClick={() => handleModuleClick(m.path)}
              className={`flex items-center gap-1.5 w-full px-2 py-1 text-left transition-colors min-w-0 ${
                isSelected
                  ? 'bg-accent/10 border-l-2 border-accent'
                  : 'hover:bg-surface-3 border-l-2 border-transparent'
              }`}>
              <span className="w-4 h-4 flex items-center justify-center shrink-0">
                {hasChildren
                  ? (isOpen ? <ChevronDown size={14} className="text-dim" /> : <ChevronRight size={14} className="text-dim" />)
                  : null}
              </span>
              <span className="text-caption text-text truncate min-w-0">{m.path === '.' ? '(root)' : m.path}</span>
              <span className="text-caption text-muted ml-auto tabular-nums shrink-0">{fmtCount(m.file_count, locale)} files</span>
              <div className="flex gap-0.5 ml-1 shrink-0">
                {(m.languages ?? []).slice(0, 3).map(lang => (
                  <span key={lang} className="w-2 h-2 rounded-sm" style={{ backgroundColor: langColor(lang) }} title={lang} />
                ))}
              </div>
            </button>
            {isOpen && hasChildren && (
              <div className="ml-5 pl-2 border-l border-border/50">
                {(m.top_symbols ?? []).map((sym, i) => {
                  const style = SYMBOL_STYLE[sym.kind] ?? DEFAULT_SYMBOL_STYLE
                  const Icon = style.icon
                  const isSymSelected = selectedSymbol === sym.name
                  return (
                    <button key={i}
                      onClick={() => onSymbolClick?.(sym.name)}
                      onDoubleClick={() => openFile(sym.name)}
                      className={`flex items-center gap-1.5 py-0.5 px-1.5 text-caption hover:bg-surface-3 rounded cursor-pointer transition-colors w-full text-left min-w-0 ${
                        isSymSelected ? 'text-accent font-medium' : 'text-dim hover:text-accent'
                      }`}>
                      <Icon size={14} className={`${style.color} shrink-0`} />
                      <span className="font-mono truncate min-w-0">{sym.name}</span>
                      <span className="text-muted shrink-0">{sym.kind}</span>
                    </button>
                  )
                })}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}
