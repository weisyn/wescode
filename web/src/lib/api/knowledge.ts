import { request } from '@/bridge'

export interface KBFileInfo {
  id: string
  source_id: string
  path: string
  name: string
  ext: string
  size: number
  content_hash: string
  mod_time: string
  status: string
  category: string
  mime_type: string
  chunk_count: number
  parse_method: string
  error_message: string
  created_at: string
  metadata?: Record<string, string>
}

export interface KBSearchResult {
  file_id: string
  file_name: string
  score: number
  chunks: { id: string; content: string; score: number }[]
}

export interface KBStats {
  total_files: number
  total_chunks: number
  total_size: number
  ready_files: number
  error_files: number
  indexing_files: number
  quarantined_files: number
}

export const knowledgeApi = {
  list: () => request<KBFileInfo[]>('sidebar/listKBFiles'),

  rescan: () => request<{ ingested: number; orphaned: number }>('sidebar/kbRescan'),

  search: (query: string) => request<KBSearchResult[]>('sidebar/kbSearch', { query }),

  stats: () => request<KBStats>('sidebar/kbStats'),

  reindexFile: (fileId: string) => request<{ ok: boolean }>('sidebar/kbReindexFile', { fileId }),

  deleteFile: (fileId: string) => request<{ ok: boolean }>('sidebar/kbDeleteFile', { fileId }),

  reindexErrors: () => request<{ count: number }>('sidebar/kbReindexErrors'),

  reindexQuarantined: () => request<{ count: number }>('sidebar/kbReindexQuarantined'),
}
