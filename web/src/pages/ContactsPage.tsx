import { useEffect, useState, useCallback } from 'react'
import { useTranslation } from '@/lib/i18n'
import { useNav } from '@/lib/nav'
import { Bot } from 'lucide-react'
import { PageShell } from '@wesui/layout'
import type { TabDef } from '@wesui/layout'
import { AgentCard, NewAgentPlaceholder, type AgentSummary } from '@wesui/agent'
import { NewGroupPlaceholder, CreateGroupModal, GroupCard, type GroupMemberAgent } from '@wesui/group'
import { Toast, useToast } from '@/components/ui/Toast'
import { listAgents, deleteAgent, updateAgent, type AgentItem } from '@/lib/api/agents'
import { listGroups, createGroup, deleteGroup, type GroupItem } from '@/lib/api/groups'

function toSummary(a: AgentItem): AgentSummary & { pinned?: boolean } {
  return {
    id: a.id,
    name: a.name,
    role: a.role,
    goal: a.goal,
    description: a.description,
    emoji: a.emoji,
    tags: a.tags,
    skillBindings: a.skillBindings,
    pinned: a.pinned,
  }
}

type Tab = 'agents' | 'groups'

const TABS: TabDef<Tab>[] = [
  { id: 'agents', label: '助手' },
  { id: 'groups', label: '群组' },
]

export function ContactsPage() {
  const { t } = useTranslation()
  const navigate = useNav()
  const { toast, showToast } = useToast()
  const [agents, setAgents] = useState<AgentItem[]>([])
  const [groups, setGroups] = useState<GroupItem[]>([])
  const [tab, setTab] = useState<Tab>('agents')
  const [createGroupOpen, setCreateGroupOpen] = useState(false)

  const [loadOk, setLoadOk] = useState(false)

  const tabs: TabDef<Tab>[] = TABS.map(td => ({
    ...td,
    label: td.id === 'agents' ? t('agent.tab_agents', '助手') : t('agent.tab_groups', '群组'),
  }))

  const load = useCallback(async () => {
    try {
      setAgents(await listAgents())
      setLoadOk(true)
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      if (!msg.includes('no workspace') && !msg.includes('not initialized') && !msg.includes('backend not')) {
        showToast(t('agent.load_failed', '加载失败'), false)
      }
    }
  }, [showToast, t])

  const [groupsOk, setGroupsOk] = useState(false)
  const loadGroupList = useCallback(async () => {
    try {
      const g = await listGroups()
      setGroups(g)
      if (g.length > 0) setGroupsOk(true)
    } catch { /* groups API may not be available yet */ }
  }, [])

  useEffect(() => {
    void load(); void loadGroupList()
    if (!loadOk || !groupsOk) {
      const timer = setTimeout(() => { void load(); void loadGroupList() }, 3000)
      return () => clearTimeout(timer)
    }
  }, [load, loadGroupList, loadOk, groupsOk])

  const handleDelete = useCallback(async (id: string) => {
    try {
      await deleteAgent(id)
      await load()
      showToast(t('agent.deleted', '助手已删除'), true)
    } catch (err: any) {
      showToast(err?.message ?? t('agent.delete_failed', '删除失败'), false)
    }
  }, [load, showToast, t])

  const handleTogglePin = useCallback(async (id: string, pinned: boolean) => {
    try {
      await updateAgent({ id, pinned })
      setAgents(prev => prev.map(a => a.id === id ? { ...a, pinned } : a))
      showToast(pinned ? t('agent.pinned', '已显示在对话列表') : t('agent.unpinned', '已从对话列表移除'), true)
    } catch (err: any) {
      showToast(err?.message ?? t('agent.pin_failed', '操作失败'), false)
    }
  }, [showToast, t])

  const groupMemberAgents: GroupMemberAgent[] = agents.map(a => ({
    id: a.id, name: a.name, emoji: a.emoji, skills: a.tags,
  }))

  const handleCreateGroup = useCallback(async (agentIds: string[], title: string) => {
    try {
      await createGroup(title, agentIds)
      setCreateGroupOpen(false)
      await loadGroupList()
      showToast(t('group.created', { defaultValue: '群组「{{title}}」已创建', title }), true)
    } catch (err: any) {
      showToast(err?.message ?? t('group.create_failed', '创建群组失败'), false)
    }
  }, [showToast, t, loadGroupList])

  return (
    <PageShell
      icon={<Bot size={16} />}
      title={t('agent.myAgents', '我的助手')}
      subtitle={t('agent.myAgents_desc', '管理你的 AI 助手和协作组')}
      helpContent={{
        title: 'AI 编程助手',
        description: '为不同项目或场景创建专属 AI 角色，每个助手拥有独立的系统提示、工具权限和记忆空间。',
        items: [
          { label: '每个助手包含', values: ['定制化系统提示（角色定位）', '独立的工具权限和记忆', '专属的技能组合'] },
          { label: '适用场景', values: ['前端开发 / 后端架构 / DevOps', '代码审查 / 技术文档 / 测试'] },
        ],
      }}
      tabs={tabs}
      activeTab={tab}
      onTabChange={id => setTab(id as Tab)}
      tabLayoutId="contacts-tabs"
    >
      {tab === 'agents' && (
        <div className="grid grid-cols-2 lg:grid-cols-3 gap-3">
          <NewAgentPlaceholder onClick={() => navigate({ page: 'newAgent' })} />
          {agents.map(a => (
            <AgentCard
              key={a.id}
              agent={toSummary(a)}
              onClick={id => navigate({ page: 'agentDetail', params: { agentId: id } })}
              onEdit={id => navigate({ page: 'agentDetail', params: { agentId: id } })}
              onDelete={handleDelete}
              onTogglePin={handleTogglePin}
            />
          ))}
        </div>
      )}

      {tab === 'groups' && (
        <div className="grid grid-cols-2 lg:grid-cols-3 gap-3">
          <NewGroupPlaceholder onClick={() => setCreateGroupOpen(true)} />
          {groups.map(g => (
            <GroupCard
              key={g.id}
              group={{ id: g.id, title: g.title, emoji: g.emoji, agentIds: g.agentIds }}
              agents={g.agentIds.map(id => agents.find(a => a.id === id)).filter(Boolean).map(a => ({ id: a!.id, name: a!.name, emoji: a!.emoji, skills: a!.tags }))}
              onClick={() => navigate({ page: 'groupConversation', params: { groupId: g.id } })}
              onDelete={async () => {
                try {
                  await deleteGroup(g.id)
                  await loadGroupList()
                  showToast(t('group.disbanded', '群组已解散'), true)
                } catch {
                  showToast(t('group.disband_failed', '解散失败'), false)
                }
              }}
            />
          ))}
        </div>
      )}

      <CreateGroupModal
        open={createGroupOpen}
        onClose={() => setCreateGroupOpen(false)}
        onCreate={handleCreateGroup}
        agents={groupMemberAgents}
      />

      {toast && <Toast msg={toast.msg} ok={toast.ok} />}
    </PageShell>
  )
}
