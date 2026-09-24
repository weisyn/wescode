import { useState, useEffect } from 'react'
import { User, Key, LogOut, Trash2, Wallet, Pencil } from 'lucide-react'
import { request, navigate, openExternal } from '../../bridge'
import { SettingSection, SettingRow } from '@wesui/settings'
import { FormField } from '@wesui/forms'
import { Button } from '../ui/Button'
import { useTranslation } from '@/lib/i18n'
import { useBillingStore } from '@/stores/billing'
import { formatWesBillingAccountText } from '@wesui/billing'
import { useLocale } from '@wesui/locale'
import { isIdentityAuthenticated, type AuthState } from '@/lib/auth'

function Input({ type = 'text', value, onChange, placeholder, className = '' }: {
  type?: string
  value: string
  onChange: (v: string) => void
  placeholder?: string
  className?: string
}) {
  return (
    <div className={`rounded border border-border-hi bg-surface overflow-hidden ${className}`}>
      <input
        type={type}
        value={value}
        onChange={e => onChange(e.target.value)}
        placeholder={placeholder}
        className="w-full h-8 px-3 text-small bg-transparent text-text placeholder:text-muted focus:outline-none"
        style={{ border: 'none', outline: 'none', boxShadow: 'none' }}
      />
    </div>
  )
}

export function AccountTab() {
  const { t } = useTranslation()
  const { t: wt } = useLocale()
  const [user, setUser] = useState<AuthState | null>(null)
  const [loading, setLoading] = useState(true)
  const billing = useBillingStore(s => s.wesBilling)
  const loadWesBilling = useBillingStore(s => s.loadWesBilling)

  const [editingName, setEditingName] = useState(false)
  const [displayName, setDisplayName] = useState('')
  const [savingName, setSavingName] = useState(false)
  // Three of the four writes on this page swallowed their rejection. A failed
  // rename left the field open with the typed text still in it, and a failed
  // sign-out or deletion left the button re-enabled — none of which is
  // distinguishable from "the click didn't register". Deletion is the worst of
  // the three: the one action whose silence reads as "already done".
  const [nameError, setNameError] = useState('')

  const [showPassword, setShowPassword] = useState(false)
  const [currentPwd, setCurrentPwd] = useState('')
  const [newPwd, setNewPwd] = useState('')
  const [confirmPwd, setConfirmPwd] = useState('')
  const [pwdError, setPwdError] = useState('')
  const [savingPwd, setSavingPwd] = useState(false)

  const [signingOut, setSigningOut] = useState(false)
  const [signOutError, setSignOutError] = useState('')

  const [showDelete, setShowDelete] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [deletePwd, setDeletePwd] = useState('')
  const [deleteError, setDeleteError] = useState('')

  useEffect(() => {
    let cancelled = false
    async function load() {
      try {
        const me = await request<AuthState>('authMe')
        if (cancelled) return
        setUser(me)
        setDisplayName(me.displayName || '')
        loadWesBilling().catch(() => {})
      } catch {
        // ignore
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    return () => { cancelled = true }
  }, [loadWesBilling])

  const isLoggedIn = isIdentityAuthenticated(user?.status)

  if (loading) {
    return <p className="text-body text-dim py-8 text-center">{t('common.loading', '加载中…')}</p>
  }

  if (!isLoggedIn) {
    return (
      <div className="flex flex-col items-center gap-4 py-12">
        <p className="text-body text-dim">{t('account.notLoggedIn', '未登录')}</p>
        <Button variant="primary" onClick={() => navigate('login')}>
          {t('account.loginBtn', '登录')}
        </Button>
      </div>
    )
  }

  const handleSaveName = async () => {
    setSavingName(true)
    setNameError('')
    try {
      await request('authUpdateProfile', { displayName })
      setUser(prev => prev ? { ...prev, displayName } : prev)
      setEditingName(false)
    } catch (e) {
      // Stay in edit mode with the typed value intact — closing the editor here
      // would show the old name back and read as a successful no-op.
      setNameError(e instanceof Error ? e.message : t('common.saveFailed', '保存失败'))
    } finally {
      setSavingName(false)
    }
  }

  const handleChangePassword = async () => {
    setPwdError('')
    if (newPwd.length < 8) {
      setPwdError(t('account.passwordMinLength', '新密码至少 8 位'))
      return
    }
    if (newPwd !== confirmPwd) {
      setPwdError(t('account.passwordMismatch', '两次输入密码不一致'))
      return
    }
    setSavingPwd(true)
    try {
      await request('authChangePassword', { currentPassword: currentPwd, newPassword: newPwd })
      setShowPassword(false)
      setCurrentPwd('')
      setNewPwd('')
      setConfirmPwd('')
      navigate('login')
    } catch (e) {
      setPwdError(e instanceof Error ? e.message : t('common.saveFailed', '保存失败'))
    } finally {
      setSavingPwd(false)
    }
  }

  const handleSignOut = async () => {
    setSigningOut(true)
    setSignOutError('')
    try {
      await request('authLogout')
      navigate('login')
    } catch (e) {
      // Without this the session survives and the page looks unchanged, so the
      // user walks away from a machine believing they are signed out.
      setSignOutError(e instanceof Error ? e.message : t('account.signOutFailed', '退出失败'))
    } finally {
      setSigningOut(false)
    }
  }

  const handleDeleteAccount = async () => {
    setDeleting(true)
    setDeleteError('')
    try {
      await request('authDeleteAccount', { currentPassword: deletePwd })
      navigate('login')
    } catch (e) {
      setDeleteError(e instanceof Error ? e.message : t('account.deleteFailed', '注销失败'))
    } finally {
      setDeleting(false)
    }
  }

  const billingView = formatWesBillingAccountText(billing, wt)

  return (
    <>
      <SettingSection title={t('account.title', '账号管理')}>
        <SettingRow icon={<User size={16} />} label={t('account.email', '邮箱')}>
          <span className="text-caption text-dim">{user?.email}</span>
        </SettingRow>

        <SettingRow icon={<Pencil size={16} />} label={t('account.displayName', '昵称')}>
          {editingName ? (
            <div className="flex flex-col items-end gap-1">
              <div className="flex items-center gap-2">
                <Input
                  value={displayName}
                  onChange={setDisplayName}
                  className="w-40"
                />
                <Button size="sm" variant="primary" onClick={() => void handleSaveName()} disabled={savingName || !displayName.trim()}>
                  {savingName ? t('account.saving', '保存中…') : t('account.save', '保存')}
                </Button>
                <Button size="sm" variant="outline" onClick={() => { setEditingName(false); setNameError(''); setDisplayName(user?.displayName || '') }}>
                  {t('common.cancel', '取消')}
                </Button>
              </div>
              {nameError && <span className="text-caption text-danger">{nameError}</span>}
            </div>
          ) : (
            <div className="flex items-center gap-2">
              <span className="text-caption text-dim">{user?.displayName || t('account.setName', '设置昵称')}</span>
              <button
                onClick={() => setEditingName(true)}
                className="text-caption text-accent hover:underline"
              >
                {t('common.edit', '编辑')}
              </button>
            </div>
          )}
        </SettingRow>
      </SettingSection>

      {billingView && (
        <SettingSection title={t('account.billing', 'WES 余额')}>
          <SettingRow icon={<Wallet size={16} />} label={t('account.billing', 'WES 余额')}>
            <div className="flex items-center gap-2">
              <span className={`text-caption ${billingView.warn ? 'text-warning' : 'text-success'}`}>
                {billingView.text}
              </span>
              {billingView.warn && billing.debtsUrl && (
                <button
                  type="button"
                  onClick={() => openExternal(billing.debtsUrl!)}
                  className="text-caption text-accent hover:underline"
                >
                  {wt('billing.pay_debts', '去结清')}
                </button>
              )}
            </div>
          </SettingRow>
        </SettingSection>
      )}

      <SettingSection title={t('account.changePassword', '修改密码')}>
        {!showPassword ? (
          <SettingRow icon={<Key size={16} />} label={t('account.changePassword', '修改密码')}>
            <Button size="sm" variant="outline" onClick={() => setShowPassword(true)}>
              {t('account.changePassword', '修改密码')}
            </Button>
          </SettingRow>
        ) : (
          <div className="px-4 py-4 space-y-4">
            <p className="text-caption text-dim">{t('account.passwordNote', '修改密码后需要重新登录')}</p>
            <FormField label={t('account.currentPassword', '当前密码')}>
              <Input
                type="password"
                value={currentPwd}
                onChange={setCurrentPwd}
                placeholder={t('account.currentPassword', '当前密码')}
              />
            </FormField>
            <FormField label={t('account.newPassword', '新密码（至少 8 位）')}>
              <Input
                type="password"
                value={newPwd}
                onChange={setNewPwd}
                placeholder={t('account.newPassword', '新密码（至少 8 位）')}
              />
            </FormField>
            <FormField label={t('account.confirmPassword', '确认新密码')} error={pwdError || undefined}>
              <Input
                type="password"
                value={confirmPwd}
                onChange={setConfirmPwd}
                placeholder={t('account.confirmPassword', '确认新密码')}
              />
            </FormField>
            <div className="flex gap-2 pt-1">
              <Button size="sm" variant="primary" onClick={() => void handleChangePassword()} disabled={savingPwd || !currentPwd || !newPwd}>
                {savingPwd ? t('account.saving', '保存中…') : t('account.save', '保存')}
              </Button>
              <Button size="sm" variant="outline" onClick={() => { setShowPassword(false); setPwdError('') }}>
                {t('account.cancel', '取消')}
              </Button>
            </div>
          </div>
        )}
      </SettingSection>

      <SettingSection title="">
        <SettingRow icon={<LogOut size={16} />} label={t('account.signOut', '退出登录')}>
          <div className="flex flex-col items-end gap-1">
            <Button size="sm" variant="outline" onClick={() => void handleSignOut()} disabled={signingOut}>
              {signingOut ? t('account.signingOut', '退出中…') : t('account.signOut', '退出登录')}
            </Button>
            {signOutError && <span className="text-caption text-danger">{signOutError}</span>}
          </div>
        </SettingRow>
      </SettingSection>

      <SettingSection title="">
        <SettingRow icon={<Trash2 size={16} />} label={t('account.deleteAccount', '注销账号')}>
          {!showDelete ? (
            <Button size="sm" variant="danger" onClick={() => setShowDelete(true)}>
              {t('account.deleteAccount', '注销账号')}
            </Button>
          ) : (
            <div className="flex flex-col gap-2">
              <p className="text-caption text-danger">{t('account.deleteWarning', '此操作不可撤销。注销后，您的所有数据将被永久删除。')}</p>
              <Input
                type="password"
                value={deletePwd}
                onChange={setDeletePwd}
                placeholder={t('account.currentPassword', '当前密码')}
                className="w-60"
              />
              <div className="flex gap-2">
                <Button size="sm" variant="danger-solid" onClick={() => void handleDeleteAccount()} disabled={deleting || !deletePwd}>
                  {deleting ? t('account.deleting', '注销中…') : t('account.confirmDelete', '确认注销')}
                </Button>
                <Button size="sm" variant="outline" onClick={() => { setShowDelete(false); setDeleteError(''); setDeletePwd('') }} disabled={deleting}>
                  {t('account.cancel', '取消')}
                </Button>
              </div>
              {deleteError && <span className="text-caption text-danger">{deleteError}</span>}
            </div>
          )}
        </SettingRow>
      </SettingSection>
    </>
  )
}
