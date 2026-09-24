import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { ToolPolicySection } from './tool-policy'

// Config Mode (no workspace): the adapter must refuse to fabricate defaults.
// The shared panel therefore renders a read-only workspace-required prompt —
// never an editable form seeded with stale values that a save could persist
// over the real RunSettings.
vi.mock('@/bridge', () => ({
  getConfigMode: () => true,
  request: vi.fn(() => Promise.reject(new Error('no_workspace'))),
}))

afterEach(cleanup)

describe('ToolPolicySection — Config Mode', () => {
  it('renders workspace-required prompt, not an editable form', async () => {
    render(<ToolPolicySection />)
    await waitFor(() => {
      expect(screen.getByText(/项目文件夹|project folder/)).toBeTruthy()
    })
    // No editable surface: no save button, no thinking-depth buttons.
    expect(screen.queryByRole('button', { name: /保存|save/i })).toBeNull()
    expect(screen.queryByRole('button', { name: /high|medium|max/i })).toBeNull()
  })
})
