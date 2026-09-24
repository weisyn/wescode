// API-key mask handling.
//
// The backend returns a masked preview like "sk-2****a5c52" via list.
// wesui's editable form displays this in the API Key input field so
// the user can see what is currently stored. Without a guard, hitting
// "Save" without re-typing would send that mask string back as the new
// key, and the backend would store literal "sk-2****a5c52".
//
// The convention here:
//   - Add:    caller MUST supply a real, non-masked key. sanitizeDraftForAdd
//             passes it through unchanged (empty is preserved as empty
//             so the backend can reject with its "api_key is required"
//             error message — do NOT silently substitute anything).
//   - Update: masked or empty apiKey means "keep the stored key".
//             sanitizeDraftForUpdate drops the api_key field entirely
//             in that case, and the backend's Update handler falls
//             back to the previous value.

import type { LLMProviderDraft } from '@wesui/llm'
import type { LLMProviderDTO } from './types'

export function isMasked(key: string | undefined | null): boolean {
  return typeof key === 'string' && key.includes('****')
}

/**
 * Build the DTO for a fresh Add call. api_key is passed through even
 * if empty — the backend layer decides whether empty is acceptable
 * (wesapp provider.CellClient does `catalog.ValidateAPIKey`; empty
 * → nil error; non-empty invalid → rejected).
 */
export function sanitizeDraftForAdd(draft: LLMProviderDraft): LLMProviderDTO {
  return {
    name: draft.label.trim(),
    type: draft.type,
    base_url: draft.baseUrl.trim(),
    model: draft.model.trim(),
    api_key: (draft.apiKey ?? '').trim(),
    is_default: draft.isDefault,
    no_stream_usage: draft.noStreamUsage,
    context_window: draft.contextWindow,
    max_output: draft.maxOutput,
  }
}

/**
 * Build the DTO for an Update call. api_key is included ONLY when the
 * user typed a real new key. Empty or masked → field omitted, backend
 * preserves the stored key.
 *
 * INV-LLM-01: consumer apps MUST route Update through this function.
 */
export function sanitizeDraftForUpdate(draft: LLMProviderDraft): LLMProviderDTO {
  const dto: LLMProviderDTO = {
    name: draft.label.trim(),
    type: draft.type,
    base_url: draft.baseUrl.trim(),
    model: draft.model.trim(),
    is_default: draft.isDefault,
    no_stream_usage: draft.noStreamUsage,
    context_window: draft.contextWindow,
    max_output: draft.maxOutput,
  }
  const raw = (draft.apiKey ?? '').trim()
  if (raw !== '' && !isMasked(raw)) {
    dto.api_key = raw
  }
  return dto
}
