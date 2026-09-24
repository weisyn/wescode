import { useState, useEffect, useCallback, useRef, type ReactNode } from 'react'
import {
  ChatInputCard,
  ChatTextarea,
  ChatSendButton,
  ChatToolbarButton,
  ChatContextBar,
  ModelPickerDropdown,
  AttachmentChipList,
  ContextPicker,
  ContextChipList,
  SkillActivationBar,
  useFilePicker,
  useModelOptions,
  useSkillActivation,
  attachmentsToMediaPayloads,
  isImageAttachment,
} from '@wesui/chat'
import type {
  ChatModelOption,
  ChatTextareaHandle,
  AttachmentItem,
  ContextAgent,
  ContextGroup,
  SkillActivationItem,
  ContextSource,
  ContextItem,
  ContextSearchResult,
  DroppedFile,
  OrgModelProvider,
} from '@wesui/chat'
import type { WesProvider, LLMProvider } from '@wesui/llm'
import { formatWES, isWES, parseProbeClass } from '@wesui/llm'
import { request, sendChat, stopChat, navigate, onHostMessage, onAuthChanged, importFiles, diag } from '@/bridge'
import { useBillingStore } from '@/stores/billing'
import { useTranslation } from '@/lib/i18n'
import { isIdentityAuthenticated, type AuthState } from '@/lib/auth'
import { reasonOf } from '@/lib/failure'
import { fileBasename, filePathFromUriString, isAbsoluteFilePath } from '@/lib/path'
import { PROVIDER_RETRY_DELAY_MS, PROVIDER_RETRY_MAX_DELAY_MS } from '@/lib/constants'
import type { AvailableModelRPC, AvailableModelsPayload } from '@/lib/orgModels'
import { orgFromAvailable } from '@/lib/orgModels'
import { AtSign, Paperclip, Settings, File, FolderOpen, Code2, Terminal, GitBranch, AlertCircle, Network, MessageSquare } from 'lucide-react'

interface WescodeChatInputProps {
  isStreaming: boolean
  disabled?: boolean
  /** Optimistic user message (webview-only path — host does NOT re-post
   *  `userMessage` to avoid duplicate bubbles, P0). `attachments` mirror the
   *  media sent in the payload so the optimistic bubble can render
   *  images/file refs immediately. */
  onSendOptimistic?: (
    text: string,
    attachments?: Array<{ fileId: string; fileName: string; mimeType?: string; previewUrl?: string }>,
    contextRefs?: Array<{ id: string; sourceId: string; label: string }>,
    activatedSkills?: string[],
  ) => void
  /** Notifies parent when the user-selected providerId changes. Includes
   *  provider availability so the parent can derive `wesProviderSelected`. */
  onProviderChange?: (providerId: string | undefined, hasWes?: boolean, hasByok?: boolean) => void
  /** Fires when the composer gains focus. The page uses it to re-anchor the
   *  message list to the bottom (focus after scrolling up otherwise leaves
   *  the list stranded above the input). */
  onFocus?: () => void
}

// AvailableModelRPC lives in @/lib/orgModels (INV-PROVIDER-VIEW-01): the
// unified model DTO shared by the Chat picker and the settings page org
// section.

// Row status and provider status are different vocabularies: a row says
// whether it is offerable (`available` / `no_key` / `error` / `billing_blocked`),
// a provider card says what the last probe found (`ok` / `error` / `unknown` /
// `testing`). Casting one to the other made `available` masquerade as a
// ProviderStatus, so every `status === 'ok'` check silently missed while
// `status === 'error'` matched by coincidence of spelling.
function rowStatusToProviderStatus(status: AvailableModelRPC['status']): LLMProvider['status'] {
  switch (status) {
    case 'available': return 'ok'
    case 'error': return 'error'
    // no_key rows are filtered out before this maps; billing_blocked never
    // reaches a BYOK row. Either way "not probed" is the honest answer.
    default: return 'unknown'
  }
}

function rpcToLLMProvider(item: AvailableModelRPC): LLMProvider {
  return {
    id: item.providerId,
    label: item.providerLabel,
    type: 'openai_compat',
    baseUrl: '',
    model: item.model,
    apiKey: '',
    isDefault: item.isDefault ?? false,
    status: rowStatusToProviderStatus(item.status),
    // Narrow, don't assert: the row's probeClass is a wire string, and a
    // backend newer than this build can name a class the picker has no copy
    // for. parseProbeClass drops it so mapProbeClass falls back instead of
    // printing an unknown identifier.
    testError: item.status === 'error' ? parseProbeClass(item.probeClass) : undefined,
    createdAt: '',
  }
}

function rpcToWesProvider(item: AvailableModelRPC): WesProvider {
  const id = formatWES(item.providerId ?? '')
  return {
    id,
    name: item.providerId,
    displayName: item.providerLabel,
    model: item.model,
    type: 'openai_compat',
    isDefault: item.isDefault ?? false,
    inputPrice: item.inputPrice ?? 0,
    outputPrice: item.outputPrice ?? 0,
  }
}

export function WescodeChatInput({ isStreaming, disabled, onSendOptimistic, onProviderChange, onFocus }: WescodeChatInputProps) {
  const { t } = useTranslation()
  const textareaRef = useRef<ChatTextareaHandle>(null)

  const [text, setText] = useState('')
  const [focused, setFocused] = useState(false)
  const [attachments, setAttachments] = useState<AttachmentItem[]>([])
  const [contextItems, setContextItems] = useState<ContextItem[]>([])
  const [contextPickerOpen, setContextPickerOpen] = useState(false)
  const [activeSourceId, setActiveSourceId] = useState<string | null>(null)
  const [contextResults, setContextResults] = useState<ContextSearchResult[]>([])
  const [contextLoading, setContextLoading] = useState(false)
  const [contextQuery, setContextQuery] = useState('')
  const [contextSources, setContextSources] = useState<ContextSource[]>([])
  const [recentRefs, setRecentRefs] = useState<ContextItem[]>([])
  const rootRef = useRef<HTMLDivElement>(null)

  const [providers, setProviders] = useState<LLMProvider[]>([])
  const [wesProviders, setWesProviders] = useState<WesProvider[]>([])
  const [orgProviders, setOrgProviders] = useState<OrgModelProvider[]>([])
  const [wesLoaded, setWesLoaded] = useState(false)
  const [providersLoaded, setProvidersLoaded] = useState(false)
  const [isAuthenticated, setIsAuthenticated] = useState(false)
  const [isBillingOverdue, setIsBillingOverdue] = useState(false)

  // INV-PROVIDER-VIEW-01: the Chat model picker and the settings page consume
  // the SAME availableModels RPC — no per-panel filtering/mapping divergence.
  // Auth/billing status is orthogonal to the model list, so it stays a
  // separate authMe call (not a model data source).
  // INV-PROVIDER-HEAL: exponential backoff for provider fetch; reset on
  // success. Prevents the composer from being permanently disabled when
  // availableModels fails during the engine boot race (one-shot retry was
  // insufficient — the engine may still be initializing 3s later).
  const retryDelayRef = useRef(PROVIDER_RETRY_DELAY_MS)
  // Set once the engine answers availableModels with a real (possibly empty)
  // list. A successful response means the engine is ready — an empty list is
  // genuine state (no models configured), NOT a transient failure. Keeping
  // this ref unset on failure is what allows the backoff loop below to
  // continue; once set, the loop stops and providersChanged events (provider
  // CRUD / engine-ready broadcast) own subsequent refreshes.
  const providerListLoadedRef = useRef(false)
  const [attachBlockMsg, setAttachBlockMsg] = useState<string | null>(null)

  // Attachment caps aligned with the backend history cap (store.go 10MB
  // localImageDataURL): pasting/dropping is otherwise unbounded and would
  // balloon memory + DB payloads (audit finding #3).
  const MAX_ATTACH_BYTES = 10 * 1024 * 1024
  const MAX_ATTACH_COUNT = 5
  const fetchProviders = useCallback(() => {
    request<AvailableModelsPayload>('availableModels')
      .then(payload => {
        const list = payload?.models
        if (!Array.isArray(list)) return
        const byok = list.filter(m => m.source === 'byok' && m.status !== 'no_key')
        const wes = list.filter(m => m.source === 'platform')
        setProviders(byok.map(rpcToLLMProvider))
        setWesProviders(wes.map(rpcToWesProvider))
        setOrgProviders(orgFromAvailable(list))
        providerListLoadedRef.current = true
      })
      .catch(() => { /* keep stale lists; providersChanged will retry */ })
      .finally(() => {
        setProvidersLoaded(true)
        setWesLoaded(true)
      })
  }, [])

  useEffect(() => { fetchProviders() }, [fetchProviders])

  useEffect(() => {
    const onVis = () => {
      if (document.visibilityState === 'visible') fetchProviders()
    }
    window.addEventListener('focus', onVis)
    document.addEventListener('visibilitychange', onVis)
    return () => {
      window.removeEventListener('focus', onVis)
      document.removeEventListener('visibilitychange', onVis)
    }
  }, [fetchProviders])

  // Auth status is independent of the model list (INV-PROVIDER-VIEW-01 only
  // governs model data); keep it as its own single-purpose call.
  // Re-checked on mount AND on authChanged (engine-ready broadcast / login
  // events) so wes model options unlock once the engine finishes booting.
  const recheckAuth = useCallback(() => {
    request<AuthState>('authMe')
      .then(r => {
        const s = r?.status
        setIsAuthenticated(isIdentityAuthenticated(s))
        setIsBillingOverdue(s === 'billing_overdue')
      })
      .catch(() => {})
  }, [])

  useEffect(() => { recheckAuth() }, [recheckAuth])

  useEffect(() => {
    return onAuthChanged(() => { recheckAuth(); fetchProviders() })
  }, [recheckAuth, fetchProviders])

  const [agents, setAgents] = useState<ContextAgent[]>([])
  const [groups, setGroups] = useState<ContextGroup[]>([])
  const [activeAgent, setActiveAgent] = useState<ContextAgent | null>(null)
  const [activeGroup, setActiveGroup] = useState<ContextGroup | null>(null)
  const [isCollabMode, setIsCollabMode] = useState(true)

  const fetchGroups = useCallback(() => {
    request<Array<{ id: string; title: string; emoji?: string; agentIds: string[] }>>('sidebar/listGroups')
      .then(list => {
        if (Array.isArray(list) && list.length > 0) {
          setGroups(list.map(g => ({
            id: g.id,
            title: g.title,
            memberCount: g.agentIds.length,
          })))
        }
      })
      .catch(() => {})
  }, [])

  useEffect(() => {
    return onHostMessage(msg => {
      if (msg.type === 'providersChanged') {
        fetchProviders()
        fetchGroups()
      }
    })
  }, [fetchProviders, fetchGroups])

  // Persistent backoff retry until a provider list actually loads.
  useEffect(() => {
    if (providerListLoadedRef.current || !wesLoaded || wesProviders.length > 0 || providers.length > 0) return
    const t = setTimeout(() => {
      retryDelayRef.current = Math.min(retryDelayRef.current * 2, PROVIDER_RETRY_MAX_DELAY_MS)
      fetchProviders()
    }, retryDelayRef.current)
    return () => clearTimeout(t)
  }, [wesLoaded, wesProviders.length, providers.length, fetchProviders])

  useEffect(() => {
    request<Array<{ id: string; name: string; emoji?: string; hue?: number; description?: string; pinned?: boolean; isBuiltin?: boolean }>>('listAgents')
      .then(list => {
        if (Array.isArray(list)) {
          setAgents(list.map(a => ({
            id: a.id,
            name: a.name,
            emoji: a.emoji,
            hue: a.hue,
            subtitle: a.description,
            pinned: a.pinned ?? true,
          })))
        }
      })
      .catch(() => {})

    fetchGroups()
  }, [fetchGroups])

  // Per-agent visible skills feed the SkillActivationBar (ADR-326). The
  // backend intersects installed & enabled skills with the agent's Skills
  // whitelist server-side (INV-SKILL-01).
  const [visibleSkills, setVisibleSkills] = useState<SkillActivationItem[]>([])
  const [skillRefreshKey, setSkillRefreshKey] = useState(0)
  useEffect(() => {
    const handler = () => {
      if (document.visibilityState === 'visible') setSkillRefreshKey(k => k + 1)
    }
    document.addEventListener('visibilitychange', handler)
    return () => document.removeEventListener('visibilitychange', handler)
  }, [])
  useEffect(() => {
    let cancelled = false
    const agentId = activeAgent?.id
    request<Array<{
      name: string
      description?: string
      tags?: string[]
      version?: string
      always_on: boolean
      enabled: boolean
      degraded?: boolean
      missing?: string[]
    }>>('visibleSkills', agentId ? { agentId } : undefined)
      .then(list => {
        if (cancelled || !Array.isArray(list)) return
        setVisibleSkills(list.map(v => ({
          name: v.name,
          description: v.description,
          tags: v.tags,
          version: v.version,
          alwaysOn: v.always_on,
          enabled: v.enabled,
          degraded: v.degraded,
          missing: v.missing,
        })))
      })
      .catch(() => {
        if (!cancelled) setVisibleSkills([])
      })
    return () => { cancelled = true }
  }, [activeAgent?.id, skillRefreshKey])
  const {
    activated: activatedSkills,
    setActivated: setActivatedSkills,
    clearAfterSend: clearActivatedSkills,
  } = useSkillActivation({ skills: visibleSkills })

  useEffect(() => {
    const iconBySourceId: Record<string, ReactNode> = {
      file: <File size={14} />,
      folder: <FolderOpen size={14} />,
      symbol: <Code2 size={14} />,
      terminal: <Terminal size={14} />,
      diff: <GitBranch size={14} />,
      diagnostic: <AlertCircle size={14} />,
      ckg: <Network size={14} />,
      history: <MessageSquare size={14} />,
    }
    request<{ sources: Array<{ id: string; label: string; icon: string; searchable: boolean; available: boolean }> }>('context/sources')
      .then(r => {
        if (!r?.sources) return
        setContextSources(
          r.sources
            .filter(s => s.available)
            .map(s => ({
              id: s.id,
              label: s.label,
              icon: iconBySourceId[s.id] ?? <File size={14} />,
              searchable: s.searchable,
            }))
        )
      })
      .catch(() => {})
  }, [])

  const handleContextSearch = useCallback(async (sourceId: string, query: string) => {
    if (sourceId === '*' && !query && recentRefs.length > 0) {
      setContextResults(recentRefs.map(r => ({ item: r })))
      setContextLoading(false)
      return
    }
    setContextLoading(true)
    try {
      const resp = await request<{ items: Array<{ id: string; sourceId: string; label: string; detail?: string; icon?: string }> }>('context/search', { sourceId, query, limit: 20 })
      if (resp?.items) {
        setContextResults(resp.items.map(i => ({
          item: {
            id: i.id,
            sourceId: i.sourceId,
            label: i.label,
            detail: i.detail,
            data: (i as { data?: Record<string, unknown> }).data,
          },
        })))
      }
    } catch { /* ignore */ }
    finally { setContextLoading(false) }
  }, [recentRefs])

  const handleContextSelect = useCallback((item: ContextItem) => {
    setContextItems(prev => prev.some(i => i.id === item.id) ? prev : [...prev, item])
    setRecentRefs(prev => {
      const filtered = prev.filter(r => r.id !== item.id)
      return [item, ...filtered].slice(0, 5)
    })
    setContextPickerOpen(false)
    setActiveSourceId(null)
    setContextQuery('')
    setContextResults([])
    if (atPositionRef.current !== null) {
      setText(prev => {
        const before = prev.slice(0, atPositionRef.current!)
        const afterAt = prev.slice(atPositionRef.current!)
        const spaceIdx = afterAt.search(/\s/)
        const after = spaceIdx >= 0 ? afterAt.slice(spaceIdx) : ''
        return (before + after).replace(/\s+$/, '') + (after ? '' : '')
      })
      atPositionRef.current = null
    }
  }, [])

  const handleRemoveContext = useCallback((id: string) => {
    setContextItems(prev => prev.filter(i => i.id !== id))
  }, [])

  const handleAtButton = useCallback(() => {
    setContextPickerOpen(true)
    setActiveSourceId(null)
    setContextQuery('')
    setContextResults([])
  }, [])

  const atPositionRef = useRef<number | null>(null)

  const handleSelectionChange = useCallback((text: string, selStart: number) => {
    const before = text.slice(0, selStart)
    const match = before.match(/@([^\s]*)$/)
    if (match) {
      atPositionRef.current = before.lastIndexOf('@')
      if (!contextPickerOpen) {
        setContextPickerOpen(true)
        setActiveSourceId(null)
      }
      if (match[1]) {
        handleContextSearch('*', match[1])
      }
    } else if (contextPickerOpen) {
      atPositionRef.current = null
      setContextPickerOpen(false)
      setActiveSourceId(null)
      setContextResults([])
    }
  }, [contextPickerOpen, handleContextSearch])

  useEffect(() => {
    return onHostMessage(msg => {
      if (msg.type === 'prefillInput') {
        setText(msg.text)
        textareaRef.current?.focus()
      }
      if (msg.type === 'agentInfo' && msg.agent) {
        const a = msg.agent as { name: string; role: string; id?: string; emoji?: string; hue?: number }
        if (a.name) {
          setActiveAgent({ id: a.id ?? a.name, name: a.name, emoji: a.emoji, hue: a.hue })
          setIsCollabMode(false)
        } else {
          setActiveAgent(null)
          setIsCollabMode(true)
        }
      }
    })
  }, [])


  const wesBilling = useBillingStore(s => s.wesBilling)
  const wesBillingBlocked = isBillingOverdue || (wesBilling.enabled && wesBilling.active === false)

  const modelOptions = useModelOptions({
    wesProviders,
    wesProvidersLoaded: wesLoaded,
    providers,
    orgProviders,
    isAuthenticated,
    billingBlocked: wesBillingBlocked,
  })

  const [selectedModel, setSelectedModel] = useState<string | undefined>(undefined)
  const defaultModelOption =
    modelOptions.find(o => o.isDefault && !o.disabled && o.source !== 'org')
    ?? modelOptions.find(o => !o.disabled && o.source !== 'org')
    ?? modelOptions.find(o => !o.disabled)
  const effectiveSelectedModel = selectedModel ?? defaultModelOption?.id

  useEffect(() => {
    if (!selectedModel && modelOptions.length > 0) {
      if (defaultModelOption) setSelectedModel(defaultModelOption.id)
    }
  }, [defaultModelOption, modelOptions.length, selectedModel])

  // Notify parent of provider change so it can drive WES-only UI (e.g. the
  // BillingDebtBanner in ChatMessages). When selectedModel is still undefined
  // but modelOptions already loaded, look ahead to the would-be-default to
  // avoid a false-negative flash (React batches the default-selection effect
  // in the same cycle, so selectedModel lags one render behind modelOptions).
  useEffect(() => {
    const opt = modelOptions.find(o => o.id === effectiveSelectedModel)
    onProviderChange?.(
      opt?.providerId,
      wesProviders.length > 0,
      providers.length > 0,
    )
  }, [effectiveSelectedModel, modelOptions, onProviderChange, wesProviders.length, providers.length])

  // ── Image paste/drop → attachments ──────────────────────────────────────
  // Both gestures funnel through here; the source is recorded in the
  // audit event so the observable chain keeps paste vs drop distinct
  // (2026-08-17 log rebuild: attach.paste / attach.drop).
  const handleFilesReceived = useCallback((files: File[], gesture: 'paste' | 'drop') => {
    if (!files.length) return
    const oversized = files.filter(f => f.size > MAX_ATTACH_BYTES)
    const accepted = files.filter(f => f.size <= MAX_ATTACH_BYTES)
    if (oversized.length > 0) {
      setAttachBlockMsg(t('chat.attachTooLarge', '单个附件不能超过 10MB：') + oversized.map(f => f.name).join('、'))
    } else if (accepted.length + attachments.length > MAX_ATTACH_COUNT) {
      setAttachBlockMsg(t('chat.attachTooMany', '一次最多添加 5 个附件。'))
    }
    // Attach gesture audit (no dataUrl bodies, no message text).
    diag('WEBVIEW', `attach.${gesture}`, {
      count: accepted.length,
      rejectedCount: oversized.length,
      files: accepted.map(f => ({ name: f.name, mimeType: f.type || null, size: f.size })),
    })
    const room = Math.max(0, MAX_ATTACH_COUNT - attachments.length)
    for (const file of accepted.slice(0, room)) {
      const reader = new FileReader()
      reader.onload = () => {
        const dataUrl = reader.result as string
        const isImage = isImageAttachment(file.name, file.type)
        const item: AttachmentItem = {
          id: `img-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
          name: file.name,
          type: isImage ? 'image' : 'file',
          size: file.size,
          previewUrl: isImage ? dataUrl : undefined,
          _dataUrl: dataUrl,
          _mimeType: file.type,
        }
        setAttachments(prev => [...prev, item])
      }
      reader.readAsDataURL(file)
    }
  }, [attachments.length, t])

  // INV-MODEL-02: sending requires both content AND a selected model.
  const hasContent = text.trim().length > 0 || attachments.length > 0 || contextItems.length > 0
  const hasModel = !!effectiveSelectedModel && modelOptions.some(o => o.id === effectiveSelectedModel && !o.disabled)
  const canSend = hasContent && hasModel

  const handleSend = useCallback(() => {
    if (isStreaming) {
      stopChat()
      return
    }
    if (!canSend) return

    const selected = modelOptions.find(o => o.id === effectiveSelectedModel)
    if (selected?.disabled) return

    const activatedSnapshot = [...activatedSkills]
    // P2/P3: user picking a specific (non-default) provider in the
    // WescodeChatInput dropdown → Strict brand promise. wescode
    // reconfigures the provider from ProviderID per Run so the provider
    // choice IS the model choice — treat both as user intent. (ModelBinding
    // tri-state removed in wesgine v1.0; brand-honoring lives on
    // CellSpec.AllowedModels + LogicalModelGroups.)
    const isUserPicked = Boolean(selected && !selected.isDefault && selected.providerId)
    // Pass `agentId` explicitly so the host can't silently run against a
    // stale `_activeAgent` when the earlier `setActiveAgent` RPC dropped
    // (audit gap #3, INV-consistency). `null` = collab mode.
    const media = attachmentsToMediaPayloads(attachments)
    // Paperclip-imported files (real persisted file IDs) travel through
    // fileIds — no redundant base64 in media (audit finding #5).
    const fileIds = attachments.filter(a => a._fileId).map(a => a._fileId! as string)

    const payload = {
      text: text.trim(),
      providerId: selected?.providerId,
      model: selected?.model || undefined,
      modelBinding: isUserPicked ? 'strict' as const : '' as const,
      fileIds: fileIds.length > 0 ? fileIds : undefined,
      media: media.length > 0 ? media : undefined,
      contextItems: contextItems.length > 0 ? contextItems.map(i => ({ id: i.id, sourceId: i.sourceId, label: i.label, detail: i.detail, data: i.data })) : undefined,
      activatedSkills: activatedSnapshot.length > 0 ? activatedSnapshot : undefined,
      agentId: isCollabMode ? null : (activeAgent?.id ?? null),
      groupId: activeGroup?.id ?? undefined,
    }

    // Mirror the media sent to the host so the optimistic bubble renders
    // images/file refs immediately (webview path never re-posts userMessage).
    const attachmentRefs = attachments.length > 0
      ? attachments.map(a => ({
          fileId: a._fileId ?? a.id,
          fileName: a.name,
          mimeType: a._mimeType,
          previewUrl: a.type === 'image' ? a.previewUrl : undefined,
        }))
      : undefined
    const contextRefs = contextItems.length > 0
      ? contextItems.map(i => ({
          id: i.id,
          sourceId: i.sourceId,
          label: i.label,
          detail: i.detail,
          // Best-effort uri for the optimistic chip; the authoritative uri
          // comes from the persisted context_ref block on history hydrate.
          uri: typeof i.data?.path === 'string' ? i.data.path : undefined,
        }))
      : undefined
    const skillNames = activatedSnapshot.length > 0 ? activatedSnapshot : undefined
    onSendOptimistic?.(payload.text, attachmentRefs, contextRefs, skillNames)
    sendChat(payload)

    setText('')
    setAttachments([])
    setContextItems([])
    clearActivatedSkills()
  }, [isStreaming, canSend, text, attachments, effectiveSelectedModel, modelOptions, contextItems, onSendOptimistic, activatedSkills, clearActivatedSkills, isCollabMode, activeAgent])


  const pickFilesHook = useFilePicker()
  const handleImportPaths = useCallback(async (paths: string[]) => {
    // Unified attachment routing: every imported file (image or not) becomes
    // an AttachmentItem that travels via `fileIds` → file_ref. There is no
    // MIME split anymore — a PDF and a PNG are both "files first"; vision
    // expansion happens engine-side per model capability. @ stays an explicit
    // user gesture (ContextPicker) only; no physical entry falls through to
    // the context channel.
    try {
      const imported = await importFiles(paths)
      const byPath = new Map(imported.map(i => [i.path, i]))
      // The host drops any path it could not persist. Say so: a file the user
      // picked that produces no chip and no message is the exact shape of
      // "the paperclip does nothing", and it stayed invisible for five weeks
      // because this loop skipped silently.
      const missing = paths.filter(p => !byPath.get(p)?.fileId)
      if (missing.length > 0) {
        setAttachBlockMsg(
          t('chat.attachImportPartial', '这些文件没能附加成功：') +
          missing.map(fileBasename).join('、'),
        )
      }
      for (const p of paths) {
        const info = byPath.get(p)
        if (!info?.fileId) continue
        const isImage = info.mimeType?.startsWith('image/')
          || /\.(png|jpe?g|gif|webp|bmp|svg)$/i.test(info.fileName || p)
        const item: AttachmentItem = {
          id: info.fileId,
          name: info.fileName || fileBasename(p),
          type: isImage ? 'image' : 'file',
          size: 0,
          previewUrl: info.previewUrl,
          // Imported files are already persisted (importLocalFile): carry the
          // real file ID through `fileIds` on send instead of duplicating the
          // full base64 in `media` (audit finding #5).
          _fileId: info.fileId,
          _mimeType: info.mimeType,
        }
        setAttachments(prev => prev.some(a => a.id === item.id) ? prev : [...prev, item])
      }
    } catch (err) {
      // Import failed — nothing was persisted, so there is no file_ref to
      // send. Drop the file instead of falling back to a context chip: @ is
      // a pointer (file stays in place), never a degraded attachment route.
      // But say it out loud: this branch also catches the Config Mode guard
      // (`no_workspace`) and RPC timeouts, and a console.warn in a console
      // nobody has open is why "selected a file, nothing happened" was the
      // only symptom anyone ever saw.
      //
      // A named failure already carries its own localised sentence
      // (lib/failure FAILURE_TEXT) — re-writing "请先打开一个文件夹" here would
      // be a second copy of the very thing this fix is about.
      setAttachBlockMsg(
        reasonOf(err) ? (err as Error).message : t('chat.attachImportFailed', '附加文件失败，请重试。'),
      )
      diag('WEBVIEW', 'attach.importFiles.ERROR', {
        count: paths.length,
        reason: reasonOf(err) ?? null,
        message: err instanceof Error ? err.message : String(err),
      })
    }
  }, [t])

  const handlePickFiles = useCallback(async () => {
    setAttachBlockMsg(null)
    try {
      // Files only: a directory cannot be hashed or copied into the
      // attachment store. Pointing at a folder is the `@` gesture.
      const paths = await pickFilesHook({ multiple: true, allowFolders: false })
      if (paths && paths.length > 0) {
        await handleImportPaths(paths)
      }
    } catch (err) {
      // Cancelling resolves with [] per the wesui FilePicker contract, so a
      // rejection here is always a real failure. Show something for every one
      // of them: gating the message on a named reason left the unnamed ones
      // (the RPC timeout that was killing this gesture mid-dialog) exactly as
      // silent as the bare `catch {}` it replaced.
      setAttachBlockMsg(
        reasonOf(err) ? (err as Error).message : t('chat.attachPickFailed', '打开文件选择器失败，请重试。'),
      )
      diag('WEBVIEW', 'attach.pickFiles.ERROR', {
        reason: reasonOf(err) ?? null,
        message: err instanceof Error ? err.message : String(err),
      })
    }
  }, [pickFilesHook, handleImportPaths])

  const handleRemoveAttachment = useCallback((id: string) => {
    setAttachments(prev => prev.filter(a => a.id !== id))
  }, [])

  const handleDropUris = useCallback((uris: string[]) => {
    const paths = uris
      .map(filePathFromUriString)
      .filter(isAbsoluteFilePath)
    if (paths.length === 0) return
    // Same unified routing as paperclip: image and non-image drops both
    // become attachments (file_ref). No context-chip fallback.
    void handleImportPaths(paths)
  }, [handleImportPaths])

  const handleHostDroppedFiles = useCallback((dropped: DroppedFile[]) => {
    // Host-shell drop handoff (webview container native DOM, Finder drags):
    // the host resolved real paths (webUtils.getPathForFile) or already
    // persisted the file (upload before handoff). Both become attachments —
    // never context chips (@ is an explicit pointer gesture, not a degraded
    // attachment route). `dataUrl` bodies are handled by handleFilesReceived
    // inside the webview (paste / browser File objects).
    if (!dropped?.length) return
    const withPath = dropped
      .filter((d): d is DroppedFile & { path: string } => typeof d.path === 'string' && d.path.length > 0)
    const persisted = dropped
      .filter((d): d is DroppedFile & { fileId: string } => typeof d.fileId === 'string' && d.fileId.length > 0 && !d.path)
    if (withPath.length > 0) void handleImportPaths(withPath.map(d => d.path))
    if (persisted.length === 0) return
    setAttachments(prev => {
      const next = [...prev]
      for (const d of persisted) {
        if (next.some(a => a.id === d.fileId)) continue
        const isImage = d.mimeType?.startsWith('image/')
          || /\.(png|jpe?g|gif|webp|bmp|svg)$/i.test(d.name)
        next.push({
          id: d.fileId,
          name: d.name,
          type: isImage ? 'image' : 'file',
          size: 0,
          _fileId: d.fileId,
          _mimeType: d.mimeType,
        })
      }
      return next
    })
  }, [handleImportPaths])

  useEffect(() => {
    return onHostMessage(msg => {
      if (msg.type === 'droppedFiles' && Array.isArray(msg.files)) {
        // Finder / OS file drags land on the webview container (native DOM),
        // the host resolves paths and hands them over here.
        handleHostDroppedFiles(msg.files)
      }
    })
  }, [handleHostDroppedFiles])

  const noProviders = providersLoaded && wesLoaded && modelOptions.length === 0
  const selectedOpt = modelOptions.find(o => o.id === effectiveSelectedModel)
  const selectedModelDisabled = selectedOpt?.disabled === true
  const modelBlocked = noProviders || selectedModelDisabled
  // INV-CHAT-02/04: billing only blocks the composer when no usable BYOK
  // or org channel remains. Selecting BYOK/org must leave input usable;
  // WES-selected debt UI is owned by BillingDebtBanner.
  const hasByok = modelOptions.some(o => o.source === 'byok' && !o.disabled)
  const hasOrg = modelOptions.some(o => o.source === 'org' && !o.disabled)
  const hasNonWes = hasByok || hasOrg
  const selectedIsWes = selectedOpt?.source === 'platform'
    || isWES(selectedOpt?.providerId)
  const billingBlocks = isBillingOverdue && !hasNonWes
  const effectiveDisabled = disabled || billingBlocks
  // Show the inline debt hint only while a WES model is selected (or no
  // BYOK/org escape hatch exists). Never nag after the user already switched.
  const showBillingOverdueHint = isBillingOverdue && (selectedIsWes || !hasNonWes)

  const handleAgentSwitch = useCallback((target: { type: 'agent' | 'group'; id: string } | null) => {
    if (!target) {
      setActiveAgent(null)
      setActiveGroup(null)
      setIsCollabMode(true)
      request('setActiveAgent', { agentId: null }).catch((err) => {
        console.warn('[WescodeChatInput] setActiveAgent(null) RPC failed', err)
      })
    } else if (target.type === 'agent') {
      if (target.id === '__all__') {
        navigate('contacts')
        return
      }
      const a = agents.find(x => x.id === target.id)
      if (a) {
        setActiveAgent(a)
        setActiveGroup(null)
        setIsCollabMode(false)
        request('setActiveAgent', { agentId: a.id }).catch((err) => {
          console.warn('[WescodeChatInput] setActiveAgent RPC failed', { agentId: a.id, err })
        })
      }
    } else if (target.type === 'group') {
      const g = groups.find(x => x.id === target.id)
      if (g) {
        setActiveGroup(g)
        setActiveAgent(null)
        setIsCollabMode(false)
        request('setActiveAgent', { groupId: g.id }).catch((err) => {
          console.warn('[WescodeChatInput] setActiveAgent(group) RPC failed', { groupId: g.id, err })
        })
      }
    }
  }, [agents, groups])

  return (
    <div ref={rootRef} className="flex-none w-full min-w-0 max-w-full bg-bg px-5 py-2 relative">
      {showBillingOverdueHint && (
        <div className="mb-1.5 px-2 py-1.5 rounded bg-danger-soft text-small text-danger text-center">
          {hasNonWes
            ? t('chat.billingOverdueBYOK', 'WES 欠费：请改选组织或自有模型，或结清后再用平台模型')
            : t('chat.billingOverdue', 'WES欠费，需结清欠款')}
        </div>
      )}
      {attachBlockMsg && (
        <div className="mb-1.5 px-2 py-1.5 rounded bg-danger-soft text-small text-danger text-center">
          {attachBlockMsg}
        </div>
      )}
      <ContextPicker
        open={contextPickerOpen}
        sources={contextSources}
        activeSourceId={activeSourceId}
        onSourceSelect={setActiveSourceId}
        onBack={() => setActiveSourceId(null)}
        results={contextResults}
        loading={contextLoading}
        query={contextQuery}
        onQueryChange={setContextQuery}
        onSearch={handleContextSearch}
        onSelect={handleContextSelect}
        onClose={() => { setContextPickerOpen(false); setActiveSourceId(null) }}
        className="absolute bottom-full mb-2 left-3 z-[500]"
      />
      <ChatInputCard
        onDropFiles={(f) => handleFilesReceived(f, 'drop')}
        onDropUris={handleDropUris}
        contextSlot={
          <ChatContextBar
            mode="switchable"
            agent={activeAgent}
            group={activeGroup}
            agents={agents}
            groups={groups}
            showCollabMode
            isCollabMode={isCollabMode}
            onSwitch={handleAgentSwitch}
          />
        }
        focused={focused}
        disabled={effectiveDisabled}
        topSlot={
          (attachments.length > 0 || contextItems.length > 0) ? (
            <>
              {contextItems.length > 0 && (
                <ContextChipList
                  items={contextItems}
                  onRemove={handleRemoveContext}
                  onClear={() => setContextItems([])}
                  className="px-0 py-0"
                />
              )}
              {attachments.length > 0 && (
                <AttachmentChipList attachments={attachments} onRemove={handleRemoveAttachment} />
              )}
            </>
          ) : undefined
        }
        toolbarLeft={
          <>
            <ChatToolbarButton
              icon={<Paperclip size={14} />}
              title={t('chat.attachFile', '附加文件')}
              onClick={handlePickFiles}
            />
            <ChatToolbarButton
              icon={<AtSign size={14} />}
              title={t('chat.addContext', '@ 添加上下文')}
              onClick={handleAtButton}
            />
            <ModelPickerDropdown
              models={modelOptions}
              value={effectiveSelectedModel}
              onChange={setSelectedModel}
            />
            {visibleSkills.length > 0 && (
              <SkillActivationBar
                skills={visibleSkills}
                activated={activatedSkills}
                onChange={setActivatedSkills}
              />
            )}
          </>
        }
        toolbarRight={
          noProviders ? (
            <button
              className="flex items-center gap-1.5 px-2.5 py-1 text-caption text-accent hover:text-accent-light transition-colors"
              onClick={() => navigate('settings', { tab: 'model' })}
            >
              <Settings size={14} />
              {t('chat.configureModel', '去配置')}
            </button>
          ) : (
            <ChatSendButton
              isStreaming={isStreaming}
              disabled={(!canSend || modelBlocked) && !isStreaming}
              onClick={handleSend}
            />
          )
        }
      >
        <ChatTextarea
          ref={textareaRef}
          value={text}
          onChange={setText}
          onSubmit={handleSend}
          onFocusChange={(f) => {
            setFocused(f)
            if (f) onFocus?.()
          }}
          onPasteFiles={(f) => handleFilesReceived(f, 'paste')}
          onDropFiles={(f) => handleFilesReceived(f, 'drop')}
          onDropUris={handleDropUris}
          onSelectionChange={handleSelectionChange}
          submitBlocked={contextPickerOpen || modelBlocked}
          placeholder={billingBlocks
            ? t('chat.billingOverduePlaceholder', '请先结清欠款')
            : disabled
              ? t('chat.hitlComposerPlaceholder', '请先在上方卡片中完成确认')
              : noProviders
              ? t('chat.noModelPlaceholder', '尚未配置模型。打开设置 → 模型配置，可用平台、企业或自有 Key')
              : t('chat.inputPlaceholder', '输入消息...')}
          disabled={effectiveDisabled}
        />
      </ChatInputCard>
    </div>
  )
}
