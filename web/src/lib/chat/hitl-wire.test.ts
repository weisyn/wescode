import { describe, expect, it } from 'vitest'
import { hydratePartsFromServer, streamEventToWireEvent } from './useStream'
import type { StreamEvent } from '@/bridge'
import type { HITLPart } from '@wesui/message'

describe('streamEventToWireEvent HITL', () => {
  it('maps prompt (not reason) and infers choice from choices', () => {
    const wire = streamEventToWireEvent({
      type: 'hitl_request',
      hitl: {
        requestId: 'r1',
        prompt: '继续尝试还是终止？',
        reason: '',
        choices: ['继续尝试', '终止任务'],
      },
    } as StreamEvent)
    expect(wire).not.toBeNull()
    expect(wire!.data.kind).toBe('choice')
    expect(wire!.data.prompt).toBe('继续尝试还是终止？')
    expect(wire!.data.summary).toBe('继续尝试还是终止？')
    expect(wire!.data.choices).toEqual(['继续尝试', '终止任务'])
  })

  it('never defaults kind to approval', () => {
    const wire = streamEventToWireEvent({
      type: 'hitl_request',
      hitl: { requestId: 'r2', prompt: '请输入' },
    } as StreamEvent)
    expect(wire!.data.kind).toBe('input')
  })

  it('drops the part only when hitl payload is missing', () => {
    expect(streamEventToWireEvent({ type: 'hitl_request' } as StreamEvent)).toBeNull()
  })

  it('maps hitl_resolved to submitted (never Track A once)', () => {
    const wire = streamEventToWireEvent({
      type: 'hitl_resolved',
      hitl: { requestId: 'r3' },
    } as StreamEvent)
    expect(wire).toEqual({
      type: 'hitl_resolved',
      data: { request_id: 'r3', verdict: 'submitted' },
    })
  })
})

describe('hydratePartsFromServer HITL', () => {
  it('restores a choice card from history', () => {
    const parts = hydratePartsFromServer({
      role: 'assistant',
      text: '',
      contentParts: [{
        kind: 'hitl',
        hitl: {
          requestId: 'r9',
          kind: 'choice',
          prompt: '拆不拆分？',
          choices: ['拆分', '不拆分'],
        },
      } as any],
    })
    const part = parts.find(p => p.kind === 'hitl') as HITLPart | undefined
    expect(part).toBeDefined()
    expect(part!.hitl.kind).toBe('choice')
    expect(part!.hitl.description).toBe('拆不拆分？')
    expect(part!.hitl.choices).toEqual(['拆分', '不拆分'])
  })

  it('restores a flat history payload (no nested hitl)', () => {
    const parts = hydratePartsFromServer({
      role: 'assistant',
      text: '',
      contentParts: [{
        kind: 'hitl',
        requestId: 'r10',
        prompt: '请输入连接串',
      } as any],
    })
    const part = parts.find(p => p.kind === 'hitl') as HITLPart | undefined
    expect(part).toBeDefined()
    expect(part!.hitl.kind).toBe('input')
    expect(part!.hitl.description).toBe('请输入连接串')
  })
})
