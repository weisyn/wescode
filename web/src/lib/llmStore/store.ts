// createLLMStore — this product's LLM provider CRUD state for
// <ModelSettingsPanel adapter={adapter} />.
//
// Not a shared SDK. wesui owns the panel; this file owns the store.
// INV-LLM-01..04 live here. Do not re-export this module from another product.

import { create } from 'zustand'
import type {
  LLMProvider,
  LLMProviderDraft,
  ModelSettingsAdapter,
  ProbeVerdict,
  WesProvider,
} from '@wesui/llm'
import { normalizeLLMProvider, normalizeWesProvider, isWES } from '@wesui/llm'
import type {
  LLMProviderDTO,
  LLMProviderListItem,
  WesBillingSnapshot,
  WesProviderDTO,
} from './types'
import { sanitizeDraftForAdd, sanitizeDraftForUpdate } from './mask'
import { VerifyCache, type VerifyEntry } from './verify-cache'

// -----------------------------------------------------------------------------
// RPC adapter — the shape each consumer app must implement
// -----------------------------------------------------------------------------

export interface LLMRpcAdapter {
  list: () => Promise<LLMProviderListItem[]>
  add: (dto: LLMProviderDTO) => Promise<LLMProviderListItem | void>
  update: (name: string, dto: LLMProviderDTO) => Promise<LLMProviderListItem | void>
  del: (name: string) => Promise<void>
  setDefault: (name: string) => Promise<void>
  /**
   * Run one probe. Returns wesui's verdict, not this product's wire shape:
   * the transport spelling of the class field differs per product (HTTP
   * `probe_class`, JSON-RPC `probeClass`), so narrowing it belongs at the
   * fetch site, where `parseProbeClass` can reject a string this build does
   * not know. A store that took the raw shape would have to guess which
   * spelling arrived, and guessing wrong reads `undefined` in silence.
   */
  test: (name: string) => Promise<ProbeVerdict>

  // Optional WES-managed section. Apps that do not participate in
  // WES-hosted providers pass none of these.
  listWes?: () => Promise<WesProviderDTO[]>
  testWes?: (name: string) => Promise<ProbeVerdict>
  loadWesBilling?: () => Promise<WesBillingSnapshot | null>
}

// -----------------------------------------------------------------------------
// Store options
// -----------------------------------------------------------------------------

export interface CreateLLMStoreOptions {
  rpc: LLMRpcAdapter
  /**
   * localStorage key for the verify status cache. Omit to disable
   * persistence entirely (verify status resets on each page load).
   * Use a product-local key (e.g. 'wescode-provider-verify-cache').
   */
  cacheKey?: string
  /**
   * Optional structured logger (e.g. console.info) for diagnostics
   * around Add/Update/Test outcomes. Defaults to a no-op.
   */
  logger?: (msg: string, ctx?: Record<string, unknown>) => void
}

// -----------------------------------------------------------------------------
// Public state shape
// -----------------------------------------------------------------------------

export interface LLMStoreState {
  providers: LLMProvider[]
  providersLoaded: boolean
  wesProviders: WesProvider[]
  wesProvidersLoaded: boolean
  wesBilling: WesBillingSnapshot
  wesBillingLoaded: boolean

  loadProviders: () => Promise<void>
  loadWesProviders: () => Promise<void>
  loadWesBilling: () => Promise<void>
  addProvider: (draft: LLMProviderDraft) => Promise<void>
  updateProvider: (id: string, draft: LLMProviderDraft) => Promise<void>
  deleteProvider: (id: string) => Promise<void>
  setDefaultProvider: (id: string) => Promise<void>
  testProvider: (id: string) => Promise<ProbeVerdict>
  testWesProvider: (name: string) => Promise<ProbeVerdict>
}

const DEFAULT_BILLING: WesBillingSnapshot = { enabled: false }

// -----------------------------------------------------------------------------
// createLLMStore — factory
// -----------------------------------------------------------------------------

export function createLLMStore(opts: CreateLLMStoreOptions) {
  const cache = new VerifyCache(opts.cacheKey)
  const log = opts.logger ?? (() => {})

  // Short-lived cooldown prevents redundant RPCs when store-internal
  // operations (addProvider → loadProviders) are immediately followed
  // by external refreshes (panel → loadProviders). Within the window
  // the store returns its current state without a network round-trip.
  let lastProviderLoadAt = 0
  let lastBillingLoadAt = 0
  const LOAD_COOLDOWN_MS = 500

  // Merge a fresh provider row from the backend with a cached verify
  // status (if any) so the UI shows "Verified 3m ago" across reloads.
  const applyCache = (raw: LLMProviderListItem, cached: VerifyEntry | undefined): LLMProvider => {
    const base = normalizeLLMProvider(raw)
    if (!cached) return base
    return {
      ...base,
      status: cached.status,
      lastTestedAt: cached.lastTestedAt,
      testError: cached.status === 'error' ? cached.testError : undefined,
    }
  }

  // Defense-in-depth: hide platform-managed wes:* entries even if the
  // backend forgets to filter them out. BYOK UI must never surface
  // WES-platform providers.
  const isPlatform = (name: string) => isWES(name)

  // zustand v5 uses a curried factory: create<T>()(initializer).
  const useStore = create<LLMStoreState>()((set, get) => ({
    providers: [],
    providersLoaded: false,
    wesProviders: [],
    wesProvidersLoaded: false,
    wesBilling: DEFAULT_BILLING,
    wesBillingLoaded: false,

    loadProviders: async () => {
      const now = Date.now()
      if (now - lastProviderLoadAt < LOAD_COOLDOWN_MS && get().providersLoaded) return
      try {
        const list = await opts.rpc.list()
        const filtered = (list ?? []).filter((p) => !isPlatform(p.name))
        const cached = cache.load()
        const providers = filtered.map((raw) => applyCache(raw, cached[raw.name]))
        set({ providers, providersLoaded: true })
        lastProviderLoadAt = Date.now()
      } catch (err) {
        log('llm.loadProviders.failed', { err: String(err) })
        set({ providersLoaded: true })
      }
    },

    loadWesProviders: async () => {
      if (!opts.rpc.listWes) {
        set({ wesProvidersLoaded: true })
        return
      }
      try {
        const list = await opts.rpc.listWes()
        const wesProviders = (list ?? []).map((raw) => normalizeWesProvider(raw))
        set({ wesProviders, wesProvidersLoaded: true })
      } catch (err) {
        log('llm.loadWesProviders.failed', { err: String(err) })
        set({ wesProvidersLoaded: true })
      }
    },

    loadWesBilling: async () => {
      if (!opts.rpc.loadWesBilling) {
        set({ wesBillingLoaded: true })
        return
      }
      const now = Date.now()
      if (now - lastBillingLoadAt < LOAD_COOLDOWN_MS && get().wesBillingLoaded) return
      try {
        const snap = await opts.rpc.loadWesBilling()
        set({ wesBilling: snap ?? DEFAULT_BILLING, wesBillingLoaded: true })
        lastBillingLoadAt = Date.now()
      } catch (err) {
        log('llm.loadWesBilling.failed', { err: String(err) })
        set({ wesBillingLoaded: true })
      }
    },

    addProvider: async (draft) => {
      // INV-LLM-01 (Add): key is passed through unchanged; backend
      // performs shape validation (wesapp provider.CellClient →
      // catalog.ValidateAPIKey).
      const dto = sanitizeDraftForAdd(draft)
      const saved = await opts.rpc.add(dto)
      log('llm.addProvider.ok', { name: dto.name, model: dto.model })

      // If backend returns the saved item, reflect it immediately;
      // otherwise re-list to stay authoritative.
      if (saved) {
        const item: LLMProvider = normalizeLLMProvider(saved)
        const others = get().providers.map((p) =>
          draft.isDefault ? { ...p, isDefault: false } : p,
        )
        const nextIsDefault = draft.isDefault || others.length === 0
        const next: LLMProvider = { ...item, isDefault: nextIsDefault }
        const providers = nextIsDefault ? [next, ...others] : [...others, next]
        set({ providers })
        lastProviderLoadAt = Date.now()
      } else {
        await get().loadProviders()
      }
      if (draft.isDefault) {
        try {
          await opts.rpc.setDefault(dto.name)
        } catch (err) {
          log('llm.addProvider.setDefault.failed', { err: String(err) })
        }
      }
    },

    updateProvider: async (id, draft) => {
      // INV-LLM-01 (Update): masked or empty apiKey → api_key field
      // OMITTED from DTO. Backend preserves stored key. This is the
      // frontend half of the fix for the 2026-07-05 event.
      const dto = sanitizeDraftForUpdate(draft)
      await opts.rpc.update(id, dto)
      log('llm.updateProvider.ok', { name: dto.name, apiKeyChanged: 'api_key' in dto })

      // Refresh from source rather than patching locally — the backend
      // may have applied side effects (e.g. re-ordering default).
      await get().loadProviders()
    },

    deleteProvider: async (id) => {
      await opts.rpc.del(id)
      const remaining = get().providers.filter((p) => p.id !== id)
      if (remaining.length > 0 && !remaining.some((p) => p.isDefault)) {
        remaining[0] = { ...remaining[0], isDefault: true }
      }
      set({ providers: remaining })
      lastProviderLoadAt = Date.now()
    },

    setDefaultProvider: async (id) => {
      await opts.rpc.setDefault(id)
      const list = get().providers
      const target = list.find((p) => p.id === id)
      if (!target) return
      const others = list.filter((p) => p.id !== id).map((p) => ({ ...p, isDefault: false }))
      set({ providers: [{ ...target, isDefault: true }, ...others] })
      lastProviderLoadAt = Date.now()
    },

    testProvider: async (id) => {
      const result = await opts.rpc.test(id)
      const now = new Date().toISOString()
      const target = get().providers.find((p) => p.id === id)
      if (target) {
        const entry: VerifyEntry = result.ok
          ? { status: 'ok', lastTestedAt: now }
          : { status: 'error', lastTestedAt: now, testError: result.probeClass }
        cache.save(target.label, entry)
        set({
          providers: get().providers.map((p) =>
            p.id === id
              ? {
                  ...p,
                  status: entry.status,
                  lastTestedAt: entry.lastTestedAt,
                  testError: entry.status === 'error' ? entry.testError : undefined,
                }
              : p,
          ),
        })
      }
      return result
    },

    testWesProvider: async (name) => {
      // A missing RPC is not a probe outcome. This used to answer
      // `{ ok: false, error_kind: 'not_supported' }`, which the card read as
      // a verdict about the provider and told the user their provider was
      // unsupported — when the truth was that this build has no WES probe
      // wired. The adapter below only exposes testWesProvider when the RPC
      // exists, so reaching this line means a caller went around it.
      const probe = opts.rpc.testWes
      if (!probe) throw new Error('llmStore: testWesProvider called without rpc.testWes')
      return probe(name)
    },
  }))

  // Bridge to wesui's ModelSettingsAdapter shape. Note: adapter
  // methods are stable references pinned to the store instance, so
  // consumers can useMemo(() => createLLMStore(...), []) once.
  const adapter: ModelSettingsAdapter = {
    loadProviders: async () => {
      await useStore.getState().loadProviders()
      return useStore.getState().providers
    },
    addProvider: (draft) => useStore.getState().addProvider(draft),
    updateProvider: (id, draft) => useStore.getState().updateProvider(id, draft),
    deleteProvider: (id) => useStore.getState().deleteProvider(id),
    setDefault: (id) => useStore.getState().setDefaultProvider(id),
    testProvider: (id) => useStore.getState().testProvider(id),
    loadWesProviders: opts.rpc.listWes
      ? async () => {
          await useStore.getState().loadWesProviders()
          return useStore.getState().wesProviders
        }
      : undefined,
    testWesProvider: opts.rpc.testWes
      ? (name) => useStore.getState().testWesProvider(name)
      : undefined,
    loadWesBilling: opts.rpc.loadWesBilling
      ? () => useStore.getState().loadWesBilling()
      : undefined,
  }

  return { useStore, adapter }
}
