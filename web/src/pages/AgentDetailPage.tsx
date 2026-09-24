import { useEffect, useState, useCallback } from 'react'
import { useTranslation } from '@/lib/i18n'
import { useNav } from '@/lib/nav'
import { navigate as bridgeNavigate, request } from '@/bridge'
import { Bot, Pencil, Trash2, MessageSquare, MessageCircle, Settings2, Brain, Shield, ChevronDown } from 'lucide-react'
import {
  AgentHeroCard,
  AgentConfigView,
  AgentConversationList,
  AgentMemoryList,
  AgentEditSlidePanel,
  AgentIdentityFields,
  SuggestionChipsEditor,
  SkillBindingSelector,
  ModelOverrideSelect,
  ToolPolicyEditor,
  type AgentSummary,
  type SkillOption,
  type ModelOption,
  type ToolOption,
} from '@wesui/agent'
import { PageShell } from '@wesui/layout'
import { Button } from '@wesui/primitives'
import { Toast, useToast } from '@/components/ui/Toast'
import {
  listAgents, updateAgent, deleteAgent, listConversations, listSkills, listTools, listAvailableModels,
  type AgentItem, type ConversationItem, type SkillItem, type ToolItem,
} from '@/lib/api/agents'
import { listMemoryByAgent } from '@/lib/api/data'
import type { MemoryEntryWire } from '@wesui/memory'

export interface AgentDetailPageProps {
  agentId: string
}

const splitList = (v: string | undefined): string[] | undefined =>
  v === undefined ? undefined : v.split(/[,，、]/).map(s => s.trim()).filter(Boolean)

const joinList = (v: string[] | undefined): string =>
  v ? v.join(', ') : ''

export function AgentDetailPage({ agentId }: AgentDetailPageProps) {
  const { t } = useTranslation()
  const navigate = useNav()
  const { toast, showToast } = useToast()

  const [agent, setAgent] = useState<AgentItem | null>(null)
  const [editing, setEditing] = useState(false)
  const [name, setName] = useState('')
  const [role, setRole] = useState('')
  const [goal, setGoal] = useState('')
  const [intent, setIntent] = useState('')
  const [expertise, setExpertise] = useState('')
  const [description, setDescription] = useState('')
  const [systemPrompt, setSystemPrompt] = useState('')
  const [suggestions, setSuggestions] = useState<string[]>([])
  const [skillBindings, setSkillBindings] = useState<string[] | undefined>(undefined)
  const [modelOverride, setModelOverride] = useState<string | undefined>(undefined)
  const [toolAllow, setToolAllow] = useState<string[] | undefined>(undefined)
  const [toolDeny, setToolDeny] = useState<string[] | undefined>(undefined)
  const [maxTurns, setMaxTurns] = useState('')
  const [timeoutSeconds, setTimeoutSeconds] = useState('')
  const [workspaceAccess, setWorkspaceAccess] = useState<string | undefined>(undefined)
  const [workspaceAllowPaths, setWorkspaceAllowPaths] = useState('')
  const [delegationChildDeny, setDelegationChildDeny] = useState('')
  const [headless, setHeadless] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [activeTab, setActiveTab] = useState('config')
  const [memory, setMemory] = useState<MemoryEntryWire[]>([])
  const [conversations, setConversations] = useState<ConversationItem[]>([])
  const [skills, setSkills] = useState<SkillOption[]>([])
  const [tools, setTools] = useState<ToolOption[]>([])
  const [models, setModels] = useState<ModelOption[]>([])

  useEffect(() => {
    void listAgents().then(agents => {
      const found = agents.find(a => a.id === agentId)
      if (found) {
        setAgent(found)
        setName(found.name ?? '')
        setRole(found.role ?? '')
        setGoal(found.goal ?? '')
        setIntent(found.intent ?? '')
        setExpertise(joinList(found.expertise))
        setDescription(found.description ?? '')
        setSystemPrompt(found.systemPrompt ?? '')
        setSuggestions(found.suggestions ?? [])
        setSkillBindings(found.skillBindings)
        setModelOverride(found.modelOverride)
        setToolAllow(found.toolAllow)
        setToolDeny(found.toolDeny)
        setMaxTurns(found.maxTurns ? String(found.maxTurns) : '')
        setTimeoutSeconds(found.timeoutSeconds ? String(found.timeoutSeconds) : '')
        setWorkspaceAccess(found.workspaceAccess)
        setWorkspaceAllowPaths(joinList(found.workspaceAllowPaths))
        setDelegationChildDeny(joinList(found.delegationChildDenyTools))
        setHeadless(!!found.headless)
      }
    }).catch(() => showToast(t('agent.load_failed', '加载失败'), false))
  }, [agentId, showToast, t])

  // Load data sources for the advanced editor when the panel opens.
  useEffect(() => {
    if (!editing) return
    void listSkills().then((items: SkillItem[]) => {
      setSkills(items.map(s => ({ name: s.slug, description: s.name, enabled: s.enabled })))
    }).catch(() => setSkills([]))
    void listTools().then((items: ToolItem[]) => {
      setTools(items.map(x => ({ name: x.name, description: x.description })))
    }).catch(() => setTools([]))
    void listAvailableModels().then((ids: string[]) => {
      setModels(ids.map(id => ({ id, label: id })))
    }).catch(() => setModels([]))
  }, [editing])

  useEffect(() => {
    if (activeTab !== 'memory') return
    void listMemoryByAgent(agentId).then(setMemory)
  }, [activeTab, agentId])

  useEffect(() => {
    if (activeTab !== 'conversations') return
    void listConversations().then(all => {
      setConversations(all.filter(c => c.agentId === agentId))
    })
  }, [activeTab, agentId])

  const agentSummary: AgentSummary | undefined = agent ? {
    id: agent.id,
    name: agent.name,
    role: agent.role,
    goal: agent.goal,
    intent: agent.intent,
    expertise: agent.expertise,
    description: agent.description,
    emoji: agent.emoji,
    tags: agent.tags,
    skillBindings: agent.skillBindings,
    systemPrompt: agent.systemPrompt,
    model: agent.modelOverride,
    toolAllow: agent.toolAllow,
    toolDeny: agent.toolDeny,
    maxTurns: agent.maxTurns,
    timeoutSeconds: agent.timeoutSeconds,
    workspaceAccess: agent.workspaceAccess as 'ro' | 'rw' | undefined,
    workspaceAllowPaths: agent.workspaceAllowPaths,
    delegationChildDenyTools: agent.delegationChildDenyTools,
    headless: agent.headless,
  } : undefined

  const handleSave = useCallback(async () => {
    if (!name.trim() || !role.trim() || !goal.trim()) {
      showToast(t('agent.required_fields_hint', '名称、角色与目标为必填'), false)
      return
    }
    setSubmitting(true)
    try {
      // 数值上限与后端校验一致（wesapp agent 包 CRUD 层拒绝越界）。
      const turns = maxTurns === '' ? 0 : Number(maxTurns)
      const timeout = timeoutSeconds === '' ? 0 : Number(timeoutSeconds)
      if (turns > 100000 || timeout > 604800) {
        showToast(t('agent.numeric_limit_hint', '数值超出允许范围（轮次 ≤ 100000、超时 ≤ 604800 秒）'), false)
        return
      }
      await updateAgent({
        id: agentId,
        name: name.trim(),
        role: role.trim(),
        goal: goal.trim(),
        intent: intent.trim(),
        expertise: splitList(expertise),
        description: description.trim(),
        systemPrompt: systemPrompt.trim(),
        suggestions: suggestions.filter(Boolean),
        skillBindings,
        modelOverride,
        toolAllow,
        toolDeny,
        // 数字输入为空 = 0 = 恢复引擎默认（与文本字段"空=清空"语义一致）。
        maxTurns: turns,
        timeoutSeconds: timeout,
        workspaceAccess,
        workspaceAllowPaths: splitList(workspaceAllowPaths),
        delegationChildDenyTools: splitList(delegationChildDeny),
        headless,
      })
      const agents = await listAgents()
      setAgent(agents.find(a => a.id === agentId) ?? null)
      setEditing(false)
      showToast(t('agent.saved', '已保存'), true)
    } catch (err: any) {
      showToast(err?.message ?? t('agent.save_failed', '保存失败'), false)
    } finally {
      setSubmitting(false)
    }
  }, [agentId, name, role, goal, intent, expertise, description, systemPrompt, suggestions,
    skillBindings, modelOverride, toolAllow, toolDeny, maxTurns, timeoutSeconds,
    workspaceAccess, workspaceAllowPaths, delegationChildDeny, headless,
    showToast, t])

  const handleDelete = useCallback(async () => {
    if (!window.confirm(t('agent.delete_confirm', '确定删除该助手？此操作不可撤销。'))) return
    try {
      await deleteAgent(agentId)
      navigate({ page: 'contacts' })
      showToast(t('agent.deleted', '助手已删除'), true)
    } catch (err: any) {
      showToast(err?.message ?? t('agent.delete_failed', '删除失败'), false)
    }
  }, [agentId, navigate, showToast, t])

  const handleDeleteMemory = useCallback(async (id: string) => {
    try {
      await request('sidebar/deleteMemory', { id })
      setMemory(prev => prev.filter(m => m.id !== id))
      showToast(t('agent.memory_deleted', '已删除'), true)
    } catch {
      showToast(t('agent.memory_delete_failed', '删除失败'), false)
    }
  }, [showToast, t])

  const handleDeleteConversation = useCallback(async (sessionId: string) => {
    try {
      await request('sidebar/deleteConversation', { sessionId })
      setConversations(prev => prev.filter(c => c.sessionId !== sessionId))
      showToast(t('agent.conv_deleted', '已删除'), true)
    } catch {
      showToast(t('agent.conv_delete_failed', '删除失败'), false)
    }
  }, [showToast, t])

  if (!agent) {
    return (
      <PageShell contentWidth="standard">
        <div className="flex flex-col items-center gap-3 py-16 text-center">
          <p className="text-small text-dim">{t('agent.loading', '加载中...')}</p>
        </div>
      </PageShell>
    )
  }

  const shellTabs = [
    { id: 'conversations', label: t('agent.detail_tab_chat', '对话'), icon: <MessageCircle size={14} /> },
    { id: 'config', label: t('agent.detail_tab_config', '配置'), icon: <Settings2 size={14} /> },
    { id: 'memory', label: t('agent.detail_tab_memory', '记忆'), icon: <Brain size={14} />, badge: memory.length || undefined },
  ]

  const tabContent: Record<string, React.ReactNode> = {
    conversations: (
      <AgentConversationList
        conversations={conversations.map(c => ({
          id: c.sessionId,
          title: c.title,
          lastTime: c.lastTime,
          lastMessage: c.lastMessage || c.title,
        }))}
        onSelect={(id) => bridgeNavigate('openChatSession', { sessionId: id, agentId, agentName: agent?.name })}
        onDelete={handleDeleteConversation}
      />
    ),
    config: agentSummary ? <AgentConfigView agent={agentSummary} onEdit={agent.isBuiltin ? undefined : () => setEditing(true)} /> : null,
    memory: <AgentMemoryList memories={memory} agentName={agent.name} onDelete={handleDeleteMemory} />,
  }

  return (
    <>
      <PageShell
        icon={<Bot size={16} />}
        title={agent.name ?? t('agent.detail', '助手详情')}
        subtitle={agent.role || agent.goal}
        onBack={() => navigate({ page: 'contacts' })}
        contentWidth="standard"
        tabs={shellTabs}
        activeTab={activeTab}
        onTabChange={setActiveTab}
        tabLayoutId="agent-detail-tabs"
        className="scrollbar-thin"
        actions={
          <>
            <Button size="sm" variant="primary" onClick={() => bridgeNavigate('startChatWithAgent', { agentId, agentName: agent.name })}>
              <MessageSquare size={14} />
              {t('agent.detail_start_chat', '开始对话')}
            </Button>
            {!agent.isBuiltin && (
              <Button size="sm" variant="outline" onClick={() => setEditing(true)}>
                <Pencil size={14} />
                {t('agent.detail_edit', '编辑')}
              </Button>
            )}
            {!agent.isBuiltin && (
              <Button size="sm" variant="danger" onClick={handleDelete}>
                <Trash2 size={14} />
                {t('agent.detail_delete', '删除')}
              </Button>
            )}
          </>
        }
      >
        {agentSummary && <AgentHeroCard agent={agentSummary} onEdit={() => setEditing(true)} className="mb-4" />}
        {tabContent[activeTab]}
      </PageShell>

      <AgentEditSlidePanel
        open={editing}
        onClose={() => setEditing(false)}
        title={t('agent.edit_panel_title', '编辑助手')}
        width="560px"
        footer={
          <div className="flex gap-3">
            <Button variant="primary" onClick={handleSave} disabled={submitting || !name.trim() || !role.trim() || !goal.trim()}>
              {submitting ? t('agent.saving', '保存中...') : t('agent.save', '保存')}
            </Button>
            <Button variant="outline" onClick={() => setEditing(false)}>
              {t('common.cancel', '取消')}
            </Button>
          </div>
        }
      >
        <div className="space-y-6">
          <AgentIdentityFields
            name={name} role={role} goal={goal} description={description}
            onNameChange={setName} onRoleChange={setRole} onGoalChange={setGoal} onDescriptionChange={setDescription}
            showGoal
          />

          <div className="space-y-2">
            <label className="text-small font-medium text-text">{t('agent.system_prompt_label', '系统指令')}</label>
            <textarea
              value={systemPrompt}
              onChange={e => setSystemPrompt(e.target.value)}
              placeholder={t('agent.system_prompt_placeholder', '定义助手的行为准则和工作方式...')}
              className="w-full min-h-[120px] px-3 py-2 text-small rounded bg-surface-2 border border-border text-text placeholder:text-muted focus:outline-none focus:border-border-hi transition-colors duration-fast resize-y"
            />
          </div>

          <SuggestionChipsEditor suggestions={suggestions} onChange={setSuggestions} />

          {/* Advanced capabilities — aligned with wesgine AgentConfig */}
          <details className="rounded-md border border-border bg-surface-2 group">
            <summary className="flex items-center gap-2 px-3 py-2.5 cursor-pointer select-none list-none">
              <Shield size={14} className="text-warning" />
              <span className="text-small font-medium text-text">{t('agent.advanced_title', '高级能力')}</span>
              <ChevronDown size={14} className="ml-auto text-muted-foreground transition-transform duration-fast group-open:rotate-180" />
            </summary>
            <div className="px-3 pb-4 space-y-5">
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <label className="text-small font-medium text-text">{t('agent.intent_label', '意图')}</label>
                  <input
                    value={intent}
                    onChange={e => setIntent(e.target.value)}
                    placeholder={t('agent.intent_placeholder', '用于路由描述，如：代码实现、重构')}
                    className="w-full px-3 py-2 text-small rounded bg-surface-2 border border-border text-text placeholder:text-muted focus:outline-none focus:border-border-hi"
                  />
                </div>
                <div className="space-y-1.5">
                  <label className="text-small font-medium text-text">{t('agent.expertise_label', '专长')}</label>
                  <input
                    value={expertise}
                    onChange={e => setExpertise(e.target.value)}
                    placeholder={t('agent.expertise_placeholder', '逗号分隔，如：Go, React')}
                    className="w-full px-3 py-2 text-small rounded bg-surface-2 border border-border text-text placeholder:text-muted focus:outline-none focus:border-border-hi"
                  />
                </div>
              </div>

              <SkillBindingSelector skills={skills} selectedBindings={skillBindings} onChange={setSkillBindings} />
              <ModelOverrideSelect models={models} value={modelOverride} onChange={setModelOverride} />
              <ToolPolicyEditor tools={tools} allow={toolAllow} deny={toolDeny} onChange={(a, d) => { setToolAllow(a); setToolDeny(d) }} />

              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <label className="text-small font-medium text-text">{t('agent.max_turns_label', '最大轮次')}</label>
                  <input
                    type="number" min={0} max={100000}
                    value={maxTurns}
                    onChange={e => setMaxTurns(e.target.value)}
                    placeholder={t('agent.max_turns_placeholder', '默认')}
                    className="w-full px-3 py-2 text-small rounded bg-surface-2 border border-border text-text placeholder:text-muted focus:outline-none focus:border-border-hi"
                  />
                </div>
                <div className="space-y-1.5">
                  <label className="text-small font-medium text-text">{t('agent.timeout_label', '超时（秒）')}</label>
                  <input
                    type="number" min={0} max={604800}
                    value={timeoutSeconds}
                    onChange={e => setTimeoutSeconds(e.target.value)}
                    placeholder={t('agent.timeout_placeholder', '默认')}
                    className="w-full px-3 py-2 text-small rounded bg-surface-2 border border-border text-text placeholder:text-muted focus:outline-none focus:border-border-hi"
                  />
                </div>
              </div>

              <div className="space-y-1.5">
                <label className="text-small font-medium text-text">{t('agent.workspace_label', '工作区访问')}</label>
                <div className="flex items-center gap-2">
                  <select
                    value={workspaceAccess ?? ''}
                    onChange={e => setWorkspaceAccess(e.target.value === '' ? undefined : e.target.value)}
                    className="flex-1 px-3 py-2 text-small rounded bg-surface-2 border border-border text-text focus:outline-none focus:border-border-hi"
                  >
                    <option value="">{t('agent.workspace_default', '默认')}</option>
                    <option value="ro">{t('agent.workspace_ro', '只读')}</option>
                    <option value="rw">{t('agent.workspace_rw', '读写')}</option>
                  </select>
                  <input
                    value={workspaceAllowPaths}
                    onChange={e => setWorkspaceAllowPaths(e.target.value)}
                    placeholder={t('agent.workspace_paths_placeholder', '允许路径（逗号分隔）')}
                    className="flex-[2] w-full px-3 py-2 text-small rounded bg-surface-2 border border-border text-text placeholder:text-muted focus:outline-none focus:border-border-hi"
                  />
                </div>
              </div>

              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <label className="text-small font-medium text-text">{t('agent.delegation_child_deny_label', '子助手禁用工具')}</label>
                  <input
                    value={delegationChildDeny}
                    onChange={e => setDelegationChildDeny(e.target.value)}
                    placeholder={t('agent.delegation_child_deny_placeholder', '逗号分隔')}
                    className="w-full px-3 py-2 text-small rounded bg-surface-2 border border-border text-text placeholder:text-muted focus:outline-none focus:border-border-hi"
                  />
                </div>
                <label className="flex items-center gap-2 cursor-pointer self-end pb-2">
                  <input
                    type="checkbox"
                    checked={headless}
                    onChange={e => setHeadless(e.target.checked)}
                    className="accent-primary"
                  />
                  <span className="text-small text-text">{t('agent.headless_label', '无头模式（禁用交互确认）')}</span>
                </label>
              </div>
            </div>
          </details>
        </div>
      </AgentEditSlidePanel>

      {toast && <Toast msg={toast.msg} ok={toast.ok} />}
    </>
  )
}
