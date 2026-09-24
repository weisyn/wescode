import { useState, useEffect, useCallback, type ReactNode } from 'react'
import { getHasWorkspace, onHasWorkspaceChange, onAuthChanged, openFolder, request, navigate } from '../bridge'
import { Button } from '@wesui/primitives'
import { useLocale } from '@wesui'
import { isIdentityAuthenticated, type AuthState } from '../lib/auth'

// ─── Shared auth check (single source of truth) ────────────────────

function raceTimeout<T>(promise: Promise<T>, ms: number, fallback: T): Promise<T> {
  let timer: ReturnType<typeof setTimeout>
  return Promise.race([
    promise,
    new Promise<T>(resolve => { timer = setTimeout(() => resolve(fallback), ms) }),
  ]).finally(() => clearTimeout(timer!))
}

export interface AuthCheckResult {
  authenticated: boolean
  hasBYOK: boolean
}

export async function checkAuth(): Promise<AuthCheckResult> {
  try {
    const authResult = await raceTimeout<AuthState>(
      request<AuthState>('authMe', {}), 3000, { status: 'anonymous' },
    )
    let models: Array<{ source: string; status: string }> = []
    try {
      models = await raceTimeout(
        request<{ models: Array<{ source: string; status: string }>; orgCatalogError?: string }>('availableModels', {}), 3000, { models: [] },
      ).then(p => p?.models ?? [])
      if (!Array.isArray(models)) models = []
    } catch {
      // Config Mode: no_workspace — expected, not an error.
    }
    const authenticated = isIdentityAuthenticated(authResult.status)
    const hasBYOK = models.some(m => m.source === 'byok' && m.status !== 'no_key')
    return { authenticated, hasBYOK }
  } catch {
    return { authenticated: false, hasBYOK: false }
  }
}

export function useAuthCheck(): AuthCheckResult | null {
  const [auth, setAuth] = useState<AuthCheckResult | null>(null)

  const check = useCallback(() => { void checkAuth().then(setAuth) }, [])

  useEffect(() => {
    let retryCount = 0
    const retryDelays = [1000, 2000, 3000, 5000, 8000]
    let timer: ReturnType<typeof setTimeout> | null = null
    let cancelled = false

    function run() {
      checkAuth().then(result => {
        if (cancelled) return
        setAuth(result)
        if (!result.authenticated && !result.hasBYOK && retryCount < retryDelays.length) {
          timer = setTimeout(() => { retryCount++; run() }, retryDelays[retryCount])
        }
      })
    }
    run()
    return () => { cancelled = true; if (timer) clearTimeout(timer) }
  }, [])

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | null = null
    return onAuthChanged(() => {
      if (timer) clearTimeout(timer)
      timer = setTimeout(check, 300 + Math.random() * 500)
    })
  }, [check])

  return auth
}

// ─── Shared gate UI blocks ─────────────────────────────────────────

export function AuthGateBlock() {
  const { t } = useLocale()
  return (
    <div className="h-full flex items-center justify-center p-6">
      <div className="flex flex-col items-center gap-4 max-w-xs text-center">
        <div className="text-h2 text-dim">✦</div>
        <p className="text-body" style={{ color: 'var(--vscode-foreground)' }}>
          {t('gate.login_title', '登录以启用 AI 编程能力')}
        </p>
        <p className="text-small" style={{ color: 'var(--vscode-descriptionForeground)' }}>
          {t('gate.login_detail', '登录后可用平台和企业模型；也可先在设置里添加自有 Key。')}
        </p>
        <div className="flex flex-col items-center gap-2">
          <Button variant="primary" size="sm" onClick={() => navigate('login')}>
            {t('message.go_login', '去登录')}
          </Button>
          <Button variant="ghost" size="sm" onClick={() => navigate('settings', { tab: 'model' })}>
            {t('gate.go_model_settings', '去配置模型')}
          </Button>
        </div>
      </div>
    </div>
  )
}

export function WorkspaceGateBlock() {
  const { t } = useLocale()
  return (
    <div className="h-full flex items-center justify-center p-6">
      <div className="flex flex-col items-center gap-4 max-w-sm text-center">
        <div className="text-h1 text-dim">📂</div>
        <p className="text-body" style={{ color: 'var(--vscode-foreground)' }}>
          {t('gate.open_folder_title', '打开项目文件夹以启用 AI 编程')}
        </p>
        <p className="text-small" style={{ color: 'var(--vscode-descriptionForeground)' }}>
          {t('gate.open_folder_detail', 'WES Code 需要理解你的项目结构，才能提供代码智能和编辑增强')}
        </p>
        <Button variant="primary" size="md" onClick={() => openFolder()}>
          {t('gate.open_folder_btn', '打开文件夹')}
        </Button>
      </div>
    </div>
  )
}

// ─── GateGuard: unified gate for function pages ────────────────────

export function GateGuard({ children }: { children: ReactNode }) {
  const auth = useAuthCheck()
  const [hasWs, setHasWs] = useState(getHasWorkspace)

  useEffect(() => onHasWorkspaceChange(setHasWs), [])

  if (auth === null) {
    return (
      <div className="h-full flex items-center justify-center p-6">
        <p className="text-small text-dim">Connecting…</p>
      </div>
    )
  }
  if (!auth.authenticated && !auth.hasBYOK) return <AuthGateBlock />
  if (!hasWs) return <WorkspaceGateBlock />
  return <>{children}</>
}
