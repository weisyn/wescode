import { useState, useEffect, useCallback } from 'react'
import { useLocale } from '@wesui'
import { request } from '@/bridge'
import { Button } from '@/components/ui/Button'
import { saveEngineSettings, saveHintClass, type SaveHint } from './save-settings'

interface ResilienceData {
  retryMaxAttempts: number
  healthThreshold: number
  firstByteTimeoutSeconds: number
  interChunkTimeoutSeconds: number
  maxOverloadRetries: number
}

function NumberField({ label, value, unit, onChange }: {
  label: string; value: number; unit: string
  onChange: (v: number) => void
}) {
  return (
    <div className="flex items-center gap-3">
      <label className="text-body text-text w-40 shrink-0">{label}</label>
      <div className="rounded border border-border-hi bg-surface overflow-hidden">
        <input
          type="number"
          value={value}
          onChange={e => onChange(Number(e.target.value))}
          className="w-24 px-3 py-1.5 text-body bg-transparent text-text focus:outline-none"
          style={{ border: 'none', outline: 'none', boxShadow: 'none' }}
        />
      </div>
      <span className="text-small text-dim">{unit}</span>
    </div>
  )
}

export function ResilienceSettings() {
  const { t } = useLocale()
  const [data, setData] = useState<ResilienceData | null>(null)
  const [loading, setLoading] = useState(true)
  const [failed, setFailed] = useState(false)
  const [saving, setSaving] = useState(false)
  const [dirty, setDirty] = useState(false)

  useEffect(() => {
    request<ResilienceData>('sidebar/getResilienceSettings')
      .then(d => setData(d))
      .catch(() => setFailed(true))
      .finally(() => setLoading(false))
  }, [])

  const update = useCallback((key: keyof ResilienceData, value: number) => {
    setData(prev => prev ? { ...prev, [key]: value } : prev)
    setDirty(true)
  }, [])

  const [saveHint, setSaveHint] = useState<SaveHint | null>(null)
  const handleSave = useCallback(async () => {
    if (!data || !dirty) return
    setSaving(true)
    // These five apply live: the whole resilience surface sits behind the
    // cognitive chain, which UpdateSpec rebuilds and swaps atomically. Until
    // recently they reached nothing at all — the page was read-only in
    // effect — so the confirmation here is the engine's, not this page's.
    const { ok, hint } = await saveEngineSettings('sidebar/updateResilienceSettings', data, t)
    // Stay dirty on failure so the retry path is the same button.
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

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-h2 text-text mb-1">{t('engine_settings.resilience_title', '韧性与重试')}</h2>
        <p className="text-small text-dim">{t('engine_settings.resilience_desc', '控制 LLM 调用失败时的重试策略和健康检测参数')}</p>
      </div>

      {/* LLM 重试 */}
      <div className="rounded-md border border-border bg-surface p-4 space-y-4">
        <h3 className="text-h3 text-text">{t('engine_settings.llm_retry', 'LLM 重试')}</h3>
        <p className="text-small text-dim">{t('engine_settings.llm_retry_desc', 'LLM 调用失败后自动重试的次数')}</p>
        <NumberField
          label={t('engine_settings.retry_count', '重试次数')}
          value={data.retryMaxAttempts}
          unit={t('engine_settings.unit_times', '次')}
          onChange={v => update('retryMaxAttempts', v)}
        />
        <NumberField
          label={t('engine_settings.overload_retries', '过载重试')}
          value={data.maxOverloadRetries}
          unit={t('engine_settings.unit_times', '次')}
          onChange={v => update('maxOverloadRetries', v)}
        />
      </div>

      {/* 健康跟踪 */}
      <div className="rounded-md border border-border bg-surface p-4 space-y-4">
        <h3 className="text-h3 text-text">{t('engine_settings.health_tracking', '健康跟踪')}</h3>
        <p className="text-small text-dim">{t('engine_settings.health_tracking_desc', 'Provider 健康状态的滑动窗口周期')}</p>
        <NumberField
          label={t('engine_settings.health_window', '健康阈值')}
          value={data.healthThreshold}
          unit={t('engine_settings.unit_seconds', '秒')}
          onChange={v => update('healthThreshold', v)}
        />
      </div>

      {/* 流超时 */}
      <div className="rounded-md border border-border bg-surface p-4 space-y-4">
        <h3 className="text-h3 text-text">{t('engine_settings.stream_timeout', '流超时')}</h3>
        <p className="text-small text-dim">{t('engine_settings.stream_timeout_desc', '流式响应的超时控制')}</p>
        <NumberField
          label={t('engine_settings.first_byte_timeout', '首字节超时')}
          value={data.firstByteTimeoutSeconds}
          unit={t('engine_settings.unit_seconds', '秒')}
          onChange={v => update('firstByteTimeoutSeconds', v)}
        />
        <NumberField
          label={t('engine_settings.inter_chunk_timeout', '分片间超时')}
          value={data.interChunkTimeoutSeconds}
          unit={t('engine_settings.unit_seconds', '秒')}
          onChange={v => update('interChunkTimeoutSeconds', v)}
        />
      </div>

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
