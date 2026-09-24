import type { ContentPart, PlanPart } from '@wesui/message'

/** Alias — wesui PlanPart now carries all previously-private fields. */
export type WescodePlanPart = PlanPart

// Phase 3 completed: ErrorPart / FileRefPart / EditStatusPart moved into the
// wesui ContentPart union, so this alias is now a straight pass-through.
// Kept as `WescodeContentPart` so downstream call sites don't need mass rename.
export type WescodeContentPart = ContentPart

export interface ChatMessage {
  id: string
  role: 'user' | 'assistant'
  parts: WescodeContentPart[]
  timestamp?: number
  agentName?: string
  _evicted?: true
  _cachedHeight?: number
}
