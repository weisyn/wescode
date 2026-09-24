import { useState, useCallback, useEffect } from 'react'
import { useTranslation } from '@/lib/i18n'
import { GroupDetailPanel, type GroupMemberAgent, type GroupConversation } from '@wesui/group'
import { Toast, useToast } from '@/components/ui/Toast'
import { listAgents, type AgentItem } from '@/lib/api/agents'
import { listGroups, renameGroup, updateGroupMembers, deleteGroup, type GroupItem } from '@/lib/api/groups'
import { useNav } from '@/lib/nav'

export interface GroupConversationPageProps {
  groupId?: string
}

export function GroupConversationPage({ groupId }: GroupConversationPageProps) {
  const { t } = useTranslation()
  const navigate = useNav()
  const { toast, showToast } = useToast()

  const [agents, setAgents] = useState<AgentItem[]>([])
  const [groups, setGroups] = useState<GroupItem[]>([])

  useEffect(() => {
    void listAgents().then(setAgents).catch(() => {})
    void listGroups().then(setGroups).catch(() => {})
  }, [])

  const group = groups.find(g => g.id === groupId)

  const reload = useCallback(async () => {
    const [a, g] = await Promise.all([listAgents(), listGroups()])
    setAgents(a)
    setGroups(g)
  }, [])

  const members: GroupMemberAgent[] = (group?.agentIds ?? [])
    .map(id => agents.find(a => a.id === id))
    .filter(Boolean)
    .map(a => ({ id: a!.id, name: a!.name, emoji: a!.emoji, hue: a!.hue, skills: a!.tags }))

  const memberIdSet = new Set(group?.agentIds ?? [])
  const available: GroupMemberAgent[] = agents
    .filter(a => !memberIdSet.has(a.id))
    .map(a => ({ id: a.id, name: a.name, emoji: a.emoji, hue: a.hue, skills: a.tags }))

  const handleAddMember = useCallback(async (agentId: string) => {
    if (!group) return
    try {
      await updateGroupMembers(group.id, [...group.agentIds, agentId])
      await reload()
    } catch {
      showToast(t('group.add_member_failed', '添加成员失败'), false)
    }
  }, [group, reload, showToast])

  const handleRemoveMember = useCallback(async (agentId: string) => {
    if (!group) return
    try {
      await updateGroupMembers(group.id, group.agentIds.filter(id => id !== agentId))
      await reload()
    } catch {
      showToast(t('group.remove_member_failed', '移除成员失败'), false)
    }
  }, [group, reload, showToast])

  const handleRename = useCallback(async (title: string) => {
    if (!group) return
    try {
      await renameGroup(group.id, title)
      await reload()
    } catch {
      showToast(t('group.rename_failed', '重命名失败'), false)
    }
  }, [group, reload, showToast])

  const handleDelete = useCallback(async () => {
    if (!group) return
    try {
      await deleteGroup(group.id)
      navigate({ page: 'contacts' })
    } catch {
      showToast(t('group.disband_failed', '解散失败'), false)
    }
  }, [group, navigate, showToast])

  if (!group) {
    return (
      <div className="h-full flex items-center justify-center text-dim">
        {t('group.not_found', '群组不存在或加载中…')}
      </div>
    )
  }

  return (
    <div className="h-full overflow-y-auto bg-bg">
      <GroupDetailPanel
        group={group}
        members={members}
        available={available}
        conversations={[]}
        onAddMember={handleAddMember}
        onRemoveMember={handleRemoveMember}
        onRename={handleRename}
        onDelete={handleDelete}
        onBack={() => navigate({ page: 'contacts' })}
        className="h-auto overflow-visible"
      />
      {toast && <Toast msg={toast.msg} ok={toast.ok} />}
    </div>
  )
}
