import { useEffect, useRef, useState, useCallback, useMemo } from 'react'
import { onHostMessage, notifyReady, navigate } from '@/bridge'
import { useStream } from '@/lib/chat/useStream'
import type { ChatMessage } from '@/lib/chat/types'
import { useTranslation } from '@/lib/i18n'
import { MessageBubble } from '@/components/chat/MessageBubble'
import { PlanTracker } from '@/components/chat/PlanTracker'
import { PlanPanel } from '@/components/chat/PlanPanel'
import { TaskProgressIndicator, MessageViewport } from '@wesui/message'
import type { HITLData, HITLPart } from '@wesui/message'
import { ActivityIndicator } from '@wesui/streaming'
import { WescodeChatInput } from '@/components/chat/WescodeChatInput'
import { BillingDebtBanner } from '@wesui/billing'
import { isWES } from '@wesui/llm'
import { useBillingStore } from '@/stores/billing'
import { ContextSnapshotBar } from '@/components/codeintel/ContextSnapshotBar'

type ActiveAgent = {
  name: string
  role: string
  goal: string
  emoji?: string
  suggestions: string[]
} | null

export function ChatMessages() {
  const { t } = useTranslation()
  const { messages, isStreaming, activity, activePlan, addUserMessage, clearMessages, loadMessages, applySessionPlan, patchImagePreview, setActiveSessionId } = useStream()
  const scrollRef = useRef<HTMLDivElement>(null)
  const [agent, setAgent] = useState<ActiveAgent>(null)

  const [searchQuery, setSearchQuery] = useState('')
  const [planPanelOpen, setPlanPanelOpen] = useState(false)
  const [isLoadingSession, setIsLoadingSession] = useState(false)
  const [wesProviderSelected, setWesProviderSelected] = useState(false)

  const wesBilling = useBillingStore(s => s.wesBilling)
  const billingDismissKey = useBillingStore(s => s.billingDebtBannerDismissKey)
  const dismissBillingDebtBanner = useBillingStore(s => s.dismissBillingDebtBanner)

  const handleProviderChange = useCallback((providerId: string | undefined, _hasWes?: boolean, _hasByok?: boolean) => {
    // INV-CHAT-04: banner only when the currently selected model is WES.
    // Org / BYOK selection must not show "请切换到自有模型".
    setWesProviderSelected(isWES(providerId))
  }, [])

  const quickStarts = useMemo(() => [
    { icon: '💡', text: t('chat.quickExplainCode', '解释选中的代码') },
    { icon: '🐛', text: t('chat.quickFindBug', '查找并修复 Bug') },
    { icon: '✏️', text: t('chat.quickRefactor', '重构优化代码') },
    { icon: '📝', text: t('chat.quickGenTest', '生成单元测试') },
  ], [t])

  useEffect(() => {
    notifyReady()
  }, [])

  useEffect(() => {
    if (!activePlan) setPlanPanelOpen(false)
  }, [activePlan])

  const isAtBottomRef = useRef(true)
  const [showScrollBtn, setShowScrollBtn] = useState(false)
  // Programmatic scrollTop (stick-to-bottom / focus / send) plus Chromium
  // overflow-anchor can emit MORE than one scroll event. Swallowing only the
  // first one let the second event mark "user scrolled up" and then the next
  // token's stick-to-bottom yanked the list back — the transcript flashes.
  const programmaticUntilRef = useRef(0)
  const stickRafRef = useRef(0)

  const markProgrammatic = useCallback(() => {
    programmaticUntilRef.current = performance.now() + 80
  }, [])

  const stickToBottom = useCallback(() => {
    if (!isAtBottomRef.current || searchQuery) return
    if (stickRafRef.current) return
    stickRafRef.current = requestAnimationFrame(() => {
      stickRafRef.current = 0
      const el = scrollRef.current
      if (!el || !isAtBottomRef.current) return
      markProgrammatic()
      el.scrollTop = el.scrollHeight
      setShowScrollBtn(false)
    })
  }, [searchQuery, markProgrammatic])

  useEffect(() => () => {
    if (stickRafRef.current) cancelAnimationFrame(stickRafRef.current)
  }, [])

  const handleScroll = useCallback(() => {
    const el = scrollRef.current
    if (!el) return
    if (performance.now() < programmaticUntilRef.current) {
      isAtBottomRef.current = true
      setShowScrollBtn(false)
      return
    }
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40
    isAtBottomRef.current = atBottom
    setShowScrollBtn(!atBottom)
  }, [])

  const scrollToBottom = useCallback(() => {
    isAtBottomRef.current = true
    markProgrammatic()
    const el = scrollRef.current
    if (!el) return
    el.scrollTop = el.scrollHeight
    setShowScrollBtn(false)
  }, [markProgrammatic])

  useEffect(() => {
    stickToBottom()
  }, [messages, isStreaming, stickToBottom])

  // Composer / banner / HITL changing height shrinks the scroller's clientHeight
  // without a messages commit. Only re-anchor on a real viewport resize — content
  // growth inside the scroller must not loop ResizeObserver ↔ scrollTop.
  useEffect(() => {
    const el = scrollRef.current
    if (!el) return
    let lastH = el.clientHeight
    const ro = new ResizeObserver(() => {
      const h = el.clientHeight
      if (h === lastH) return
      lastH = h
      stickToBottom()
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [stickToBottom])

  // Composer focus (click after scrolling up to read history) should bring the
  // latest message back into view — the input sits below the scroll list, so
  // without this the list stays put and the user sees a gap of old content.
  const handleInputFocus = useCallback(() => {
    if (searchQuery) return // search mode shows filtered results, don't jump
    const el = scrollRef.current
    if (!el) return
    isAtBottomRef.current = true
    markProgrammatic()
    el.scrollTop = el.scrollHeight
    setShowScrollBtn(false)
  }, [searchQuery, markProgrammatic])

  // reportPlanState is now dispatched from useStream via `onPlanActiveChange`
  // so we no longer need a duplicate effect here (2026-07-01 alignment).

  const [loadError, setLoadError] = useState<string | null>(null)

  const isStreamingRef = useRef(isStreaming)
  isStreamingRef.current = isStreaming

  useEffect(() => {
    return onHostMessage(msg => {
      if (msg.type === 'userMessage') {
        addUserMessage(msg.text, msg.attachments, msg.codeContext)
      } else if (msg.type === 'clearMessages') {
        // Reset scope so the next stream is not filtered against a stale
        // session id from the previous conversation (blank UI on error/done).
        setActiveSessionId(undefined)
        clearMessages()
        setSearchQuery('')
        setLoadError(null)
        isAtBottomRef.current = true
        setShowScrollBtn(false)
      } else if (msg.type === 'loading') {
        setIsLoadingSession(true)
        setLoadError(null)
      } else if (msg.type === 'loadMessages') {
        setIsLoadingSession(false)
        setLoadError(null)
        setActiveSessionId(msg.sessionId || undefined)
        isAtBottomRef.current = true
        setShowScrollBtn(false)
        if (!isStreamingRef.current) {
          loadMessages(msg.messages)
        }
        setSearchQuery('')
      } else if (msg.type === 'loadError') {
        setIsLoadingSession(false)
        setLoadError(msg.error ?? t('chat.loadFailed', '加载失败'))
      } else if (msg.type === 'agentInfo') {
        setAgent(msg.agent)
      } else if (msg.type === 'sessionPlan') {
        applySessionPlan(msg.plan)
      } else if (msg.type === 'imagePreview') {
        patchImagePreview(msg.fileId, msg.url)
      } else if (msg.type === 'sessionChange') {
        // Always sync — empty string clears stale scope on "新建对话".
        setActiveSessionId(msg.sessionId || undefined)
      } else if (msg.type === 'search') {
        setSearchQuery((msg.query as string) ?? '')
      }
    })
  }, [addUserMessage, clearMessages, loadMessages, applySessionPlan, patchImagePreview, setActiveSessionId, t])

  const lowerQ = searchQuery.toLowerCase()
  const filtered = searchQuery
    ? messages.filter(m =>
        m.parts.some(p =>
          (p.kind === 'text' || p.kind === 'thinking' || p.kind === 'error') &&
          'text' in p && typeof p.text === 'string' &&
          p.text.toLowerCase().includes(lowerQ)
        )
      )
    : messages

  const pendingHitl = useMemo(() => findPendingHITL(messages), [messages])

  useEffect(() => {
    if (!pendingHitl) return
    requestAnimationFrame(() => {
      if (scrollRef.current) {
        isAtBottomRef.current = true
        markProgrammatic()
        scrollRef.current.scrollTop = scrollRef.current.scrollHeight
        setShowScrollBtn(false)
      }
    })
  }, [pendingHitl?.requestId, markProgrammatic])

  const defaultAgent = t('chat.defaultAgent', '协作者')

  return (
    <div className="relative flex flex-col h-full min-h-0 min-w-0 overflow-hidden bg-bg">
      {isLoadingSession && (
        <div className="absolute inset-0 z-10 flex items-center justify-center bg-bg/80">
          <div className="flex items-center gap-2 text-small text-muted">
            <span className="animate-spin inline-block w-4 h-4 border-2 border-current border-t-transparent rounded-full" />
            {t('chat.loadingSession', '加载会话…')}
          </div>
        </div>
      )}
      {loadError && (
        <div className="absolute inset-0 z-10 flex items-center justify-center bg-bg/80">
          <div className="flex flex-col items-center gap-2 text-small text-destructive">
            <span>{t('chat.sessionLoadFailed', '会话加载失败')}</span>
            <span className="text-caption text-muted max-w-[240px] text-center break-words">{loadError}</span>
          </div>
        </div>
      )}
      {searchQuery && (
        <div className="flex-none px-4 py-1 text-caption text-muted border-b border-border overflow-hidden">
          {t('chat.searchFound', { defaultValue: '找到 {{count}} 条匹配「{{query}}」', count: filtered.length, query: searchQuery })}
        </div>
      )}
      <div className="relative min-h-0 min-w-0 flex-1">
      <div
        ref={scrollRef}
        onScroll={handleScroll}
        className="absolute inset-0 overflow-y-auto px-5 py-4"
        style={{ overflowAnchor: 'none', scrollbarGutter: 'stable' }}
      >
        {filtered.length === 0 ? (
          searchQuery ? (
            <div className="flex items-center justify-center h-full text-small text-muted">
              {t('chat.searchNotFound', { defaultValue: '当前会话中未找到「{{query}}」', query: searchQuery })}
            </div>
          ) : (
            <EmptyState agent={agent} quickStarts={quickStarts} onPrefill={(text) => {
              window.postMessage({ type: 'prefillInput', text }, '*')
            }} />
          )
        ) : (
          <MessageViewport
            messages={filtered as any}
            scrollRef={scrollRef}
            isStreaming={isStreaming}
            isAtBottom={isAtBottomRef.current}
            overscan={5}
            renderMessage={(msg, globalIdx, isLast) => {
              const typedMsg = msg as (typeof filtered)[number]
              const prevMsg = globalIdx > 0 ? filtered[globalIdx - 1] : undefined
              const resolvedName = typedMsg.role === 'assistant'
                ? (typedMsg.agentName ?? agent?.name ?? defaultAgent)
                : undefined
              const showAgent = resolvedName && (
                prevMsg?.role !== 'assistant' ||
                (prevMsg.agentName ?? agent?.name ?? defaultAgent) !== resolvedName
              )
              if (typedMsg._evicted) {
                return (
                  <div className="h-16 flex items-center justify-center text-caption text-muted">
                    ···
                  </div>
                )
              }
              return (
                <MessageBubble
                  key={typedMsg.id}
                  message={typedMsg}
                  highlight={searchQuery}
                  isStreaming={!searchQuery && isStreaming && isLast && typedMsg.role === 'assistant'}
                  agentName={showAgent ? resolvedName : undefined}
                  onRetry={() => navigate('retryLastMessage')}
                  onConfigure={() => navigate('settings', { tab: 'model' })}
                  onEdit={(text) => {
                    window.postMessage({ type: 'prefillInput', text }, '*')
                  }}
                  // actionable 动作走**预填**而不是自动发送：
                  //
                  // 一是可逆性——直接执行会绕过 EditEngine 的检查点与治理链（EE-13），
                  // 而"点了就回不去"对中低端用户是净负值（见 wesui actionableData.ts）。
                  // 预填让 agent 去调工具，写入照常进检查点。
                  //
                  // 二是预填而非自动提交：用户能在发送前看清这句指令、改掉它、或者放弃。
                  // 一个按钮点下去就直接跑，与"点了就写盘"只差一层，而那一层正是
                  // 他理解发生了什么的唯一机会。
                  onRunPrompt={(prompt) => {
                    window.postMessage({ type: 'prefillInput', text: prompt }, '*')
                  }}
                />
              )
            }}
          />
        )}
        {isStreaming && messages.length > 0 && (
          <TaskProgressIndicator
            streamedParts={messages[messages.length - 1]?.parts ?? []}
            isStreaming={isStreaming}
          />
        )}
      </div>
        {showScrollBtn && (
          <button
            type="button"
            onClick={scrollToBottom}
            className="absolute bottom-3 right-5 z-10 w-8 h-8 rounded
              bg-[var(--vscode-button-background,#0078d4)] text-[var(--vscode-button-foreground,#fff)]
              flex items-center justify-center shadow-md
              hover:opacity-85 transition-opacity cursor-pointer border-none"
            title={t('chat.scrollToBottom', '返回底部')}
          >
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M8 3v10M4 9l4 4 4-4" />
            </svg>
          </button>
        )}
      </div>
      <div className="flex-none shrink-0 min-w-0">
      {!isStreaming && activePlan && ((activePlan.steps ?? []).length > 0 || activePlan.analysis || activePlan.overview) && (
        <PlanTracker
          plan={activePlan}
          onExpand={() => setPlanPanelOpen(true)}
          isStreaming={isStreaming}
        />
      )}
      {planPanelOpen && activePlan && (
        <PlanPanel plan={activePlan} onClose={() => setPlanPanelOpen(false)} />
      )}
      {pendingHitl && (
        <div className="mx-5 mb-1 px-3 py-2 rounded-md bg-[var(--vscode-inputValidation-warningBackground,#5a4000)] border border-[var(--vscode-inputValidation-warningBorder,#856900)] text-small text-[var(--vscode-inputValidation-warningForeground,#ccc)] flex items-center gap-2 min-w-0">
          <span className="shrink-0">⏳</span>
          <span className="truncate min-w-0">
            {pendingHitl.title || pendingHitl.description || t('chat.hitlPending', 'AI 等待你的操作确认')}
          </span>
          <button
            type="button"
            className="ml-auto shrink-0 px-2 py-0.5 rounded border border-[var(--vscode-inputValidation-warningBorder,#856900)] bg-transparent text-caption cursor-pointer hover:opacity-85"
            onClick={() => scrollToHITL(pendingHitl.requestId)}
          >
            {t('chat.hitlAnswer', '回答')}
          </button>
        </div>
      )}
      <div className="px-5">
        <BillingDebtBanner
          state={wesBilling}
          visible={wesProviderSelected}
          dismissedKey={billingDismissKey}
          onDismiss={dismissBillingDebtBanner}
        />
      </div>
      {isStreaming && <ContextSnapshotBar />}
      {isStreaming && messages.length > 0 && (
        <div className="px-5 pb-1">
          <ActivityIndicator activity={activity} isStreaming />
        </div>
      )}
      <WescodeChatInput
        isStreaming={isStreaming}
        disabled={Boolean(pendingHitl)}
        onSendOptimistic={(text, attachments, contextRefs, activatedSkills) => {
          // Pass attachments through so the optimistic bubble renders
          // images/file refs immediately (R1 — previously always undefined).
          addUserMessage(text, attachments, undefined, contextRefs, activatedSkills)
          isAtBottomRef.current = true
          stickToBottom()
        }}
        onProviderChange={handleProviderChange}
        onFocus={handleInputFocus}
      />
      </div>
    </div>
  )
}

function findPendingHITL(messages: ChatMessage[]): HITLData | null {
  for (let i = messages.length - 1; i >= 0; i--) {
    const msg = messages[i]
    if (msg.role !== 'assistant') continue
    for (let j = msg.parts.length - 1; j >= 0; j--) {
      const p = msg.parts[j]
      if (p.kind === 'hitl' && !(p as HITLPart).hitl.resolved) {
        return (p as HITLPart).hitl
      }
    }
  }
  return null
}

function scrollToHITL(requestId: string) {
  document.getElementById(`hitl-${requestId}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' })
}

function EmptyState({ agent, quickStarts, onPrefill }: { agent: ActiveAgent; quickStarts: { icon: string; text: string }[]; onPrefill: (text: string) => void }) {
  const { t } = useTranslation()
  const agentWithSuggestions = agent && (agent.suggestions ?? []).length > 0 ? agent : null
  return (
    <div className="flex flex-col items-center justify-center h-full gap-6 text-center">
      {agentWithSuggestions ? (
        <>
          <div className="flex flex-col items-center gap-2">
            <div className="w-10 h-10 rounded-md border border-border bg-surface-2 flex items-center justify-center text-h3">
              {agentWithSuggestions.emoji || '✦'}
            </div>
            <p className="text-body font-medium text-text">{agentWithSuggestions.name}</p>
            <p className="text-small text-muted max-w-[240px]">{agentWithSuggestions.goal}</p>
          </div>
          <div className="w-full max-w-[260px] flex flex-col gap-2">
            {(agentWithSuggestions.suggestions ?? []).map((text, idx) => (
              <button
                key={`${text}-${idx}`}
                onClick={() => onPrefill(text)}
                className="w-full text-left rounded border border-border bg-transparent hover:bg-surface-2 transition-colors px-3 py-2.5"
              >
                <span className="text-small text-dim leading-relaxed">{text}</span>
              </button>
            ))}
          </div>
        </>
      ) : (
        <>
          <div className="flex flex-col items-center gap-2">
            <p className="text-body font-medium text-text">✦ WES Code</p>
            <p className="text-small text-muted">{t('chat.collabMode', '智能路由 · AI 自动选择最合适的助手')}</p>
          </div>
          <div className="w-full max-w-[260px] flex flex-col gap-2">
            {quickStarts.map(item => (
              <button
                key={item.text}
                onClick={() => onPrefill(item.text)}
                className="w-full text-left rounded border border-border bg-transparent hover:bg-surface-2 transition-colors px-3 py-2.5"
              >
                <span className="text-small text-dim">{item.icon} {item.text}</span>
              </button>
            ))}
          </div>
          <p className="text-small text-muted">{t('chat.orDescribe', '或直接描述你的问题')}</p>
        </>
      )}
    </div>
  )
}
