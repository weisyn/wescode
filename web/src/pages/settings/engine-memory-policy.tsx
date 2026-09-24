import { useState, useEffect, useCallback } from 'react'
import { useLocale, tk } from '@wesui'
import { request } from '@/bridge'
import { Button } from '@/components/ui/Button'
import { saveEngineSettings, saveHintClass, type SaveHint } from './save-settings'

// The two caps are separate because the engine keeps them separate, with
// different defaults (10k role / 5k session). They were one "最大记忆条数" box
// that wrote both: the page showed one of the two values, saving overwrote the
// other with it, and nothing said the ratio had just been flattened.
interface MemoryPolicyData {
  writePolicy: 'silent' | 'logged' | 'gated'
  maxEntriesAgent: number
  maxEntriesSession: number
}

// Mirrors memory.WritePolicy (INV-MEM-27). `t(`..._${value}`, value)` put the
// enum token itself in the fallback slot, so the three buttons read
// silent / logged / gated with a blank line under each — in both languages,
// since the keys were never written to en.json either.
//
// The wording for `gated` matters: the engine refuses every write outright
// (ErrMemoryWriteGated) and has no approval queue behind it, so anything that
// reads like "pending review" describes a mechanism that does not exist.
const WRITE_POLICIES = [
  {
    value: 'silent' as const,
    label: tk('engine_settings.write_policy_silent', '静默写入'),
    desc: tk('engine_settings.write_policy_silent_desc', 'AI 记住你的偏好时不提示、不留审计记录。默认选项。'),
  },
  {
    value: 'logged' as const,
    label: tk('engine_settings.write_policy_logged', '记录审计'),
    desc: tk('engine_settings.write_policy_logged_desc', '照常写入，但每成功写入一条记忆就留一条审计记录。'),
  },
  {
    value: 'gated' as const,
    label: tk('engine_settings.write_policy_gated', '禁止写入'),
    desc: tk('engine_settings.write_policy_gated_desc', '拒绝一切记忆写入，此工作区不会积累任何记忆。不是排队等审批。'),
  },
]

export function MemoryPolicySettings() {
  const { t } = useLocale()
  const [data, setData] = useState<MemoryPolicyData | null>(null)
  const [loading, setLoading] = useState(true)
  const [failed, setFailed] = useState(false)
  const [saving, setSaving] = useState(false)
  const [dirty, setDirty] = useState(false)

  useEffect(() => {
    request<MemoryPolicyData>('sidebar/getMemoryPolicySettings')
      .then(d => setData(d))
      .catch(() => setFailed(true))
      .finally(() => setLoading(false))
  }, [])

  const update = useCallback(<K extends keyof MemoryPolicyData>(key: K, value: MemoryPolicyData[K]) => {
    setData(prev => prev ? { ...prev, [key]: value } : prev)
    setDirty(true)
  }, [])

  const [saveHint, setSaveHint] = useState<SaveHint | null>(null)

  // One save path for both halves of this page. The write-policy buttons used
  // to fire their own request with `.catch(() => {})`, which kept the newly
  // selected policy highlighted even when the write was rejected — the same
  // "looks applied, isn't" shape this page's other fields had.
  const apply = useCallback(async (next: MemoryPolicyData) => {
    const prev = data
    if (!prev) return
    setData(next)
    setSaving(true)
    // The write policy is live (the engine reads it per write, INV-MEM-27);
    // the entry caps are resolved at Cell.Start. The old copy flattened both
    // into 已保存 and hid the second half — the engine now answers which is
    // which, see save-settings.ts.
    const { ok, hint } = await saveEngineSettings('sidebar/updateMemoryPolicySettings', next, t)
    // This page writes optimistically so the policy buttons highlight on click,
    // so a rejected write has to put the old selection back — otherwise the
    // highlight claims a policy the engine never accepted.
    if (ok) setDirty(false)
    else setData(prev)
    setSaveHint(hint)
    setSaving(false)
    setTimeout(() => setSaveHint(null), 6000)
  }, [data, t])

  const handleSave = useCallback(() => {
    if (!data || !dirty) return
    void apply(data)
  }, [data, dirty, apply])

  if (failed) {
    return <div className="p-6 text-dim text-body">{t('engine_settings.workspace_required', '打开项目文件夹后可配置引擎设置')}</div>
  }
  if (loading || !data) {
    return <div className="p-6 text-dim text-body">{t('engine_settings.loading', '加载中...')}</div>
  }

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-h2 text-text mb-1">{t('engine_settings.memory_policy_title', '记忆策略')}</h2>
        <p className="text-small text-dim">{t('engine_settings.memory_policy_desc', '控制 AI 是否自动记住你的偏好和习惯')}</p>
      </div>

      {/* 写入策略 */}
      <div className="rounded-md border border-border bg-surface p-4 space-y-4">
        <h3 className="text-h3 text-text">{t('engine_settings.write_policy', '写入策略')}</h3>
        <div className="space-y-2">
          {WRITE_POLICIES.map(policy => (
            <button
              key={policy.value}
              onClick={() => void apply({ ...data, writePolicy: policy.value })}
              className={`w-full rounded-md border p-3 text-left transition-colors duration-fast ${
                data.writePolicy === policy.value
                  ? 'border-accent bg-accent-soft'
                  : 'border-border bg-surface hover:bg-surface-3'
              }`}
            >
              <div className="text-body text-text">{t(policy.label.k, policy.label.zh)}</div>
              <div className="text-small text-dim mt-0.5">{t(policy.desc.k, policy.desc.zh)}</div>
            </button>
          ))}
        </div>
      </div>

      {/* 容量上限 */}
      <div className="rounded-md border border-border bg-surface p-4 space-y-4">
        <h3 className="text-h3 text-text">{t('engine_settings.memory_limit', '容量上限')}</h3>
        <div className="space-y-3">
          {/*
            No "0 = 不限" here: the engine reads a zero cap as "use the default"
            (10k / 5k), so the old hint offered an unlimited option that
            silently resolved to a finite one. There is no unlimited setting at
            this layer, and the backend now refuses anything below 1.
          */}
          <div className="flex items-center gap-3">
            <label className="text-body text-text w-40 shrink-0">{t('engine_settings.max_entries_agent', '角色记忆上限')}</label>
            <div className="rounded border border-border-hi bg-surface overflow-hidden">
              <input
                type="number"
                min={1}
                value={data.maxEntriesAgent}
                onChange={e => update('maxEntriesAgent', Number(e.target.value))}
                className="w-24 px-3 py-1.5 text-body bg-transparent text-text focus:outline-none"
                style={{ border: 'none', outline: 'none', boxShadow: 'none' }}
              />
            </div>
            <span className="text-small text-dim">{t('engine_settings.max_entries_agent_hint', '跨会话保留的长期记忆')}</span>
          </div>
          <div className="flex items-center gap-3">
            <label className="text-body text-text w-40 shrink-0">{t('engine_settings.max_entries_session', '会话记忆上限')}</label>
            <div className="rounded border border-border-hi bg-surface overflow-hidden">
              <input
                type="number"
                min={1}
                value={data.maxEntriesSession}
                onChange={e => update('maxEntriesSession', Number(e.target.value))}
                className="w-24 px-3 py-1.5 text-body bg-transparent text-text focus:outline-none"
                style={{ border: 'none', outline: 'none', boxShadow: 'none' }}
              />
            </div>
            <span className="text-small text-dim">{t('engine_settings.max_entries_session_hint', '当前会话内的运行时记忆')}</span>
          </div>
        </div>
        <p className="text-caption text-dim">{t('engine_settings.memory_gc_note', '超出上限时，引擎自动淘汰长期未被召回的记忆条目')}</p>
      </div>

      {/* 安全说明 */}
      <div className="rounded-md border border-border bg-surface p-4 space-y-2">
        <h3 className="text-h3 text-text">{t('engine_settings.memory_safety', '安全保障')}</h3>
        <p className="text-small text-dim">
          {t('engine_settings.memory_safety_desc', '以下安全机制始终开启，无法关闭：')}
        </p>
        {/*
          These three describe the always-on gates in the engine's write path.
          The wording is deliberately narrow and must track INV-MEM-24 / -31:
          the previous copy claimed prompt-injection detection and a gate that
          "rejects raw code blocks and large JSON", both of which were removed
          from the engine (too many false positives; code and JSON are valid
          memory content). Promising a filter that no longer runs is worse than
          promising nothing — a user who believes injection is filtered stops
          reading what the agent stored.
        */}
        <ul className="text-small text-dim space-y-1 list-disc list-inside">
          <li>{t('engine_settings.safety_threat_scan', '凭据扫描（含 API key、连接串、Token，命中即拒绝写入）')}</li>
          <li>{t('engine_settings.safety_quality_gate', '引擎状态前缀拦截（计划/任务/子代理的机器态不进记忆）')}</li>
          <li>{t('engine_settings.safety_invisible_strip', '不可见字符剥离（写入时拒绝，读取时清理）')}</li>
        </ul>
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
