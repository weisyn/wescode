import { useState, useEffect, useCallback } from 'react'
import { useLocale, tk } from '@wesui'
import { request } from '@/bridge'
import { saveEngineSettings, saveHintClass, type SaveHint } from './save-settings'

interface GovernanceData {
  governMode: string
  denyPaths: string[]
  redactorRules: string[]
  thinkingVisibility: string
  networkPolicy: string
}

// Every row on this page names an engine enum, so the label and its Chinese
// text have to travel together: `t(\`prefix_${value}\`, value)` looks like it
// resolves but the second argument IS the Chinese source, so a Chinese user
// read `open` / `phone` / `pass` / `allow` off the screen while an English one
// got the en.json string. tk binds key and Chinese in one literal call, which
// is also the only shape the locale gate can see (scripts/locale-audit.mjs).
const GOVERN_MODES = [
  {
    value: 'open',
    label: tk('engine_settings.govern_mode_open', '开放'),
    desc: tk('engine_settings.govern_mode_open_desc', '写文件、执行命令等操作直接放行并记入审计。编程场景的默认选项。'),
  },
  {
    value: 'locked',
    label: tk('engine_settings.govern_mode_locked', '锁定'),
    desc: tk('engine_settings.govern_mode_locked_desc', '写文件、执行命令等操作一律拒绝，只保留只读能力。演示或受管环境使用。'),
  },
]

const REDACTOR_RULES = [
  { value: 'phone', label: tk('engine_settings.redactor_phone', '手机号') },
  { value: 'email', label: tk('engine_settings.redactor_email', '邮箱地址') },
  { value: 'id_card', label: tk('engine_settings.redactor_id_card', '身份证号') },
  { value: 'bank_card', label: tk('engine_settings.redactor_bank_card', '银行卡号') },
]

const THINKING_OPTIONS = [
  {
    value: 'pass',
    label: tk('engine_settings.thinking_pass', '完整显示'),
    desc: tk('engine_settings.thinking_pass_desc', '原样展示模型的思考过程。'),
  },
  {
    value: 'redact',
    label: tk('engine_settings.thinking_redact', '脱敏显示'),
    desc: tk('engine_settings.thinking_redact_desc', '按上面的脱敏规则处理后再展示思考过程。'),
  },
  {
    value: 'hide',
    label: tk('engine_settings.thinking_hide', '不显示'),
    desc: tk('engine_settings.thinking_hide_desc', '思考内容不会推送到界面，只保留最终回答。'),
  },
]

// Mirrors govern.NetworkPolicy. The engine rejects anything outside this set
// (ValidateNetworkPolicy) precisely because its sandbox switch has no default
// arm — an unrecognised value would fall through and enforce as allow.
const NETWORK_POLICIES = [
  {
    value: 'allow',
    label: tk('engine_settings.network_allow', '允许全部'),
    desc: tk('engine_settings.network_allow_desc', '不限制出站访问。编程场景推荐。'),
  },
  {
    value: 'internal_only',
    label: tk('engine_settings.network_internal_only', '仅内网'),
    desc: tk('engine_settings.network_internal_only_desc', '只能访问 localhost 与内网地址，外网请求被拒绝。'),
  },
  {
    value: 'deny',
    label: tk('engine_settings.network_deny', '禁止联网'),
    desc: tk('engine_settings.network_deny_desc', '拒绝一切出站请求，包括 localhost。'),
  },
]

export function GovernanceSettings() {
  const { t } = useLocale()
  const [data, setData] = useState<GovernanceData | null>(null)
  const [loading, setLoading] = useState(true)
  const [failed, setFailed] = useState(false)
  const [saveHint, setSaveHint] = useState<SaveHint | null>(null)
  const [newPath, setNewPath] = useState('')

  useEffect(() => {
    request<GovernanceData>('sidebar/getGovernanceSettings')
      .then(d => setData({
        ...d,
        denyPaths: d.denyPaths ?? [],
        redactorRules: d.redactorRules ?? [],
      }))
      .catch(() => setFailed(true))
      .finally(() => setLoading(false))
  }, [])

  // Single write path for the page. Each control here saves on click, so each
  // one used to carry its own `request(...).catch(() => {})` — four copies that
  // dropped the same two things: the engine's restartRequired list, and the
  // failure. A rejected save left the control visually flipped and said
  // nothing, which on screen is indistinguishable from a setting that persists
  // and then gets ignored — the exact shape this whole pass exists to remove.
  // Rolling the optimistic state back is the other half: the control must show
  // what the engine holds, not what the click intended.
  const apply = useCallback(async (patch: Partial<GovernanceData>) => {
    const prev = data
    if (!prev) return
    setData({ ...prev, ...patch })
    const { ok, hint } = await saveEngineSettings('sidebar/updateGovernanceSettings', patch, t)
    if (!ok) setData(prev)
    setSaveHint(hint)
    setTimeout(() => setSaveHint(null), 6000)
  }, [data, t])

  const addPath = useCallback(() => {
    const trimmed = newPath.trim()
    if (!trimmed || !data) return
    if (!data.denyPaths.includes(trimmed)) {
      apply({ denyPaths: [...data.denyPaths, trimmed] })
    }
    setNewPath('')
  }, [newPath, data, apply])

  const removePath = useCallback((idx: number) => {
    if (!data) return
    apply({ denyPaths: data.denyPaths.filter((_, i) => i !== idx) })
  }, [data, apply])

  const toggleRedactor = useCallback((key: string) => {
    if (!data) return
    apply({
      redactorRules: data.redactorRules.includes(key)
        ? data.redactorRules.filter(r => r !== key)
        : [...data.redactorRules, key],
    })
  }, [data, apply])

  if (failed) {
    return <div className="p-6 text-dim text-body">{t('engine_settings.workspace_required', '打开项目文件夹后可配置引擎设置')}</div>
  }
  if (loading || !data) {
    return <div className="p-6 text-dim text-body">{t('engine_settings.loading', '加载中...')}</div>
  }

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-h2 text-text mb-1">{t('engine_settings.governance_title', '安全与边界')}</h2>
        <p className="text-small text-dim">{t('engine_settings.governance_desc', 'AI 能做什么、不能碰什么')}</p>
      </div>

      {/* 操作模式 */}
      <div className="rounded-md border border-border bg-surface p-4 space-y-4">
        <h3 className="text-h3 text-text">{t('engine_settings.govern_mode', '操作模式')}</h3>
        <div className="grid grid-cols-2 gap-3">
          {GOVERN_MODES.map(mode => (
            <button
              key={mode.value}
              onClick={() => apply({ governMode: mode.value })}
              className={`rounded-md border p-4 text-left transition-colors duration-fast ${
                data.governMode === mode.value
                  ? 'border-accent bg-accent-soft'
                  : 'border-border bg-surface hover:bg-surface-3'
              }`}
            >
              <div className="text-h3 text-text">{t(mode.label.k, mode.label.zh)}</div>
              <div className="text-small text-dim mt-1">{t(mode.desc.k, mode.desc.zh)}</div>
            </button>
          ))}
        </div>
      </div>

      {/* 禁止访问 */}
      <div className="rounded-md border border-border bg-surface p-4 space-y-4">
        <h3 className="text-h3 text-text">{t('engine_settings.deny_paths', '禁止访问的路径')}</h3>
        <p className="text-small text-dim">{t('engine_settings.deny_paths_desc', 'AI 不会读取或修改这些文件和目录（如 .env、密钥文件、凭证目录）')}</p>
        <div className="space-y-2">
          {data.denyPaths.map((path, idx) => (
            <div key={idx} className="flex items-center gap-2">
              <span className="text-mono text-body text-text flex-1 truncate">{path}</span>
              <button
                onClick={() => removePath(idx)}
                className="text-caption text-danger hover:opacity-80 transition-colors duration-fast px-2 py-1"
              >
                {t('engine_settings.delete', '删除')}
              </button>
            </div>
          ))}
        </div>
        <div className="flex items-center gap-2">
          <div className="flex-1 rounded border border-border-hi bg-surface overflow-hidden">
            <input
              value={newPath}
              onChange={e => setNewPath(e.target.value)}
              onKeyDown={e => e.key === 'Enter' && addPath()}
              placeholder={t('engine_settings.deny_paths_placeholder', '输入路径，如 ~/.ssh')}
              className="w-full px-3 py-1.5 text-body bg-transparent text-text placeholder:text-muted focus:outline-none"
              style={{ border: 'none', outline: 'none', boxShadow: 'none' }}
            />
          </div>
          <button
            onClick={addPath}
            className="px-3 py-1.5 rounded border border-border-hi text-body text-text hover:bg-surface-3 active:bg-surface-hover transition-colors duration-fast"
          >
            +
          </button>
        </div>
      </div>

      {/* 脱敏规则 */}
      <div className="rounded-md border border-border bg-surface p-4 space-y-4">
        <h3 className="text-h3 text-text">{t('engine_settings.redactor', '脱敏规则')}</h3>
        <p className="text-small text-dim">
          {t('engine_settings.redactor_desc', '勾选的类型会在 AI 输出、工具结果和审批弹窗中被打码。默认全部关闭：代码里的邮箱、电话往往是测试数据，打码后 AI 读到的是掩码串，改回文件就把掩码写进了代码。')}
        </p>
        <p className="text-small text-dim">
          {t('engine_settings.redactor_floor', 'API 密钥与令牌始终打码，不受此处影响，也无法关闭。')}
        </p>
        <div className="space-y-2">
          {REDACTOR_RULES.map(rule => (
            <label key={rule.value} className="flex items-center gap-3 cursor-pointer">
              <input
                type="checkbox"
                checked={data.redactorRules.includes(rule.value)}
                onChange={() => toggleRedactor(rule.value)}
                className="accent-[var(--color-accent)]"
              />
              <span className="text-body text-text">{t(rule.label.k, rule.label.zh)}</span>
            </label>
          ))}
        </div>
      </div>

      {/* 思维透明度 */}
      <div className="rounded-md border border-border bg-surface p-4 space-y-4">
        <h3 className="text-h3 text-text">{t('engine_settings.thinking_visibility', '思维透明度')}</h3>
        <div className="space-y-2">
          {THINKING_OPTIONS.map(opt => (
            <button
              key={opt.value}
              onClick={() => apply({ thinkingVisibility: opt.value })}
              className={`w-full rounded-md border p-3 text-left transition-colors duration-fast ${
                data.thinkingVisibility === opt.value
                  ? 'border-accent bg-accent-soft'
                  : 'border-border bg-surface hover:bg-surface-3'
              }`}
            >
              <div className="text-body text-text">{t(opt.label.k, opt.label.zh)}</div>
              <div className="text-small text-dim mt-0.5">{t(opt.desc.k, opt.desc.zh)}</div>
            </button>
          ))}
        </div>
      </div>

      {/* 网络策略 */}
      <div className="rounded-md border border-border bg-surface p-4 space-y-4">
        <h3 className="text-h3 text-text">{t('engine_settings.network_policy', '网络访问')}</h3>
        <p className="text-small text-dim">
          {t('engine_settings.network_policy_desc', '限制 AI 的出站访问。收紧后终端里的 curl、wget、ssh、scp、rsync 会被一并拒绝——编程场景下这会影响依赖安装等操作，请按需选择。')}
        </p>
        <div className="space-y-2">
          {NETWORK_POLICIES.map(policy => (
            <button
              key={policy.value}
              onClick={() => apply({ networkPolicy: policy.value })}
              className={`w-full rounded-md border p-3 text-left transition-colors duration-fast ${
                data.networkPolicy === policy.value
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

      {/* 内置保护说明 */}
      <div className="rounded-md border border-border bg-surface p-4 space-y-2">
        <h3 className="text-h3 text-text">{t('engine_settings.builtin_protection', '内置保护（始终生效）')}</h3>
        <ul className="text-small text-dim space-y-1 list-disc list-inside">
          <li>{t('engine_settings.hardline_note', '灾难命令拦截：rm -rf /、fork bomb、shutdown 等 18 条命令永久阻断')}</li>
          <li>{t('engine_settings.workdir_note', '工作区写入边界：AI 只能修改当前项目目录内的文件')}</li>
        </ul>
      </div>

      {/* Controls save on click; this is the only place the outcome is reported. */}
      {saveHint && (
        <div className={`text-small ${saveHintClass(saveHint.tone)}`}>
          {saveHint.text}
        </div>
      )}
    </div>
  )
}
