import { request } from '@/bridge'
import {
  narrowLayerCounts,
  narrowMemoryRows,
  type MemoryEntryWire,
  type MemoryLayer,
  type MemoryListOptions,
  type WritableMemoryLayer,
} from '@wesui/memory'

// ─── Types ────────────────────────────────────────────────────────────────────

// 记忆条目的线上形状由引擎独占（wesgine handle_memory.go MemoryEntry，snake_case），
// 类型与收窄函数在 wesui 里只有一份，本文件只搬运不改名。这里曾手抄一份 camelCase
// 的 `MemoryEntry`：`createdAt` / `accessedAt` / `consensusCount` 线上永远不会出现，
// 于是记忆中心的时间戳恒为 undefined——类型检查全绿，因为谎报的字段名不会有人反驳。
// 也不在这里 `export type { MemoryEntryWire as MemoryEntry }`：同一形状挂两个名字，
// 下一次线上加字段时只有被 import 的那个名字会被想起来。

// 每层条数取代了旧的 MemoryStats：那是一次「按 scope 问」的诊断快照，而记忆中心
// 问的是「每层各有多少条」。两者不是同一个问题——about_me 与 consensus 共用
// global 这一个 scope，所以无论怎么累加 scope 维度的答案，页面都分不出这两层。
//
// 形状与收窄都在 wesui（narrowLayerCounts），这里不另起一个 MemoryCounts 别名：
// 同一形状挂两个名字，下一次线上加层时只有被 import 的那个名字会被想起来。

export interface RunSummary {
  runId: string
  agentId: string
  sessionId: string
  model: string
  status: string
  startedAt: string
  endedAt?: string
  totalTurns: number
  stepCount: number
  elapsedMs: number
  inputTokens: number
  outputTokens: number
  stopReason?: string
  agentName?: string
}

// ─── Memory API ───────────────────────────────────────────────────────────────

// layer 参数收为 MemoryLayer 而不是 string：下游是引擎的 `layer` 判据，引擎不认识
// 的词（筛选器的 'all'、已删除的 'owner'）会被 ParseMemoryLayer 拒掉，而空串是
// 「不按层筛」——两者语义相反，所以过不去类型这一关比过去了更好。
export async function listMemory(opts: MemoryListOptions = {}): Promise<MemoryEntryWire[]> {
  const result = await request<unknown>('sidebar/listMemory', {
    layer: opts.layer ?? '',
    namespace: opts.namespace ?? '',
    kind: opts.kind ?? '',
    limit: opts.limit ?? 200,
    offset: opts.offset ?? 0,
  })
  return narrowMemoryRows(result)
}

export async function searchMemory(query: string, layer?: MemoryLayer, limit = 50): Promise<MemoryEntryWire[]> {
  const result = await request<unknown>('sidebar/searchMemory', { query, layer: layer ?? '', limit })
  return narrowMemoryRows(result)
}

export async function deleteMemory(id: string): Promise<void> {
  await request('sidebar/deleteMemory', { id })
}

export async function memoryCounts(): Promise<Record<MemoryLayer, number>> {
  return narrowLayerCounts(await request<unknown>('sidebar/memoryCounts', {}))
}

// 只收可写层：environment 由引擎的宿主感知器产出、session/working 是运行时态，
// 对它们的导入引擎一律拒绝。让类型先拒，用户就不会点完确认才拿到一个错误。
export async function importMemory(
  entries: string[],
  layer: WritableMemoryLayer,
  namespace?: string,
): Promise<{ imported: number }> {
  return request<{ imported: number }>('sidebar/importMemory', {
    entries,
    layer,
    namespace: namespace ?? '',
  })
}

// namespace 只对 agent_memory 有意义：about_me 是本人那一格、consensus 是全 Cell，
// 两者自带地址，引擎对它们带 namespace 的清空请求是硬拒而不是忽略。
export async function clearMemoryLayer(
  layer: WritableMemoryLayer,
  namespace?: string,
): Promise<{ removed: number }> {
  return request<{ removed: number }>('sidebar/clearMemoryLayer', {
    layer,
    namespace: namespace ?? '',
  })
}

// ─── Runs API ─────────────────────────────────────────────────────────────────

export async function listRuns(limit = 50, offset = 0): Promise<RunSummary[]> {
  const result = await request<RunSummary[]>('sidebar/listRuns', { limit, offset })
  return result ?? []
}

export async function listMemoryByAgent(agentId: string, limit = 50): Promise<MemoryEntryWire[]> {
  const result = await request<unknown>('sidebar/listMemoryByAgent', { agentId, limit })
  return narrowMemoryRows(result)
}

