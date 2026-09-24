// LLM provider CRUD types for this product.
//
// DTO shape (snake_case) is the wire contract with this product's Go
// backend. Draft shape (camelCase) is the UI-facing type consumed by
// wesui's ModelSettingsPanel.

export type ProviderType = 'openai_compat' | 'anthropic'

// DTO — backend wire shape. Matches wesconfig.ProviderConfig JSON tags
// on the Go side (snake_case). This product's RPC adapter translates
// the DTO into its own transport.
export interface LLMProviderDTO {
  name: string
  type: ProviderType
  base_url: string
  model: string
  /**
   * When omitted, backend preserves the stored key. When present and
   * empty, treated the same as omitted. See INV-LLM-01.
   */
  api_key?: string
  is_default?: boolean
  no_stream_usage?: boolean
  /** Context window size in tokens — deterministic source for compression budget. */
  context_window?: number
  /** Maximum output tokens. */
  max_output?: number
}

// Extended DTO returned from list/get. `api_key` here is the *masked
// preview* (e.g. "sk-2****a5c52"), never plaintext.
export interface LLMProviderListItem extends Required<Pick<LLMProviderDTO, 'name' | 'type' | 'base_url' | 'model'>> {
  api_key: string
  is_default: boolean
  no_stream_usage?: boolean
  has_api_key?: boolean
}

// A probe verdict has no DTO here on purpose. Its wire shape is this
// product's transport (see the store's LLMRpcAdapter.test), and the class
// field has to be narrowed into wesgine's ProbeClass domain at the fetch
// site with `parseProbeClass`. The type this file used to declare —
// `{ ok, error_kind?: string }` — carried a field name no current backend
// sends, typed loosely enough that nobody noticed: the store stored
// `undefined` into testError on every failed probe, so the card showed
// generic copy for classes the engine had named precisely.

// WES-managed platform provider. Read-only, no API key, appears in the
// "WES 提供" section of the settings page. Mirrors wesui's
// WesProviderRaw wire shape.
export interface WesProviderDTO {
  id: string
  name: string
  display_name: string
  model: string
  type: string
  is_default: boolean
  input_price: number
  output_price: number
}

// Billing state fragment used by the settings page's debt banner. Kept
// deliberately loose — apps that do not participate in WES billing
// pass `undefined` from loadWesBilling.
export interface WesBillingSnapshot {
  enabled: boolean
  active?: boolean
  reason?: string
  balanceMills?: number
  balance?: number
  currency?: string
  unpaidMills?: number
  unpaidCount?: number
  debtsUrl?: string
  lastUpdatedAt?: string
}
