import { useState, useEffect, useCallback } from 'react'
import { useLocale } from '@wesui'
import { request } from '@/bridge'
import { Button } from '@/components/ui/Button'
import { saveEngineSettings, saveHintClass, type SaveHint } from './save-settings'

interface ContextBudgetData {
  appIdentity: string
  [key: string]: unknown
}

export function ContextBudgetSettings() {
  const { t } = useLocale()
  const [data, setData] = useState<ContextBudgetData | null>(null)
  const [loading, setLoading] = useState(true)
  const [failed, setFailed] = useState(false)
  const [saving, setSaving] = useState(false)
  const [dirty, setDirty] = useState(false)

  useEffect(() => {
    request<ContextBudgetData>('sidebar/getContextBudgetSettings')
      .then(d => setData(d))
      .catch(() => setFailed(true))
      .finally(() => setLoading(false))
  }, [])

  const [saveHint, setSaveHint] = useState<SaveHint | null>(null)
  const handleSave = useCallback(async () => {
    if (!data || !dirty) return
    setSaving(true)
    // The hint comes from the engine, not from a guess here. `appIdentity`
    // hot-applies (the assembler re-reads it per Run); the numeric context
    // parameters below it do not. This page used to claim the opposite for
    // both — see save-settings.ts.
    const { ok, hint } = await saveEngineSettings('sidebar/updateContextBudgetSettings', data, t)
    // The form stays dirty on failure: the button must remain live, because
    // what is on screen is still an unsaved edit.
    if (ok) setDirty(false)
    setSaveHint(hint)
    setTimeout(() => setSaveHint(null), 6000)
    setSaving(false)
  }, [data, dirty, t])

  if (failed) {
    return <div className="p-6 text-dim text-body">{t('engine_settings.workspace_required', '打开项目文件夹后可配置引擎设置')}</div>
  }
  if (loading || !data) {
    return <div className="p-6 text-dim text-body">{t('engine_settings.loading', '加载中...')}</div>
  }

  const extraFields = Object.entries(data).filter(([k]) => k !== 'appIdentity')

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-h2 text-text mb-1">{t('engine_settings.context_budget_title', '系统指令')}</h2>
        <p className="text-small text-dim">{t('engine_settings.context_budget_desc', '自定义注入每次 AI 对话的系统级指令')}</p>
      </div>

      {/* 系统 Prompt */}
      <div className="rounded-md border border-border bg-surface p-4 space-y-4">
        <h3 className="text-h3 text-text">{t('engine_settings.system_prompt', '系统 Prompt')}</h3>
        <p className="text-small text-dim">{t('engine_settings.system_prompt_desc', '自定义系统级指令，会注入到每次 AI 对话的上下文中')}</p>
        <div className="rounded border border-border-hi bg-surface overflow-hidden">
          <textarea
            value={data.appIdentity}
            onChange={e => {
              setData(prev => prev ? { ...prev, appIdentity: e.target.value } : prev)
              setDirty(true)
            }}
            placeholder={t('engine_settings.system_prompt_placeholder', '自定义系统指令（留空使用默认）')}
            rows={6}
            className="w-full px-3 py-2 text-body bg-transparent text-text placeholder:text-muted focus:outline-none resize-y"
            style={{ border: 'none', outline: 'none', boxShadow: 'none', minHeight: 120 }}
          />
        </div>
      </div>

      {/* 上下文管理说明 */}
      <div className="rounded-md border border-border bg-surface-2 p-3">
        <p className="text-small text-dim">
          {t('engine_settings.context_auto_note', '上下文压缩与保留策略由引擎根据当前模型的上下文窗口自动计算，无需手动配置。长对话时 AI 会自动压缩历史消息，保留重要内容。')}
        </p>
      </div>

      {/* 高级上下文参数（通常无需修改） */}
      {extraFields.length > 0 && (
        <details className="rounded-md border border-border bg-surface overflow-hidden">
          <summary className="p-4 cursor-pointer hover:bg-surface-3 transition-colors duration-fast">
            <span className="text-h3 text-text">{t('engine_settings.context_params', '高级上下文参数')}</span>
            <span className="text-caption text-dim ml-2">{t('engine_settings.context_params_note', '（通常无需修改）')}</span>
          </summary>
          <div className="p-4 pt-0 space-y-4">
          <div className="space-y-3">
            {extraFields.map(([key, value]) => (
              <div key={key} className="flex items-center gap-3">
                <label className="text-body text-text w-48 shrink-0">{key}</label>
                {typeof value === 'number' ? (
                  <div className="rounded border border-border-hi bg-surface overflow-hidden">
                    <input
                      type="number"
                      value={value}
                      onChange={e => {
                        setData(prev => prev ? { ...prev, [key]: Number(e.target.value) } : prev)
                        setDirty(true)
                      }}
                      className="w-24 px-3 py-1.5 text-body bg-transparent text-text focus:outline-none"
                      style={{ border: 'none', outline: 'none', boxShadow: 'none' }}
                    />
                  </div>
                ) : typeof value === 'boolean' ? (
                  <button
                    onClick={() => {
                      setData(prev => prev ? { ...prev, [key]: !value } : prev)
                      setDirty(true)
                    }}
                    className={`relative w-10 h-5 rounded-full transition-colors duration-fast ${
                      value ? 'bg-accent' : 'bg-surface-3'
                    }`}
                  >
                    <span
                      className={`absolute top-0.5 left-0.5 w-4 h-4 rounded-full bg-white transition-transform duration-fast ${
                        value ? 'translate-x-5' : ''
                      }`}
                    />
                  </button>
                ) : (
                  <div className="flex-1 rounded border border-border-hi bg-surface overflow-hidden">
                    <input
                      type="text"
                      value={String(value ?? '')}
                      onChange={e => {
                        setData(prev => prev ? { ...prev, [key]: e.target.value } : prev)
                        setDirty(true)
                      }}
                      className="w-full px-3 py-1.5 text-body bg-transparent text-text focus:outline-none"
                      style={{ border: 'none', outline: 'none', boxShadow: 'none' }}
                    />
                  </div>
                )}
              </div>
            ))}
          </div>
          </div>
        </details>
      )}

      <div className="flex items-center justify-end gap-3">
        {saveHint && (
          <span className={`text-small ${saveHintClass(saveHint.tone)}`}>
            {saveHint.text}
          </span>
        )}
        <Button variant="primary" size="sm" onClick={handleSave} disabled={!dirty || saving}>
          {saving ? t('engine_settings.saving', '保存中...') : t('engine_settings.save', '保存')}
        </Button>
      </div>
    </div>
  )
}
