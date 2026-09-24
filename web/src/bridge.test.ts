import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// R2 regression suite: the sendChat bridge must forward `media` (paste/drop
// inline attachments) and must NOT carry the deprecated `images` field that
// made the diagnostic log read `imagesCount:0` forever.
//
// The webview obtains the host API through the global __VSCODE_API__ injected
// by the Vite build (or acquireVsCodeApi() in dev). Tests stub the global
// before dynamic-importing the module so the module-level `vscode` binding is
// exercised realistically.

const postMessage = vi.fn()

async function loadBridge() {
  vi.resetModules()
  return await import('@/bridge')
}

beforeEach(() => {
  postMessage.mockClear()
  ;(globalThis as Record<string, unknown>).__VSCODE_API__ = { postMessage }
})

afterEach(() => {
  delete (globalThis as Record<string, unknown>).__VSCODE_API__
  delete (globalThis as Record<string, unknown>).acquireVsCodeApi
})

describe('bridge.sendChat — media payload', () => {
  it('forwards the media array to the host', async () => {
    const { sendChat } = await loadBridge()
    sendChat({
      text: 'analyze this',
      media: [{ name: 'shot.png', mimeType: 'image/png', dataUrl: 'data:image/png;base64,AAAA' }],
    })

    const sent = postMessage.mock.calls
      .map((c) => c[0] as { type: string; payload?: Record<string, unknown> })
      .filter((m) => m.type === 'sendChat')
    expect(sent).toHaveLength(1)
    const msg = sent[0]
    expect(msg.payload?.media).toHaveLength(1)
    expect(msg.payload?.media).toEqual([
      { name: 'shot.png', mimeType: 'image/png', dataUrl: 'data:image/png;base64,AAAA' },
    ])
  })

  it('never emits the deprecated images field (R2)', async () => {
    const { sendChat } = await loadBridge()
    sendChat({
      text: 'hi',
      media: [{ name: 'a.png', mimeType: 'image/png', dataUrl: 'data:image/png;base64,A' }],
    })
    const sent = postMessage.mock.calls
      .map((c) => c[0] as { type: string; payload?: Record<string, unknown> })
      .filter((m) => m.type === 'sendChat')
    expect(sent).toHaveLength(1)
    expect(sent[0].payload).not.toHaveProperty('images')
  })

  it('drops the message without crashing when the host API is absent', async () => {
    delete (globalThis as Record<string, unknown>).__VSCODE_API__
    const { sendChat } = await loadBridge()
    expect(() => sendChat({ text: 'hi' })).not.toThrow()
    expect(postMessage).not.toHaveBeenCalled()
  })
})

// The RPC timeout guards transport liveness — a postMessage that raced the
// host's listener registration, which fails in milliseconds. `pickFiles` opens
// a modal OS dialog instead, so the call is outstanding for as long as a person
// browses. Under the shared 15s budget the promise was rejected while the
// dialog was still open, the caller's catch ran, and the user's actual
// selection came back to an id no longer in `pending` and was dropped: picking
// a file did nothing, and only if you took your time.
//
// Asserting the behaviour, not the constants: what must hold is "a dialog
// outliving the transport budget is not cancelled" and "the guard is still
// finite", either of which a future retune can satisfy with other numbers.
describe('bridge.request — human-paced methods', () => {
  beforeEach(() => { vi.useFakeTimers() })
  afterEach(() => { vi.useRealTimers() })

  async function track(p: Promise<unknown>) {
    const state = { settled: false, error: undefined as unknown }
    p.then(() => { state.settled = true }, (e) => { state.settled = true; state.error = e })
    return state
  }

  it('does not cancel pickFiles while the dialog is still open', async () => {
    const { pickFiles } = await loadBridge()
    const state = await track(pickFiles({ multiple: true, allowFolders: false }))

    // Well past the transport budget: a user browsing for half a minute is
    // ordinary, and this is the window the gesture used to die in.
    await vi.advanceTimersByTimeAsync(30_000)
    expect(state.settled).toBe(false)
  })

  it('still fails eventually, so a dead host cannot hang the caller forever', async () => {
    const { pickFiles } = await loadBridge()
    const state = await track(pickFiles())

    await vi.advanceTimersByTimeAsync(60 * 60_000)
    expect(state.settled).toBe(true)
    expect((state.error as Error).message).toContain('RPC timeout')
  })

  it('keeps the short transport budget for machine-answered methods', async () => {
    const { request } = await loadBridge()
    const state = await track(request('authMe'))

    await vi.advanceTimersByTimeAsync(15_500)
    expect(state.settled).toBe(true)
    expect((state.error as Error).message).toContain('RPC timeout')
  })
})
