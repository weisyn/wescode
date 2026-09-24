import { asFailureReason, FailureError, reasonOf } from '@/lib/failure'

declare function acquireVsCodeApi(): {
  postMessage(msg: unknown): void
  getState(): unknown
  setState(state: unknown): void
}

declare const __VSCODE_API__: { postMessage(msg: unknown): void } | undefined

const vscode: { postMessage(msg: unknown): void } | undefined =
  (typeof __VSCODE_API__ !== 'undefined' && __VSCODE_API__)
    ? __VSCODE_API__
    : (typeof acquireVsCodeApi === 'function' ? acquireVsCodeApi() : undefined)

// ─── Diagnostic infrastructure ──────────────────────────────────────
//
// End-to-end chat pipeline debugging. Every diag() call is:
//   1. Echoed to console.warn (webview DevTools).
//   2. Forwarded to the Extension host via postMessage, which writes it
//      to `~/.wescode/data/logs/wescode-chat.log` alongside logs from
//      the host, backend service, and Go backend — enabling correlation
//      of a single chat message across all layers via traceId.
//
// Turn off by setting `WESCODE_DIAG_CHAT=0` at build time (Vite env)
// or setting `window.__WESCODE_DIAG_OFF__ = true` at runtime.

function newTraceId(): string {
  const t = Date.now().toString(36)
  const r = Math.random().toString(36).slice(2, 8)
  return `trc-${t}-${r}`
}

let diagEnabled = true
try {
  if (typeof window !== 'undefined' && (window as any).__WESCODE_DIAG_OFF__ === true) {
    diagEnabled = false
  }
} catch { /* ignore */ }

// High-frequency stream event sampling: token-level recv events are only
// persisted 1-in-50; terminal/error/structural events always persist.
const STREAM_RECV_SAMPLE = 50
const STREAM_RECV_ALWAYS: ReadonlySet<string> = new Set([
  'error', 'done', 'interrupted', 'edit.preview', 'edit.applied', 'tool_call', 'plan',
])
let streamRecvCounter = 0

export function diag(source: string, event: string, data: Record<string, unknown> = {}, traceId?: string): void {
  if (!diagEnabled) return
  const entry = {
    ts: new Date().toISOString(),
    source,
    trace_id: traceId,
    event,
    ...data,
  }
  try { vscode?.postMessage({ type: 'diag', entry }) } catch { /* ignore */ }
}

/** Group inline media by MIME category for send.enter audit events. */
function groupMediaByType(media: Array<{ name: string; mimeType: string; dataUrl: string }>): Record<string, number> {
  const groups: Record<string, number> = { image: 0, document: 0, other: 0 }
  for (const m of media) {
    const mt = (m.mimeType || '').toLowerCase()
    if (mt.startsWith('image/')) {
      groups.image++
    } else if (mt === 'application/pdf' || mt.includes('pdf')) {
      groups.document++
    } else {
      groups.other++
    }
  }
  return groups
}

// One-shot diagnostic dump for bridge initialization state — lets us
// know AT PAGE LOAD whether the VS Code API is usable.
diag('WEBVIEW-INIT', 'bridge.loaded', {
  hasVscodeApi: !!vscode,
  hasVscodeApiGlobal: typeof __VSCODE_API__ !== 'undefined',
  hasAcquireVsCodeApi: typeof acquireVsCodeApi === 'function',
})

const pending = new Map<string, { method: string; resolve: (v: any) => void; reject: (e: Error) => void; _dbg?: string }>()
const streamListeners = new Set<(event: StreamEvent) => void>()
const hostMessageListeners = new Set<(msg: HostMessage) => void>()
const channelEventListeners = new Set<(event: { kind: string; platform: string; account_id: string; data?: Record<string, string> }) => void>()
const engineHealthListeners = new Set<(state: EngineHealthState) => void>()
const contextSnapshotListeners = new Set<(snapshot: ContextSnapshotData) => void>()
const cronRunFinishedListeners = new Set<(event: CronRunFinishedEvent) => void>()

export interface ContextSnapshotData {
  fragments: Array<{ path: string; symbol?: string; kind: string; reason: string; token_cost: number; value_score: number }>
  token_budget: number
  token_used: number
  task_type: string
}

/**
 * EngineHealthState mirrors the backend watchdog state-change notifications
 * (`engine/unhealthy` / `engine/healthy`). Only fires on state transitions,
 * not on every 30s sample — the frontend can safely gate ChatInput / show
 * StatusBar warning without flapping.
 */
export type EngineHealthState = {
  healthy: boolean
  reason?: string
  consecutiveFailures?: number
  latencyMs?: number
}
let engineHealthy = true
let workspaceReady = !!(window as any).__WESCODE_INIT__?.params?.workspaceReady
let hasWorkspace = !!(window as any).__WESCODE_INIT__?.params?.hasWorkspace
let configMode = !!(window as any).__WESCODE_INIT__?.params?.configMode
const workspaceReadyListeners = new Set<(ready: boolean) => void>()
const hasWorkspaceListeners = new Set<(has: boolean) => void>()
const authChangedListeners = new Set<() => void>()

// ─── Config Mode Guard (INV-WV-05) ─────────────────────────────────
//
// In Config Mode (no workspace open) the Go backend rejects workspace-
// dependent methods with reason `no_workspace`. We short-circuit these
// calls at the bridge layer without a network round-trip, eliminating
// console noise from unhandled rejections.
//
// The short-circuit rejects with the same FailureError the round-trip would
// have produced. That symmetry is the point: a caller branching on
// `reasonOf(err) === 'no_workspace'` cannot tell whether the guard fired or
// the backend answered, so it needs one code path rather than two.
//
// This set must use the method names that React components pass to request(),
// NOT the Go-backend-level names. The Extension Host's RPC_REGISTRY maps
// unprefixed names (e.g. 'addProvider') to backend calls ('sidebar/addProvider').
// Methods that bypass RPC_REGISTRY via generic passthrough keep their prefix
// (e.g. 'sidebar/getProviderStrategySettings').
//
// A gesture is admitted whole or not at all. `pickFiles` used to sit in here
// while its other half `importFiles` did not, so the paperclip opened a real
// dialog and then dropped the chosen file — a half-admitted gesture reads as a
// broken app, not as "this needs a folder". Anything that only makes sense
// with a workspace (pick, import, preview) belongs outside this set, and the
// caller shows one honest `no_workspace` message for the whole gesture.

const CONFIG_MODE_SAFE = new Set([
  'authMe', 'authLogin', 'authRegister', 'authSendCode',
  'authResetPassword', 'authLogout', 'authUpdateProfile',
  'authChangePassword', 'authDeleteAccount',
  'cell/info', 'shutdown',
  'getWesBilling', 'listWesProviders', 'testWesProvider',
  'providerCatalog', 'availableModels',
  'listProviders', 'addProvider',
  'updateProvider', 'deleteProvider',
  'setDefaultProvider', 'testProvider',
  'testProviderByName',
  'diagnostics/report', '_diag',
  'sidebar/developerProfile',
])

function isConfigModeSafe(method: string): boolean {
  return CONFIG_MODE_SAFE.has(method)
}

export type HostMessage =
  | { type: 'userMessage'; text: string; attachments?: Array<{ fileId: string; fileName: string; mimeType?: string; previewUrl?: string }>; codeContext?: Array<{ filePath: string; startLine: number; endLine: number; code: string; language: string }> }
  | { type: 'clearMessages' }
  | { type: 'loading' }
  | {
    type: 'loadMessages'
    sessionId?: string
    messages: Array<{
      role: 'user' | 'assistant'
      text: string
      id?: string
      timestamp?: number
      agentName?: string
      // History attachments arrive as `file_ref` entries inside contentParts
      // (store.buildUserParts). There is deliberately no sibling
      // `attachments` field: it was declared on both sides and assigned by
      // neither, so the branch reading it stood as proof for a payload that
      // never came.
      contentParts?: Array<{ kind: string; text?: string; toolName?: string; status?: string; params?: any; result?: string; fileId?: string; fileName?: string; mimeType?: string; previewUrl?: string; refId?: string; source?: string; label?: string; uri?: string; detail?: string; planId?: string; title?: string; overview?: string; analysis?: string; steps?: any[]; interrupted?: boolean; interruptReason?: string }>
    }>
  }
  | {
    type: 'agentInfo'
    agent: {
      name: string
      role: string
      goal: string
      emoji?: string
      suggestions: string[]
    } | null
  }
  | { type: 'imagePreview'; fileId: string; url: string }
  | { type: 'sessionPlan'; plan: { id?: string; title?: string; overview?: string; analysis?: string; status?: string; steps: Array<{ id: string; title?: string; status?: string; detail?: string; result?: string }>; interrupted?: boolean; interruptReason?: string } }
  | { type: 'loadError'; error: string }
  | { type: 'search'; query: string }
  | { type: 'sessionChange'; sessionId: string }
  | { type: 'prefillInput'; text: string }
  | { type: 'providersChanged' }
  | { type: 'droppedFiles'; files: Array<{ name: string; mimeType?: string; path?: string; dataUrl?: string; fileId?: string }> }

interface EditPreviewHunk {
  oldStart: number
  oldEnd: number
  oldText: string
  newText: string
}

interface EditPreviewData {
  txId: string
  path: string
  isNew?: boolean
  status: 'pending' | 'applied' | 'failed'
  hunks?: EditPreviewHunk[]
}

interface PlanEventData {
  id?: string
  title?: string
  overview?: string
  analysis?: string
  steps?: Array<{ id: string; title?: string; status?: string; detail?: string; result?: string }>
  planId?: string
  sessionId?: string
  status?: string
  result?: string
  detail?: string
  field?: string
  reason?: string
  summary?: string
  stallCount?: number
  lastUpdatedStep?: string
  pendingCount?: number
  attempt?: number
}

/**
 * StreamEvent envelope — wescode's transport for wesgine engine events.
 *
 * ALIGNMENT (2026-07-01): the `type` field values are the same wire event
 * strings emitted by wesclaw/chat.MapEvent (`text.delta` / `tool.start` /
 * `plan_created` / `edit.preview` / `edit.applied` / `done` / etc.). This
 * lets us feed events into `@wesui/streaming.normalizeToWireEvent` /
 * `reduceContentParts` — the exact same pipeline used by wesclaw and
 * wescraft. The auxiliary fields (`toolCall`, `plan`, `editPreview`, ...)
 * are wescode-specific projections carried alongside for legacy consumers
 * during the migration to fully data-driven WireEvents.
 */
export interface StreamEvent {
  type:
    | 'text.delta' | 'thinking' | 'thinking.done'
    | 'tool.start' | 'tool.done' | 'tool.progress'
    | 'done' | 'interrupted' | 'error'
    | 'hitl_request' | 'hitl_timeout' | 'hitl_resolved'
    | 'edit.preview' | 'edit.applied'
    | 'plan_created' | 'plan_edited' | 'plan_updated' | 'plan_completed' | 'plan_interrupted' | 'plan_replan' | 'plan_stall' | 'plan_nudge'
    | 'subagent_start' | 'subagent_progress' | 'subagent_done'
    | 'run.end' | 'run.start' | 'turn.start' | 'turn.end' | 'token_usage'
    | 'error_streak' | 'cognitive_retry' | 'context_pressure' | 'cache_hints'
    | 'structured_output'
    | (string & {})
  requestId?: string
  text?: string
  toolCall?: { id?: string; tool: string; status: string; args?: Record<string, string>; result?: string }
  error?: {
    text?: string
    kind?: string
    code?: string
    recoverable?: boolean
    /** A-type LLM pass-through (INV-ERROR-ATTRIBUTION) */
    provider_message?: string
    provider?: string
  }
  hitl?: {
    requestId: string; kind?: string; hitlKind?: string; toolName?: string; reason?: string
    prompt?: string; commandText?: string; choices?: string[]
    expiresAt?: string; decisionKind?: string; grant?: string
    sensitive?: boolean
    metadata?: Record<string, string>
  }
  editPreview?: EditPreviewData
  plan?: PlanEventData
  subagent?: { subagentId?: string; description?: string; status: string; result?: string; elapsedMs?: number; error?: string }
  planId?: string
  stepId?: string
  sessionId?: string
  /** Engine observability payload (snake_case), projected to WireEvent.data. */
  observe?: Record<string, unknown>
  /**
   * 工具的 `ToolResult.StructuredData`，原样透传（PC-03/PC-04）。
   *
   * `unknown` 而非具体类型是刻意的：形状由产出它的工具定义，形态由 wesui 决定，
   * 这一层只搬运。在这里收窄类型就等于在中间层立第三份真相。
   */
  structuredData?: unknown
  runId?: string
  agentName?: string
}

/**
 * Rebuild an RPC failure from the host's reply.
 *
 * The host forwards `reason` alongside `error` when the Go side named a
 * condition (see `backend/internal/failure`). When it did, the renderer builds
 * a FailureError whose message is the reader's own language — the backend's
 * `error` string is an English diagnostic and belongs in `detail`, not in the
 * sentence a user reads.
 *
 * When there is no reason the string is all we have, so it becomes the message
 * unchanged. Those are the conditions no one has named yet; they read as
 * English to every user, which is exactly the state this migration is shrinking.
 */
function rpcError(msg: { error?: unknown; reason?: unknown }): Error {
  const text = typeof msg.error === 'string' ? msg.error : 'RPC error'
  const reason = asFailureReason(msg.reason)
  return reason ? new FailureError(reason, text) : new Error(text)
}

window.addEventListener('message', (e) => {
  const msg = e.data

  if (msg.id && pending.has(msg.id)) {
    const p = pending.get(msg.id)!
    pending.delete(msg.id)
    if (p._dbg) {
      console.warn('[bridge] RPC ←', p._dbg, msg.error ? `ERR:${msg.reason ?? ''}:${msg.error}` : JSON.stringify(msg.result).slice(0, 120))
    }
    if (msg.error) {
      const err = rpcError(msg)
      if (reasonOf(err) === 'no_workspace') {
        // The guard above should have answered this without a round-trip.
        // Arriving here means CONFIG_MODE_SAFE is missing `method`, or
        // configMode flipped mid-flight. That drift is worth seeing, but it is
        // not actionable to the reader — so it lands in the log rather than as
        // an unhandled rejection in a console nobody has open.
        diag('WEBVIEW', 'rpc.no_workspace.roundtrip', { method: p.method })
      }
      p.reject(err)
    } else {
      p.resolve(msg.result)
    }
    return
  }

  if (msg.type === 'stream') {
    const event = msg.event as StreamEvent
    const type = event?.type ?? ''
    const mustLog = STREAM_RECV_ALWAYS.has(type) || !!event?.error
    streamRecvCounter++
    if (mustLog || streamRecvCounter % STREAM_RECV_SAMPLE === 0) {
      diag('WEBVIEW', 'stream.event.recv', {
        type,
        hasSessionId: !!event?.sessionId,
        hasRequestId: !!event?.requestId,
        hasEditPreview: !!(event as any)?.editPreview,
        textLen: event?.text?.length ?? 0,
        hasProviderMessage: !!event?.error?.provider_message,
        errorCode: event?.error?.code ?? event?.error?.kind,
        listenerCount: streamListeners.size,
      }, (msg as any)._traceId ?? event?.requestId)
    }
    for (const fn of streamListeners) {
      fn(event)
    }
    return
  }

  if (msg.type === 'channelEvent') {
    for (const fn of channelEventListeners) {
      fn(msg.event)
    }
    return
  }

  if (msg.type === 'userMessage') {
    for (const fn of hostMessageListeners) {
      fn(msg)
    }
    return
  }

  if (msg.type === 'clearMessages' || msg.type === 'loading') {
    for (const fn of hostMessageListeners) {
      fn(msg)
    }
    return
  }

  if (msg.type === 'agentInfo') {
    for (const fn of hostMessageListeners) {
      fn(msg)
    }
    return
  }

  if (msg.type === 'loadMessages') {
    for (const fn of hostMessageListeners) {
      fn(msg)
    }
    return
  }

  if (msg.type === 'prefillInput') {
    for (const fn of hostMessageListeners) {
      fn(msg)
    }
    return
  }

  if (msg.type === 'providersChanged') {
    for (const fn of hostMessageListeners) {
      fn(msg)
    }
    return
  }

  if (msg.type === 'droppedFiles') {
    for (const fn of hostMessageListeners) {
      fn(msg)
    }
    return
  }

  if (msg.type === 'search' || msg.type === 'sessionPlan' || msg.type === 'imagePreview' || msg.type === 'sessionChange' || msg.type === 'loadError') {
    for (const fn of hostMessageListeners) {
      fn(msg)
    }
    return
  }

  if (msg.type === 'contextSnapshot') {
    const snapshot = msg.snapshot as ContextSnapshotData | undefined
    if (snapshot) {
      for (const fn of contextSnapshotListeners) fn(snapshot)
    }
    return
  }

  if (msg.type === 'workspaceState') {
    workspaceReady = !!msg.ready
    const prevHasWs = hasWorkspace
    const prevConfigMode = configMode
    if ('hasWorkspace' in msg) hasWorkspace = !!msg.hasWorkspace
    if ('configMode' in msg) configMode = !!msg.configMode
    for (const fn of workspaceReadyListeners) fn(workspaceReady)
    if (hasWorkspace !== prevHasWs) {
      for (const fn of hasWorkspaceListeners) fn(hasWorkspace)
    }
    // Config Mode → Cell Mode transition changes the reachability of
    // RPC methods (availableModels, listConversations, etc.). GateGuard
    // must re-run checkAuth so hasBYOK reflects the newly-available
    // model list. Without this, BYOK-only users (no login, just API key)
    // would stay locked on Gate 1 for up to 19s (retry loop fallback).
    if (configMode !== prevConfigMode) {
      for (const fn of authChangedListeners) fn()
    }
    return
  }

  if (msg.type === 'authChanged') {
    for (const fn of authChangedListeners) fn()
    return
  }

  if (msg.type === 'setLocale' && msg.locale) {
    if (_localeChangeHandler) {
      _localeChangeHandler(msg.locale)
    }
  }

  if (msg.type === 'notification' && msg.method === 'cron/run-finished') {
    for (const fn of cronRunFinishedListeners) fn(msg.params as CronRunFinishedEvent)
    return
  }

  if (msg.type === 'notification' && msg.method === 'codeintel/index-progress') {
    for (const fn of indexProgressListeners) fn(msg.params as IndexProgressState)
    return
  }

  if (msg.type === 'notification' && msg.method === 'codeintel/index-complete') {
    for (const fn of indexCompleteListeners) fn()
    return
  }

  if (msg.type === 'notification' && msg.method === 'codeintel/index-error') {
    for (const fn of indexErrorListeners) fn(msg.params as IndexErrorState)
    return
  }

  // Backend watchdog state changes (engine/unhealthy | engine/healthy). Emitted
  // only on transitions; per-tick engine/health samples are ignored here.
  if (msg.type === 'notification' && (msg.method === 'engine/unhealthy' || msg.method === 'engine/healthy')) {
    const params = (msg.params ?? {}) as Record<string, unknown>
    engineHealthy = msg.method === 'engine/healthy'
    const state: EngineHealthState = {
      healthy: engineHealthy,
      reason: typeof params.reason === 'string' ? params.reason : undefined,
      consecutiveFailures: typeof params.consecutive_failures === 'number' ? params.consecutive_failures : undefined,
      latencyMs: typeof params.latency_ms === 'number' ? params.latency_ms : undefined,
    }
    for (const fn of engineHealthListeners) fn(state)
    return
  }
})

// ── Cron run finished (pushed from cronNotifier → chatViewPane → webview) ─────

export interface CronRunFinishedEvent {
  jobId: string
  jobName: string
  runId: string
  status: string
  sessionKey?: string
  errorMsg?: string
  scheduleKind?: string
  isOneShot?: boolean
}

export function onCronRunFinished(fn: (event: CronRunFinishedEvent) => void): () => void {
  cronRunFinishedListeners.add(fn)
  return () => { cronRunFinishedListeners.delete(fn) }
}

export function onStream(fn: (event: StreamEvent) => void): () => void {
  streamListeners.add(fn)
  return () => { streamListeners.delete(fn) }
}

export function onChannelEvent(fn: (event: { kind: string; platform: string; account_id: string; data?: Record<string, string> }) => void): () => void {
  channelEventListeners.add(fn)
  request('sidebar/subscribeChannelEvents').catch(() => {})
  return () => { channelEventListeners.delete(fn) }
}

export function onHostMessage(fn: (msg: HostMessage) => void): () => void {
  hostMessageListeners.add(fn)
  return () => { hostMessageListeners.delete(fn) }
}

export function onContextSnapshot(fn: (snapshot: ContextSnapshotData) => void): () => void {
  contextSnapshotListeners.add(fn)
  return () => { contextSnapshotListeners.delete(fn) }
}

export function onEngineHealth(fn: (state: EngineHealthState) => void): () => void {
  engineHealthListeners.add(fn)
  return () => { engineHealthListeners.delete(fn) }
}

export function isEngineReady(): boolean {
  return engineHealthy
}

export function getWorkspaceReady(): boolean {
  return workspaceReady
}

export function getHasWorkspace(): boolean {
  return hasWorkspace
}

export function onWorkspaceReadyChange(fn: (ready: boolean) => void): () => void {
  workspaceReadyListeners.add(fn)
  return () => { workspaceReadyListeners.delete(fn) }
}

export function onHasWorkspaceChange(fn: (has: boolean) => void): () => void {
  hasWorkspaceListeners.add(fn)
  return () => { hasWorkspaceListeners.delete(fn) }
}

export function getConfigMode(): boolean {
  return configMode
}

export function onAuthChanged(fn: () => void): () => void {
  authChangedListeners.add(fn)
  return () => { authChangedListeners.delete(fn) }
}

export function useBackendReady(): boolean {
  return workspaceReady
}

export function onceBackendReady(): Promise<void> {
  if (workspaceReady) return Promise.resolve()
  return new Promise(resolve => {
    const unsub = onWorkspaceReadyChange(ready => {
      if (ready) { unsub(); resolve() }
    })
  })
}

export function openFolder(): void {
  vscode?.postMessage({ type: 'openFolder' })
}

const inflight = new Map<string, Promise<any>>()

/**
 * Transport liveness, not patience. This guard exists for one failure: a
 * `postMessage` sent before the Extension Host registered its listener, which
 * shows up in milliseconds — 15s is already three orders of magnitude of slack.
 */
const RPC_TIMEOUT_MS = 15_000

/**
 * Methods whose reply waits on a person, not on a machine.
 *
 * `pickFiles` opens a modal OS dialog: the call is outstanding for exactly as
 * long as the user browses, and browsing past 15s is ordinary, not a fault.
 * Under RPC_TIMEOUT_MS the promise was rejected while the dialog was still on
 * screen, the caller's `catch` ran, and the real selection arrived to an id no
 * longer in `pending` — so it was dropped. That is the whole of "I picked a
 * file and nothing happened" when the user took their time: the transport was
 * healthy and the answer was correct, it just had nowhere to land.
 *
 * Still finite, because the guard's purpose survives: a host that died mid
 * dialog must eventually fail rather than leave a promise pending forever.
 */
const HUMAN_PACED_TIMEOUT_MS = 10 * 60_000
const HUMAN_PACED = new Set(['pickFiles'])

export function request<T>(method: string, params?: unknown): Promise<T> {
  if (!vscode) {
    return Promise.reject(new Error('VSCode API not available'))
  }
  if (configMode && !isConfigModeSafe(method)) {
    return Promise.reject(new FailureError('no_workspace'))
  }
  if (params === undefined) {
    const existing = inflight.get(method)
    if (existing) return existing as Promise<T>
  }
  const id = crypto.randomUUID()
  const timeoutMs = HUMAN_PACED.has(method) ? HUMAN_PACED_TIMEOUT_MS : RPC_TIMEOUT_MS
  const p = new Promise<T>((resolve, reject) => {
    const dbg = (method === 'authMe' || method === 'availableModels') ? method : undefined
    pending.set(id, { method, resolve, reject, _dbg: dbg })
    vscode!.postMessage({ type: 'rpc', id, method, params })
    setTimeout(() => {
      if (pending.has(id)) {
        pending.delete(id)
        reject(new Error(`RPC timeout: ${method}`))
      }
    }, timeoutMs)
  })
  if (params === undefined) {
    inflight.set(method, p)
    // `then(cleanup, cleanup)` rather than `finally(cleanup)`: finally derives
    // a promise that inherits the rejection, and nobody holds it — so every
    // failing deduped call (authMe, availableModels, providerCatalog…) raised
    // an unhandled rejection on top of whatever the real caller did with the
    // error. That is the console noise INV-WV-05 set out to remove, arriving
    // through the dedup bookkeeping instead of the guard. Handling it in both
    // arms leaves the derived promise resolved; `p` itself still rejects for
    // the caller.
    const forget = () => { inflight.delete(method) }
    p.then(forget, forget)
  }
  return p
}

export function navigate(target: string, params?: Record<string, unknown>): void {
  vscode?.postMessage({ type: 'navigate', target, params })
}

export function openExternal(url: string): void {
  vscode?.postMessage({ type: 'openExternal', url })
}

export function closeTab(): void {
  vscode?.postMessage({ type: 'close' })
}

export function openFile(path: string, line?: number): void {
  vscode?.postMessage({ type: 'openFile', path, line })
}

export function notifyReady(): void {
  vscode?.postMessage({ type: 'ready' })
}

export function notifyLocaleChanged(locale: string): void {
  vscode?.postMessage({ type: 'localeChanged', locale })
}

export function prefillInput(text: string): void {
  vscode?.postMessage({ type: 'prefillInput', text })
}

export function reportPlanState(plan: { total: number; completed: number; title: string } | null): void {
  vscode?.postMessage({ type: 'planState', plan })
}

export function sendChat(payload: {
  text: string
  providerId?: string
  model?: string
  /**
   * ModelBinding — legacy observability tag (v1.0 removed the tri-state
   * fallback gate; see wesgine design/archive/pre-recast/model-binding.md
   * and wesgine AGENTS.md T-10). "strict" when the user explicitly picked a
   * provider in the chat input dropdown; empty otherwise. Forwarded as
   * ChatSendParams.modelBinding → written to run tags["model_binding"] only;
   * brand-honoring lives on CellSpec.AllowedModels + LogicalModelGroups.
   */
  modelBinding?: '' | 'preferred' | 'strict'
  filePaths?: string[]
  fileIds?: string[]
  codeSnippets?: Array<{ filePath: string; startLine: number; endLine: number; code: string; language: string }>
  contextItems?: Array<{ id: string; sourceId: string; label?: string; detail?: string; data?: Record<string, unknown> }>
  /** Inline media from paste/drop (images AND non-image files), base64 data URLs. Extension host forwards to chat/send → MediaPayloadsToBlocks. */
  media?: Array<{ name: string; mimeType: string; dataUrl: string }>
  activatedSkills?: string[]
  /** Per-request thinking level override (value domain: 'low' | 'medium' | 'high' | 'max' | ''). */
  thinkingLevel?: string
  /**
   * Explicit agent selection for this turn. When present the host MUST use
   * this value instead of any implicit `_activeAgent` state — this makes the
   * webview the authoritative source and eliminates the drift window where a
   * failed `setActiveAgent` RPC leaves UI/host state disagreeing (audit gap #3).
   */
  agentId?: string | null
  /**
   * Group target for this turn (INV-ROUTE-01/02). Mutually exclusive with
   * `agentId` — a group run has no single agent identity. Only the group id
   * travels: the backend resolves the members, so a picker holding a stale
   * roster cannot run a group against the wrong membership.
   */
  groupId?: string
  /**
   * Optional external trace id. If omitted, `sendChat` generates one.
   * Propagated through every layer (Extension host → RPC params → Go
   * backend → notification events) for end-to-end log correlation.
   */
  _traceId?: string
}): void {
  const traceId = payload._traceId ?? newTraceId()
  diag('WEBVIEW', 'send.enter', {
    textLen: payload.text?.length ?? 0,
    hasVscode: !!vscode,
    hasAgentId: payload.agentId !== undefined,
    agentId: payload.agentId,
    groupId: payload.groupId,
    providerId: payload.providerId,
    model: payload.model,
    modelBinding: payload.modelBinding,
    filePathsCount: payload.filePaths?.length ?? 0,
    fileIdsCount: payload.fileIds?.length ?? 0,
    fileIds: payload.fileIds,
    mediaCount: payload.media?.length ?? 0,
    mediaByType: payload.media?.length ? groupMediaByType(payload.media) : undefined,
    contextItemsCount: payload.contextItems?.length ?? 0,
    activatedSkillsCount: payload.activatedSkills?.length ?? 0,
  }, traceId)
  if (!vscode) {
    diag('WEBVIEW', 'send.DROPPED.noVscodeApi', {}, traceId)
    return
  }
  const enriched = { ...payload, _traceId: traceId }
  try {
    vscode.postMessage({ type: 'sendChat', payload: enriched })
    diag('WEBVIEW', 'send.postMessage.dispatched', {}, traceId)
  } catch (e) {
    diag('WEBVIEW', 'send.postMessage.ERROR', { err: (e as Error).message }, traceId)
  }
}

export function stopChat(): void {
  const traceId = newTraceId()
  diag('WEBVIEW', 'stopChat', { hasVscode: !!vscode }, traceId)
  vscode?.postMessage({ type: 'stopChat', _traceId: traceId })
}

/** Host-native open dialog. Structurally compatible with wesui's
 *  `FilePickerOptions` so main.tsx can forward the caller's request verbatim:
 *  the paperclip asks for files only, and a dialog that still offers folders
 *  hands back a path the attachment pipeline cannot hash or copy. */
export function pickFiles(opts?: { multiple?: boolean; allowFolders?: boolean; title?: string }): Promise<string[]> {
  return request<string[]>('pickFiles', {
    multiple: opts?.multiple ?? true,
    allowFolders: opts?.allowFolders ?? true,
    title: opts?.title,
  })
}

/** Rich file import (R3): returns fileId + optional image data URL so the
 *  webview paperclip path renders thumbnails exactly like the native input
 *  path. Every file (image or not) comes back with a real fileId and MIME
 *  type; the webview renders all of them as attachments (file_ref). There is
 *  no image/non-image split — a PDF and a PNG are both files first; vision
 *  expansion happens engine-side per model capability. */
export interface ImportedFileInfo {
  path: string
  fileId: string
  fileName: string
  mimeType?: string
  previewUrl?: string
}

export function importFiles(paths: string[]): Promise<ImportedFileInfo[]> {
  return request<ImportedFileInfo[]>('importFiles', { paths })
}

// ─── CodeIntel Index Progress Push ──────────────────────────────────

const indexProgressListeners = new Set<(state: IndexProgressState) => void>()
const indexCompleteListeners = new Set<() => void>()
const indexErrorListeners = new Set<(state: IndexErrorState) => void>()

export interface IndexProgressState {
  indexing: boolean
  indexed_files: number
  total_files: number
  completeness: number
  current_file?: string
}

export function onIndexProgress(fn: (state: IndexProgressState) => void): () => void {
  indexProgressListeners.add(fn)
  return () => { indexProgressListeners.delete(fn) }
}

export function onIndexComplete(fn: () => void): () => void {
  indexCompleteListeners.add(fn)
  return () => { indexCompleteListeners.delete(fn) }
}

export interface IndexErrorState {
  error: string
  status: string
  root?: string
}

export function onIndexError(fn: (state: IndexErrorState) => void): () => void {
  indexErrorListeners.add(fn)
  return () => { indexErrorListeners.delete(fn) }
}

// ─── Locale change callback ─────────────────────────────────────────
let _localeChangeHandler: ((locale: string) => void) | null = null

export function onLocaleChange(fn: (locale: string) => void): void {
  _localeChangeHandler = fn
}

// `no_workspace` rejection noise is suppressed centrally in main.tsx (INV-WV-05).
