/**
 * Failure reasons: the renderer half of `backend/internal/failure`.
 *
 * The backend names the condition; this file writes the sentence. That split
 * exists because the backend has no locale in scope — any sentence it writes
 * is one language shown to every reader.
 *
 * What this replaces is worth stating, because the shape of the old code is
 * the argument for the new one. There was no `reason` field on the wire, so
 * the one code the renderer needed (`no_workspace`) travelled as the error
 * *message*, and `lib/errors.ts` grew a six-way substring match to read it
 * back out — `msg === 'no_workspace' || msg.includes('no workspace') ||
 * msg.includes('not initialized') || msg.includes('backend not')`. Each arm
 * was a guess at some other layer's phrasing, and any of them could match a
 * sentence that meant something else entirely.
 *
 * Two rules keep this file honest:
 *
 *  1. FAILURE_TEXT is a `Record<FailureReason, …>`, so TypeScript refuses to
 *     compile a reason with no sentence. That is the totality enforcement
 *     (INV-CLOSED-02), not a runtime fallback — a runtime fallback is exactly
 *     how a reason ends up rendering as its own identifier.
 *  2. `internal/failure/domain_test.go` asserts this union equals Go's
 *     `failure.AllReasons()`. Adding a Reason in Go without adding it here is
 *     a test failure rather than a surprise in production.
 */

import i18n from '@/lib/i18n'

/** Mirror of Go `failure.Reason`. Wire values are the contract. */
export const FAILURE_REASONS = [
  'no_workspace',
  'engine_not_ready',
  'login_required',
  'skill_repair_failed',
  'provider_in_use_by_cron',
] as const

export type FailureReason = (typeof FAILURE_REASONS)[number]

const FAILURE_REASON_SET: ReadonlySet<string> = new Set(FAILURE_REASONS)

export function asFailureReason(v: unknown): FailureReason | undefined {
  return typeof v === 'string' && FAILURE_REASON_SET.has(v) ? (v as FailureReason) : undefined
}

/**
 * The sentence for each reason, in the reader's language.
 *
 * Thunks rather than plain strings: this module is evaluated at import time,
 * before i18n finishes reading the saved language, and the user can switch
 * language later. A frozen string would be the boot language forever.
 */
const FAILURE_TEXT: Record<FailureReason, () => string> = {
  no_workspace: () => i18n.t('failure.no_workspace', '请先打开一个文件夹'),
  engine_not_ready: () => i18n.t('failure.engine_not_ready', 'AI 引擎正在启动，请稍后重试'),
  login_required: () => i18n.t('failure.login_required', '请先登录'),
  skill_repair_failed: () => i18n.t('failure.skill_repair_failed', 'AI 修复失败，请重试'),
  // 不提任务名：后端把它放在 detail 里（诊断面，不显示），而句子由这里按读者语言写。
  // 指路到定时任务页——那里会列出全部任务，比在一句话里塞一串名字更有用。
  provider_in_use_by_cron: () =>
    i18n.t('failure.provider_in_use_by_cron', '该模型正被定时任务使用，请先在定时任务中改用其它模型或删除相关任务'),
}

/** The reader-facing sentence for a reason. */
export function failureText(reason: FailureReason): string {
  return FAILURE_TEXT[reason]()
}

/**
 * An error that names a backend condition.
 *
 * `message` is already localised so the many call sites that just show
 * `err.message` are correct without touching any of them. `reason` stays
 * attached for the few that branch instead of print — GateGuard swaps the
 * whole page for "open a folder" on `no_workspace`, and the model settings
 * page suppresses its org-catalog error banner on the same reason because an
 * empty catalog with no workspace is the expected state, not a fault.
 *
 * `detail` carries the upstream diagnostic (a provider's error text, a parse
 * failure) when the backend had one. It is English and belongs in logs and
 * details views, never in the primary sentence.
 */
export class FailureError extends Error {
  readonly reason: FailureReason
  readonly detail?: string

  constructor(reason: FailureReason, detail?: string) {
    super(failureText(reason))
    this.name = 'FailureError'
    this.reason = reason
    this.detail = detail
  }
}

/** The reason behind a thrown value, when it names one. */
export function reasonOf(err: unknown): FailureReason | undefined {
  return err instanceof FailureError ? err.reason : undefined
}

/** Whether a thrown value names one specific reason. */
export function isFailure(err: unknown, reason: FailureReason): boolean {
  return reasonOf(err) === reason
}
