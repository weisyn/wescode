import { useState, useEffect, useCallback } from 'react'
import { Plus, Loader2, Clock } from 'lucide-react'
import * as Dialog from '@radix-ui/react-dialog'
import { PageShell } from '@wesui/layout'
import { Toast, useToast, Button } from '@wesui/primitives'
import { TaskList, RunsList, TaskFormPanel } from '@wesui/cron'
import type { CronEntry, CronJobState, CronRunRecord, CreateCronEntry } from '@wesui/cron'
import type { ModelPickerOption } from '@wesui/chat'
import { cronApi } from '@/lib/api/cron'
import { onCronRunFinished } from '@/bridge'
import type { AvailableModelsPayload } from '@/lib/orgModels'
import { useTranslation } from '@/lib/i18n'
import { request, navigate as bridgeNavigate } from '@/bridge'

interface CronAgent { id: string; name: string }

async function fetchAgents(): Promise<CronAgent[]> {
  try {
    const list = await request<Array<{ id: string; name: string }>>('sidebar/listAgents') ?? []
    return list.map(a => ({ id: a.id, name: a.name }))
  } catch { return [] }
}

/**
 * Same `availableModels` pipeline the Chat picker reads (INV-PROVIDER-VIEW-01),
 * so a task offers exactly the models the user can talk to. A task's model is
 * chosen here and stored on the task — the scheduler must not pick one when the
 * job fires, or the user cannot tell which model their task runs on.
 */
async function fetchModels(): Promise<ModelPickerOption[]> {
  try {
    const payload = await request<AvailableModelsPayload>('availableModels')
    return (payload?.models ?? []).map(m => ({
      id: m.id,
      label: `${m.providerLabel} · ${m.model}`,
      modelLabel: m.model,
      source: m.source,
      disabled: m.status !== 'available',
    }))
  } catch { return [] }
}

export function CronPage({ autoCreate }: { autoCreate?: boolean } = {}) {
  const { t } = useTranslation()
  const { toast, showToast } = useToast()

  const [jobs, setJobs] = useState<CronEntry[]>([])
  const [states, setStates] = useState<Record<string, CronJobState>>({})
  const [agents, setAgents] = useState<CronAgent[]>([])
  const [models, setModels] = useState<ModelPickerOption[]>([])
  const [runs, setRuns] = useState<CronRunRecord[]>([])
  const [loading, setLoading] = useState(true)

  const [editEntry, setEditEntry] = useState<CronEntry | null>(null)
  const [formOpen, setFormOpen] = useState(!!autoCreate)
  const [runsJobId, setRunsJobId] = useState<string | null>(null)

  const [loadOk, setLoadOk] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [list, a, m] = await Promise.all([cronApi.list(), fetchAgents(), fetchModels()])
      setJobs(list.jobs ?? [])
      setStates(list.states ?? {})
      setAgents(a)
      setModels(m)
      setLoadOk(true)
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      if (!msg.includes('no workspace') && !msg.includes('not initialized') && !msg.includes('backend not')) {
        showToast(t('cron.load_error', '加载失败'), 'error')
      }
    } finally {
      setLoading(false)
    }
  }, [t, showToast])

  useEffect(() => {
    void load()
    if (!loadOk) {
      const timer = setTimeout(() => { void load() }, 3000)
      return () => clearTimeout(timer)
    }
  }, [load, loadOk])

  // Auto-refresh when a scheduled run finishes — the page would otherwise
  // show stale state until the user navigates away and back. This is the
  // CronPage counterpart of chatViewPane's _loadConversationMessages on
  // onDidCronRunFinished.
  useEffect(() => {
    return onCronRunFinished((_e) => {
      void load()
    })
  }, [load])

  const loadRuns = useCallback(async (jobId: string) => {
    try {
      const r = await cronApi.listRuns(jobId)
      setRuns(r)
    } catch {
      showToast(t('cron.load_error', '加载失败'), 'error')
    }
  }, [t, showToast])

  const handleEnable = async (id: string) => {
    try { await cronApi.enable(id); void load() }
    catch { showToast(t('cron.toast_toggle_failed', '操作失败'), 'error') }
  }
  const handleDisable = async (id: string) => {
    try { await cronApi.disable(id); void load() }
    catch { showToast(t('cron.toast_toggle_failed', '操作失败'), 'error') }
  }
  const handleRemove = async (id: string) => {
    try { await cronApi.remove(id); void load() }
    catch { showToast(t('cron.toast_delete_failed', '删除失败'), 'error') }
  }
  const handleTrigger = async (id: string) => {
    try {
      await cronApi.trigger(id)
      showToast(t('cron.toast_triggered', '已触发'))
      if (runsJobId === id) {
        setTimeout(() => void loadRuns(id), 2000)
      }
    } catch { showToast(t('cron.toast_trigger_failed', '触发失败'), 'error') }
  }

  const handleNavigateChat = (path: string) => {
    const sessionId = path.replace(/^\/c\//, '')
    bridgeNavigate('openChatSession', { sessionId })
  }

  const openCreate = () => { setEditEntry(null); setFormOpen(true) }
  const openEdit = (e: CronEntry) => { setEditEntry(e); setFormOpen(true) }
  const closeForm = () => { setFormOpen(false); setEditEntry(null) }

  const headerActions = (
    <Button size="sm" variant="secondary" onClick={openCreate}>
      <Plus size={14} />{t('cron.new_task', '新建任务')}
    </Button>
  )

  return (
    <PageShell
      icon={<Clock size={16} />}
      title={t('cron.page_title', '定时任务')}
      subtitle={t('cron.page_subtitle', '让智能体按计划自动执行')}
      helpContent={{
        title: '定时任务',
        description: '让 AI 按设定的时间自动执行任务，无需手动触发。',
        items: [
          { label: '调度方式', values: ['Cron 表达式（0 9 * * 1）', '自然语言间隔（每 2 小时）', '一次性定时（指定时间点）'] },
          { label: '适用场景', values: ['每日代码审查 / 安全扫描', '定期生成周报 / 项目总结', '监控日志 / 异常告警'] },
        ],
      }}
      actions={headerActions}
    >
      {loading ? (
        <div className="flex items-center gap-2 py-16 text-small text-dim justify-center">
          <Loader2 size={14} className="animate-spin" />{t('cron.loading', '加载中…')}
        </div>
      ) : (
        <div className="space-y-8">
          <div className="max-w-5xl mx-auto">
            <TaskList
              jobs={jobs}
              states={states}
              agents={agents}
              onEdit={openEdit}
              onViewRuns={(e) => { setRunsJobId(e.id); void loadRuns(e.id) }}
              onEnable={handleEnable}
              onDisable={handleDisable}
              onRemove={handleRemove}
              onTrigger={handleTrigger}
              onNavigateChat={handleNavigateChat}
            />
          </div>

          {runsJobId && (
            <div className="max-w-5xl mx-auto rounded-md border border-border overflow-hidden">
              <RunsList
                runs={runs}
                showAllLabel
                onNavigateSession={(key) => bridgeNavigate('openChatSession', { sessionId: key })}
              />
            </div>
          )}
        </div>
      )}

      <Dialog.Root open={formOpen} onOpenChange={(open) => { if (!open) closeForm() }}>
        <Dialog.Portal>
          <Dialog.Overlay className="fixed inset-0 bg-black/50 backdrop-blur-sm z-50" />
          <Dialog.Content
            className="fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 z-50 w-full max-w-lg max-h-[90vh] overflow-y-auto bg-surface border border-border rounded-lg shadow-lg focus:outline-none"
            aria-describedby={undefined}
          >
            <Dialog.Title className="sr-only">
              {editEntry ? t('cron.edit_task', '编辑任务') : t('cron.new_task', '新建任务')}
            </Dialog.Title>
            <TaskFormPanel
              entry={editEntry ?? undefined}
              agents={agents}
              models={models}
              onSave={async (data) => {
                if (editEntry) await cronApi.update(editEntry.id, data)
                else await cronApi.add(data as CreateCronEntry)
              }}
              onClose={closeForm}
              onSuccess={() => { closeForm(); void load() }}
            />
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>

      {toast && <Toast msg={toast.msg} type={toast.type} />}
    </PageShell>
  )
}
