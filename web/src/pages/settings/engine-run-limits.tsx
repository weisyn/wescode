import { useState, useEffect, useCallback } from 'react'
import { useLocale, tk, type TLabel } from '@wesui'
import { request } from '@/bridge'
import { Button } from '@/components/ui/Button'
import { saveEngineSettings, saveHintClass, type SaveHint } from './save-settings'

interface RunLimitsData {
  timeoutSeconds: number
  panicStopTurns: number
  execTimeoutSeconds: number
  readMaxChars: number
  grepMaxResults: number
  maxConcurrentRuns: number
  tokensPerMinute: number
  tokensPerDay: number
}

interface RunLimitField {
  key: keyof RunLimitsData
  label: TLabel
  unit: TLabel
}

// The label and its Chinese text travel together for the same reason they do in
// save-settings.ts: `t(`engine_settings.run_${key}_label`, key)` reads like it
// resolves, but the fallback IS the field name, and none of those keys were ever
// written to en.json. Both languages therefore rendered this page as eight rows
// of `timeoutSeconds` / `panicStopTurns` / … with no unit beside them — the
// fallback made a missing translation indistinguishable from a present one.
const SECTION_RUN_FIELDS: RunLimitField[] = [
  {
    key: 'timeoutSeconds',
    label: tk('engine_settings.run_timeout_seconds_label', '单次运行超时'),
    unit: tk('engine_settings.unit_seconds', '秒'),
  },
  {
    key: 'panicStopTurns',
    // Not a turn cap: the engine does not terminate on turn count
    // (INV-TERM-01). This is the runaway-loop safety net.
    label: tk('engine_settings.run_panic_stop_turns_label', '熔断轮数'),
    unit: tk('engine_settings.unit_turns', '轮'),
  },
]

const SECTION_TOOL_FIELDS: RunLimitField[] = [
  {
    key: 'execTimeoutSeconds',
    label: tk('engine_settings.run_exec_timeout_seconds_label', '命令执行超时'),
    unit: tk('engine_settings.unit_seconds', '秒'),
  },
  {
    key: 'readMaxChars',
    label: tk('engine_settings.run_read_max_chars_label', '单次读取上限'),
    unit: tk('engine_settings.unit_chars', '字符'),
  },
  {
    key: 'grepMaxResults',
    label: tk('engine_settings.run_grep_max_results_label', '搜索结果上限'),
    unit: tk('engine_settings.unit_items', '条'),
  },
]

const SECTION_RESOURCE_FIELDS: RunLimitField[] = [
  {
    key: 'maxConcurrentRuns',
    label: tk('engine_settings.run_max_concurrent_runs_label', '并发运行数'),
    unit: tk('engine_settings.unit_count', '个'),
  },
  {
    key: 'tokensPerMinute',
    label: tk('engine_settings.run_tokens_per_minute_label', '每分钟 token 上限'),
    unit: tk('engine_settings.unit_tokens', 'tokens'),
  },
  {
    key: 'tokensPerDay',
    label: tk('engine_settings.run_tokens_per_day_label', '每天 token 上限'),
    unit: tk('engine_settings.unit_tokens', 'tokens'),
  },
]

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
          placeholder="0"
          className="w-24 px-3 py-1.5 text-body bg-transparent text-text focus:outline-none"
          style={{ border: 'none', outline: 'none', boxShadow: 'none' }}
        />
      </div>
      <span className="text-small text-dim">{unit}</span>
    </div>
  )
}

export function RunLimitsSettings() {
  const { t } = useLocale()
  const [data, setData] = useState<RunLimitsData | null>(null)
  const [loading, setLoading] = useState(true)
  const [failed, setFailed] = useState(false)
  const [saving, setSaving] = useState(false)
  const [dirty, setDirty] = useState(false)

  useEffect(() => {
    request<RunLimitsData>('sidebar/getRunLimitsSettings')
      .then(d => setData(d))
      .catch(() => setFailed(true))
      .finally(() => setLoading(false))
  }, [])

  const update = useCallback((key: keyof RunLimitsData, value: number) => {
    setData(prev => prev ? { ...prev, [key]: value } : prev)
    setDirty(true)
  }, [])

  const [saveHint, setSaveHint] = useState<SaveHint | null>(null)
  const handleSave = useCallback(async () => {
    if (!data || !dirty) return
    setSaving(true)
    // Every field on this page lands in run_limits / tool_limits, both
    // resolved once at Cell.Start — so the hint is normally the restart one.
    // `readMaxChars` in particular spent its whole life looking editable
    // while its engine-side mapping was missing; a bare 已保存 here is the
    // same shape of silence.
    const { ok, hint } = await saveEngineSettings('sidebar/updateRunLimitsSettings', data, t)
    // Stay dirty on failure — the numbers on screen are still an unsaved edit.
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

  const renderSection = (title: string, fields: RunLimitField[]) => (
    <div className="rounded-md border border-border bg-surface p-4 space-y-4">
      <h3 className="text-h3 text-text">{title}</h3>
      <div className="space-y-3">
        {fields.map(field => (
          <NumberField
            key={field.key}
            label={t(field.label.k, field.label.zh)}
            value={data[field.key]}
            unit={t(field.unit.k, field.unit.zh)}
            onChange={v => update(field.key, v)}
          />
        ))}
      </div>
    </div>
  )

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-h2 text-text mb-1">{t('engine_settings.run_limits_title', '高级设置')}</h2>
        <p className="text-small text-dim">{t('engine_settings.run_limits_desc', '运行参数、工具执行限制和资源配额。通常无需修改，引擎已针对编程场景优化默认值。')}</p>
      </div>

      {/* 终止哲学说明 */}
      <div className="rounded-md border border-border bg-surface-2 p-3">
        <p className="text-small text-dim">
          {t('engine_settings.termination_note', '引擎不因轮次或 token 数终止正常运行的任务。循环检测器（CycleDetector）是唯一的引擎内终止来源——它检测"可证明的死循环"，而不是"运行太久"。')}
        </p>
      </div>

      {renderSection(t('engine_settings.section_run', '单次运行'), SECTION_RUN_FIELDS)}
      {renderSection(t('engine_settings.section_tool', '工具执行'), SECTION_TOOL_FIELDS)}
      {renderSection(t('engine_settings.section_resource', '资源限制'), SECTION_RESOURCE_FIELDS)}

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
