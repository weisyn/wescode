// LocalStorage-backed cache for provider verify status.
//
// Motivation: when the user reopens the settings page, we want to
// show "Verified · 3 minutes ago" for previously-tested providers
// rather than "Not tested". Without persistence, every mount would
// clear the badge and pressure users to re-hit "Test".
//
// Scope: only the *last-known verify outcome* per provider name is
// cached. No secrets, no models, no config. The cache is scoped by
// the app-supplied cacheKey (e.g. "wescode-provider-verify-cache").
// Apps that pass no cacheKey opt out entirely.
//
// Storage failures (quota exceeded, private browsing) are swallowed —
// caching is best-effort and MUST NOT block the CRUD flow.

import { parseProbeClass } from '@wesui/llm'
import type { LLMProvider, ProviderStatus } from '@wesui/llm'

// Derived from the provider shape rather than restated, so retiring a probe
// class from the engine domain shrinks the cache's type in the same build.
export type VerifyProbeClass = LLMProvider['testError']

export interface VerifyEntry {
  status: Extract<ProviderStatus, 'ok' | 'error'>
  lastTestedAt: string
  testError?: VerifyProbeClass
}

export type VerifyCacheMap = Record<string, VerifyEntry>

export class VerifyCache {
  readonly key: string | undefined

  constructor(key: string | undefined) {
    this.key = key
  }

  // localStorage is input from a *previous build*, so it is as untrusted as
  // the wire: entries written before the probe vocabulary closed hold classes
  // (`not_supported`, `internal`) that no longer exist, and a cast would carry
  // them into the card's label lookup as a blank badge. Narrowing here keeps
  // the timestamp and the error status — what the badge is actually for — and
  // drops only the class it cannot name.
  load(): VerifyCacheMap {
    if (!this.key) return {}
    let parsed: unknown
    try {
      const raw = localStorage.getItem(this.key)
      if (!raw) return {}
      parsed = JSON.parse(raw)
    } catch {
      return {}
    }
    if (typeof parsed !== 'object' || parsed === null) return {}
    const out: VerifyCacheMap = {}
    for (const [name, entry] of Object.entries(parsed as Record<string, unknown>)) {
      if (typeof entry !== 'object' || entry === null) continue
      const { status, lastTestedAt, testError } = entry as Record<string, unknown>
      if (status !== 'ok' && status !== 'error') continue
      if (typeof lastTestedAt !== 'string') continue
      out[name] = {
        status,
        lastTestedAt,
        testError: typeof testError === 'string' ? parseProbeClass(testError) : undefined,
      }
    }
    return out
  }

  save(name: string, entry: VerifyEntry): void {
    if (!this.key) return
    try {
      const map = this.load()
      map[name] = entry
      localStorage.setItem(this.key, JSON.stringify(map))
    } catch {
      /* ignore — quota exhausted or private browsing */
    }
  }
}
