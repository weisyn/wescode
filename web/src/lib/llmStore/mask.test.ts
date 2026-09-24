import { describe, expect, it } from 'vitest'
import type { LLMProviderDraft } from '@wesui/llm'
import { isMasked, sanitizeDraftForAdd, sanitizeDraftForUpdate } from './mask'

const baseDraft: LLMProviderDraft = {
  label: 'DeepSeek',
  type: 'openai_compat',
  baseUrl: 'https://api.deepseek.com',
  model: 'deepseek-v4-flash',
  apiKey: '',
  isDefault: false,
}

describe('isMasked', () => {
  it('detects the four-star mask marker', () => {
    expect(isMasked('sk-2****a5c52')).toBe(true)
    expect(isMasked('/Use****/bin')).toBe(true)  // the 2026-07-05 event ghost
  })
  it('rejects real keys', () => {
    expect(isMasked('sk-2b0124542b064d12ab93efce0a8a5c52')).toBe(false)
    expect(isMasked('')).toBe(false)
  })
  it('handles null/undefined defensively', () => {
    expect(isMasked(null)).toBe(false)
    expect(isMasked(undefined)).toBe(false)
  })
})

describe('sanitizeDraftForAdd', () => {
  it('passes api_key through unchanged (backend layer decides empty policy)', () => {
    const dto = sanitizeDraftForAdd({ ...baseDraft, apiKey: 'sk-real-key-plaintext-value-32c' })
    expect(dto.api_key).toBe('sk-real-key-plaintext-value-32c')
  })
  it('trims whitespace but keeps otherwise-empty value', () => {
    const dto = sanitizeDraftForAdd({ ...baseDraft, apiKey: '   ' })
    expect(dto.api_key).toBe('')
  })
  it('produces snake_case wire fields', () => {
    const dto = sanitizeDraftForAdd({ ...baseDraft, apiKey: 'x'.repeat(32) })
    expect(dto).toMatchObject({
      name: 'DeepSeek',
      base_url: 'https://api.deepseek.com',
      model: 'deepseek-v4-flash',
    })
  })
})

describe('sanitizeDraftForUpdate — INV-LLM-01 regression net', () => {
  it('OMITS api_key when the form value is empty', () => {
    const dto = sanitizeDraftForUpdate({ ...baseDraft, apiKey: '' })
    expect('api_key' in dto).toBe(false)
  })
  it('OMITS api_key when the form value is only whitespace', () => {
    const dto = sanitizeDraftForUpdate({ ...baseDraft, apiKey: '   ' })
    expect('api_key' in dto).toBe(false)
  })
  it('OMITS api_key when the form value is a mask preview (the 2026-07-05 regression)', () => {
    const dto = sanitizeDraftForUpdate({ ...baseDraft, apiKey: 'sk-2****a5c52' })
    expect('api_key' in dto).toBe(false)
  })
  it('INCLUDES api_key when the user typed a real new key', () => {
    const dto = sanitizeDraftForUpdate({
      ...baseDraft,
      apiKey: 'sk-fresh-rotated-key-plaintext-x',
    })
    expect(dto.api_key).toBe('sk-fresh-rotated-key-plaintext-x')
  })
})
