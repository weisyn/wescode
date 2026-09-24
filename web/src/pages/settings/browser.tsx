import { useMemo } from 'react'
import { BrowserSettingsWrapper, type BrowserSettingsAdapter } from '@wesui/connections'
import type { BrowserSettings, BrowserStatus } from '@wesui/connections'
import { request } from '@/bridge'

interface LocalBrowserConfig {
  enabled: boolean
  mode: 'user' | 'sandbox'
  has_token: boolean
}

interface LocalBrowserStatus {
  extensionConnected: boolean
  mcpReady: boolean
  tokenInvalid?: boolean
}

function toSettings(cfg: LocalBrowserConfig): BrowserSettings {
  return { enabled: cfg.enabled, mode: cfg.mode, hasToken: cfg.has_token }
}

function deriveStatus(cfg: LocalBrowserConfig, st: LocalBrowserStatus): BrowserStatus {
  if (!cfg.enabled) return { state: 'unavailable' }
  if (st.extensionConnected) return { state: 'connected' }
  if (st.tokenInvalid) return { state: 'token_invalid' }
  if (st.mcpReady) return { state: 'ready' }
  return { state: 'disconnected' }
}

export function BrowserSettings() {
  const adapter = useMemo<BrowserSettingsAdapter>(() => ({
    load: async () => {
      const [cfgRes, stRes] = await Promise.allSettled([
        request<LocalBrowserConfig>('sidebar/browserSettings'),
        request<LocalBrowserStatus>('sidebar/browserStatus'),
      ])
      const cfg: LocalBrowserConfig = cfgRes.status === 'fulfilled' ? cfgRes.value : { enabled: false, mode: 'user', has_token: false }
      const st: LocalBrowserStatus = stRes.status === 'fulfilled' ? stRes.value : { extensionConnected: false, mcpReady: false }
      return { settings: toSettings(cfg), status: deriveStatus(cfg, st) }
    },
    toggleEnabled: (enabled) => request<LocalBrowserConfig>('sidebar/browserToggle', { enabled }).then(toSettings),
    saveToken: (token) => request<LocalBrowserConfig>('sidebar/browserSaveToken', { token }).then(toSettings),
    clearToken: () => request<LocalBrowserConfig>('sidebar/browserSaveToken', { token: '' }).then(toSettings),
    connect: () => request('sidebar/browserConnect'),
  }), [])

  return <BrowserSettingsWrapper adapter={adapter} />
}
