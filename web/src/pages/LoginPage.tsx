import { useState, useEffect } from 'react'
import { AuthForm } from '@wesui/auth'
import type { AuthMode } from '@wesui/auth'
import { request, closeTab } from '@/bridge'
import { useTranslation } from '@/lib/i18n'
import { isIdentityAuthenticated, type AuthState } from '@/lib/auth'

function notifyAuthSuccess(state: AuthState) {
  if (isIdentityAuthenticated(state.status)) {
    closeTab()
  }
}

export function LoginPage() {
  const { t } = useTranslation()
  const [mode, setMode] = useState<AuthMode>('login')
  const [checked, setChecked] = useState(false)

  useEffect(() => {
    request<AuthState>('authMe')
      .then(me => {
        if (isIdentityAuthenticated(me?.status)) {
          closeTab()
          return
        }
        setChecked(true)
      })
      .catch(() => setChecked(true))
  }, [])
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState<string | null>(null)

  if (!checked) return null

  const linkCls = `text-small text-[var(--vscode-textLink-foreground)] hover:underline cursor-pointer`

  const inputCls = [
    'bg-[var(--vscode-input-background)] text-[var(--vscode-input-foreground)]',
    'border-[var(--vscode-input-border,transparent)]',
    'focus:border-[var(--vscode-focusBorder)]',
    'placeholder:text-[var(--vscode-input-placeholderForeground)]',
  ].join(' ')

  return (
    <div className="min-h-screen flex items-center justify-center p-6"
      style={{ background: 'var(--vscode-editor-background)' }}>
      <div className="w-full max-w-sm space-y-6">

        {/* Logo + title */}
        <div className="text-center space-y-1">
          <div className="text-h1" style={{ color: 'var(--vscode-foreground)' }}>
            ✦ WES Code
          </div>
          <div className="text-small" style={{ color: 'var(--vscode-descriptionForeground)' }}>
            {mode === 'login' && t('login.loginTitle', '登录 weisyn 账号以开始使用')}
            {mode === 'register' && t('login.registerTitle', '创建 weisyn 账号')}
            {mode === 'reset' && t('login.resetTitle', '重置密码')}
          </div>
        </div>

        <AuthForm
          mode={mode}
          onModeChange={(m) => { setError(null); setSuccess(null); setMode(m) }}
          onLogin={async (email, pw) => {
            setError(null)
            try {
              const state = await request<AuthState>('authLogin', { email, password: pw })
              notifyAuthSuccess(state)
            } catch (e: any) {
              const msg = e?.message || ''
              if (msg.includes('401') || msg.includes('password') || msg.includes('密码')) {
                setError(t('login.wrongCredentials', '邮箱或密码错误'))
              } else if (msg.includes('unreachable') || msg.includes('service')) {
                setError(t('login.networkError', '无法连接到 weisyn 平台，请检查网络'))
              } else {
                setError(msg || t('login.loginFailed', '登录失败，请稍后重试'))
              }
              throw e
            }
          }}
          onRegister={async (email, pw, code) => {
            setError(null)
            try {
              const state = await request<AuthState>('authRegister', { email, password: pw, code })
              notifyAuthSuccess(state)
            } catch (e: any) {
              setError(e?.message || t('login.registerFailed', '注册失败，请稍后重试'))
              throw e
            }
          }}
          onSendCode={async (email, purpose) => {
            setError(null)
            try {
              await request('authSendCode', { email, purpose })
              setSuccess(t('login.codeSent', '验证码已发送，请查收邮件'))
            } catch (e: any) {
              setError(e?.message || t('login.sendFailed', '发送失败，请稍后重试'))
              throw e
            }
          }}
          onResetPassword={async (email, code, newPw) => {
            setError(null)
            try {
              await request('authResetPassword', { email, code, newPassword: newPw })
              setSuccess(t('login.resetSuccess', '密码已重置，请使用新密码登录'))
            } catch (e: any) {
              setError(e?.message || t('login.resetFailed', '重置失败，请稍后重试'))
              throw e
            }
          }}
          error={error}
          success={success}
          inputClassName={inputCls}
        />

        <p className="text-caption text-center" style={{ color: 'var(--vscode-descriptionForeground)' }}>
          {t('login.termsPrefix', '登录即代表同意 weisyn ')}{' '}
          <a href="https://www.weisyn.com/terms" target="_blank" rel="noopener" className={linkCls}>{t('login.termsLink', '服务条款')}</a>
          {' '}{t('login.termsAnd', '与')}{' '}
          <a href="https://www.weisyn.com/privacy" target="_blank" rel="noopener" className={linkCls}>{t('login.privacyLink', '隐私政策')}</a>
        </p>
      </div>
    </div>
  )
}
