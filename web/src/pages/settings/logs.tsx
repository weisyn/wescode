import { useMemo } from 'react'
import { LogsSettingsPanel } from '@wesui/memory'
import type { LogsSettingsAdapter, RunSummary as WesuiRunSummary } from '@wesui/memory'
import { listRuns, type RunSummary } from '@/lib/api/data'

function mapRun(r: RunSummary): WesuiRunSummary {
  return {
    run_id: r.runId, agent_id: r.agentId, agent_name: r.agentName,
    session_id: r.sessionId,
    status: r.status, started_at: r.startedAt, ended_at: r.endedAt,
    elapsed_ms: r.elapsedMs, total_turns: r.totalTurns,
    input_tokens: r.inputTokens, output_tokens: r.outputTokens, model: r.model,
  }
}

export function LogsSettings() {
  const adapter: LogsSettingsAdapter = useMemo(() => ({
    fetchRuns: (limit) => listRuns(limit).then(r => r.map(mapRun)),
  }), [])

  return <LogsSettingsPanel adapter={adapter} />
}
