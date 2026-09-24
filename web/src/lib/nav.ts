import { createContext, useContext } from 'react'

export type PageRoute =
  | { page: 'contacts' }
  | { page: 'newAgent' }
  | { page: 'agentDetail'; params: { agentId: string } }
  | { page: 'skills' }
  | { page: 'settings'; params?: { tab?: string } }
  | { page: 'chat'; params?: { sessionId?: string; agentId?: string } }
  | { page: 'chatMessages' }
  | { page: 'login' }
  | { page: 'pageMemory' }
  | { page: 'pageTokens' }
  | { page: 'pageLogs' }
  | { page: 'data'; params?: { tab?: string } }
  | { page: 'cron' }
  | { page: 'cronNew'; params?: { presetId?: string } }
  | { page: 'skillWorkshop'; params?: { draftId?: string } }
  | { page: 'provider'; params?: { name?: string } }
  | { page: 'about' }
  | { page: 'toolPolicy' }
  | { page: 'connections'; params?: { tab?: string } }
  | { page: 'pageBrowser' }
  | { page: 'pageDesktop' }
  | { page: 'pageChannels' }
  | { page: 'pageEmail' }
  | { page: 'pageMcp' }
  | { page: 'pagePlugins' }
  | { page: 'groupConversation'; params: { groupId: string } }
  | { page: 'projectOverview' }
  | { page: 'files' }
  | { page: 'knowledge' }
  | { page: 'engineGovernance' }
  | { page: 'engineMemoryPolicy' }
  | { page: 'engineRunLimits' }
  | { page: 'engineCycleDetect' }
  | { page: 'engineContextBudget' }
  | { page: 'engineResilience' }

export type NavigateFn = (route: PageRoute) => void

export const NavContext = createContext<NavigateFn>(() => {})

export function useNav(): NavigateFn {
  return useContext(NavContext)
}
