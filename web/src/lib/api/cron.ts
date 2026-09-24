import type {
  CronListResponse,
  CreateCronEntry,
  CronPatch,
  CronRunRecord,
  SchedulerStats,
  CronEntry,
  CronJobState,
} from '@wesui/cron'
import { request } from '@/bridge'

interface CronJobWithState { entry: CronEntry; state?: CronJobState }

function toListResponse(items: CronJobWithState[]): CronListResponse {
  const jobs = items.map(i => i.entry)
  const states: Record<string, CronJobState> = {}
  for (const i of items) {
    if (i.state) states[i.entry.id] = i.state
  }
  return { jobs, states }
}

export const cronApi = {
  list: async (): Promise<CronListResponse> => {
    const items = await request<CronJobWithState[]>('sidebar/listCronJobs') ?? []
    return toListResponse(items)
  },

  add: async (entry: CreateCronEntry): Promise<CronEntry> =>
    await request<CronEntry>('sidebar/addCronJob', entry) as CronEntry,

  update: async (id: string, patch: CronPatch): Promise<void> => {
    await request('sidebar/updateCronJob', { id, patch })
  },

  remove: async (id: string): Promise<void> => {
    await request('sidebar/deleteCronJob', { id })
  },

  enable: async (id: string): Promise<void> => {
    await request('sidebar/enableCronJob', { id })
  },

  disable: async (id: string): Promise<void> => {
    await request('sidebar/disableCronJob', { id })
  },

  trigger: async (id: string): Promise<void> => {
    await request('sidebar/triggerCronJob', { id })
  },

  listRuns: async (jobId: string, limit = 20): Promise<CronRunRecord[]> =>
    await request<CronRunRecord[]>('sidebar/listCronRuns', { jobId, limit }) ?? [],

  stats: async (): Promise<SchedulerStats> =>
    await request<SchedulerStats>('sidebar/cronStats', {}) ?? { total_jobs: 0, enabled_jobs: 0 },
}
