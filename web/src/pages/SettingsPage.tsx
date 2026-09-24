import { useState, useEffect, type ReactNode } from 'react'
import { useAuthCheck, AuthGateBlock, WorkspaceGateBlock } from '@/components/GateGuard'
import { AboutSettings } from './settings/about'
import { AccountSettings } from './settings/account'
import { AppearanceSettings } from './settings/appearance'
import { BrowserSettings } from './settings/browser'
import { ChannelsSettings } from './settings/channels'
import { DesktopSettings } from './settings/desktop'
import { EmailSettings } from './settings/email'
import { ContextBudgetSettings } from './settings/engine-context-budget'
import { CycleDetectSettings } from './settings/engine-cycle-detect'
import { GovernanceSettings } from './settings/engine-governance'
import { MemoryPolicySettings } from './settings/engine-memory-policy'
import { ResilienceSettings } from './settings/engine-resilience'
import { RunLimitsSettings } from './settings/engine-run-limits'
import { LogsSettings } from './settings/logs'
import { MCPSettings } from './settings/mcp'
import { ModelSettings } from './settings/model'
import { PluginsSettings } from './settings/plugins'
import { ProfileSettings } from './settings/profile'
import { TokensSettings } from './settings/tokens'
import { ToolPolicySection } from './settings/tool-policy'
import { getHasWorkspace, onHasWorkspaceChange } from '@/bridge'

interface SettingsPageProps {
  initialTab?: string
}

export function SettingsPage({ initialTab }: SettingsPageProps) {
  const activeId = initialTab || 'model'

  return (
    <div className="h-full overflow-y-auto scrollbar-thin">
      <div className="content-narrow py-6">
        <SettingsContent activeId={activeId} />
      </div>
    </div>
  )
}

// ─── Gate classification ───────────────────────────────────────────
//
// Every settings tab falls into exactly one of four tiers:
//
// Tier 0 (AUTH_EXEMPT): Always accessible — login entry points and
//         pure UI/info pages. No auth, no workspace check.
//         account | appearance | about | model (BYOK config entry)
//
// Tier 1 (AUTH_ONLY): Requires auth OR BYOK but no workspace.
//         profile (developer profile from weisyn platform)
//
// Tier 2 (AUTH + WORKSPACE): Requires auth OR BYOK AND a workspace
//         Cell. This is the default for all AI-engine/connection/data
//         tabs that read or mutate Cell state via RPC.
//
// Fail-closed: unknown / future tabs default to Tier 2.

const AUTH_EXEMPT_TABS = new Set(['account', 'appearance', 'about', 'model'])
const AUTH_ONLY_TABS = new Set(['profile'])

function SettingsContent({ activeId }: { activeId: string }) {
  if (AUTH_EXEMPT_TABS.has(activeId)) {
    return <SettingsContentInner activeId={activeId} />
  }
  if (AUTH_ONLY_TABS.has(activeId)) {
    return (
      <SettingsAuthGate>
        <SettingsContentInner activeId={activeId} />
      </SettingsAuthGate>
    )
  }
  return (
    <SettingsAuthGate>
      <SettingsCellGate>
        <SettingsContentInner activeId={activeId} />
      </SettingsCellGate>
    </SettingsAuthGate>
  )
}

function SettingsContentInner({ activeId }: { activeId: string }) {
  switch (activeId) {
    case 'account': return <AccountSettings />
    case 'appearance': return <AppearanceSettings />
    case 'model': return <ModelSettings />
    case 'tool-policy': return <ToolPolicySection />
    case 'conn-browser': return <BrowserSettings />
    case 'conn-desktop': return <DesktopSettings />
    case 'conn-channels': return <ChannelsSettings />
    case 'conn-email': return <EmailSettings />
    case 'conn-mcp': return <MCPSettings />
    case 'conn-plugins': return <PluginsSettings />
    case 'tokens': return <TokensSettings />
    case 'logs': return <LogsSettings />
    case 'about': return <AboutSettings />
    case 'profile': return <ProfileSettings />
    case 'engine-governance': return <GovernanceSettings />
    case 'engine-memory-policy': return <MemoryPolicySettings />
    case 'engine-run-limits': return <RunLimitsSettings />
    case 'engine-cycle-detect': return <CycleDetectSettings />
    case 'engine-context-budget': return <ContextBudgetSettings />
    case 'engine-resilience': return <ResilienceSettings />
    default: return null
  }
}

function SettingsAuthGate({ children }: { children: ReactNode }) {
  const auth = useAuthCheck()
  if (auth === null) return null
  if (!auth.authenticated && !auth.hasBYOK) return <AuthGateBlock />
  return <>{children}</>
}

function SettingsCellGate({ children }: { children: ReactNode }) {
  const [hasWs, setHasWs] = useState(getHasWorkspace)
  useEffect(() => onHasWorkspaceChange(setHasWs), [])
  if (hasWs) return <>{children}</>
  return <WorkspaceGateBlock />
}
