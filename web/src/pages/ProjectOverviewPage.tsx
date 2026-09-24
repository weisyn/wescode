import { useEffect, useState, useCallback, useRef, type RefObject } from 'react'
import {
  Loader2, AlertTriangle, MoreVertical, RefreshCw, Trash2,
  FileCode2, FolderGit2, Network, ChevronDown, ChevronRight,
  Package, Zap, ArrowRight,
} from 'lucide-react'
import { fmtCount, useLocale } from '@wesui'
import { PageShell, ConfirmDialog } from '@wesui/layout'
import { SearchInput } from '@wesui/forms'
import { Button } from '@wesui/primitives'
import { request, onIndexProgress, onIndexComplete, onIndexError, onHasWorkspaceChange, openFile, type IndexProgressState, type IndexErrorState } from '../bridge'
import type { CodeIntelStats } from '../components/codeintel/StatsBar'
import type { ModuleInfo } from '../components/codeintel/ModuleList'
import { SymbolDetail } from '../components/codeintel/SymbolDetail'
import { FullImpactView } from '../components/codeintel/FullImpactView'
import { type CoverageData } from '../components/codeintel/AiStatusBar'

interface ProjectTreeResponse {
  status?: string
  modules: ModuleInfo[]
}

interface ModuleDepEdge {
  source_dir: string
  target_dir: string
  weight: number
}

import { LANG_COLORS, SYMBOL_STYLE, DEFAULT_SYMBOL_STYLE } from '../components/codeintel/lang-colors'

export function ProjectOverviewPage() {
  const { t, locale } = useLocale()
  const [stats, setStats] = useState<CodeIntelStats | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const [selectedProject, setSelectedProject] = useState<string>('')
  const [tree, setTree] = useState<ProjectTreeResponse | null>(null)
  const [coreModules, setCoreModules] = useState<{ path: string; edgeCount: number }[]>([])

  const [expandedModule, setExpandedModule] = useState<string | null>(null)
  const [selectedSymbol, setSelectedSymbol] = useState<string | null>(null)
  const [searchQuery, setSearchQuery] = useState('')
  const [indexError, setIndexError] = useState<string | null>(null)
  const [menuOpen, setMenuOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)
  const [reindexing, setReindexing] = useState(false)

  useEffect(() => {
    if (!menuOpen) return
    const handleClickOutside = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setMenuOpen(false)
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [menuOpen])

  const [coverage, setCoverage] = useState<CoverageData | null>(null)
  const [fullImpact, setFullImpact] = useState<{ symbol: string; file?: string } | null>(null)

  const activeProjectRef = useRef<string>('')
  const retryCountRef = useRef(0)
  const retryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const [bootRetrying, setBootRetrying] = useState(false)
  const MAX_BOOT_RETRIES = 10

  const loadAll = useCallback(async (projectOverride?: string) => {
    try {
      const knownTarget = projectOverride || activeProjectRef.current

      const statsPromise = request<CodeIntelStats>('codeintel/stats')
      const treePromise = knownTarget
        ? request<ProjectTreeResponse>('codeintel/project-tree', { project_root: knownTarget })
        : null

      const data = await statsPromise
      retryCountRef.current = 0
      setBootRetrying(false)
      setStats(data)
      setError(null)

      if (data.status === 'not_initialized') {
        setIndexError(t('codeintel.not_initialized', '索引尚未初始化，等待工作区加载...'))
        return
      }
      setIndexError(null)

      const projects = data.projects ?? []
      const target = knownTarget || activeProjectRef.current || (projects.length > 0 ? projects[0].root : '')
      if (target !== activeProjectRef.current) {
        activeProjectRef.current = target
        setSelectedProject(target)
      }

      if (treePromise) {
        const treeData = await treePromise
        if (activeProjectRef.current === (knownTarget || target)) setTree(treeData)
      } else if (target) {
        const treeData = await request<ProjectTreeResponse>('codeintel/project-tree', { project_root: target })
        if (activeProjectRef.current === target) setTree(treeData)
      }

      if (target) {
        request<{ edges: ModuleDepEdge[] }>('codeintel/module-deps', { project_root: target })
          .then(resp => {
            const inDeg = new Map<string, number>()
            for (const e of resp.edges) inDeg.set(e.target_dir, (inDeg.get(e.target_dir) ?? 0) + e.weight)
            setCoreModules(
              [...inDeg.entries()]
                .filter(([p]) => p !== '.' && p !== '')
                .sort((a, b) => b[1] - a[1])
                .slice(0, 6)
                .map(([path, edgeCount]) => ({ path, edgeCount }))
            )
          })
          .catch(() => setCoreModules([]))
      }

      if (!data.indexing) {
        request<CoverageData>('codeintel/coverage', {}).then(setCoverage).catch(() => {})
      }
    } catch (e) {
      if (retryCountRef.current < MAX_BOOT_RETRIES) {
        retryCountRef.current++
        setBootRetrying(true)
        retryTimerRef.current = setTimeout(() => loadAll(projectOverride), 2000)
        return
      }
      setBootRetrying(false)
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    loadAll()
    const unsub1 = onIndexProgress((state: IndexProgressState) => {
      setStats(prev => prev ? { ...prev, indexing: true, indexed_files: state.indexed_files, total_files: state.total_files, completeness: state.completeness } : prev)
    })
    const unsub2 = onIndexComplete(() => { loadAll() })
    const unsub3 = onIndexError((state: IndexErrorState) => { setIndexError(state.error) })
    // Windows: webview may initialize with configMode=true (backend still booting).
    // When workspace folders arrive, configMode flips to false and codeintel RPCs
    // become reachable. Re-trigger loadAll so the graph doesn't stay stuck on empty.
    const unsub4 = onHasWorkspaceChange((has) => { if (has) loadAll() })
    return () => { unsub1(); unsub2(); unsub3(); unsub4(); if (retryTimerRef.current) clearTimeout(retryTimerRef.current) }
  }, [loadAll])

  const handleProjectSelect = useCallback((root: string) => {
    activeProjectRef.current = root
    setSelectedProject(root)
    setExpandedModule(null)
    setSelectedSymbol(null)
    setTree(null)
    setCoreModules([])
    loadAll(root)
  }, [loadAll])

  const handleSearch = useCallback(() => {
    if (!searchQuery.trim()) return
    setSelectedSymbol(searchQuery.trim())
    setExpandedModule(null)
  }, [searchQuery])

  const handleReindex = useCallback(async () => {
    if (!selectedProject) return
    setReindexing(true)
    setMenuOpen(false)
    try {
      await request('codeintel/reindex', { project_root: selectedProject })
      setIndexError(null)
    } catch (e) {
      setIndexError(t('codeintel.reindex_failed', '重建索引失败：{{msg}}', { msg: (e as Error).message }))
    } finally {
      setReindexing(false)
    }
  }, [t, selectedProject])

  const [confirmClear, setConfirmClear] = useState(false)

  const handleClearIndex = useCallback(async () => {
    if (!selectedProject) return
    setConfirmClear(false)
    setMenuOpen(false)
    try {
      await request('codeintel/clear', { project_root: selectedProject })
      setTree(null)
      setIndexError(null)
      void loadAll(selectedProject)
    } catch (e) {
      setIndexError(t('codeintel.clear_failed', '清除索引失败：{{msg}}', { msg: (e as Error).message }))
    }
  }, [t, selectedProject, loadAll])

  // --- Sub-views (symbol detail / impact analysis) use PageShell with back ---

  if (selectedSymbol) {
    if (fullImpact) {
      return (
        <PageShell
          icon={<Zap size={16} />}
          title={t('codeintel.impact_analysis', '影响分析')}
          subtitle={fullImpact.symbol}
          onBack={() => setFullImpact(null)}
        >
          <FullImpactView symbolName={fullImpact.symbol} filePath={fullImpact.file} onBack={() => setFullImpact(null)} />
        </PageShell>
      )
    }
    return (
      <PageShell
        icon={<FileCode2 size={16} />}
        title={selectedSymbol}
        onBack={() => setSelectedSymbol(null)}
      >
        <SymbolDetail
          symbolName={selectedSymbol}
          onOpenFullImpact={() => setFullImpact({ symbol: selectedSymbol })}
          onBack={() => setSelectedSymbol(null)}
        />
      </PageShell>
    )
  }

  // --- Pre-ready states ---

  if ((loading || bootRetrying) && !stats) {
    return (
      <PageShell icon={<Network size={16} />} title={t('codeintel.knowledge_graph', '知识图谱')}>
        <div className="flex items-center justify-center py-16">
          <Loader2 size={20} className="animate-spin mr-2 text-dim" />
          <span className="text-dim">
            {bootRetrying
              ? t('codeintel.engine_booting', '引擎正在启动，等待索引服务就绪…')
              : t('codeintel.loading_state', '加载索引状态...')}
          </span>
        </div>
      </PageShell>
    )
  }

  if (error && !stats) {
    return (
      <PageShell icon={<Network size={16} />} title={t('codeintel.knowledge_graph', '知识图谱')}>
        <p className="text-dim text-body">{t('codeintel.service_unavailable', '索引服务暂时不可用')}</p>
        <p className="text-caption text-muted mt-1">{t('codeintel.retry_later', '请稍后重试')}</p>
      </PageShell>
    )
  }

  if (indexError) {
    return (
      <PageShell icon={<Network size={16} />} title={t('codeintel.knowledge_graph', '知识图谱')}>
        <div className="flex flex-col items-center justify-center gap-3 py-16">
          <AlertTriangle size={24} className="text-warning" />
          <p className="text-body text-text">{t('codeintel.write_failed_title', '索引构建完成但写入失败')}</p>
          <p className="text-caption text-muted max-w-md text-center">
            {t('codeintel.write_failed_desc', '代码分析已完成扫描，但索引数据未能持久化。AI 仍可使用基础文件工具（read / grep）。')}
          </p>
          <div className="flex items-center gap-3 mt-1">
            <Button variant="secondary" size="sm" onClick={handleReindex}>
              {t('codeintel.reindex', '重建索引')}
            </Button>
            <Button variant="ghost" size="sm" onClick={() => setIndexError(null)}>
              {t('codeintel.dismiss', '忽略')}
            </Button>
          </div>
        </div>
      </PageShell>
    )
  }

  if (!stats || stats.status === 'not_initialized') {
    return (
      <PageShell icon={<Network size={16} />} title={t('codeintel.knowledge_graph', '知识图谱')}>
        <div className="flex flex-col items-center justify-center gap-3 py-16">
          <AlertTriangle size={24} className="text-warning" />
          <p className="text-body text-text">{t('codeintel.not_initialized_title', '代码索引尚未初始化')}</p>
          <p className="text-caption text-muted">{t('codeintel.not_initialized_desc', '请确保工作区已打开且包含有效的代码文件')}</p>
          <Button variant="ghost" size="sm" onClick={() => { setLoading(true); loadAll() }}>
            {t('codeintel.recheck', '重新检查')}
          </Button>
        </div>
      </PageShell>
    )
  }

  if (stats.indexing && stats.symbols_count === 0) {
    return (
      <PageShell icon={<Network size={16} />} title={t('codeintel.knowledge_graph', '知识图谱')}>
        <div className="flex flex-col items-center justify-center gap-3 py-16">
          <Loader2 size={24} className="animate-spin text-accent" />
          <p className="text-body text-text">{t('codeintel.building_index', '正在构建代码索引…')}</p>
          <p className="text-caption text-muted">{t('codeintel.building_hint_prefix', '首次索引')} {stats.projects?.length ?? 0} {t('codeintel.building_hint_suffix', '个项目，通常需要 1-2 分钟')}</p>
        </div>
      </PageShell>
    )
  }

  // --- Main view ---
  const projects = stats.projects ?? []
  const dependencies = stats.dependencies ?? []
  const modules = tree?.modules ?? []
  const activeProject = projects.find(p => p.root === selectedProject) ?? projects[0] ?? null

  const viewFiles = activeProject?.file_count ?? 0
  const viewSymbols = activeProject?.symbol_count ?? 0

  const viewLanguages: { language: string; file_count: number; symbol_count: number }[] =
    activeProject?.language_stats ?? []

  const blindCount = coverage ? (coverage.total_functions - coverage.functions_with_edges) : 0

  const activeProjectName = (stats?.projects ?? []).find(p => p.root === selectedProject)?.name
    ?? selectedProject.split('/').pop() ?? ''

  const headerActions = (
    <div className="relative" ref={menuRef}>
      <Button variant="ghost" size="icon-sm" onClick={() => setMenuOpen(!menuOpen)}
        aria-label={t('codeintel.index_actions', '索引操作')}>
        <MoreVertical size={14} />
      </Button>
      {menuOpen && (
        <div className="absolute right-0 top-full mt-1 w-52 rounded-lg border border-border-hi bg-surface shadow-lg z-50">
          <button onClick={handleReindex} disabled={reindexing || !selectedProject}
            className="w-full flex items-center gap-2 px-3 py-2 text-small text-text hover:bg-surface-3 active:bg-surface-hover transition-colors duration-fast disabled:opacity-50 disabled:pointer-events-none">
            <RefreshCw size={14} className={reindexing ? 'animate-spin' : ''} />
            {t('codeintel.reindex_project', '重建 {{name}} 索引', { name: activeProjectName })}
          </button>
          <button onClick={() => { setMenuOpen(false); setConfirmClear(true) }} disabled={!selectedProject}
            className="w-full flex items-center gap-2 px-3 py-2 text-small text-danger hover:bg-surface-3 active:bg-surface-hover transition-colors duration-fast disabled:opacity-50 disabled:pointer-events-none">
            <Trash2 size={14} />
            {t('codeintel.clear_project', '清除 {{name}} 索引', { name: activeProjectName })}
          </button>
        </div>
      )}
      <ConfirmDialog
        open={confirmClear}
        onOpenChange={setConfirmClear}
        title={t('codeintel.confirm_clear_title', '确认清除索引')}
        description={t('codeintel.confirm_clear_desc', '将清除 {{name}} 的全部代码索引数据，需要重新扫描才能恢复。', { name: activeProjectName })}
        confirmLabel={t('codeintel.confirm_clear_btn', '清除')}
        cancelLabel={t('codeintel.cancel', '取消')}
        variant="danger"
        onConfirm={handleClearIndex}
      />
    </div>
  )

  return (
    <PageShell
      icon={<Network size={16} />}
      title={t('codeintel.knowledge_graph', '知识图谱')}
      subtitle={`${fmtCount(stats.indexed_files ?? 0, locale)} ${t('codeintel.files', '文件')} · ${fmtCount(stats.symbols_count ?? 0, locale)} ${t('codeintel.symbols', '符号')}`}
      helpContent={{
        title: '代码知识图谱',
        description: '扫描工作区代码文件，构建符号关系图谱。AI 通过图谱精确定位代码、理解调用链和依赖关系。',
        items: [
          { label: '支持语言', values: ['Go / TypeScript / JavaScript', 'Python / Java / Rust / C++', 'C# / PHP / Ruby / Kotlin'] },
          { label: '索引内容', values: ['函数 / 方法 / 类 / 接口', '类型定义 / 变量 / 常量', '模块依赖 / 调用关系 / 继承链'] },
        ],
      }}
      actions={headerActions}
    >
      {/* AI coverage — global signal, above project tabs */}
      {coverage && !stats.indexing && (stats.indexed_files ?? 0) > 0 && (
        <div className="flex items-center gap-3 px-4 py-2.5 rounded-md border border-border bg-surface mb-5">
          <Zap size={14} className="text-accent shrink-0" />
          <span className="text-small text-dim">{t('codeintel.ai_coverage', 'AI 理解度')}:</span>
          <span className={`text-small font-medium ${
            coverage.edge_coverage > 0.7 ? 'text-success' : coverage.edge_coverage >= 0.3 ? 'text-warning' : 'text-danger'
          }`}>
            {coverage.edge_coverage > 0.7 ? t('codeintel.coverage_high', '高') : coverage.edge_coverage >= 0.3 ? t('codeintel.coverage_medium', '中') : t('codeintel.coverage_low', '低')}
            <span className="text-muted ml-1">({Math.round(coverage.edge_coverage * 100)}%)</span>
          </span>
          {blindCount > 0 && (
            <>
              <span className="text-muted">·</span>
              <span className="text-small text-warning tabular-nums">{fmtCount(blindCount, locale)} {t('codeintel.blind_spots', '盲区')}</span>
            </>
          )}
        </div>
      )}

      {/* Project tabs — only when multiple projects */}
      {projects.length > 1 && (
        <div className="flex flex-col gap-0 mb-5">
          <div className="flex items-center gap-2 flex-wrap">
            {projects.map(p => {
              const isActive = selectedProject === p.root
              const isProjectIndexing = stats.indexing && p.file_count === 0
              return (
                <button key={p.root} onClick={() => handleProjectSelect(p.root)}
                  className={`flex items-center gap-1.5 px-3 py-1.5 rounded text-small transition-colors ${
                    isActive
                      ? 'bg-accent/10 text-accent border border-accent/20'
                      : 'text-text hover:bg-surface-3 border border-border'
                  }`}>
                  <FolderGit2 size={12} className={isActive ? 'text-accent' : 'text-dim'} />
                  {p.name}
                  {isProjectIndexing && (
                    <span className="w-3 h-3 border-[1.5px] border-accent border-t-transparent rounded-full animate-spin shrink-0" />
                  )}
                </button>
              )
            })}
          </div>
          {stats.completeness < 1 && stats.completeness > 0 && (
            <div className="h-[2px] bg-surface-2 rounded-full overflow-hidden mt-2">
              <div className="h-full bg-accent rounded-full transition-all duration-700 ease-out"
                style={{ width: `${Math.min(stats.completeness * 100, 100)}%` }} />
            </div>
          )}
        </div>
      )}

      {/* Per-project summary line + language bar */}
      {viewLanguages.length > 0 && (viewFiles > 0 || viewSymbols > 0) && (
        <div className="flex flex-col gap-2 mb-6">
          <div className="flex items-center gap-4 text-caption text-dim">
            <span className="tabular-nums">{fmtCount(viewFiles, locale)} {t('codeintel.files', '文件')}</span>
            <span>·</span>
            <span>{viewLanguages.map(l => l.language).join(' · ')}</span>
            {modules.length > 0 && <><span>·</span><span>{modules.length} {t('codeintel.modules', '模块')}</span></>}
          </div>
          <div className="flex h-2 rounded-full overflow-hidden bg-surface-2">
            {viewLanguages.map(l => {
              const total = viewLanguages.reduce((s, x) => s + x.file_count, 0)
              return (
                <div key={l.language} className="h-full transition-all duration-700"
                  style={{ width: `${(l.file_count / (total || 1)) * 100}%`, backgroundColor: LANG_COLORS[l.language] ?? 'var(--color-dim)', minWidth: l.file_count > 0 ? 4 : 0 }}
                  title={`${l.language}: ${fmtCount(l.file_count, locale)} files · ${fmtCount(l.symbol_count, locale)} symbols`} />
              )
            })}
          </div>
          <div className="flex flex-wrap gap-x-4 gap-y-1">
            {viewLanguages.map(l => (
              <div key={l.language} className="flex items-center gap-1.5 text-caption text-dim">
                <span className="w-2.5 h-2.5 rounded-sm inline-block" style={{ backgroundColor: LANG_COLORS[l.language] ?? 'var(--color-dim)' }} />
                <span className="font-medium">{l.language}</span>
                <span className="text-muted tabular-nums">{fmtCount(l.file_count, locale)}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      <div className="mb-4">
        <SearchInput
          value={searchQuery}
          onChange={setSearchQuery}
          onSearch={handleSearch}
          placeholder={t('codeintel.search_symbol', '搜索符号...')}
        />
      </div>

      {/* Empty state */}
      {viewFiles === 0 && viewSymbols === 0 && !stats.indexing && (
        <div className="flex flex-col items-center justify-center py-16 text-center">
          <div className="w-12 h-12 rounded-md bg-surface-2 flex items-center justify-center mb-3">
            <FolderGit2 size={20} className="text-dim" />
          </div>
          <p className="text-body text-text">{t('codeintel.empty_title', '暂无代码文件')}</p>
          <p className="text-small text-dim mt-1 max-w-sm">
            {t('codeintel.empty_desc', '打开包含代码的项目文件夹后，知识图谱会自动构建索引')}
          </p>
        </div>
      )}

      {/* Per-project indexing state */}
      {viewFiles === 0 && viewSymbols === 0 && stats.indexing && (
        <div className="flex flex-col items-center justify-center py-12 text-center">
          <Loader2 size={24} className="animate-spin text-accent mb-3" />
          <p className="text-body text-text">{t('codeintel.project_building', '正在为此项目构建索引…')}</p>
          <p className="text-caption text-dim mt-1">{t('codeintel.project_building_hint', '索引完成后将自动显示模块结构')}</p>
        </div>
      )}

      {/* Index progress — single-project mode */}
      {projects.length <= 1 && stats.completeness < 1 && stats.completeness > 0 && (
        <div className="flex items-center gap-3 mb-6">
          <Loader2 size={14} className="animate-spin text-accent shrink-0" />
          <div className="flex-1 flex flex-col gap-0.5">
            <div className="flex items-center justify-between text-caption text-dim">
              <span>{t('codeintel.index_progress', '索引进度')}</span>
              <span className="tabular-nums">{Math.round(stats.completeness * 100)}%</span>
            </div>
            <div className="h-[2px] bg-surface-2 rounded-full overflow-hidden">
              <div className="h-full bg-accent rounded-full transition-all duration-700 ease-out"
                style={{ width: `${Math.min(stats.completeness * 100, 100)}%` }} />
            </div>
          </div>
        </div>
      )}

      {/* Core modules */}
      {coreModules.length > 0 && viewFiles > 0 && (
        <div className="mb-6">
          <h3 className="text-h3 text-text mb-3">{t('codeintel.core_modules', '核心模块')}</h3>
          <div className="flex flex-col gap-1">
            {coreModules.map((mod, i) => {
              const maxEdges = coreModules[0]?.edgeCount ?? 1
              const barWidth = (mod.edgeCount / maxEdges) * 100
              return (
                <div key={mod.path}
                  className="flex items-center gap-3 px-3 py-2 rounded-md border border-border hover:bg-surface-3 transition-colors relative overflow-hidden cursor-pointer"
                  onClick={() => setExpandedModule(expandedModule === mod.path ? null : mod.path)}>
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

      {/* Module list */}
      {modules.length > 0 && viewFiles > 0 && (
        <div className="mb-6">
          <h3 className="text-h3 text-text mb-3">{t('codeintel.all_modules', '全部模块')} ({modules.length})</h3>
          <div className="border border-border rounded-md overflow-hidden">
            {modules.map(m => {
              const isOpen = expandedModule === m.path
              const hasSymbols = (m.top_symbols ?? []).length > 0
              return (
                <div key={m.path} className="border-b border-border last:border-b-0">
                  <button onClick={() => setExpandedModule(isOpen ? null : m.path)}
                    className={`flex items-center gap-2 w-full px-3 py-2.5 text-left transition-colors ${
                      isOpen ? 'bg-surface-2' : 'hover:bg-surface-3'
                    }`}>
                    <span className="w-4 h-4 flex items-center justify-center shrink-0">
                      {hasSymbols
                        ? (isOpen ? <ChevronDown size={14} className="text-dim" /> : <ChevronRight size={14} className="text-dim" />)
                        : <span className="w-1 h-1 rounded-sm bg-border" />}
                    </span>
                    <span className="text-small text-text truncate font-mono min-w-0">
                      {m.path === '.' ? '(root)' : m.path}
                    </span>
                    <span className="text-caption text-muted ml-auto tabular-nums shrink-0">{fmtCount(m.file_count, locale)} files</span>
                    <div className="flex gap-0.5 ml-1 shrink-0">
                      {(m.languages ?? []).slice(0, 3).map(lang => (
                        <span key={lang} className="w-2 h-2 rounded-sm"
                          style={{ backgroundColor: LANG_COLORS[lang] ?? 'var(--color-dim)' }} title={lang} />
                      ))}
                    </div>
                  </button>
                  {isOpen && hasSymbols && (
                    <div className="px-3 py-2 bg-surface border-t border-border">
                      <div className="grid gap-0.5">
                        {(m.top_symbols ?? []).map((sym, i) => {
                          const style = SYMBOL_STYLE[sym.kind] ?? DEFAULT_SYMBOL_STYLE
                          const Icon = style.icon
                          return (
                            <button key={i}
                              onClick={() => setSelectedSymbol(sym.name)}
                              onDoubleClick={() => openFile(sym.name)}
                              className="flex items-center gap-2 py-1 px-2 rounded hover:bg-surface-3 transition-colors text-left">
                              <Icon size={14} className={`${style.color} shrink-0`} />
                              <span className="text-small text-text font-mono truncate">{sym.name}</span>
                              <span className="text-caption text-muted ml-auto shrink-0">{sym.kind}</span>
                            </button>
                          )
                        })}
                      </div>
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        </div>
      )}

      {/* Cross-project dependencies — embedded in project view */}
      {dependencies.length > 0 && viewFiles > 0 && (
        <div className="mb-6">
          <h3 className="text-h3 text-text mb-3">{t('codeintel.inter_project_deps', '跨项目依赖')}</h3>
          <div className="flex flex-wrap gap-2">
            {dependencies.map((dep, i) => (
              <div key={i} className="flex items-center gap-1.5 px-2.5 py-1 rounded bg-surface-2 text-caption">
                <span className="text-text font-mono">{dep.from}</span>
                <ArrowRight size={10} className="text-dim" />
                <span className="text-text font-mono">{dep.to}</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </PageShell>
  )
}
