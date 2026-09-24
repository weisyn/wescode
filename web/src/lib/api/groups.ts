import { request } from '@/bridge'

export interface GroupItem {
  id: string
  title: string
  emoji?: string
  agentIds: string[]
}

export async function listGroups(): Promise<GroupItem[]> {
  return await request<GroupItem[]>('sidebar/listGroups') ?? []
}

export async function createGroup(title: string, agentIds: string[]): Promise<GroupItem> {
  return request<GroupItem>('sidebar/createGroup', { title, agentIds })
}

export async function deleteGroup(id: string): Promise<void> {
  await request<void>('sidebar/deleteGroup', { id })
}

export async function renameGroup(id: string, title: string): Promise<void> {
  await request<void>('sidebar/renameGroup', { id, title })
}

export async function updateGroupMembers(id: string, agentIds: string[]): Promise<void> {
  await request<void>('sidebar/updateGroupMembers', { groupId: id, agentIds })
}
