import '@fontsource-variable/noto-sans-sc'
import { createRoot } from 'react-dom/client'
import { useState, useEffect, useCallback, lazy, Suspense } from 'react'
import './styles/globals.css'
import i18n from './lib/i18n'
import { changeLanguage } from './lib/i18n'
import { onLocaleChange } from './bridge'
import { isFailure } from './lib/failure'

// INV-WV-05: silence expected rejections from the Config Mode RPC guard.
//
// Matches only a typed FailureError carrying `no_workspace` — never a raw
// message string, so a backend error that happens to contain those words still
// surfaces. Both the guard and the backend mint the same typed value on
// purpose (a caller cannot tell them apart, so it needs one code path); the
// round-trip case is separately recorded as a diag in bridge.ts, which is
// where the CONFIG_MODE_SAFE drift it signals can actually be read.
window.addEventListener('unhandledrejection', (e) => {
  if (isFailure(e.reason, 'no_workspace')) {
    e.preventDefault()
  }
})

// VS Code webview preload forwards keydown to the workbench. Tab then
// moves workbench focus and blurs the input. Stop bubbling at document
// so native tab-order stays on the page (covers `make dev` before editor rebuild).
function isTabbable(el: EventTarget | null): boolean {
  if (!(el instanceof HTMLElement)) return false
  const tag = el.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || tag === 'BUTTON' || tag === 'A') {
    return !(el as HTMLButtonElement).disabled
  }
  return el.isContentEditable || el.tabIndex >= 0
}
function stopWebviewTabLeak(e: KeyboardEvent) {
  if (e.key === 'Tab' && isTabbable(e.target)) e.stopPropagation()
}
document.addEventListener('keydown', stopWebviewTabLeak)
document.addEventListener('keyup', stopWebviewTabLeak)

function syncThemeClass() {
  const isDark = document.body.classList.contains('vscode-dark') ||
    document.body.classList.contains('vscode-high-contrast')
  document.documentElement.classList.toggle('dark', isDark)
}
syncThemeClass()
new MutationObserver(syncThemeClass).observe(document.body, { attributes: true, attributeFilter: ['class'] })

import { NavContext, type PageRoute } from './lib/nav'
import { FilePickerProvider, type FilePicker } from '@wesui/chat'
import { WesUIProvider, flattenStrings, toLocale } from '@wesui/locale'
import type { Locale } from '@wesui/locale'
import enStrings from './locales/en.json'
import { pickFiles, notifyReady } from './bridge'
import { useBillingStore } from './stores/billing'

onLocaleChange(changeLanguage)

// Forward the caller's options. Dropping them silently is how the paperclip
// (`allowFolders: false`) still got a dialog that could return a directory.
const wescodeFilePicker: FilePicker = async (opts) => pickFiles(opts)

const ContactsPage = lazy(() => import('./pages/ContactsPage').then(m => ({ default: m.ContactsPage })))
const NewAgentPage = lazy(() => import('./pages/NewAgentPage').then(m => ({ default: m.NewAgentPage })))
const AgentDetailPage = lazy(() => import('./pages/AgentDetailPage').then(m => ({ default: m.AgentDetailPage })))
const SkillsPage = lazy(() => import('./pages/SkillsPage').then(m => ({ default: m.SkillsPage })))
const SettingsPage = lazy(() => import('./pages/SettingsPage').then(m => ({ default: m.SettingsPage })))
const ChatMessages = lazy(() => import('./pages/ChatMessages').then(m => ({ default: m.ChatMessages })))
const LoginPage = lazy(() => import('./pages/LoginPage').then(m => ({ default: m.LoginPage })))
const CronPage = lazy(() => import('./pages/CronPage').then(m => ({ default: m.CronPage })))
const CronNewPage = lazy(() => import('./pages/CronNewPage').then(m => ({ default: m.CronNewPage })))
const SkillWorkshopPage = lazy(() => import('./pages/SkillWorkshopPage').then(m => ({ default: m.SkillWorkshopPage })))
const MemoryPage = lazy(() => import('./pages/MemoryPage').then(m => ({ default: m.MemoryPage })))
const GroupConversationPage = lazy(() => import('./pages/GroupConversationPage').then(m => ({ default: m.GroupConversationPage })))
const ProjectOverviewPage = lazy(() => import('./pages/ProjectOverviewPage').then(m => ({ default: m.ProjectOverviewPage })))
const KnowledgePage = lazy(() => import('./pages/KnowledgePage').then(m => ({ default: m.KnowledgePage })))

import { GateGuard } from './components/GateGuard'

declare global {
  interface Window {
    __WESCODE_INIT__?: { page: string; params: Record<string, unknown> }
  }
}

function readInlineRoute(): PageRoute | null {
  const init = window.__WESCODE_INIT__
  if (!init) return null
  return { page: init.page, params: init.params } as PageRoute
}

// wesui 组件在产品页面里调 `useLocale().t`，查的是 wesui 自己的 en.ts——产品的键
// （`nav.me` / `account.title`）在那里不存在。不传这份包，英文用户看到的是调用点的中文，
// 而语言包闸门查的正是本文件，所以它报绿。缺一个 prop 的症状与「功能正常」同形。
// 模块作用域算一次：写在 JSX 里每次 render 都是新对象，会让 Provider 的 useMemo 恒失效。
const PRODUCT_EN = flattenStrings(enStrings)

function App() {
  const [route, setRoute] = useState<PageRoute | null>(readInlineRoute)
  const [locale, setLocale] = useState<Locale>(() => toLocale(i18n.language))

  useEffect(() => {
    notifyReady()
  }, [])

  useEffect(() => {
    useBillingStore.getState().loadWesBilling().catch(() => {})
    const onVisible = () => {
      if (document.visibilityState === 'visible') {
        useBillingStore.getState().loadWesBilling().catch(() => {})
      }
    }
    document.addEventListener('visibilitychange', onVisible)
    return () => document.removeEventListener('visibilitychange', onVisible)
  }, [])

  useEffect(() => {
    const langHandler = (lng: string) => setLocale(toLocale(lng))
    i18n.on('languageChanged', langHandler)
    return () => {
      i18n.off('languageChanged', langHandler)
    }
  }, [])

  useEffect(() => {
    const handler = (e: MessageEvent) => {
      if (e.data.type === 'navigate') {
        setRoute({ page: e.data.page, params: e.data.params } as PageRoute)
      } else if (e.data.type === 'setLocale') {
        changeLanguage(e.data.locale)
      }
    }
    window.addEventListener('message', handler)
    return () => window.removeEventListener('message', handler)
  }, [])

  const navigate = useCallback((r: PageRoute) => {
    setRoute(r)
  }, [])

  if (!route) {
    return (
      <div style={{ padding: 24, color: 'var(--vscode-errorForeground)' }}>
        Webview init data missing. This is a bug.
      </div>
    )
  }

  return (
    <WesUIProvider locale={locale} productEn={PRODUCT_EN}>
      <NavContext.Provider value={navigate}>
        <FilePickerProvider value={wescodeFilePicker}>
          <Suspense fallback={null}>
            {renderPage(route, navigate)}
          </Suspense>
        </FilePickerProvider>
      </NavContext.Provider>
    </WesUIProvider>
  )
}

function renderSettingsPage(route: PageRoute) {
  switch (route.page) {
    case 'settings': return <SettingsPage initialTab={route.params?.tab as string | undefined} />
    case 'login': return <LoginPage />
    case 'pageTokens': return <SettingsPage initialTab="tokens" />
    case 'pageLogs': return <SettingsPage initialTab="logs" />
    case 'data': {
      const tab = route.params?.tab
      if (tab === 'tokens') return <SettingsPage initialTab="tokens" />
      if (tab === 'logs') return <SettingsPage initialTab="logs" />
      return <SettingsPage initialTab="tokens" />
    }
    case 'provider': return <SettingsPage initialTab="model" />
    case 'about': return <SettingsPage initialTab="about" />
    case 'toolPolicy': return <SettingsPage initialTab="tool-policy" />
    case 'connections':
    case 'pageBrowser': return <SettingsPage initialTab="conn-browser" />
    case 'pageDesktop': return <SettingsPage initialTab="conn-desktop" />
    case 'pageChannels': return <SettingsPage initialTab="conn-channels" />
    case 'pageEmail': return <SettingsPage initialTab="conn-email" />
    case 'pageMcp': return <SettingsPage initialTab="conn-mcp" />
    case 'pagePlugins': return <SettingsPage initialTab="conn-plugins" />
    case 'engineGovernance': return <SettingsPage initialTab="engine-governance" />
    case 'engineMemoryPolicy': return <SettingsPage initialTab="engine-memory-policy" />
    case 'engineRunLimits': return <SettingsPage initialTab="engine-run-limits" />
    case 'engineCycleDetect': return <SettingsPage initialTab="engine-cycle-detect" />
    case 'engineContextBudget': return <SettingsPage initialTab="engine-context-budget" />
    case 'engineResilience': return <SettingsPage initialTab="engine-resilience" />
    default: return null
  }
}

function renderPage(route: PageRoute, _setRoute: (r: PageRoute) => void) {
  const settingsPage = renderSettingsPage(route)
  if (settingsPage) return settingsPage

  return (
    <GateGuard>
      {renderGatedPage(route)}
    </GateGuard>
  )
}

function renderGatedPage(route: PageRoute) {
  switch (route.page) {
    case 'contacts': return <ContactsPage />
    case 'newAgent': return <NewAgentPage />
    case 'agentDetail': return <AgentDetailPage agentId={route.params.agentId} />
    case 'skills': return <SkillsPage />
    case 'chatMessages':
    case 'chat': return <ChatMessages />
    case 'pageMemory': return <MemoryPage />
    case 'groupConversation': return <GroupConversationPage groupId={route.params.groupId} />
    case 'cron': return <CronPage />
    case 'cronNew': return <CronNewPage />
    case 'skillWorkshop': return <SkillWorkshopPage draftId={route.params?.draftId} />
    case 'projectOverview':
    case 'files': return <ProjectOverviewPage />
    case 'knowledge': return <KnowledgePage />
    default: return <ChatMessages />
  }
}

createRoot(document.getElementById('root')!).render(<App />)
