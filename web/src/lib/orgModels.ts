import type { OrgModelProvider } from '@wesui/llm'

/**
 * AvailableModelRPC mirrors the Go backend's unified model DTO — wesapp
 * `models.Option`, serialized straight through (INV-PROVIDER-VIEW-01): ONE
 * request feeds the Chat model picker and the read-only org section of the
 * settings page. Source is "byok" (private providers), "platform" (WES sync)
 * or "org" (company Key via weisyn proxy).
 *
 * There is no message field, by design on the Go side: a row's only failure is
 * a BYOK probe class, and the copy belongs to the consumer. This DTO used to
 * declare `error?: string` — never emitted by `models.Option` — and the chat
 * adapter assigned it to `LLMProvider.testError`, which is a probe class. The
 * field was simultaneously undefined at runtime and wrong in type.
 */
export interface AvailableModelRPC {
  id: string
  providerId: string
  providerLabel: string
  model: string
  source: 'byok' | 'platform' | 'org'
  status: 'available' | 'no_key' | 'error' | 'billing_blocked'
  /** Set only when status is "error" (BYOK rows only). Narrow before use. */
  probeClass?: string
  isDefault?: boolean
  contextWindow?: number
  inputPrice?: number
  outputPrice?: number
  orgExpiry?: string
  orgStatus?: string
  keyLast4?: string
}

/**
 * AvailableModelsPayload is the RPC response shape for availableModels.
 * An org-catalog fetch failure is a structured field (orgCatalogError),
 * never a sentinel model entry — the model list is always clean for any
 * consumer (INV-PROVIDER-VIEW-01).
 */
export interface AvailableModelsPayload {
  models: AvailableModelRPC[]
  orgCatalogError?: string
  /** weisyn deployment base URL (env-aware org links, B2). */
  siteBaseUrl?: string
}

export function orgCatalogFailed(payload?: AvailableModelsPayload): boolean {
  return !!payload?.orgCatalogError
}

/**
 * orgFromAvailable derives org-provided models from the SAME availableModels
 * pipeline used by the Chat panel (INV-PROVIDER-VIEW-01). The settings page
 * always shows the enterprise section (empty CTA when none); source visibility
 * and the edit form are different views over one data source.
 */
export function orgFromAvailable(list: AvailableModelRPC[]): OrgModelProvider[] {
  return list
    .filter((m) => m.source === 'org')
    .map((m) => ({
      id: m.id,
      label: m.model,
      model: m.model,
      isDefault: m.isDefault ?? false,
      orgName: m.providerLabel || m.id,
      orgExpiry: m.orgExpiry ?? undefined,
      orgStatus: m.orgStatus ?? undefined,
      orgBlocked: m.status === 'billing_blocked',
      keyLast4: m.keyLast4 ?? undefined,
    }))
}
