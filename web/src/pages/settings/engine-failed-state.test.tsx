import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import type { ReactElement } from 'react'
import { ContextBudgetSettings } from './engine-context-budget'
import { CycleDetectSettings } from './engine-cycle-detect'
import { GovernanceSettings } from './engine-governance'
import { MemoryPolicySettings } from './engine-memory-policy'
import { ResilienceSettings } from './engine-resilience'
import { RunLimitsSettings } from './engine-run-limits'

// S-1 invariant: when the backend request fails (Config Mode / no
// workspace), every engine settings page must render a workspace-required
// prompt — never an editable form seeded with stale defaults that could
// overwrite the real configuration once a workspace opens.
vi.mock('@/bridge', () => ({
  request: vi.fn(() => Promise.reject(new Error('no_workspace'))),
}))

// 只覆盖 useLocale（让 t 确定性地返回中文兜底），其余一律走真实模块。
//
// 此前这里是整模块替换（只返回 useLocale），于是 engine-governance.tsx 在模块级调用
// 的 `tk` 变成 undefined，整个测试文件在 import 阶段就崩——而它崩在"No tk export"
// 而不是任何与 S-1 有关的地方。整模块替换的 mock 必须列出 prod 碰到的每一个绑定，
// 这是一份需要人手同步的清单，而下一个页面导入新符号时它就又过期了。
vi.mock('@wesui', async importOriginal => ({
  ...(await importOriginal<typeof import('@wesui')>()),
  useLocale: () => ({ t: (_key: string, fallback?: string) => fallback ?? _key }),
}))

const pages: Array<[string, () => ReactElement]> = [
  ['context-budget', ContextBudgetSettings],
  ['cycle-detect', CycleDetectSettings],
  ['governance', GovernanceSettings],
  ['memory-policy', MemoryPolicySettings],
  ['resilience', ResilienceSettings],
  ['run-limits', RunLimitsSettings],
]

afterEach(cleanup)

describe('engine settings pages — Config Mode failure', () => {
  it.each(pages)('%s renders workspace-required prompt, not an editable form', async (_name, Comp) => {
    render(<Comp />)
    await waitFor(() => {
      expect(screen.getByText(/打开项目文件夹后可配置引擎设置/)).toBeTruthy()
    })
    // No editable surface (no save button) when the request failed.
    expect(screen.queryByRole('button', { name: /保存|save/i })).toBeNull()
  })
})
