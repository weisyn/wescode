/*
 * wescode useStream — thin wrapper around @wesui/streaming.useMessageStream.
 *
 * All the reducer / assistant flush / done handling / session filtering is
 * now provided by wesui. This hook only owns:
 *   - VSCode bridge subscription (`onStream`)
 *   - StreamEvent → WireEvent field projection (wescode's compat envelope)
 *   - Host RPC hooks (applySessionPlan, patchImagePreview)
 *   - History hydration parser (server-formatted `contentParts`)
 *   - Product side-effect callbacks (billing refresh, plan tracker report)
 *
 * See: [wesui.git/src/streaming/useMessageStream.ts](wesui.git/src/streaming/useMessageStream.ts)
 */
import { useCallback, useMemo, useState } from 'react'
import { onStream, type StreamEvent } from '@/bridge'
import { reportPlanState } from '@/bridge'
import {
  useMessageStream,
  createPushSubscriber,
  type WireEvent,
  type ChatMessage as WesuiChatMessage,
} from '@wesui/streaming'
import type { PlanStep, PlanStepStatus, PlanPart, ContentPart, HITLPart } from '@wesui/message'
import type { UserMessageInput } from '@wesui/streaming'
import type { ChatMessage, WescodeContentPart, WescodePlanPart } from './types'
import { createErrorMapper, createDoneReasonMapper, useLocale } from '@wesui'

// ── StreamEvent (VSCode bridge envelope) → WireEvent projection ──────────
//
// The Go backend still wraps events in a wescode-specific `StreamEvent`
// struct with parallel side-channel fields (toolCall, plan, subagent,
// hitl, editPreview). The `type` field values already match wesui
// WireEvent constants after the 2026-07-01 alignment; this function is
// pure field projection.

export function streamEventToWireEvent(ev: StreamEvent): WireEvent | null {
  switch (ev.type) {
    case 'text.delta':
      return { type: 'text.delta', data: ev.text ?? '' }
    case 'thinking':
      return { type: 'thinking', data: ev.text ?? '' }
    case 'thinking.done':
      return { type: 'thinking.done', data: undefined }
    // PC-03/PC-04：原样透传工具的结构化产出。
    //
    // 这一层不解释结构——形状由产出它的工具定义（`ToolResult.StructuredData`），
    // 形态由 wesui 决定（`reduce.ts` → `StructuredOutputPart`）。中间层一旦开始
    // 重塑就成了第三份真相。
    //
    // 无 payload 返回 null 而不是 `data: undefined`：wesui 的 reducer 确实会跳过
    // undefined，但让一个无内容的事件穿过整条链路到最远端再被丢弃，等于把判断推到
    // 离原因最远的地方。
    case 'structured_output':
      if (ev.structuredData === undefined || ev.structuredData === null) return null
      return { type: 'structured_output', data: ev.structuredData }
    case 'tool.start': {
      const tc = ev.toolCall
      if (!tc) return null
      return { type: 'tool.start', data: { id: tc.id, name: tc.tool, input: tc.args } }
    }
    case 'tool.done': {
      const tc = ev.toolCall
      if (!tc) return null
      return {
        type: 'tool.done',
        data: {
          id: tc.id,
          name: tc.tool,
          result: typeof tc.result === 'string' ? tc.result : tc.result != null ? String(tc.result) : undefined,
          is_error: tc.status === 'error',
        },
      }
    }
    case 'hitl_request': {
      const hitl = ev.hitl
      if (!hitl) return null
      const prompt = hitl.prompt || hitl.reason || ''
      const choices = Array.isArray(hitl.choices)
        ? hitl.choices.filter(c => typeof c === 'string' && c.trim() !== '')
        : undefined
      const kind = hitl.kind === 'choice' || hitl.kind === 'input'
        ? hitl.kind
        : (choices && choices.length > 0 ? 'choice' : 'input')
      return {
        type: 'hitl_request',
        data: {
          request_id: hitl.requestId,
          kind,
          hitl_kind: hitl.hitlKind,
          tool_name: hitl.toolName,
          prompt,
          summary: prompt,
          reason: hitl.reason,
          risk: 'medium',
          choices,
          sensitive: hitl.sensitive,
          expires_at: hitl.expiresAt,
          command_text: hitl.commandText,
          decision_kind: hitl.decisionKind,
        },
      }
    }
    case 'hitl_timeout':
      if (!ev.hitl) return null
      return { type: 'hitl_timeout', data: { request_id: ev.hitl.requestId } }
    case 'hitl_resolved':
      if (!ev.hitl) return null
      return {
        type: 'hitl_resolved',
        data: {
          request_id: ev.hitl.requestId,
          verdict: ev.hitl.grant === 'cancelled' || ev.hitl.grant === 'deny'
            ? ev.hitl.grant
            : (ev.hitl.grant || 'submitted'),
        },
      }
    case 'plan_created':
    case 'plan_replan':
      if (!ev.plan) return null
      return {
        type: ev.type,
        data: {
          planId: ev.planId ?? ev.plan.id ?? ev.plan.planId,
          title: ev.plan.title,
          intro: ev.plan.overview,
          summary: ev.plan.analysis,
          overview: ev.plan.overview,
          analysis: ev.plan.analysis,
          steps: ev.plan.steps,
        },
      }
    case 'plan_edited':
      if (!ev.plan) return null
      return {
        type: 'plan_edited',
        data: {
          planId: ev.planId ?? ev.plan.planId,
          field: ev.plan.field,
          detail: ev.plan.detail ?? ev.plan.result,
        },
      }
    case 'plan_updated':
      if (!ev.plan) return null
      return {
        type: 'plan_updated',
        data: {
          planId: ev.planId ?? ev.plan.planId,
          plan_title: ev.plan.title,
          id: ev.stepId ?? ev.plan.id,
          title: ev.plan.title,
          status: ev.plan.status,
          detail: ev.plan.result ?? ev.plan.detail,
        },
      }
    case 'plan_completed':
      return {
        type: 'plan_completed',
        data: { planId: ev.planId ?? ev.plan?.planId, summary: ev.plan?.summary },
      }
    case 'plan_interrupted':
      return {
        type: 'plan_interrupted',
        data: { planId: ev.planId ?? ev.plan?.planId, reason: ev.plan?.reason },
      }
    case 'plan_stall':
      if (!ev.plan) return null
      return {
        type: 'plan_stall',
        data: {
          stallCount: ev.plan.stallCount,
          lastUpdatedStep: ev.plan.lastUpdatedStep,
        },
      }
    case 'plan_nudge':
      return {
        type: 'plan_nudge',
        data: {
          planId: ev.planId ?? ev.plan?.planId,
          pendingCount: ev.plan?.pendingCount,
          attempt: ev.plan?.attempt,
        },
      }
    case 'error_streak':
    case 'cognitive_retry':
    case 'context_pressure':
    case 'cache_hints':
      return { type: ev.type, data: ev.observe }
    case 'subagent_start':
      if (!ev.subagent) return null
      return {
        type: 'subagent_start',
        data: {
          name: ev.subagent.description || ev.subagent.subagentId || 'sub-agent',
          agent_id: ev.subagent.subagentId,
        },
      }
    case 'subagent_progress':
      return { type: 'subagent_progress', data: undefined }
    case 'subagent_done':
      if (!ev.subagent) return null
      return {
        type: 'subagent_done',
        data: {
          name: ev.subagent.description,
          agent_id: ev.subagent.subagentId,
          status: ev.subagent.status,
          result: ev.subagent.result,
          is_error: ev.subagent.status === 'failed',
          duration_ms: ev.subagent.elapsedMs,
        },
      }
    case 'edit.preview':
      if (!ev.editPreview) return null
      return {
        type: 'edit.preview',
        data: {
          tx_id: ev.editPreview.txId,
          path: ev.editPreview.path,
          is_new: ev.editPreview.isNew,
          hunks: ev.editPreview.hunks?.map(h => ({
            old_start: h.oldStart,
            old_end: h.oldEnd,
            old_text: h.oldText,
            new_text: h.newText,
          })),
        },
      }
    case 'edit.applied':
      if (!ev.editPreview) return null
      return {
        type: 'edit.applied',
        data: { tx_id: ev.editPreview.txId, status: ev.editPreview.status },
      }
    case 'error': {
      const providerMessage = ev.error?.provider_message
      // INV-ERROR-ATTRIBUTION: A-type keeps text empty; provider_message +
      // provider drive ErrorBlock. Do not dual-write provider body into text.
      const text = providerMessage
        ? ''
        : (ev.text || ev.error?.text || '')
      return {
        type: 'error',
        data: {
          message: text,
          text,
          kind: ev.error?.kind,
          code: ev.error?.code,
          recoverable: ev.error?.recoverable ?? false,
          provider_message: providerMessage,
          provider: ev.error?.provider,
        },
      }
    }
    case 'done':
      // No `?? 'end_turn'`: the backend used to send nothing here (a failed
      // type assertion left the field empty and omitempty dropped it), so the
      // fallback was not a fallback — it was the only value this branch ever
      // produced, and wesui maps end_turn at severity 'silent'. Aborts were
      // therefore indistinguishable from normal completion. `undefined` is a
      // first-class input to createDoneReasonMapper (`if (!reason) return
      // null`), so naming a second value for "no reason" only cost us the
      // real one.
      return { type: 'done', data: ev.text }
    case 'interrupted':
      return { type: 'interrupted', data: undefined }
    default:
      return null
  }
}

// ── Server-history hydration helpers (VSCode-specific format) ─────────────

function mapPlanStepStatus(raw?: string): PlanStepStatus {
  switch (raw) {
    case 'completed': case 'done': return 'completed'
    case 'running': case 'in_progress': return 'running'
    case 'failed': return 'failed'
    case 'blocked': return 'blocked'
    case 'skipped': return 'skipped'
    default: return 'pending'
  }
}

/**
 * Hydrate server-stored contentParts into wesui ContentPart[].
 *
 * The backend now returns camelCase fields and correct status values
 * ("success" not "done") via wire.FoldHistoryWith, so this is a thin
 * type-narrowing pass. Plan step status still needs mapping
 * ("done" → "completed") and HITL parts still need structural reshaping.
 */
export function hydratePartsFromServer(item: HistoryItem): WescodeContentPart[] {
  const cp = item.contentParts
  if (!cp || cp.length === 0) {
    return item.text ? [{ kind: 'text', text: item.text }] : []
  }
  const parts: WescodeContentPart[] = []
  const hasTextPart = cp.some(p => p.kind === 'text')
  if (item.text && !hasTextPart) parts.push({ kind: 'text', text: item.text })
  for (const p of cp) {
    switch (p.kind) {
      case 'text':
        parts.push({ kind: 'text', text: p.text ?? '' })
        break
      case 'thinking':
        parts.push({ kind: 'thinking', text: p.text ?? '' })
        break
      case 'tool':
        parts.push({
          kind: 'tool',
          toolName: p.toolName ?? '',
          toolDescription: p.toolName ?? '',
          status: (p.status as 'running' | 'success' | 'error') ?? 'running',
          params: p.params as Record<string, unknown> | undefined,
          result: p.result,
        })
        break
      case 'file_ref':
        parts.push({
          kind: 'file_ref',
          fileId: p.fileId ?? '',
          fileName: p.fileName ?? '',
          mimeType: p.mimeType,
          previewUrl: p.previewUrl,
        })
        break
      case 'context_ref':
        parts.push({
          kind: 'context_ref',
          refId: p.refId,
          source: p.source,
          label: p.label ?? '',
          uri: p.uri,
          detail: p.detail,
        })
        break
      case 'plan': {
        const pp = p as any
        parts.push({
          kind: 'plan',
          planId: pp.planId,
          title: pp.title,
          intro: pp.overview,
          summary: pp.analysis,
          overview: pp.overview,
          analysis: pp.analysis,
          steps: (pp.steps ?? []).map((s: any, i: number) => ({
            id: s.id ?? '',
            title: s.title || s.description || `Step ${i + 1}`,
            status: mapPlanStepStatus(s.status),
            detail: s.result ?? s.detail,
          })),
          interrupted: pp.interrupted,
          interruptReason: pp.interruptReason,
        } as WescodePlanPart)
        break
      }
      case 'hitl': {
        const hp = p as Record<string, unknown>
        const h = (hp.hitl && typeof hp.hitl === 'object' ? hp.hitl : hp) as Record<string, unknown>
        const choices = Array.isArray(h.choices)
          ? (h.choices as unknown[]).filter((c): c is string => typeof c === 'string' && c.trim() !== '')
          : undefined
        const kind = h.kind === 'choice' || h.kind === 'input'
          ? h.kind
          : (choices && choices.length > 0 ? 'choice' : 'input')
        const description = (
          (typeof h.description === 'string' && h.description)
          || (typeof h.prompt === 'string' && h.prompt)
          || (typeof h.summary === 'string' && h.summary)
          || (typeof h.reason === 'string' && h.reason)
          || ''
        )
        const hitlKind = (typeof h.hitlKind === 'string' ? h.hitlKind : '') as '' | 'browser_hitl'
        const firstLine = description.split('\n')[0]?.trim()
        const toolName = typeof h.toolName === 'string' ? h.toolName : ''
        parts.push({
          kind: 'hitl',
          hitl: {
            requestId: (typeof h.requestId === 'string' && h.requestId) || '',
            kind,
            hitlKind,
            title: (typeof h.title === 'string' && h.title)
              || (hitlKind === 'browser_hitl' ? '浏览器人工介入' : (firstLine || toolName || '请求确认')),
            description,
            risk: (h.risk === 'low' || h.risk === 'high' ? h.risk : 'medium'),
            choices,
            sensitive: h.sensitive === true ? true : undefined,
            expiresAt: typeof h.expiresAt === 'string' ? h.expiresAt : undefined,
            resolved: h.resolved as HITLPart['hitl']['resolved'],
            decisionKind: typeof h.decisionKind === 'string' ? h.decisionKind : undefined,
            commandText: typeof h.commandText === 'string' ? h.commandText : undefined,
          },
        })
        break
      }
      case 'edit_status':
        parts.push({
          kind: 'edit_status',
          txId: p.txId ?? '',
          file: p.file ?? '',
          status: (p.status ?? 'applied') as 'pending' | 'applied' | 'failed',
          linesAdded: p.linesAdded ?? 0,
          linesRemoved: p.linesRemoved ?? 0,
          hunks: p.hunks,
        })
        break
      default:
        if (p.text) parts.push({ kind: 'text', text: p.text })
        break
    }
  }
  return parts
}

interface HistoryItem {
  role: 'user' | 'assistant'
  text: string
  id?: string
  timestamp?: number
  agentName?: string
  contentParts?: Array<{
    kind: string; text?: string; toolName?: string; status?: string; params?: any; result?: string;
    fileId?: string; fileName?: string; mimeType?: string; previewUrl?: string;
    refId?: string; source?: string; label?: string; uri?: string; detail?: string;
    txId?: string; file?: string; linesAdded?: number; linesRemoved?: number;
    hunks?: Array<{ oldStart: number; oldText: string; newText: string }>
  }>
}

function planHasOpenSteps(plan: { status?: string; steps?: PlanStep[] } | null | undefined): boolean {
  if (!plan) return false
  if (plan.status === 'completed') return false
  const steps = plan.steps ?? []
  if (steps.length === 0) return false
  return steps.some(s => s.status === 'pending' || s.status === 'running' || s.status === 'blocked')
}

function extractLatestPlan(history: HistoryItem[]): WescodePlanPart | null {
  for (let i = history.length - 1; i >= 0; i--) {
    const cp = history[i].contentParts
    if (!cp) continue
    const planPart = cp.find(p => p.kind === 'plan')
    if (!planPart) continue
    const pp = planPart as any
    const steps: PlanStep[] = (pp.steps ?? []).map((s: any) => ({
      id: s.id ?? '',
      title: s.title ?? s.description ?? '',
      status: mapPlanStepStatus(s.status),
      detail: s.result ?? s.detail,
    }))
    const next: WescodePlanPart = {
      kind: 'plan',
      planId: pp.planId,
      title: pp.title,
      intro: pp.overview,
      summary: pp.analysis,
      overview: pp.overview,
      analysis: pp.analysis,
      steps,
      status: pp.status,
      interrupted: pp.interrupted,
      interruptReason: pp.interruptReason,
    }
    if (!planHasOpenSteps(next)) return null
    return next
  }
  return null
}

// ── Subscriber (module-scope, shared across hook instances) ─────────────

const streamSubscriber = createPushSubscriber<StreamEvent>(onStream)

// ── Hook ──────────────────────────────────────────────────────────────

export function useStream(activeSessionId?: string) {
  const [planOverride, setPlanOverride] = useState<WescodePlanPart | null>(null)
  const { t } = useLocale()

  const stream = useMessageStream<StreamEvent>({
    subscriber: streamSubscriber,
    scopeId: activeSessionId,
    toWireEvent: streamEventToWireEvent,
    eventScopeKey: (ev) => ev.sessionId,
    eventAgentKey: (ev) => ev.agentName,
    // Shared A/B-type mapping — single source of truth (previously a
    // hand-rolled duplicate of createErrorMapper / createDoneReasonMapper
    // in this file).
    mapError: createErrorMapper({ t }),
    mapDoneReason: createDoneReasonMapper({ t }),
    onBillingSignal: () => {
      // Lazy import — avoid pulling in Zustand store at module load.
      void import('@/stores/billing').then(({ useBillingStore }) => {
        void useBillingStore.getState().loadWesBilling()
      })
    },
    onPlanActiveChange: (plan) => {
      // Bridge to VSCode status bar. reportPlanState accepts a flat shape.
      if (!plan) {
        reportPlanState(null)
        return
      }
      const total = plan.steps.length
      const completed = plan.steps.filter(s => s.status === 'completed' || s.status === 'skipped').length
      reportPlanState({ total, completed, title: plan.title ?? t('message.plan_title', '执行计划') })
    },
  })

  // Effective activePlan = external override (from VSCode host sessionPlan)
  // wins over the reducer-derived one.
  // Stream-derived plan is live truth. Host sessionPlan is only a
  // hydration fallback — a stale override must not resurrect 3/5 after
  // plan_completed already cleared the stream tracker.
  const activePlan = useMemo(() => {
    if (stream.activePlan) return stream.activePlan as WescodePlanPart
    if (stream.isStreaming) return null
    return planHasOpenSteps(planOverride) ? planOverride : null
  }, [planOverride, stream.activePlan, stream.isStreaming])

  // ── VSCode host RPC hooks ──

  const applySessionPlan = useCallback((plan: {
    id?: string; title?: string; overview?: string; analysis?: string;
    status?: string;
    steps: Array<{ id: string; title?: string; status?: string; detail?: string; result?: string }>;
    interrupted?: boolean; interruptReason?: string;
  }) => {
    const next: WescodePlanPart = {
      kind: 'plan',
      planId: plan.id,
      title: plan.title,
      intro: plan.overview,
      summary: plan.analysis,
      overview: plan.overview,
      analysis: plan.analysis,
      status: plan.status as WescodePlanPart['status'],
      steps: plan.steps.map(s => ({
        id: s.id,
        title: s.title ?? s.id,
        status: mapPlanStepStatus(s.status),
        detail: s.result ?? s.detail,
      })),
      interrupted: plan.interrupted,
      interruptReason: plan.interruptReason,
    }
    setPlanOverride(planHasOpenSteps(next) ? next : null)
  }, [])

  const patchImagePreview = useCallback((fileId: string, url: string) => {
    // Rebuild the message array so wesui's shallow-compare setState re-renders.
    // (This is a rare event — batch mutation is fine.)
    stream.loadMessages(stream.messages.map(msg => ({
      ...msg,
      parts: msg.parts.map(p =>
        p.kind === 'file_ref' && 'fileId' in p && (p as any).fileId === fileId ? { ...p, previewUrl: url } : p,
      ),
    })))
  }, [stream])

  // ── Rich history hydration (server-formatted contentParts) ──

  const loadMessages = useCallback((history: HistoryItem[]) => {
    const parsed: WesuiChatMessage[] = history.map(item => ({
      id: item.id || `hist-${Math.random().toString(36).slice(2)}`,
      role: item.role,
      parts: hydratePartsFromServer(item) as ContentPart[],
      timestamp: item.timestamp ?? Date.now(),
      agentName: item.agentName,
    }))
    stream.loadMessages(parsed)
    setPlanOverride(extractLatestPlan(history))
  }, [stream])

  const clearMessages = useCallback(() => {
    stream.clearMessages()
    setPlanOverride(null)
  }, [stream])

  // Map wesui ChatMessage[] → wescode ChatMessage[] (identical shape today).
  const messages = stream.messages as ChatMessage[]

  return {
    messages,
    isStreaming: stream.isStreaming,
    activity: stream.activity,
    activePlan,
    addUserMessage: (
      text: string,
      attachments?: UserMessageInput['attachments'],
      codeContext?: UserMessageInput['codeContext'],
      contextRefs?: Array<{ id: string; sourceId: string; label: string; detail?: string; uri?: string }>,
      activatedSkills?: string[],
    ) => {
      stream.addUserMessage({ text, attachments, codeContext, contextRefs, activatedSkills })
    },
    clearMessages,
    loadMessages,
    applySessionPlan,
    patchImagePreview,
    setActiveSessionId: stream.setScopeId,
  }
}
