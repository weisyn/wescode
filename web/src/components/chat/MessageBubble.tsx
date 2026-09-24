import { useCallback, memo } from 'react'
import {
  MessageBubble as WesuiMessageBubble,
  asPresentationEnvelope,
  type MessageBubbleMessage,
} from '@wesui/message'
import type { HITLVerdict } from '@wesui/message'
import { openFile, request } from '@/bridge'
import { CallGraphPart, asCallGraphData } from '@/components/codeintel/CallGraphPart'
import type { ChatMessage } from '@/lib/chat/types'

interface Props {
  message: ChatMessage
  isStreaming?: boolean
  highlight?: string
  agentName?: string
  onRetry?: () => void
  onConfigure?: () => void
  onEdit?: (text: string) => void
  /** actionable 动作：把 prompt 预填进输入框（不自动发送，见 ChatMessages 的注释）。 */
  onRunPrompt?: (prompt: string) => void
}

/**
 * Phase 3: wescode's MessageBubble is now a *transport* wrapper around the
 * wesui MessageBubble — no local part rendering.
 *
 * Local `ErrorBlock` / `FileRefBlock` / `EditStatusBlock` and the file-ref
 * `renderUserContent` were promoted into `@wesui/message` so the default
 * PartRenderer handles error / file_ref / edit_status natively across all
 * three products.
 *
 * What remains here:
 *   - HITL resolution bridge (`request('hitlRespond', ...)`), which needs
 *     access to wescode's `bridge.request` RPC channel.
 *   - `openFile` bridge for click-to-open in the VS Code editor.
 *   - `renderPart` for `category: 'graph'` structured output (PC-01): the
 *     D3 `CallGraph` depends on CKG data shapes, and wesui serves three
 *     products — wesclaw / wescraft have no CKG. A renderer two products
 *     cannot use does not belong in the shared library; that is what wesui's
 *     `renderPart` hook is for.
 *
 * Anything richer than that, and not product-specific, belongs in wesui.
 */
export const MessageBubble = memo(function MessageBubble({
  message,
  isStreaming,
  highlight,
  agentName,
  onRetry,
  onConfigure,
  onEdit,
  onRunPrompt,
}: Props) {
  const handleResolveHITL = useCallback(
    async (
      requestId: string,
      kind: 'input' | 'choice',
      verdict: HITLVerdict,
      value?: string,
    ) => {
      try {
        if (verdict === 'cancelled') {
          await request('hitlRespond', { requestId })
          return
        }
        if (kind === 'input') {
          await request('hitlRespond', { requestId, value: value ?? '' })
          return
        }
        await request('hitlRespond', { requestId, choice: value ?? '' })
      } catch (err) {
        console.error('[HITL] respond failed', { requestId, kind, err })
        throw err
      }
    },
    [],
  )

  const handleFileClick = useCallback((part: { fileId?: string; fileName?: string; localPath?: string }) => {
    // Audit #6: non-image attachments carry a local disk path from history;
    // open the original file through the host (default OS handler).
    if (part.localPath) {
      void request('openLocalFile', { path: part.localPath })
    }
  }, [])

  // INV-CTX-REF-01: clicking an @ chip opens the LIVE workspace object at
  // `uri` (unlike FileRefBlock which opens a conversation-owned copy).
  const handleRefClick = useCallback((part: { uri?: string }) => {
    if (part.uri) {
      openFile(part.uri)
    }
  }, [])

  // PC-01：只接管 category==='graph'，其余一律返回 null 交回 wesui 的默认渲染。
  // 三道收窄各有理由：不是 structured_output 直接走开；不是合法信封说明是本契约
  // 之前的生产方（或别的产品）；载荷不是图的形状则不猜——`as CallGraphData` 会让
  // 键写错的载荷一路走进 D3，然后在一个跟原因无关的地方崩掉，或者更糟：静默画出
  // 一张空图。
  const renderPart = useCallback(
    (part: { kind: string;[key: string]: unknown }) => {
      if (part.kind !== 'structured_output') return null
      const env = asPresentationEnvelope(part.data)
      if (!env || env.category !== 'graph') return null
      const graph = asCallGraphData(env.data)
      if (!graph) return null
      return <CallGraphPart data={graph} grade={env.grade} />
    },
    [],
  )

  return (
    <WesuiMessageBubble
      message={message as unknown as MessageBubbleMessage}
      isStreaming={isStreaming}
      highlight={highlight}
      agentName={agentName}
      onRetry={onRetry}
      onConfigure={onConfigure}
      onEdit={onEdit}
      onOpenFile={openFile}
      onFileClick={handleFileClick}
      onRefClick={handleRefClick}
      onResolveHITL={handleResolveHITL}
      onRunPrompt={onRunPrompt}
      renderPart={renderPart}
    />
  )
})
