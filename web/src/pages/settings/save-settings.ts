import { tk, type TLabel } from '@wesui'

import { request } from '@/bridge'

/**
 * Every `sidebar/update*Settings` RPC answers with the spec fields that
 * persisted to the registry but did **not** reach the running Cell, straight
 * from the engine's `SpecPatch.RestartRequired()`. The list is normally empty.
 *
 * This module is the only place the renderer touches that field, because the
 * failure it prevents is precisely a second copy of the classification: the
 * settings pages used to guess. `engine-context-budget` hardcoded "系统指令将在
 * 下次启动后生效" — which named the one field on that page that hot-applies, and
 * stayed silent about the context numbers that genuinely need a restart.
 * `engine-memory-policy` hardcoded 已保存 and hid the entry caps behind it.
 * Both guesses were wrong in opposite directions, and neither would have been
 * caught by a test, because "saved, nothing happened, no signal" and "saved and
 * applied" render identically.
 */
export interface SettingsUpdateResult {
  ok?: boolean
  /** Spec fields that need a workspace restart. Absent when everything applied. */
  restartRequired?: string[]
}

/** Translator shape shared by `useLocale` and the local `useTranslation`. */
type Translate = (key: string, fallback: string) => string

export interface SaveOutcome {
  /** False when the write was rejected — the engine still holds the old value. */
  ok: boolean
  /** What to show the user: saved, saved-pending-restart, or the failure. */
  hint: SaveHint
}

/**
 * Sends a settings patch and reports the outcome. Never rejects.
 *
 * Not throwing is the point. This used to hand back the `restartRequired` list
 * and let the transport error escape, so each page had to remember its own
 * `catch`; three of the seven did. The other four ran
 * `try { … } finally { setSaving(false) }`, which re-enables the button and
 * shows nothing — a rejected save and a successful one render identically, and
 * the page keeps displaying the value the user typed rather than the one the
 * engine holds. With no exception to catch there is no branch left to omit.
 */
export async function saveEngineSettings(
  method: string,
  params: unknown,
  t: Translate,
): Promise<SaveOutcome> {
  try {
    const res = await request<SettingsUpdateResult>(method, params)
    return { ok: true, hint: saveHintFor(res?.restartRequired ?? [], t) }
  } catch (err) {
    return {
      ok: false,
      hint: {
        text: err instanceof Error ? err.message : t('engine_settings.save_failed', '保存失败'),
        tone: 'error',
      },
    }
  }
}

/**
 * Display labels for the field names the engine can return, keyed by the JSON
 * name `SpecPatch.RestartRequired()` emits. The English locale overrides them —
 * a hint that names the pending fields in the wrong language is only marginally
 * better than no hint.
 *
 * `tk` binds the i18n key and the Chinese text into one literal call because
 * the locale gate only sees keys written literally. Splitting them — a table of
 * bare stems plus `` t(`engine_settings.field_${f}`, TABLE[f]) `` — passes the
 * dynamic-key rule as passthrough while every key becomes dead in `en.json`.
 *
 * Unmapped names render raw rather than being dropped: a field added to
 * `SpecPatch` without an entry here looks ugly, which is recoverable, whereas
 * dropping it reinstates exactly the silence this surface exists to break.
 */
const FIELD_LABELS: Record<string, TLabel> = {
  run_limits: tk('engine_settings.field_run_limits', '运行限制'),
  tool_limits: tk('engine_settings.field_tool_limits', '工具限制'),
  context_budget: tk('engine_settings.field_context_budget', '上下文参数'),
  memory_limits: tk('engine_settings.field_memory_limits', '记忆容量'),
  progress: tk('engine_settings.field_progress', '进度检测'),
  observe_limits: tk('engine_settings.field_observe_limits', '观测限制'),
  hitl: tk('engine_settings.field_hitl', '人工确认'),
  fault_policy_override: tk('engine_settings.field_fault_policy_override', '故障策略'),
  channels: tk('engine_settings.field_channels', '消息渠道'),
  channel_gateway: tk('engine_settings.field_channel_gateway', '渠道网关'),
}

export interface SaveHint {
  text: string
  /**
   * `warning` means persisted but not yet in effect; `error` means not
   * persisted at all. They are separate tones because the user's next move
   * differs: reopen the workspace, versus try again.
   */
  tone: 'success' | 'warning' | 'error'
}

/** The text colour for a hint tone. One table so the seven pages agree. */
export function saveHintClass(tone: SaveHint['tone']): string {
  if (tone === 'error') return 'text-danger'
  if (tone === 'warning') return 'text-warning'
  return 'text-success'
}

/** Renders the post-save hint for a `restartRequired` list (possibly empty). */
function saveHintFor(fields: string[], t: Translate): SaveHint {
  if (fields.length === 0) {
    return { text: t('engine_settings.saved', '已保存'), tone: 'success' }
  }
  const names = fields
    .map(f => {
      const label = FIELD_LABELS[f]
      return label ? t(label.k, label.zh) : f
    })
    .join(t('engine_settings.field_separator', '、'))
  return {
    text: t('engine_settings.saved_restart_required', '已保存。以下设置在重新打开工作区后生效：') + names,
    tone: 'warning',
  }
}
