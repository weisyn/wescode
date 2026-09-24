import { useCallback, useEffect, useMemo, useState } from 'react'
import { request, navigate, openExternal, onAuthChanged, onHasWorkspaceChange } from '@/bridge'
import { isFailure } from '@/lib/failure'
import { isIdentityAuthenticated, type AuthState, type AuthStatus } from '@/lib/auth'
import { Button } from '@/components/ui/Button'
import {
  ModelSettingsPanel,
  normalizeProbeResult,
  type OrgModelProvider,
  type ProbeResultRaw,
  type ProbeVerdict,
  type ProviderPreset,
  type WesProviderRaw,
} from '@wesui/llm'
import { orgFromAvailable, orgCatalogFailed, type AvailableModelsPayload } from '@/lib/orgModels'
import {
  createLLMStore,
  type LLMProviderDTO,
  type LLMProviderListItem,
} from '@/lib/llmStore'
import { useTranslation } from '@/lib/i18n'
import { tk } from '@wesui'
import type { LLMProviderItem } from '@/lib/llm'
import { useBillingStore } from '@/stores/billing'
import { saveEngineSettings, saveHintClass, type SaveHint } from './save-settings'

const parseAllowedModels = (text: string): string[] =>
  text.split(',').map(s => s.trim()).filter(Boolean)

interface CatalogResponse {
  presets: ProviderPreset[]
  models: Record<string, { id: string; displayName?: string; contextWindow: number; maxOutput: number }[]>
}

interface ProviderStrategyData {
  strategy: string
  allowedModels: string[]
}

// wescode's Electron stdio JSON-RPC uses camelCase field names on the
// wire; the shared LLMProviderDTO / LLMProviderListItem are snake_case
// (backend Go JSON-tag canonical form). The two helpers below are the
// ONLY translation layer between them — everything else that touches
// provider CRUD must go through createLLMStore, not talk to the
// backend directly. This is the invariant that fixes the 2026-07-05
// event where wescode and wesclaw diverged.
function backendItemToListItem(item: LLMProviderItem): LLMProviderListItem {
  return {
    name: item.name,
    type: item.type,
    base_url: item.baseUrl,
    model: item.model,
    api_key: item.apiKeyPreview,
    is_default: item.isDefault,
    no_stream_usage: item.noStreamUsage,
    has_api_key: item.hasApiKey,
  }
}

function dtoToBackendPayload(dto: LLMProviderDTO): Record<string, unknown> {
  const p: Record<string, unknown> = {
    name: dto.name,
    type: dto.type,
    baseUrl: dto.base_url,
    model: dto.model,
    isDefault: dto.is_default,
    noStreamUsage: dto.no_stream_usage,
    context_window: dto.context_window,
    max_output: dto.max_output,
  }
  // Only include apiKey when the shared sanitizer emitted it. Omitting
  // → backend preserves the stored key (INV-LLM-01).
  if ('api_key' in dto) {
    p.apiKey = dto.api_key
  }
  return p
}

// Both probe RPCs return the same backend struct (rpc.TestProviderResult), so
// one reader serves both. normalizeProbeResult is shared with the other
// products rather than re-derived here: it accepts either wire spelling of the
// class field (this transport is camelCase throughout) and narrows it into the
// engine's ProbeClass domain, dropping a class this build cannot name instead
// of letting it reach the card's label lookup and render a blank badge.
async function probe(method: string, name: string): Promise<ProbeVerdict> {
  return normalizeProbeResult(await request<ProbeResultRaw>(method, { name }))
}

export function ModelSettings() {
  const { t } = useTranslation()
  const [authUser, setAuthUser] = useState<boolean | null>(null)
  // Raw auth status, not just the logged-in boolean: the WES section needs to
  // distinguish "logged in but billing-blocked" from "fully usable" — an
  // overdue account must NOT auto-verify or show a fake network error
  // (INV-BILLING-02).
  const [authStatus, setAuthStatus] = useState<AuthStatus | null>(null)
  const [catalog, setCatalog] = useState<CatalogResponse | null>(null)
  // Org-provided models come from the SAME availableModels pipeline as the
  // Chat picker (INV-PROVIDER-VIEW-01) — never a separate org list.
  const [orgProviders, setOrgProviders] = useState<OrgModelProvider[]>([])
  const [orgProvidersLoaded, setOrgProvidersLoaded] = useState(false)
  const [orgCatalogError, setOrgCatalogError] = useState(false)
  const [siteBaseUrl, setSiteBaseUrl] = useState('')
  const wesBilling = useBillingStore((s) => s.wesBilling)

  useEffect(() => {
    request<AuthState>('authMe')
      .then((me) => {
        setAuthStatus(me?.status ?? null)
        setAuthUser(isIdentityAuthenticated(me?.status))
      })
      .catch(() => { setAuthUser(false); setAuthStatus(null) })
  }, [])

  // INV-DS-06: catalog from Go backend at runtime.
  useEffect(() => {
    request<CatalogResponse>('providerCatalog').then(setCatalog).catch(() => {})
  }, [])

  const applyOrgPayload = useCallback((payload: AvailableModelsPayload | undefined | null) => {
    const models = payload?.models ?? []
    setOrgProviders(orgFromAvailable(models))
    setOrgCatalogError(orgCatalogFailed(payload ?? undefined))
    setSiteBaseUrl(payload?.siteBaseUrl ?? '')
  }, [])

  const handleOrgError = useCallback((err: unknown) => {
    setOrgProviders([])
    // Config Mode rejects this RPC as no_workspace; that is not an
    // org-catalog failure. Keep the section empty and retry on login /
    // workspace ready instead of showing "enterprise load failed".
    setOrgCatalogError(!isFailure(err, 'no_workspace'))
  }, [])

  const loadOrgProviders = useCallback(() => {
    request<AvailableModelsPayload>('availableModels')
      .then(applyOrgPayload)
      .catch(handleOrgError)
      .finally(() => { setOrgProvidersLoaded(true) })
  }, [applyOrgPayload, handleOrgError])

  // Force-refresh: clears the upstream 60s cache so newly configured
  // enterprise providers appear immediately.
  const refreshOrgProviders = useCallback(() => {
    setOrgProvidersLoaded(false)
    request<AvailableModelsPayload>('refreshModels')
      .then(applyOrgPayload)
      .catch(handleOrgError)
      .finally(() => { setOrgProvidersLoaded(true) })
  }, [applyOrgPayload, handleOrgError])

  useEffect(() => { loadOrgProviders() }, [loadOrgProviders])

  useEffect(() => {
    const reloadAuth = () => {
      request<AuthState>('authMe')
        .then((me) => {
          setAuthStatus(me?.status ?? null)
          setAuthUser(isIdentityAuthenticated(me?.status))
        })
        .catch(() => { setAuthUser(false); setAuthStatus(null) })
      loadOrgProviders()
    }
    const unsubAuth = onAuthChanged(reloadAuth)
    const unsubWs = onHasWorkspaceChange(() => loadOrgProviders())
    return () => { unsubAuth(); unsubWs() }
  }, [loadOrgProviders])

  useEffect(() => {
    const onVis = () => {
      if (document.visibilityState === 'visible') loadOrgProviders()
    }
    window.addEventListener('focus', onVis)
    document.addEventListener('visibilitychange', onVis)
    return () => {
      window.removeEventListener('focus', onVis)
      document.removeEventListener('visibilitychange', onVis)
    }
  }, [loadOrgProviders])

  const presets = catalog?.presets ?? []
  const getPresetModels = useCallback((presetId: string) => {
    return (catalog?.models[presetId] ?? []).map(m => m.id)
  }, [catalog])
  const lookupModelSpec = useCallback((modelId: string) => {
    if (!catalog) return null
    for (const models of Object.values(catalog.models)) {
      const m = models.find(x => x.id === modelId)
      if (m) return { contextWindow: m.contextWindow, maxOutput: m.maxOutput }
    }
    return null
  }, [catalog])

  const { adapter } = useMemo(
    () =>
      createLLMStore({
        cacheKey: 'wescode-provider-verify-cache',
        rpc: {
          list: async () => {
            const raw = await request<LLMProviderItem[]>('listProviders')
            return Array.isArray(raw) ? raw.map(backendItemToListItem) : []
          },
          add: async (dto) => {
            const item = await request<LLMProviderItem>('addProvider', dtoToBackendPayload(dto))
            if (item?.name) return backendItemToListItem(item)
          },
          update: async (name, dto) => {
            await request('updateProvider', { name, ...dtoToBackendPayload(dto) })
          },
          del: (name) => request('deleteProvider', { name }),
          setDefault: (name) => request('setDefaultProvider', { name }),
          test: (name) => probe('testProviderByName', name),
          listWes: async () => {
            const raw = await request<WesProviderRaw[]>('listWesProviders')
            return Array.isArray(raw) ? raw.map((r) => ({ ...r })) : []
          },
          testWes: (name) => probe('testWesProvider', name),
        },
      }),
    [],
  )

  const panelAdapter = useMemo(
    () => ({
      ...adapter,
      authUser,
      authStatus,
      billing: wesBilling,
      loadWesBilling: () => useBillingStore.getState().loadWesBilling(),
      onLogin: () => navigate('login'),
      wesEmptyDesc: t('settings.ai_service.wes_empty_desc', '平台模型暂时无法加载，请稍后重试或先添加自有 AI 服务'),
    }),
    [adapter, authUser, authStatus, wesBilling, t],
  )

  return (
    <div className="space-y-6">
      <ModelSettingsPanel
        adapter={panelAdapter}
        presets={presets}
        getPresetModels={getPresetModels}
        lookupModelSpec={lookupModelSpec}
        orgProviders={orgProviders}
        orgProvidersLoaded={orgProvidersLoaded}
        orgCatalogError={orgCatalogError}
        onRefreshCatalog={refreshOrgProviders}
        orgManageHref={siteBaseUrl ? `${siteBaseUrl}/orgs` : undefined}
        orgLearnMoreHref={siteBaseUrl ? `${siteBaseUrl}/enterprise` : undefined}
        onOpenHref={openExternal}
      />
      <ProviderStrategySection />
    </div>
  )
}

const STRATEGY_KEYS = [
  {
    value: 'own_only',
    label: tk('settings.strategy_own_only', '仅私有'),
    desc: tk('settings.strategy_own_only_desc', '只使用你配置的 API Key'),
  },
  {
    value: 'shared_only',
    label: tk('settings.strategy_shared_only', '仅平台'),
    desc: tk('settings.strategy_shared_only_desc', '只使用平台提供的模型'),
  },
  {
    value: 'own_first',
    label: tk('settings.strategy_own_first', '私有优先'),
    desc: tk('settings.strategy_own_first_desc', '优先使用私有 Key，失败时回退到平台'),
  },
  {
    value: 'shared_first',
    label: tk('settings.strategy_shared_first', '平台优先'),
    desc: tk('settings.strategy_shared_first_desc', '优先使用平台模型，失败时回退到私有 Key'),
  },
]

function ProviderStrategySection() {
  const { t } = useTranslation()
  const [data, setData] = useState<ProviderStrategyData | null>(null)
  const [modelsText, setModelsText] = useState('')
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [saveHint, setSaveHint] = useState<SaveHint | null>(null)
  const [expanded, setExpanded] = useState(false)

  useEffect(() => {
    request<ProviderStrategyData>('sidebar/getProviderStrategySettings')
      .then(d => {
        setData(d)
        setModelsText((d.allowedModels ?? []).join(', '))
      })
      .catch(() => {})
  }, [])

  // Both writes in this section go through here. The strategy radio saved with
  // `.catch(() => {})`, so a rejected write left the radio sitting on the new
  // value and said nothing — on screen that is the same picture as a setting
  // that persists and then gets ignored. The models field reported failures but
  // not the engine's restartRequired list. One path answers both, and rolls the
  // optimistic state back so the control shows what the engine holds.
  const apply = useCallback(async (next: ProviderStrategyData) => {
    const prev = data
    if (!prev) return
    setData(next)
    setSaving(true)
    const { ok, hint } = await saveEngineSettings('sidebar/updateProviderStrategySettings', {
      strategy: next.strategy,
      allowedModels: next.allowedModels,
    }, t)
    if (ok) setDirty(false)
    else setData(prev)
    setSaveHint(hint)
    setSaving(false)
  }, [data, t])

  const handleStrategyChange = useCallback((strategy: string) => {
    if (!data) return
    apply({ ...data, strategy, allowedModels: parseAllowedModels(modelsText) })
  }, [data, modelsText, apply])

  const handleModelsChange = useCallback((text: string) => {
    setModelsText(text)
    setDirty(true)
  }, [])

  const handleSave = useCallback(() => {
    if (!data) return
    apply({ ...data, allowedModels: parseAllowedModels(modelsText) })
  }, [data, modelsText, apply])

  if (!data) return null

  return (
    <div className="rounded-md border border-border bg-surface p-4">
      <button
        onClick={() => setExpanded(!expanded)}
        className="flex items-center gap-2 w-full text-left hover:bg-surface-3 -m-2 p-2 rounded transition-colors duration-fast"
      >
        <span className="text-small text-dim">{expanded ? '▾' : '▸'}</span>
        <span className="text-h3 text-text">{t('settings.advanced', '高级')}</span>
      </button>

      {expanded && (
        <div className="mt-4 space-y-4">
          <div>
            <div className="text-body text-text mb-2">{t('settings.provider_strategy', 'Provider 策略')}</div>
            <div className="grid grid-cols-2 gap-2">
              {STRATEGY_KEYS.map(opt => (
                <button
                  key={opt.value}
                  onClick={() => handleStrategyChange(opt.value)}
                  className={`rounded-md border p-3 text-left transition-colors duration-fast ${
                    data.strategy === opt.value
                      ? 'border-accent bg-accent-soft'
                      : 'border-border bg-surface hover:bg-surface-3'
                  }`}
                >
                  <div className="text-small text-text font-medium">{t(opt.label.k, opt.label.zh)}</div>
                  <div className="text-caption text-dim mt-0.5">{t(opt.desc.k, opt.desc.zh)}</div>
                </button>
              ))}
            </div>
          </div>

          <div>
            <div className="text-body text-text mb-1">{t('settings.model_allowlist', '模型白名单')}</div>
            <p className="text-caption text-dim mb-2">{t('settings.model_allowlist_hint', '限制可用模型（逗号分隔，留空 = 允许全部）')}</p>
            <div className="rounded border border-border-hi bg-surface overflow-hidden">
              <input
                type="text"
                value={modelsText}
                onChange={e => handleModelsChange(e.target.value)}
                placeholder="gpt-4o, claude-sonnet-4, deepseek-v4-flash"
                className="w-full px-3 py-2 text-body bg-transparent text-text placeholder:text-muted focus:outline-none"
                style={{ border: 'none', outline: 'none', boxShadow: 'none' }}
              />
            </div>
          </div>

          {saveHint && (
            <p className={`text-caption ${saveHintClass(saveHint.tone)}`}>
              {saveHint.text}
            </p>
          )}
          {dirty && (
            <div className="flex justify-end">
              <Button variant="primary" size="sm" onClick={handleSave} disabled={saving}>
                {saving ? t('settings.saving', '保存中...') : t('settings.save', '保存')}
              </Button>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
