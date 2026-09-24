import { useMemo } from 'react'
import { TokensSettingsPanel } from '@wesui/memory'
import type { TokensSettingsAdapter, TokenSummary, TokenGroup } from '@wesui/memory'
import { request } from '@/bridge'

interface RawTokenSummary {
  totalInput: number
  totalOutput: number
  totalTokens: number
  totalCalls: number
}

interface RawTokenGroup {
  key: string
  label: string
  inputTokens: number
  outputTokens: number
  totalTokens: number
  calls: number
}

export function TokensSettings() {
  const adapter: TokensSettingsAdapter = useMemo(() => ({
    fetchSummary: () =>
      request<RawTokenSummary>('sidebar/tokenSummary').then((raw): TokenSummary => ({
        total_input: raw.totalInput ?? 0,
        total_output: raw.totalOutput ?? 0,
        total_tokens: raw.totalTokens ?? 0,
        total_runs: raw.totalCalls ?? 0,
      })),
    fetchTokenGroups: (groupBy) =>
      request<RawTokenGroup[]>('sidebar/tokenGrouped', { groupBy }).then(rawGroups =>
        (rawGroups ?? []).map((g): TokenGroup => ({
          label: g.label || g.key || '—',
          input_tokens: g.inputTokens,
          output_tokens: g.outputTokens,
          total_tokens: g.totalTokens,
          run_count: g.calls,
        }))
      ),
  }), [])

  return <TokensSettingsPanel adapter={adapter} />
}
