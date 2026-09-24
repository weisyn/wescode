import { useMemo } from 'react'
import { RunPolicySettingsPanel, type RunPolicySettingsAdapter } from '@wesui/settings'
import { request, getConfigMode } from '@/bridge'

function useRunPolicyAdapter(): RunPolicySettingsAdapter {
  return useMemo(() => ({
    load: async () => {
      if (getConfigMode()) {
        // Refuse to fabricate defaults in Config Mode: the panel renders a
        // read-only prompt (failed state) instead of an editable form that
        // could overwrite the real RunSettings once a workspace opens.
        throw new Error('no_workspace')
      }
      const s = await request<{
        taskBudget: number
        maxTokenEstimate: number
        toolDeny: string[]
        thinkingLevel?: string
      }>('getRunSettings')
      // No client-side defaults: getRunSettings returns the resolved settings,
      // i.e. the numbers the next Run enforces. A fallback here would be a
      // second normalization point and could show a budget nothing applies.
      return {
        task_budget: s.taskBudget,
        max_token_estimate: s.maxTokenEstimate,
        tool_deny: s.toolDeny ?? [],
        thinking_level: (s.thinkingLevel ?? '') as 'low' | 'medium' | 'high' | 'max' | '',
      }
    },
    // The panel has three independent save entry points (tool toggle, thinking
    // level, save button) and each passes only the fields it owns. Undefined
    // keys are dropped by JSON.stringify, and the backend treats an absent key
    // as "leave alone" — that is what keeps a tool toggle from clearing the
    // budget, or a thinking-level change from wiping the whole denylist.
    save: async (partial) => {
      if (getConfigMode()) {
        throw new Error('no_workspace')
      }
      await request('updateRunSettings', {
        taskBudget: partial.task_budget,
        maxTokenEstimate: partial.max_token_estimate,
        toolDeny: partial.tool_deny,
        thinkingLevel: partial.thinking_level,
      })
    },
  }), [])
}

export function ToolPolicySection() {
  return <RunPolicySettingsPanel adapter={useRunPolicyAdapter()} />
}
