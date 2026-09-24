import { useState, useEffect, useCallback } from 'react'
import { useLocale } from '@wesui'
import { request } from '@/bridge'
import { Button } from '@/components/ui/Button'
import { saveEngineSettings, saveHintClass, type SaveHint } from './save-settings'

interface ToolOverride {
  name: string
  warn: number
  term: number
}

interface CycleDetectData {
  explorationWarn: number
  explorationTerm: number
  actionWarn: number
  actionTerm: number
  identicalWarn: number
  identicalTerm: number
  toolOverrides: ToolOverride[]
}

// Backend uses map[string][2]int; frontend uses {name, warn, term}[].
function mapFromBackend(raw: Record<string, [number, number]> | null): ToolOverride[] {
  if (!raw) return []
  return Object.entries(raw).map(([name, [warn, term]]) => ({ name, warn, term }))
}

function mapToBackend(overrides: ToolOverride[]): Record<string, [number, number]> {
  const out: Record<string, [number, number]> = {}
  for (const o of overrides) {
    out[o.name] = [o.warn, o.term]
  }
  return out
}

function ThresholdPair({ label, warn, term, warnLabel, termLabel, onWarnChange, onTermChange }: {
  label: string; warn: number; term: number; warnLabel: string; termLabel: string
  onWarnChange: (v: number) => void; onTermChange: (v: number) => void
}) {
  return (
    <div className="flex items-center gap-3">
      <label className="text-body text-text w-40 shrink-0">{label}</label>
      <div className="flex items-center gap-2">
        <span className="text-caption text-dim w-8">{warnLabel}</span>
        <div className="rounded border border-border-hi bg-surface overflow-hidden">
          <input
            type="number"
            value={warn}
            onChange={e => onWarnChange(Number(e.target.value))}
            className="w-16 px-2 py-1.5 text-body bg-transparent text-text focus:outline-none text-center"
            style={{ border: 'none', outline: 'none', boxShadow: 'none' }}
          />
        </div>
        <span className="text-caption text-dim w-8">{termLabel}</span>
        <div className="rounded border border-border-hi bg-surface overflow-hidden">
          <input
            type="number"
            value={term}
            onChange={e => onTermChange(Number(e.target.value))}
            className="w-16 px-2 py-1.5 text-body bg-transparent text-text focus:outline-none text-center"
            style={{ border: 'none', outline: 'none', boxShadow: 'none' }}
          />
        </div>
      </div>
    </div>
  )
}

export function CycleDetectSettings() {
  const { t } = useLocale()
  const [data, setData] = useState<CycleDetectData | null>(null)
  const [loading, setLoading] = useState(true)
  const [failed, setFailed] = useState(false)
  const [saving, setSaving] = useState(false)
  const [dirty, setDirty] = useState(false)

  useEffect(() => {
    request<any>('sidebar/getCycleDetectSettings')
      .then(d => setData({
        ...d,
        toolOverrides: mapFromBackend(d.toolOverrides),
      }))
      .catch(() => setFailed(true))
      .finally(() => setLoading(false))
  }, [])

  const update = useCallback(<K extends keyof CycleDetectData>(key: K, value: CycleDetectData[K]) => {
    setData(prev => prev ? { ...prev, [key]: value } : prev)
    setDirty(true)
  }, [])

  const updateOverride = useCallback((idx: number, field: 'warn' | 'term', value: number) => {
    if (!data) return
    const next = data.toolOverrides.map((o, i) => i === idx ? { ...o, [field]: value } : o)
    update('toolOverrides', next)
  }, [data, update])

  const [saveHint, setSaveHint] = useState<SaveHint | null>(null)
  const handleSave = useCallback(async () => {
    if (!data || !dirty) return
    setSaving(true)
    // CycleDetect applies live (the detector reads the Cell config per Run),
    // but the hint still comes from the engine rather than from a literal
    // here — one classification, in one place.
    const { ok, hint } = await saveEngineSettings('sidebar/updateCycleDetectSettings', {
      ...data,
      toolOverrides: mapToBackend(data.toolOverrides),
    }, t)
    // Stay dirty on failure: the thresholds on screen are an unsaved edit, and
    // the button is the only way back.
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
        <h2 className="text-h2 text-text mb-1">{t('engine_settings.cycle_detect_title', '循环检测')}</h2>
        <p className="text-small text-dim">{t('engine_settings.cycle_detect_desc', '当 AI 重复调用同类工具超过阈值时，触发警告或终止运行')}</p>
      </div>

      {/* 探索类工具 */}
      <div className="rounded-md border border-border bg-surface p-4 space-y-4">
        <h3 className="text-h3 text-text">{t('engine_settings.exploration_tools', '探索类工具')}</h3>
        <p className="text-small text-dim">{t('engine_settings.exploration_tools_desc', 'read、grep、ls 等只读工具的连续调用阈值')}</p>
        <ThresholdPair
          label={t('engine_settings.threshold', '阈值')}
          warn={data.explorationWarn}
          term={data.explorationTerm}
          warnLabel={t('engine_settings.warn', '警告')}
          termLabel={t('engine_settings.term', '终止')}
          onWarnChange={v => update('explorationWarn', v)}
          onTermChange={v => update('explorationTerm', v)}
        />
      </div>

      {/* 操作类工具 */}
      <div className="rounded-md border border-border bg-surface p-4 space-y-4">
        <h3 className="text-h3 text-text">{t('engine_settings.action_tools', '操作类工具')}</h3>
        <p className="text-small text-dim">{t('engine_settings.action_tools_desc', 'edit、write、exec 等写入工具的连续调用阈值')}</p>
        <ThresholdPair
          label={t('engine_settings.threshold', '阈值')}
          warn={data.actionWarn}
          term={data.actionTerm}
          warnLabel={t('engine_settings.warn', '警告')}
          termLabel={t('engine_settings.term', '终止')}
          onWarnChange={v => update('actionWarn', v)}
          onTermChange={v => update('actionTerm', v)}
        />
      </div>

      {/* 相同调用检测 */}
      <div className="rounded-md border border-border bg-surface p-4 space-y-4">
        <h3 className="text-h3 text-text">{t('engine_settings.identical_call', '相同调用检测')}</h3>
        <p className="text-small text-dim">{t('engine_settings.identical_call_desc', '完全相同参数的工具调用连续出现的阈值')}</p>
        <ThresholdPair
          label={t('engine_settings.threshold', '阈值')}
          warn={data.identicalWarn}
          term={data.identicalTerm}
          warnLabel={t('engine_settings.warn', '警告')}
          termLabel={t('engine_settings.term', '终止')}
          onWarnChange={v => update('identicalWarn', v)}
          onTermChange={v => update('identicalTerm', v)}
        />
      </div>

      {/* 工具级覆盖 */}
      <div className="rounded-md border border-border bg-surface p-4 space-y-4">
        <h3 className="text-h3 text-text">{t('engine_settings.tool_overrides', '工具级覆盖')}</h3>
        <p className="text-small text-dim">{t('engine_settings.tool_overrides_desc', '为特定工具单独配置警告和终止阈值')}</p>
        <div className="space-y-3">
          <div className="flex items-center gap-3 text-caption text-dim">
            <span className="w-40 shrink-0">{t('engine_settings.tool_name', '工具名')}</span>
            <span className="w-8">{t('engine_settings.warn', '警告')}</span>
            <span className="w-16" />
            <span className="w-8">{t('engine_settings.term', '终止')}</span>
          </div>
          {data.toolOverrides.map((override, idx) => (
            <div key={override.name} className="flex items-center gap-3">
              <span className="text-mono text-body text-text w-40 shrink-0">{override.name}</span>
              <div className="flex items-center gap-2">
                <div className="rounded border border-border-hi bg-surface overflow-hidden">
                  <input
                    type="number"
                    value={override.warn}
                    onChange={e => updateOverride(idx, 'warn', Number(e.target.value))}
                    className="w-16 px-2 py-1.5 text-body bg-transparent text-text focus:outline-none text-center"
                    style={{ border: 'none', outline: 'none', boxShadow: 'none' }}
                  />
                </div>
                <span className="text-dim text-caption">/</span>
                <div className="rounded border border-border-hi bg-surface overflow-hidden">
                  <input
                    type="number"
                    value={override.term}
                    onChange={e => updateOverride(idx, 'term', Number(e.target.value))}
                    className="w-16 px-2 py-1.5 text-body bg-transparent text-text focus:outline-none text-center"
                    style={{ border: 'none', outline: 'none', boxShadow: 'none' }}
                  />
                </div>
              </div>
            </div>
          ))}
        </div>
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
