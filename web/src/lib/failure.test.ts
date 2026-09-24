import { describe, expect, it } from 'vitest'
import {
  asFailureReason,
  FAILURE_REASONS,
  failureText,
  FailureError,
  isFailure,
  reasonOf,
} from './failure'

describe('asFailureReason', () => {
  it('accepts every wire value in the domain', () => {
    for (const r of FAILURE_REASONS) {
      expect(asFailureReason(r)).toBe(r)
    }
  })

  it('rejects anything outside it', () => {
    // A reason the backend no longer sends, or one a newer backend sends that
    // this renderer has no sentence for. Both must read as "unnamed", so the
    // raw message survives instead of an identifier reaching the reader.
    expect(asFailureReason('cell_locked')).toBeUndefined()
    expect(asFailureReason('')).toBeUndefined()
    expect(asFailureReason(undefined)).toBeUndefined()
    expect(asFailureReason(42)).toBeUndefined()
  })
})

describe('failureText', () => {
  it('never renders a reason as its own identifier', () => {
    // FAILURE_TEXT being a Record over the union is the compile-time half of
    // this; a thunk that returns the key (a missing locale default, a typo'd
    // t() call) still type-checks, and that is the shape a user would see.
    for (const r of FAILURE_REASONS) {
      const text = failureText(r)
      expect(text).not.toBe(r)
      expect(text).not.toBe(`failure.${r}`)
      expect(text.length).toBeGreaterThan(0)
    }
  })
})

describe('isFailure', () => {
  it('matches the reason a FailureError carries', () => {
    expect(isFailure(new FailureError('no_workspace'), 'no_workspace')).toBe(true)
    expect(isFailure(new FailureError('login_required'), 'no_workspace')).toBe(false)
  })

  it('does not match a plain Error whose text mentions the reason', () => {
    // This is the whole point of the field. The predecessor of this module
    // matched `msg.includes('no workspace')` and friends, so an upstream
    // sentence that merely talked about the workspace was silently classified
    // as "Config Mode has no folder open" — and then suppressed.
    expect(isFailure(new Error('no_workspace'), 'no_workspace')).toBe(false)
    expect(isFailure(new Error('org catalog failed: no workspace mounted'), 'no_workspace')).toBe(false)
    expect(isFailure('no_workspace', 'no_workspace')).toBe(false)
  })
})

describe('FailureError', () => {
  it('reads as the localised sentence, with the diagnostic kept aside', () => {
    const err = new FailureError('skill_repair_failed', 'provider returned 429')
    expect(err.message).toBe(failureText('skill_repair_failed'))
    expect(err.message).not.toContain('429')
    expect(err.detail).toBe('provider returned 429')
    expect(reasonOf(err)).toBe('skill_repair_failed')
  })

  it('leaves reasonOf undefined for anything else', () => {
    expect(reasonOf(new Error('boom'))).toBeUndefined()
    expect(reasonOf(undefined)).toBeUndefined()
  })
})
