import type { Instant } from '@wesui'
import { request } from '@/bridge'

// ── 契约说明 ──────────────────────────────────────────────
// webview 通过 bridge.request(method, params) 发送 RPC 请求。
// agentEditorService._dispatchRpc 按短方法名路由到 wescodeBackendService。
// wescodeBackendService 向 Go backend 发 sidebar/ 前缀方法，透传返回值。
// Go handler 直接返回裸数据（数组 / 对象），不再包装 {items:[...]}。
//
// 因此本文件中：
//   方法名 = 短名（不带 sidebar/ 前缀）
//   返回类型 = 裸类型（纯数组 / 纯对象）
// ──────────────────────────────────────────────────────────

export interface AgentItem {
  id: string
  name: string
  description?: string
  emoji?: string
  figure?: string
  hue?: number
  role: string
  goal: string
  intent?: string
  expertise?: string[]
  tags?: string[]
  skillBindings?: string[]
  toolAllow?: string[]
  toolDeny?: string[]
  maxTurns?: number
  timeoutSeconds?: number
  modelOverride?: string
  workspaceAccess?: string
  workspaceAllowPaths?: string[]
  delegationChildDenyTools?: string[]
  headless?: boolean
  suggestions?: string[]
  systemPrompt?: string
  isBuiltin: boolean
  pinned: boolean
}

export interface CreateAgentPayload {
  name: string
  role: string
  goal: string
  intent?: string
  expertise?: string[]
  description?: string
  systemPrompt?: string
  tags?: string[]
  suggestions?: string[]
  skills?: string[]
}

export interface UpdateAgentPayload {
  id: string
  name?: string
  role?: string
  goal?: string
  intent?: string
  expertise?: string[]
  description?: string
  systemPrompt?: string
  suggestions?: string[]
  tags?: string[]
  skillBindings?: string[]
  toolAllow?: string[]
  toolDeny?: string[]
  maxTurns?: number
  timeoutSeconds?: number
  modelOverride?: string
  workspaceAccess?: string
  workspaceAllowPaths?: string[]
  delegationChildDenyTools?: string[]
  headless?: boolean
  pinned?: boolean
}

export async function listAgents(): Promise<AgentItem[]> {
  return await request<AgentItem[]>('listAgents') ?? []
}

export interface ConversationItem {
  sessionId: string
  agentId?: string
  title: string
  lastMessage?: string
  /**
   * 原始时刻（后端发 RFC3339），不是显示串。渲染点用 `formatChatTimestamp(lastTime, locale)`
   * ——月日次序与 12/24 小时制都是界面语言的函数，而后端不知道用户界面语言。
   *
   * 它曾经由后端预格式化成 `"01-02 15:04"`：英文用户看到中式月日次序，且这个字段无法排序。
   */
  lastTime: Instant
}

export async function listConversations(): Promise<ConversationItem[]> {
  return await request<ConversationItem[]>('listConversations') ?? []
}

export async function createAgent(payload: CreateAgentPayload): Promise<AgentItem> {
  return request<AgentItem>('createAgent', payload)
}

export async function updateAgent(payload: UpdateAgentPayload): Promise<void> {
  await request<void>('updateAgent', payload)
}

export async function deleteAgent(id: string): Promise<void> {
  await request<void>('deleteAgent', { id })
}

export interface ToolItem {
  name: string
  description?: string
}

export async function listTools(): Promise<ToolItem[]> {
  return await request<ToolItem[]>('sidebar/listTools') ?? []
}

export async function listAvailableModels(): Promise<string[]> {
  return await request<string[]>('sidebar/availableModels') ?? []
}

export interface SkillItem {
  slug: string
  name: string
  description?: string
  enabled: boolean
  tags: string[]
  path: string
  operators?: string[]
  category?: string
  channel?: string
  usageCount?: number
}

export async function listSkills(): Promise<SkillItem[]> {
  return await request<SkillItem[]>('listSkills') ?? []
}

export async function toggleSkill(slug: string, enabled: boolean): Promise<void> {
  await request<void>('toggleSkill', { slug, enabled })
}

export async function deleteSkill(slug: string): Promise<void> {
  await request<void>('deleteSkill', { slug })
}

export async function reloadSkills(): Promise<void> {
  await request<void>('reloadSkills')
}

export interface SkillHealthResult {
  total: number
  healthy: number
  issues: Array<{
    slug: string
    name: string
    problems: string[]
  }>
}

export async function skillHealth(): Promise<SkillHealthResult> {
  const result = await request<SkillHealthResult>('sidebar/skillHealth')
  return result ?? { total: 0, healthy: 0, issues: [] }
}

