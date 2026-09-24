import { useState, useEffect, useCallback, useRef, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import {
  RefreshCw, FileText, Database, AlertCircle, CheckCircle2, Loader2, Book,
  MoreVertical, Trash2, ShieldAlert, RotateCcw, X,
} from 'lucide-react'
import { PageShell, ConfirmDialog } from '@wesui/layout'
import { SearchInput } from '@wesui/forms'
import { Button } from '@wesui/primitives'
import { openFile, request } from '@/bridge'
import { Toast, useToast } from '@/components/ui/Toast'
import { knowledgeApi, type KBFileInfo, type KBStats } from '@/lib/api/knowledge'
import { formatFileSize } from '@wesui/lib/format'
import { tLabel } from '@wesui/lib/tk'
import { useLocale } from '@wesui/locale'
import { FILE_CATEGORY_LABELS, asFileCategory } from '@wesui/knowledge'

function statusIcon(status: string) {
  switch (status) {
    case 'ready': return <CheckCircle2 size={14} className="text-success" />
    case 'indexing': case 'pending': return <Loader2 size={14} className="animate-spin text-accent" />
    case 'error': case 'quarantined': return <AlertCircle size={14} className="text-danger" />
    default: return <FileText size={14} className="text-dim" />
  }
}

function statusLabel(status: string, t: (k: string, d: string) => string): string {
  switch (status) {
    case 'ready': return t('kb.status_ready', '已就绪')
    case 'indexing': return t('kb.status_indexing', '索引中')
    case 'pending': return t('kb.status_pending', '等待索引')
    case 'error': return t('kb.status_error', '索引失败')
    case 'quarantined': return t('kb.status_quarantined', '待修复')
    default: return status
  }
}

export function KnowledgePage() {
  const { t } = useTranslation()
  const { t: tUi } = useLocale()

  // 表在 wesui（`knowledge/labels.ts`），这里只取。曾经本文件自带一份同键的
  // CATEGORY_I18N，只存键不存中文——于是 `tUi(key)` 少了第二参，英文表里查不到
  // 时渲染出的是键本身（`knowledge.type_pdf`）。
  const categoryLabel = useCallback((cat: string) => {
    return tLabel(tUi, FILE_CATEGORY_LABELS[asFileCategory(cat) ?? 'other'])
  }, [tUi])
  const { toast, showToast } = useToast()
  const [loading, setLoading] = useState(true)
  const [files, setFiles] = useState<KBFileInfo[]>([])
  const [stats, setStats] = useState<KBStats | null>(null)
  const [searchQuery, setSearchQuery] = useState('')
  const [rescanning, setRescanning] = useState(false)
  const [fixingQuarantined, setFixingQuarantined] = useState(false)
  const [statusFilter, setStatusFilter] = useState<string>('')
  const [reindexingIds, setReindexingIds] = useState<Set<string>>(new Set())
  const [fixingErrors, setFixingErrors] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [list, st] = await Promise.all([
        knowledgeApi.list(),
        knowledgeApi.stats(),
      ])
      setFiles(list ?? [])
      setStats(st ?? null)
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      if (!msg.includes('no workspace') && !msg.includes('not initialized') && !msg.includes('backend not')) {
        showToast(t('kb.load_error', '加载失败'), false)
      }
    } finally {
      setLoading(false)
    }
  }, [showToast, t])

  useEffect(() => { void load() }, [load])

  const handleRescan = useCallback(async () => {
    setRescanning(true)
    try {
      const result = await knowledgeApi.rescan()
      showToast(t('kb.rescan_done', {
        defaultValue: '刷新完成：{{ingested}} 文件索引，{{orphaned}} 孤儿清理',
        ingested: result.ingested, orphaned: result.orphaned,
      }))
      void load()
    } catch {
      showToast(t('kb.rescan_failed', '刷新失败'), false)
    } finally {
      setRescanning(false)
    }
  }, [showToast, t, load])

  const handleFixQuarantined = useCallback(async () => {
    setFixingQuarantined(true)
    setMenuOpen(false)
    try {
      const result = await knowledgeApi.reindexQuarantined()
      showToast(t('kb.fix_quarantined_done', { defaultValue: '已重新索引 {{count}} 个隔离文件', count: result.count }))
      void load()
    } catch {
      showToast(t('kb.fix_quarantined_failed', '修复失败'), false)
    } finally {
      setFixingQuarantined(false)
    }
  }, [showToast, t, load])

  const handleFixErrors = useCallback(async () => {
    setFixingErrors(true)
    setMenuOpen(false)
    try {
      const result = await knowledgeApi.reindexErrors()
      showToast(t('kb.fix_errors_done', { defaultValue: '已重新索引 {{count}} 个错误文件', count: result.count }))
      void load()
    } catch {
      showToast(t('kb.fix_errors_failed', '重新索引失败'), false)
    } finally {
      setFixingErrors(false)
    }
  }, [showToast, t, load])

  const handleReindexFile = useCallback(async (fileId: string) => {
    setReindexingIds(prev => new Set(prev).add(fileId))
    try {
      await knowledgeApi.reindexFile(fileId)
      showToast(t('kb.reindex_file_done', '已重新索引'))
      void load()
    } catch {
      showToast(t('kb.reindex_file_failed', '重新索引失败'), false)
    } finally {
      setReindexingIds(prev => { const n = new Set(prev); n.delete(fileId); return n })
    }
  }, [showToast, t, load])

  const handleDeleteFile = useCallback(async (fileId: string) => {
    try {
      await knowledgeApi.deleteFile(fileId)
      setFiles(prev => prev.filter(f => f.id !== fileId))
      showToast(t('kb.delete_file_done', '已从索引中移除'))
    } catch {
      showToast(t('kb.delete_file_failed', '移除失败'), false)
    }
  }, [showToast, t])

  const handleClear = useCallback(async () => {
    setConfirmClear(false)
    setMenuOpen(false)
    try {
      await request('sidebar/kbClear')
      setFiles([])
      setStats(null)
      showToast(t('kb.clear_done', '知识库已清除'))
    } catch {
      showToast(t('kb.clear_failed', '清除失败'), false)
    }
  }, [showToast, t])

  const [menuOpen, setMenuOpen] = useState(false)
  const [confirmClear, setConfirmClear] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)
  const [searchResults, setSearchResults] = useState<KBFileInfo[] | null>(null)

  useEffect(() => {
    if (!menuOpen) return
    const handleClickOutside = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setMenuOpen(false)
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [menuOpen])

  useEffect(() => {
    const q = searchQuery.trim()
    if (!q) { setSearchResults(null); return }
    if (q.length < 2) {
      setSearchResults(files.filter(f =>
        f.name.toLowerCase().includes(q.toLowerCase()) ||
        f.path.toLowerCase().includes(q.toLowerCase()),
      ))
      return
    }
    const timer = setTimeout(async () => {
      try {
        const results = await knowledgeApi.search(q)
        if (results && results.length > 0) {
          const matchedIds = new Set(results.map(r => r.file_id))
          setSearchResults(files.filter(f => matchedIds.has(f.id)))
        } else {
          setSearchResults(files.filter(f =>
            f.name.toLowerCase().includes(q.toLowerCase()) ||
            f.path.toLowerCase().includes(q.toLowerCase()),
          ))
        }
      } catch {
        setSearchResults(files.filter(f =>
          f.name.toLowerCase().includes(q.toLowerCase()) ||
          f.path.toLowerCase().includes(q.toLowerCase()),
        ))
      }
    }, 300)
    return () => clearTimeout(timer)
  }, [searchQuery, files])

  const statusFiltered = statusFilter
    ? (searchResults ?? files).filter(f => f.status === statusFilter)
    : (searchResults ?? files)
  const filteredFiles = statusFiltered

  const handleOpenFile = useCallback((path: string) => {
    openFile(path)
  }, [])

  const headerActions = (
    <div className="relative" ref={menuRef}>
      <Button variant="ghost" size="icon-sm" onClick={() => setMenuOpen(!menuOpen)}
        aria-label={t('kb.actions', '知识库操作')}>
        <MoreVertical size={14} />
      </Button>
      {menuOpen && (
        <div className="absolute right-0 top-full mt-1 w-48 rounded-lg border border-border-hi bg-surface shadow-lg z-50">
          <button onClick={() => { setMenuOpen(false); void handleRescan() }} disabled={rescanning}
            className="w-full flex items-center gap-2 px-3 py-2 text-small text-text hover:bg-surface-3 active:bg-surface-hover transition-colors duration-fast disabled:opacity-50 disabled:pointer-events-none">
            <RefreshCw size={14} className={rescanning ? 'animate-spin' : ''} />{t('kb.rescan', '刷新知识库')}
          </button>
          {(stats?.error_files ?? 0) > 0 && (
            <button onClick={handleFixErrors} disabled={fixingErrors}
              className="w-full flex items-center gap-2 px-3 py-2 text-small text-text hover:bg-surface-3 active:bg-surface-hover transition-colors duration-fast disabled:opacity-50 disabled:pointer-events-none">
              <RotateCcw size={14} className={fixingErrors ? 'animate-spin' : ''} />{t('kb.fix_errors', '重新索引错误文件')}
            </button>
          )}
          {(stats?.quarantined_files ?? 0) > 0 && (
            <button onClick={handleFixQuarantined} disabled={fixingQuarantined}
              className="w-full flex items-center gap-2 px-3 py-2 text-small text-text hover:bg-surface-3 active:bg-surface-hover transition-colors duration-fast disabled:opacity-50 disabled:pointer-events-none">
              <ShieldAlert size={14} />{t('kb.fix_quarantined', '修复隔离文件')}
            </button>
          )}
          <button onClick={() => { setMenuOpen(false); setConfirmClear(true) }}
            className="w-full flex items-center gap-2 px-3 py-2 text-small text-danger hover:bg-surface-3 active:bg-surface-hover transition-colors duration-fast">
            <Trash2 size={14} />{t('kb.clear', '清除知识库')}
          </button>
        </div>
      )}
      <ConfirmDialog
        open={confirmClear}
        onOpenChange={setConfirmClear}
        title={t('kb.confirm_clear_title', '确认清除知识库')}
        description={t('kb.confirm_clear_desc', '将清除全部文档索引数据，需要重新扫描才能恢复。')}
        confirmLabel={t('kb.confirm_clear_btn', '清除')}
        cancelLabel={t('kb.cancel', '取消')}
        variant="danger"
        onConfirm={handleClear}
      />
    </div>
  )

  return (
    <PageShell
      icon={<Book size={16} />}
      title={t('kb.title', '知识库')}
      subtitle={t('kb.subtitle_auto', '自动索引工作区文档 · AgenticRAG 检索')}
      helpContent={{
        title: '知识库',
        description: '自动索引工作区内的文档文件，AI 通过 AgenticRAG 在对话中按需检索相关内容。',
        items: [
          { label: '索引范围', values: ['.md / .markdown / .rst / .txt / .adoc', 'README / CHANGELOG / LICENSE 等文档'] },
          { label: '不索引', values: ['代码文件（归知识图谱 CKG）', '.yaml / .json / .toml（AI 按需读取）', 'node_modules / .git / vendor / 二进制文件'] },
        ],
      }}
      actions={headerActions}
    >
      {stats && !loading && (
        <div className="grid gap-2 mb-6" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(100px, 1fr))' }}>
          <StatCard
            icon={<FileText size={16} />}
            label={t('kb.stat_files', '文档文件')}
            value={String(stats.total_files ?? 0)}
            onClick={statusFilter ? () => setStatusFilter('') : undefined}
            active={!statusFilter}
          />
          <StatCard
            icon={<Database size={16} />}
            label={t('kb.stat_chunks', '索引分块')}
            value={String(stats.total_chunks ?? 0)}
          />
          <StatCard
            icon={<CheckCircle2 size={16} />}
            label={t('kb.stat_ready', '已就绪')}
            value={String(stats.ready_files ?? 0)}
            onClick={() => setStatusFilter(statusFilter === 'ready' ? '' : 'ready')}
            active={statusFilter === 'ready'}
          />
          <StatCard
            icon={<AlertCircle size={16} />}
            label={t('kb.stat_errors', '索引错误')}
            value={String(stats.error_files ?? 0)}
            accent={stats.error_files > 0}
            onClick={stats.error_files > 0 ? () => setStatusFilter(statusFilter === 'error' ? '' : 'error') : undefined}
            active={statusFilter === 'error'}
          />
          {(stats.quarantined_files ?? 0) > 0 && (
            <StatCard
              icon={<ShieldAlert size={16} />}
              label={t('kb.stat_quarantined', '待修复')}
              value={String(stats.quarantined_files)}
              accent
              onClick={() => setStatusFilter(statusFilter === 'quarantined' ? '' : 'quarantined')}
              active={statusFilter === 'quarantined'}
            />
          )}
        </div>
      )}

      {files.length > 3 && (
        <div className="mb-4">
          <SearchInput
            value={searchQuery}
            onChange={setSearchQuery}
            placeholder={t('kb.search_placeholder', '搜索文件名或文档内容...')}
          />
        </div>
      )}

      {loading ? (
        <div className="flex items-center justify-center py-16">
          <Loader2 size={20} className="animate-spin text-dim" />
        </div>
      ) : files.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-16 text-center">
          <div className="w-12 h-12 rounded-md bg-surface-2 flex items-center justify-center mb-3">
            <Book size={20} className="text-dim" />
          </div>
          <p className="text-body text-text">{t('kb.empty_auto', '暂无文档文件')}</p>
          <p className="text-small text-dim mt-1 max-w-sm">
            {t('kb.empty_hint_auto', '打开包含 .md / .yaml / .txt 等文档的项目后，知识库会自动索引')}
          </p>
        </div>
      ) : filteredFiles.length === 0 ? (
        <p className="text-small text-dim py-8 text-center">
          {t('kb.no_match', '无匹配文件')}
        </p>
      ) : (
        <div className="border border-border rounded-md overflow-hidden">
          <table className="w-full table-fixed">
            <thead>
              <tr className="bg-surface-2 text-caption text-dim border-b border-border">
                <th className="text-left px-3 py-2 font-normal w-[22%]">{t('kb.col_name', '文件名')}</th>
                <th className="text-left px-3 py-2 font-normal">{t('kb.col_path', '路径')}</th>
                <th className="text-left px-3 py-2 font-normal w-14">{t('kb.col_type', '类型')}</th>
                <th className="text-right px-3 py-2 font-normal w-14">{t('kb.col_size', '大小')}</th>
                <th className="text-right px-3 py-2 font-normal w-12">{t('kb.col_chunks', '分块')}</th>
                <th className="text-center px-3 py-2 font-normal w-16">{t('kb.col_status', '状态')}</th>
                <th className="text-center px-3 py-2 font-normal w-16">{t('kb.col_actions', '操作')}</th>
              </tr>
            </thead>
            <tbody>
              {filteredFiles.map(f => {
                const hasError = f.status === 'error' || f.status === 'quarantined'
                return (
                <tr
                  key={f.id}
                  className="border-b border-border last:border-b-0 hover:bg-surface-3 transition-colors duration-fast cursor-pointer group"
                  onClick={() => handleOpenFile(f.path)}
                >
                  <td className="px-3 py-2 text-small text-text font-medium truncate" title={f.name}>{f.name}</td>
                  <td className="px-3 py-2 text-caption text-dim truncate" title={f.path}>{shortenPath(f.path)}</td>
                  <td className="px-3 py-2 whitespace-nowrap">
                    <span className="text-caption px-1.5 py-0.5 rounded-sm bg-surface-2 text-text-secondary">
                      {categoryLabel(f.category)}
                    </span>
                  </td>
                  <td className="px-3 py-2 text-caption text-dim text-right whitespace-nowrap">{formatFileSize(f.size)}</td>
                  <td className="px-3 py-2 text-caption text-dim text-right whitespace-nowrap">{f.chunk_count}</td>
                  <td className="px-3 py-2 whitespace-nowrap">
                    <div className="flex items-center justify-center gap-1" title={hasError && f.error_message ? f.error_message : undefined}>
                      {statusIcon(f.status)}
                      <span className="text-caption text-text-secondary">{statusLabel(f.status, t)}</span>
                    </div>
                    {hasError && f.error_message && (
                      <p className="text-caption text-danger mt-0.5 truncate max-w-[200px]" title={f.error_message}>{f.error_message}</p>
                    )}
                  </td>
                  <td className="px-3 py-2 whitespace-nowrap" onClick={e => e.stopPropagation()}>
                    <div className="flex items-center justify-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity duration-fast">
                      {hasError && (
                        <button
                          onClick={() => handleReindexFile(f.id)}
                          disabled={reindexingIds.has(f.id)}
                          className="w-6 h-6 rounded flex items-center justify-center text-dim hover:text-accent hover:bg-accent-soft transition-all duration-fast disabled:opacity-50"
                          title={t('kb.action_reindex', '重新索引')}
                        >
                          <RotateCcw size={12} className={reindexingIds.has(f.id) ? 'animate-spin' : ''} />
                        </button>
                      )}
                      <button
                        onClick={() => handleDeleteFile(f.id)}
                        className="w-6 h-6 rounded flex items-center justify-center text-dim hover:text-danger hover:bg-danger-soft transition-all duration-fast"
                        title={t('kb.action_delete', '移除索引')}
                      >
                        <X size={12} />
                      </button>
                    </div>
                  </td>
                </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {toast && <Toast msg={toast.msg} ok={toast.ok} />}
    </PageShell>
  )
}

function StatCard({ icon, label, value, accent, onClick, active }: {
  icon: ReactNode
  label: string
  value: string
  accent?: boolean
  onClick?: () => void
  active?: boolean
}) {
  return (
    <div
      className={`rounded-md border bg-surface p-3 transition-all duration-fast ${
        active ? 'border-accent ring-1 ring-accent/30' : 'border-border'
      } ${onClick ? 'cursor-pointer hover:bg-surface-3 active:bg-surface-hover' : ''}`}
      onClick={onClick}
    >
      <div className={`flex items-center gap-1.5 mb-1 ${accent ? 'text-danger' : 'text-dim'}`}>
        {icon}
        <span className="text-caption">{label}</span>
      </div>
      <p className={`text-h2 ${accent ? 'text-danger' : 'text-text'}`}>{value}</p>
    </div>
  )
}

function shortenPath(path: string): string {
  const parts = path.split('/')
  if (parts.length <= 3) return path
  return '.../' + parts.slice(-3).join('/')
}
