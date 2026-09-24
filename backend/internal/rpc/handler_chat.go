package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/weisyn/wesapp/providerid"
	"github.com/weisyn/wesapp/wire"
	"github.com/weisyn/wescode/internal/editengine"
	appengine "github.com/weisyn/wescode/internal/engine"
	"github.com/weisyn/wescode/internal/notify"
	wesgine "github.com/weisyn/wesgine"
	wesengine "github.com/weisyn/wesgine/engine"
	"github.com/weisyn/wesgine/tool"
)

// ciLabelFor derives a chip label for server-resolved context items that the
// webview sent as bare {id, sourceId} (label unknown at send time).
func ciLabelFor(r appengine.ContextResolved) string {
	if p := r.FilePath; p != "" {
		if base := filepath.Base(p); base != "." && base != string(filepath.Separator) {
			return base
		}
		return p
	}
	if r.CodeSnippet != nil {
		return filepath.Base(r.CodeSnippet.Path)
	}
	return r.ID
}

// ciDetailFor derives the secondary human line for server-resolved items.
func ciDetailFor(r appengine.ContextResolved) string {
	if r.CodeSnippet != nil {
		return fmt.Sprintf("%s:%d-%d", r.CodeSnippet.Path, r.CodeSnippet.StartLine, r.CodeSnippet.EndLine)
	}
	return r.FilePath
}

func (h *Handler) handleChatSend(ctx context.Context, req Request) (any, *RPCError) {
	var params ChatSendParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		L(ctx).Warn("[chat] decode.error", "err", err)
		return nil, invalidParams(err)
	}
	// P0-3: chatLog 携带 dispatchHandler 注入的 request_id/trace_id，
	// 下游（含 goroutine）统一使用。
	chatLog := L(ctx)
	chatLog.Info("[chat] handleChatSend.enter",
		"message_len", len(params.Message),
		"session_id", params.SessionID,
		"agent_id", params.AgentID,
		"provider_id", params.ProviderID,
		"work_dir", params.WorkDir,
		"model_binding", params.ModelBinding,
		"allow_paths_count", len(params.AllowPaths),
		"file_ids_count", len(params.FileIDs),
		"file_paths_count", len(params.FilePaths),
		"code_snippets_count", len(params.CodeSnippets),
		"context_items_count", len(params.ContextItems),
		"media_count", len(params.Media),
		"activated_skills_count", len(params.ActivatedSkills),
	)
	if params.Message == "" && len(params.ContextItems) == 0 && len(params.FilePaths) == 0 && len(params.FileIDs) == 0 && len(params.Media) == 0 {
		chatLog.Warn("[chat] handleChatSend.REJECTED.empty_message")
		return nil, &RPCError{Code: -32602, Message: "message is required"}
	}

	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	var codeSnippets []appengine.CodeSnippet
	for _, s := range params.CodeSnippets {
		codeSnippets = append(codeSnippets, appengine.CodeSnippet{
			FilePath:  s.FilePath,
			StartLine: s.StartLine,
			EndLine:   s.EndLine,
			Code:      s.Code,
			Language:  s.Language,
		})
	}
	var contextItems []appengine.ContextItemResolved
	var needResolve []appengine.ContextResolveReq
	for _, ci := range params.ContextItems {
		hasContent := ci.FilePath != "" || ci.Content != "" || ci.CodeSnippet != nil
		if hasContent {
			item := appengine.ContextItemResolved{
				ID:       ci.ID,
				SourceID: ci.SourceID,
				Label:    ci.Label,
				Detail:   ci.Detail,
				FilePath: ci.FilePath,
				Content:  ci.Content,
			}
			if ci.CodeSnippet != nil {
				item.CodeSnippet = &appengine.CodeSnippet{
					FilePath:  ci.CodeSnippet.Path,
					StartLine: ci.CodeSnippet.StartLine,
					EndLine:   ci.CodeSnippet.EndLine,
					Language:  ci.CodeSnippet.Language,
					Code:      ci.CodeSnippet.Content,
				}
			}
			contextItems = append(contextItems, item)
		} else {
			needResolve = append(needResolve, appengine.ContextResolveReq{ID: ci.ID, SourceID: ci.SourceID})
		}
	}
	if len(needResolve) > 0 {
		sourceIDByItemID := make(map[string]string, len(needResolve))
		for _, nr := range needResolve {
			sourceIDByItemID[nr.ID] = nr.SourceID
		}
		resolved, resolveErr := h.engine.ResolveContextItems(ctx, needResolve)
		if resolveErr != nil {
			chatLog.Warn("[chat] contextItems.resolve.error", "err", resolveErr)
		}
		for _, r := range resolved {
			item := appengine.ContextItemResolved{
				ID:       r.ID,
				SourceID: sourceIDByItemID[r.ID],
				Label:    ciLabelFor(r),
				Detail:   ciDetailFor(r),
				FilePath: r.FilePath,
				Content:  r.Content,
			}
			if r.CodeSnippet != nil {
				item.CodeSnippet = &appengine.CodeSnippet{
					FilePath:  r.CodeSnippet.Path,
					StartLine: r.CodeSnippet.StartLine,
					EndLine:   r.CodeSnippet.EndLine,
					Language:  r.CodeSnippet.Language,
					Code:      r.CodeSnippet.Content,
				}
			}
			contextItems = append(contextItems, item)
		}
	}

	chatLog.Info("[chat] engine.RunChat.calling")
	eventCh, sessionID, err := h.engine.RunChat(runCtx, params.SessionID, params.AgentID, params.Message, appengine.ChatOpts{
		ProviderID:      params.ProviderID,
		Model:           params.Model,
		ModelBinding:    params.ModelBinding,
		FilePaths:       params.FilePaths,
		FileIDs:         params.FileIDs,
		WorkDir:         params.WorkDir,
		AllowPaths:      params.AllowPaths,
		TestTimeout:     params.TestTimeout,
		CodeSnippets:    codeSnippets,
		Resume:          params.Resume,
		ActivatedSkills: params.ActivatedSkills,
		ContextItems:    contextItems,
		Media:           params.Media,
		ThinkingLevel:   params.ThinkingLevel,
		GroupID:         params.GroupID,
	})

	// Pre-save the user message for group chats so the history page shows it.
	// Single-agent messages are persisted by the engine's Tracer (OnRunStart);
	// group runs bypass the loop, so their user message has no Tracer path.
	if params.GroupID != "" && params.Message != "" {
		if gsvc := h.engine.GroupService(); gsvc != nil {
			if _, saveErr := gsvc.SaveMessage(ctx, h.engine.CellID(), "local", params.GroupID, "user", params.Message, nil); saveErr != nil {
				chatLog.Warn("[chat] group persist: save user message failed",
					"group_id", params.GroupID, "error", saveErr)
			}
		}
	}

	if err != nil {
		chatLog.Warn("[chat] engine.RunChat.error → synthesize EventError stream", "err", err)
		// INV-ERROR-ATTRIBUTION: sole user-visible error channel is stream
		// EventError (same MapEvent → StreamError path). Pre-run failures
		// (no_provider / not initialized / etc.) synthesize the same shape —
		// never ChatSendResult.Error / ErrorHint.
		if sessionID == "" {
			sessionID = params.SessionID
		}
		if sessionID == "" {
			sessionID = fmt.Sprintf("ses-prerun-%d", time.Now().UnixNano())
		}
		eventCh = synthesizePreRunErrorEvents(err, sessionID, params.ProviderID, params.Model)
	}
	chatLog.Info("[chat] run.start",
		"session_id", sessionID,
		"agent_id", params.AgentID,
		"provider_id", params.ProviderID,
		"model", params.Model,
		"thinking_level", params.ThinkingLevel,
		"is_resume", params.Resume,
		"is_group", params.GroupID != "",
		"work_dir", params.WorkDir,
	)

	if sessionID != "" {
		if reg := h.engine.StopRegistry(); reg != nil {
			reg.Register(sessionID, runCancel)
			defer reg.Unregister(sessionID)
		}
	}

	requestID := rawIDToString(req.ID)
	result := ChatSendResult{OK: true, SessionID: sessionID}
	pendingEdits := make(map[string]string)
	var explorationPaths []string
	var runEndInput, runEndOutput int

	type outItem struct {
		notif StreamNotification
	}
	outCh := make(chan outItem, 64)
	consumerCtx, consumerCancel := context.WithCancel(ctx)
	defer consumerCancel()
	var doneSent atomic.Bool
	var interruptedSent atomic.Bool
	var errorSent atomic.Bool
	// Code of the EventError that ended the run, reused as the fallback
	// done's reason so the client dedupes instead of printing it twice.
	// Nil means no terminal error carried a code.
	var terminalErrorCode atomic.Pointer[string]
	const maxAutoRetries = 3
	var retryCount int
	var shouldAutoRetry bool
	var outChClosed atomic.Bool
	// Speaker of the group member currently holding the floor. Only
	// agent.start / agent.done carry a name; the engine forwards each
	// member's own events (text, tools, thinking) with AgentID left empty
	// (internal/agent/group.go stampGroupEvent). Without carrying the name
	// across that gap every member's output arrives anonymous and the UI
	// renders one undifferentiated reply. Stays empty for single-agent runs,
	// where the events already carry their agent or need no attribution.
	currentAgentID := ""
	currentAgentName := ""
	// Resolved lazily and only for group runs: a single-agent turn never
	// reads it, and the roster is stable for the life of one run.
	var agentNames map[string]string
	// Per-member text accumulator for group message persistence. On
	// agent.done, the accumulated text is saved to the group messages table
	// via wesapp/group.SaveMessage so history survives page refresh.
	agentTextBuf := map[string][]string{}
	isGroupChat := params.GroupID != ""
	go func() {
		chatLog.Info("[chat] consumer.start")
		eventCount := 0
		for ev := range eventCh {
			eventCount++
			if ev.Type != wesengine.EventStreamDelta && ev.Type != wesengine.EventThinkingDelta && ev.Type != wesengine.EventThinkingDone {
				chatLog.Debug("[chat] consumer.event",
					"idx", eventCount,
					"type", ev.Type,
					"run_id", ev.RunID,
					"session_id", ev.SessionID,
				)
			}
			if ev.Type == wesengine.EventRunEnd {
				if d, ok := ev.Data.(wesengine.RunEndData); ok {
					runEndInput = d.TotalInputTokens
					runEndOutput = d.TotalOutputTokens

					chatLog.Info("[chat] run.end",
						"session_id", result.SessionID,
						"run_id", result.RunID,
						"reason", d.Reason,
						"total_turns", d.TotalTurns,
						"input_tokens", d.TotalInputTokens,
						"output_tokens", d.TotalOutputTokens,
						"cache_reads", d.TotalCacheReads,
						"cache_writes", d.TotalCacheWrites,
						"elapsed_ms", d.ElapsedMS,
						"retryable", d.Retryable,
					)

					if d.Retryable && retryCount < maxAutoRetries {
						retryCount++
						chatLog.Info("[chat] auto-retry: retryable run detected, scheduling resume",
							"session_id", result.SessionID,
							"reason", d.Reason,
							"retry_count", retryCount,
							"retry_reason", d.RetryReason,
						)
						shouldAutoRetry = true
					}
				}
			}
			if ev.RunID != "" {
				result.RunID = ev.RunID
			}
			// INV-SESSION-ID-01: the public session identity is immutable
			// end-to-end. result.SessionID is the conversation ID the client
			// knows (the request sessionId, or the engine-generated "ses-…"
			// for a new conversation). Engine events may carry INTERNAL group /
			// sub-session namespaces ("auto-ses-…", "ses-…/<agent>") that must
			// never overwrite it — doing so made stream notifications and the
			// RPC response flip to a different sessionId mid-run, which the
			// extension host propagated as onDidActiveConversationChange and
			// the UI rendered as "conversation suddenly reset during streaming".

			if ev.Type == wesengine.EventToolStart {
				if editPreview := buildEditPreviewFromStart(ev, pendingEdits, h.engine.PrimaryRoot(), h.engine.FileProvider()); editPreview != nil {
					// 存 Paths 不存 Path：后者是展示串，多文件时不是可寻址的路径。
					if len(editPreview.Paths) > 0 {
						h.editTxMu.Lock()
						h.editTxPaths[editPreview.TxID] = editPreview.Paths
						h.editTxMu.Unlock()
					}
					select {
					case outCh <- outItem{notif: StreamNotification{
						RequestID: requestID,
						Event: StreamEvent{
							RequestID: requestID, Type: wire.EditPreview,
							EditPreview: editPreview,
							SessionID:   result.SessionID, RunID: result.RunID,
						},
					}}:
					case <-consumerCtx.Done():
						for range eventCh {
						}
						return
					}
				}
			}

			if ev.Type == wesengine.EventToolStart {
				if v, ok := ev.Data.(wesengine.ToolStartData); ok {
					if v.Tool == "read" || v.Tool == "search_symbols" {
						explorationPaths = append(explorationPaths, v.Tool+": "+v.Desc)
					}
				}
			}

			if ev.Type == wesengine.EventToolResult {
				if editPreview := buildEditAppliedFromResult(ev, pendingEdits); editPreview != nil {
					select {
					case outCh <- outItem{notif: StreamNotification{
						RequestID: requestID,
						Event: StreamEvent{
							RequestID: requestID, Type: wire.EditApplied,
							EditPreview: editPreview,
							SessionID:   result.SessionID, RunID: result.RunID,
						},
					}}:
					case <-consumerCtx.Done():
						for range eventCh {
						}
						return
					}
				}
			}

			streamEvent, ok := mapEvent(ev)
			if !ok {
				continue
			}
			streamEvent.RequestID = requestID
			streamEvent.SessionID = result.SessionID
			streamEvent.RunID = result.RunID
			switch streamEvent.Type {
			case wire.AgentStart:
				currentAgentID = streamEvent.AgentID
				if agentNames == nil {
					agentNames = h.engine.AgentNameMap(ctx)
				}
				currentAgentName = agentNames[currentAgentID]
				if currentAgentName == "" {
					currentAgentName = currentAgentID
				}
				streamEvent.AgentName = currentAgentName
				agentTextBuf[currentAgentID] = nil // reset for this member's turn
			case wire.AgentDone:
				streamEvent.AgentName = currentAgentName
				// Persist this member's reply to the group messages table so
				// history survives page refresh. Fires once per member per
				// group turn, mirroring wesclaw's onGroupAgentComplete.
				if isGroupChat && currentAgentID != "" {
					text := strings.Join(agentTextBuf[currentAgentID], "")
					if text != "" {
						h.persistGroupAgentMessage(ctx, params.GroupID, currentAgentID, currentAgentName, text)
					}
					delete(agentTextBuf, currentAgentID)
				}
				currentAgentID = ""
				currentAgentName = ""
			default:
				if streamEvent.AgentID == "" {
					streamEvent.AgentID = currentAgentID
				}
				if streamEvent.AgentName == "" {
					streamEvent.AgentName = currentAgentName
				}
				// Accumulate text for group message persistence.
				if isGroupChat && currentAgentID != "" && streamEvent.Type == wire.TextDelta && streamEvent.Text != "" {
					agentTextBuf[currentAgentID] = append(agentTextBuf[currentAgentID], streamEvent.Text)
				}
			}
			if streamEvent.Type == wire.Error {
				if isTransientProviderError(streamEvent.Error) && retryCount < maxAutoRetries {
					retryCount++
					shouldAutoRetry = true
					chatLog.Info("[chat] auto-retry: transient provider error, scheduling resume",
						"session_id", result.SessionID,
						"code", streamEvent.Error.Code,
						"retry_count", retryCount,
					)
					continue
				}
				// Recoverable means non-terminal — the engine's own contract
				// (engine/errors.go: self-heal hints and control-loop nudges
				// "let the run continue", all emitted with Recoverable=true).
				// Ending the stream on one abandons a run that is still
				// working: an intermediate_narration_nudge at turn 0 closed
				// this RPC after 21s while the loop went on to finish
				// end_turn at turn 5 — 66s and 403K input tokens whose output
				// never reached the user (2026-09-02, run-8a635d7e). Forward
				// it so the front end can render its hint (wesui maps these
				// at severity silent/info), then keep listening.
				if isTerminalStreamError(streamEvent.Error) {
					errorSent.Store(true)
					if streamEvent.Error != nil {
						code := string(streamEvent.Error.Code)
						terminalErrorCode.Store(&code)
						chatLog.Warn("[chat] run.terminal_error",
							"session_id", result.SessionID,
							"run_id", result.RunID,
							"error_code", streamEvent.Error.Code,
							"error_kind", streamEvent.Error.Kind,
							"provider", streamEvent.Error.Provider,
							"provider_id", params.ProviderID,
							"model", params.Model,
							"recoverable", streamEvent.Error.Recoverable,
						)
					}
				} else {
					// isTerminalStreamError treats a nil payload as terminal,
					// so Error is non-nil on this branch.
					chatLog.Info("[chat] consumer.recoverable_error, stream continues",
						"code", streamEvent.Error.Code,
						"kind", streamEvent.Error.Kind,
					)
				}
			}
			if streamEvent.Type == wire.Done {
				doneSent.Store(true)
				h.attachChangesetSummary(&streamEvent)
			} else if streamEvent.Type == wire.Interrupted {
				// INV-TERM fix (audit): WireInterrupted is itself a terminal
				// event — it closes outCh below, and the fallback WireDone
				// must NOT be synthesized afterwards (the client already saw
				// interrupted; a fabricated done("missing_terminal_event")
				// makes the stream state machine see two conflicting
				// terminals back-to-back).
				interruptedSent.Store(true)
				h.attachChangesetSummary(&streamEvent)
			}

			// After any terminal event (done/error/interrupted), close outCh
			// to release the main RPC loop (and chatInFlight) immediately.
			// Settlement runs asynchronously on the engine side; remaining
			// events (run.end) are forwarded directly by this goroutine.
			// Previous bug: only checked doneSent — but on error paths the
			// engine emits EventError without EventDone, so doneSent stays
			// false and the RPC blocks through the entire Settlement phase.
			isTerminal := doneSent.Load() || errorSent.Load() || streamEvent.Type == wire.Interrupted

			if outChClosed.Load() {
				// After outCh close the RPC has returned and the TS stream
				// route is cleaned up. Forwarding here would only produce
				// DROPPED.no_route noise. Just drain to unblock the engine
				// goroutine; Settlement outcomes are persisted by the engine
				// internally (INV-CTX-42: not user-visible).
				continue
			}

			select {
			case outCh <- outItem{notif: StreamNotification{RequestID: requestID, Event: streamEvent}}:
			case <-consumerCtx.Done():
				chatLog.Info("[chat] consumer.ctx_done.drain_start")
				for range eventCh {
				}
				chatLog.Info("[chat] consumer.ctx_done.drain_end")
				return
			}

			if isTerminal && !outChClosed.Load() {
				outChClosed.Store(true)
				close(outCh)
				chatLog.Info("[chat] consumer.outCh_closed_after_terminal",
					"event_count", eventCount, "done", doneSent.Load(), "error", errorSent.Load())
			}
		}
		if !outChClosed.Load() {
			outChClosed.Store(true)
			close(outCh)
		}
		chatLog.Info("[chat] consumer.end", "event_count", eventCount, "done_sent", doneSent.Load())
	}()

	chatLog.Info("[chat] notify.loop.start")
	notifyCount := 0
	for item := range outCh {
		notifyCount++
		if err := h.notifier.Notify(notify.ChatStream, item.notif); err != nil {
			chatLog.Error("[chat/stream] notify failed, cancelling consumer",
				"error", err,
				"eventType", item.notif.Event.Type,
				"sessionId", result.SessionID,
			)
			consumerCancel()
			break
		}
	}
	chatLog.Info("[chat] notify.loop.end", "notify_count", notifyCount)

	if shouldSendFallbackDone(doneSent.Load(), errorSent.Load(), interruptedSent.Load()) {
		doneReason := "missing_terminal_event"
		if c := terminalErrorCode.Load(); c != nil && *c != "" {
			// The engine already emitted the real terminal (EventError) and
			// the client rendered its text; the fallback done only lets the
			// bubble close. Carry that error's own code so
			// useMessageStream.handleDone — which dedupes by
			// `ErrorPart.code === reason` — closes silently instead of
			// reporting the same failure a second time.
			//
			// Two neighbouring values are both wrong. The literal "error"
			// that used to sit here is in no ErrorCode domain, so
			// createDoneReasonMapper fell through to its raw-token branch and
			// printed a bare "error" under the answer. An empty string is
			// wrong in the other direction: handleDone does `reason ||
			// 'end_turn'`, and end_turn is severity:silent, so a failed run
			// would be labelled a normal completion and an unfinished plan
			// would never be marked interrupted (that marking is gated on
			// `doneReason !== 'end_turn'`).
			doneReason = *c
		}
		// No code means the terminal error carried no payload, so the client
		// rendered nothing for it — missing_terminal_event stays, because it
		// is then the only thing telling the user the run died.
		chatLog.Warn("[chat] done event not sent by engine, sending fallback",
			"session_id", result.SessionID, "run_id", result.RunID,
			"error_sent", errorSent.Load(), "interrupted_sent", interruptedSent.Load(),
			"done_reason", doneReason)
		if err := h.notifier.Notify(notify.ChatStream, StreamNotification{
			RequestID: requestID,
			Event: StreamEvent{
				RequestID: requestID,
				Type:      wire.Done,
				Text:      doneReason,
				SessionID: result.SessionID,
				RunID:     result.RunID,
			},
		}); err != nil {
			chatLog.Warn("[chat] fallback done notify failed", "session_id", result.SessionID, "error", err)
		}
	}

	// INV-WS-11: exploration-path persistence is housekeeping, NOT part of
	// the chat/send RPC. It must never block handleChatSend.exit — CKG
	// WriterDB contention held this tail (and with it the Electron
	// chatInFlight lock) for 3m28s after the user already saw `done`
	// (2026-08-16 incident). Same discipline as Settlement (async) and
	// auto-retry (go func below): the visible turn ends at `done`; the RPC
	// tail may finish whenever.
	if len(explorationPaths) > 0 {
		bgCtx := context.WithoutCancel(ctx)
		go h.engine.SaveExplorationPaths(bgCtx, params.AgentID, result.SessionID, result.RunID, explorationPaths)
	}

	// Auto-retry runs in the background — don't block the RPC response.
	if shouldAutoRetry {
		chatLog.Info("[chat] auto-retry: initiating resume Run",
			"session_id", result.SessionID,
			"retry_count", retryCount,
		)
		go func() {
			// P0-3 fix: auto-retry must carry the original attachments —
			// resume constructs a fresh user message in run.go, so Media /
			// FileIDs / ContextItems dropped here would never reach the
			// resumed run (model would lose the files it was asked about).
			retryOpts := appengine.ChatOpts{
				WorkDir:         params.WorkDir,
				Resume:          true,
				ProviderID:      params.ProviderID,
				Model:           params.Model,
				ModelBinding:    params.ModelBinding,
				FilePaths:       params.FilePaths,
				FileIDs:         params.FileIDs,
				AllowPaths:      params.AllowPaths,
				ActivatedSkills: params.ActivatedSkills,
				ContextItems:    contextItems,
				Media:           params.Media,
				ThinkingLevel:   params.ThinkingLevel,
			}
			bgCtx := context.WithoutCancel(ctx)
			retryCh, _, retryErr := h.engine.RunChat(bgCtx, result.SessionID, params.AgentID,
				"Continue the plan — previous run was interrupted. Resume from where you left off.", retryOpts)
			if retryErr != nil {
				chatLog.Warn("[chat] auto-retry failed to start", "err", retryErr)
				return
			}
			for ev := range retryCh {
				streamEvent, ok := mapEvent(ev)
				if !ok {
					continue
				}
				streamEvent.RequestID = requestID
				streamEvent.SessionID = result.SessionID
				if err := h.notifier.Notify(notify.ChatStream, StreamNotification{
					RequestID: requestID,
					Event:     streamEvent,
				}); err != nil {
					chatLog.Warn("[chat] stream notify failed", "session_id", result.SessionID, "type", streamEvent.Type, "error", err)
				}
			}
		}()
	}

	chatLog.Info("[chat] handleChatSend.exit",
		"session_id", result.SessionID,
		"run_id", result.RunID,
		"notify_count", notifyCount,
		"done_sent", doneSent.Load(),
		"input_tokens", runEndInput,
		"output_tokens", runEndOutput,
	)
	return result, nil
}

// attachChangesetSummary attaches the active changeset snapshot to a terminal
// wire event (done/interrupted) so consumers can render the bird's-eye view
// of files changed by the run.
func (h *Handler) attachChangesetSummary(ev *StreamEvent) {
	if cs := h.engine.ActiveChangeset(); cs != nil && cs.FileCount() > 0 {
		snap := cs.Snapshot()
		ev.ChangesetSummary = &StreamChangesetSummary{
			ID:           snap.ID,
			CheckpointID: snap.CheckpointID,
			Files:        snap.Files,
			FileCount:    len(snap.Files),
		}
	}
}

func (h *Handler) handleChatCancel(_ context.Context, req Request) (any, *RPCError) {
	var params struct {
		SessionID string `json:"sessionId"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
	}
	// Cancel the specific session's run context via StopRegistry (precise).
	// Do NOT call Interrupt() / InterruptAll() — that kills ALL concurrent
	// runs in the Cell including agent sub-runs, causing premature
	// termination of retries and delegation chains.
	//
	// Two complementary mechanisms are used on purpose:
	//   reg.Stop         — cancels the run context immediately, unblocking an
	//                      in-flight LLM call so the loop can observe it.
	//   InterruptSession — marks the run interrupted (graceful flag).
	// The loop checks the interrupted flag at the top of each turn before
	// ctx.Err(), so the terminal event is EventInterrupted; all TS consumers
	// treat interrupted / done / error uniformly, and the fallback done
	// covers the engine-crash case.
	if reg := h.engine.StopRegistry(); reg != nil && params.SessionID != "" {
		reg.Stop(params.SessionID)
	}
	// Fallback: if no StopRegistry or no sessionID, use session-scoped interrupt.
	if params.SessionID != "" {
		h.engine.InterruptSession(params.SessionID)
	} else {
		h.engine.Interrupt()
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleHITLRespond(ctx context.Context, req Request) (any, *RPCError) {
	var params HITLRespondParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.RequestID == "" {
		return nil, &RPCError{Code: -32602, Message: "requestId is required"}
	}

	var err error
	switch {
	case params.Choice != "":
		err = h.engine.HITLSubmitChoice(ctx, params.RequestID, params.Choice)
	case params.Value != "":
		err = h.engine.HITLSubmitInput(ctx, params.RequestID, params.Value)
	case params.Grant != "":
		err = h.engine.HITLSubmit(ctx, params.RequestID, params.Grant, params.Scope, params.Feedback)
	default:
		err = h.engine.HITLCancel(ctx, params.RequestID)
	}
	if err != nil {
		return nil, internalError(err)
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleGetSessionPlan(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.SessionID == "" {
		return nil, &RPCError{Code: -32602, Message: "sessionId is required"}
	}
	plan, err := h.engine.GetSessionPlan(ctx, params.SessionID)
	if err != nil {
		return nil, internalError(err)
	}
	if plan == nil {
		return nil, nil
	}
	steps := make([]StreamPlanStep, 0, len(plan.Steps))
	for _, s := range plan.Steps {
		status := string(s.Status)
		if status == "done" {
			status = "completed"
		}
		sp := StreamPlanStep{
			ID:     s.ID,
			Title:  s.Description,
			Status: status,
			Detail: s.Result,
		}
		for _, v := range s.Verification {
			sp.Verification = append(sp.Verification, StreamPlanVerifyAction{
				Kind: string(v.Kind), Command: v.Command, Expect: v.Expect,
			})
		}
		for _, vr := range s.VerifyResults {
			sp.VerifyResults = append(sp.VerifyResults, StreamPlanVerifyResult{
				Kind: string(vr.Kind), Command: vr.Command,
				Passed: vr.Passed, Output: vr.Output, Duration: vr.Duration,
			})
		}
		steps = append(steps, sp)
	}
	planID := plan.ID
	if planID == "" {
		planID = "default"
	}
	return &StreamPlanData{
		ID:                planID,
		Title:             plan.Title,
		Overview:          plan.Overview,
		Analysis:          plan.Analysis,
		Steps:             steps,
		SessionID:         plan.SessionID,
		Status:            string(plan.Status),
		Interrupted:       plan.Status == wesengine.PlanTerminated,
		InterruptedReason: plan.TerminationDetail,
	}, nil
}

func (h *Handler) handleListConversations(ctx context.Context, _ Request) (any, *RPCError) {
	sessions, err := h.engine.ListConversations(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	items := make([]ConversationItem, 0, len(sessions))
	for _, s := range sessions {
		// 空标题原样发空串，不在后端填「新会话」——占位文案是界面语言的函数，
		// 而后端不知道用户界面语言。两个消费点（chatViewPane / wescodeHeaderTabs）
		// 早就写了 `c.title || wescodeL('new_session')`，那个分支此前永不触发：
		// 一个正确的兜底被上游预填挡住，于是英文用户看到中文标题而兜底看起来是活的。
		title := s.Title
		item := ConversationItem{
			SessionID:          s.ID,
			AgentID:            s.AgentID,
			Title:              title,
			CellID:             s.CellID,
			WorkspaceLabel:     s.WorkspaceLabel,
			IsCurrentWorkspace: s.IsCurrentWorkspace,
			// 发原始时刻（RFC3339），不发 "01-02 15:04"：档位（今天给时刻、往前给月日）
			// 与千分位/月日顺序都是 locale 的函数，后端不知道用户界面语言。
			// 前端 formatChatTimestamp 在渲染点决定。
			LastTime: s.UpdatedAt.UTC().Format(time.RFC3339),
		}
		if s.UpdatedAt.IsZero() {
			item.LastTime = s.CreatedAt.UTC().Format(time.RFC3339)
		}
		if s.UpdatedAt.IsZero() && s.CreatedAt.IsZero() {
			item.LastTime = time.Now().UTC().Format(time.RFC3339)
		}
		items = append(items, item)
	}
	return items, nil
}

func (h *Handler) handleDeleteConversation(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.SessionID == "" {
		return nil, &RPCError{Code: -32602, Message: "sessionId is required"}
	}
	cell := h.engine.Cell()
	if cell == nil {
		return nil, &RPCError{Code: -32603, Message: "engine not initialized"}
	}
	if err := cell.Sessions().Delete(ctx, "local", params.SessionID); err != nil {
		return nil, internalError(err)
	}
	h.engine.ForgetSessionAgent(params.SessionID)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleListConversationMessages(ctx context.Context, req Request) (any, *RPCError) {
	var params ListConversationMessagesParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.SessionID == "" {
		return nil, &RPCError{Code: -32602, Message: "sessionId is required"}
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 0
	}
	msgs, err := h.engine.ListUIMessages(ctx, params.SessionID, params.CellID, limit)
	if err != nil {
		return nil, internalError(err)
	}
	out := make([]ConversationMessageItem, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, ConversationMessageItem{
			ID:           m.ID,
			Role:         m.Role,
			Content:      m.Content,
			CreatedAt:    m.CreatedAt,
			AgentID:      m.AgentID,
			ContentParts: m.ContentParts,
		})
	}
	return out, nil
}

// mapEvent transforms a wesgine engine.Event into wescode's StreamEvent.
//
// ALIGNMENT (2026-07-01): the emitted `Type` string values MUST match
// wesclaw/wire.MapEvent's WireEvent type constants (see wire.*).
// This lets the wescode webview consume events via the same
// @wesui/streaming reducer as wesclaw / wescraft. The wescode-specific
// fields (Text, ToolCall, Plan, HITL, Error, ...) remain as compat
// shims until the frontend fully migrates to `normalizeToWireEvent`.
func mapEvent(ev wesengine.Event) (StreamEvent, bool) {
	mapped := wire.MapEvent(ev)
	if mapped == nil {
		return StreamEvent{}, false
	}

	out := StreamEvent{AgentID: ev.AgentID, Type: mapped.Type}

	switch mapped.Type {

	// --- text / thinking ---
	case wire.TextDelta:
		if v, ok := ev.Data.(string); ok {
			out.Text = v
		}
	case wire.Thinking:
		if v, ok := ev.Data.(string); ok {
			out.Text = v
		}
	case wire.ThinkingDone:
		// no payload needed

	// --- tool ---
	case wire.ToolStart:
		if v, ok := ev.Data.(wesengine.ToolStartData); ok {
			if v.Tool == "plan" {
				return StreamEvent{}, false
			}
			args := map[string]string{"description": v.Desc}
			if len(v.Params) > 0 {
				var paramMap map[string]any
				if json.Unmarshal(v.Params, &paramMap) == nil {
					for k, val := range paramMap {
						switch tv := val.(type) {
						case string:
							args[k] = tv
						default:
							if bs, err := json.Marshal(val); err == nil {
								args[k] = string(bs)
							}
						}
					}
				}
			}
			out.ToolCall = &StreamToolCall{
				ID:     v.ID,
				Tool:   v.Tool,
				Status: "running",
				Args:   args,
			}
		}
	case wire.ToolDone:
		if v, ok := ev.Data.(wesengine.ToolResultData); ok {
			if v.Tool == "plan" {
				return StreamEvent{}, false
			}
			status := "done"
			if v.IsError {
				status = "error"
			}
			out.ToolCall = &StreamToolCall{
				ID:     v.ID,
				Tool:   v.Tool,
				Status: status,
				Result: v.Result,
			}
		}

	// --- HITL ---
	case wire.HITLRequest:
		if v, ok := wire.ParseHITLRequest(ev.Data); ok {
			out.HITL = streamHITLFromRequest(v)
		}
	case wire.HITLTimeout:
		if v, ok := parseHITLResolution(ev.Data); ok {
			out.HITL = &StreamHITL{RequestID: v.RequestID, Kind: v.Kind, Grant: v.Grant}
		}
	case wire.HITLResolved:
		if v, ok := parseHITLResolution(ev.Data); ok {
			out.HITL = &StreamHITL{RequestID: v.RequestID, Kind: v.Kind, Grant: v.Grant}
		}

	// --- error ---
	case wire.Error:
		// ParseErrorData accepts ErrorData / *ErrorData / string / unknown —
		// type-assert alone misses JSON-roundtripped payloads.
		errData := wesengine.ParseErrorData(ev.Data)
		msg := errData.Message
		// INV-ERROR-ATTRIBUTION: A-type keeps Message/Text empty;
		// ProviderMessage + Provider are the only display fields.
		// Do not dual-write ProviderMessage into Text (flattens A→B).
		if errData.ProviderMessage != "" {
			msg = ""
		}
		// The no-provider branch that used to sit here rewrote Kind to
		// "no_provider" — a value the ErrorKind domain does not contain, so the
		// webview's kind switch fell through for the one failure it most needed
		// to explain — and reached that branch by text-matching the message.
		// The engine now names the condition itself
		// (ErrorCodeNoProvider + ErrorKindCognitive), and wesui keys its setup
		// prompt off the code, not the kind.
		out.Text = msg
		out.Error = &StreamError{
			Text:            msg,
			Kind:            errData.Kind,
			Code:            errData.Code,
			Recoverable:     errData.Recoverable,
			ProviderMessage: errData.ProviderMessage,
			Provider:        errData.Provider,
		}

	// --- run lifecycle ---
	case wire.RunEnd:
		if d, ok := ev.Data.(wesengine.RunEndData); ok {
			out.TokenUsage = &StreamTokenUsage{
				InputTokens:      d.TotalInputTokens,
				OutputTokens:     d.TotalOutputTokens,
				CacheReadTokens:  d.TotalCacheReads,
				CacheWriteTokens: d.TotalCacheWrites,
				CacheTokens:      d.TotalCacheReads + d.TotalCacheWrites,
				TotalTurns:       d.TotalTurns,
				ElapsedMS:        d.ElapsedMS,
			}
		}
	case wire.Done:
		// ParseStopReason, not ev.Data.(string). wire.MapEvent passes Data
		// through verbatim, so what arrives here is the engine's named
		// StopReason — and a Go assertion needs type identity, not
		// convertibility, so `.(string)` never matched it. It failed quietly:
		// `out.Text` stayed empty, `omitempty` dropped the field, and the
		// webview's `ev.text ?? 'end_turn'` filled in a stop reason that
		// wesui classifies as 'silent'. Every termination therefore rendered
		// as normal completion — budget_exhausted, cycle_detected and every
		// policy rejection looked like the model simply finished.
		//
		// This repo emitted the typed value itself (see the synthetic
		// EventDone further down this file), so the two halves of the
		// contract were written here and still disagreed. That is what an
		// assertion on `any` buys: it is a guess about a contract, not the
		// contract, and it does not fail where the guess is wrong.
		out.Text = string(wesengine.ParseStopReason(ev.Data))
	case wire.Interrupted:
		out.Text = "interrupted"

	// --- plan ---
	case wire.PlanCreated:
		if v, ok := ev.Data.(wesengine.PlanStartData); ok {
			out.PlanID = v.PlanID
			out.Plan = &StreamPlanData{ID: v.PlanID, Title: v.Title, Overview: v.Overview, Analysis: v.Analysis, Steps: mapPlanSteps(v.Steps), SessionID: v.SessionID}
		}
	case wire.PlanEdited:
		if v, ok := ev.Data.(wesengine.PlanEditData); ok {
			out.PlanID = v.PlanID
			out.Plan = &StreamPlanData{PlanID: v.PlanID, Field: v.Field, Detail: v.Value, SessionID: v.SessionID}
		}
	case wire.PlanUpdated:
		if v, ok := ev.Data.(wesengine.PlanStepData); ok {
			out.PlanID = v.PlanID
			out.StepID = v.StepID
			pd := &StreamPlanData{
				PlanID: v.PlanID,
				ID:     v.StepID,
				Status: string(v.Status),
				Result: v.Result,
				Detail: v.Note,
			}
			for _, vr := range v.VerifyResults {
				pd.VerifyResults = append(pd.VerifyResults, StreamPlanVerifyResult{
					Kind: string(vr.Kind), Command: vr.Command,
					Passed: vr.Passed, Output: vr.Output, Duration: vr.Duration,
				})
			}
			out.Plan = pd
		}
	case wire.PlanCompleted:
		if v, ok := ev.Data.(wesengine.PlanCompleteData); ok {
			out.PlanID = v.PlanID
			out.Plan = &StreamPlanData{PlanID: v.PlanID, TotalSteps: v.TotalSteps, DoneSteps: v.DoneSteps}
		}
	case wire.PlanInterrupted:
		if v, ok := ev.Data.(wesengine.PlanInterruptedData); ok {
			out.PlanID = v.PlanID
			out.Plan = &StreamPlanData{PlanID: v.PlanID, Reason: v.Reason}
		}
	// PlanReplan and PlanStall used to have arms here. Neither name exists in
	// wire or engine: replan was folded into plan_edit, and the stall detector
	// was deleted when INV-PE-09 removed implicit plan advancement (the engine
	// no longer decides a plan is stuck — the model does). The arms were
	// unreachable, and StreamPlanData.StallCount / LastUpdatedStep existed only
	// to feed them.
	case wire.PlanNudge:
		if v, ok := ev.Data.(wesengine.PlanNudgeData); ok {
			out.PlanID = v.PlanID
			out.Plan = &StreamPlanData{PlanID: v.PlanID, PendingCount: v.PendingCount, Attempt: v.Attempt}
		}

	// --- observe (own type names; never rewrite to wire.Error / loop_warning) ---
	// Observability passthrough: the engine's own payload struct, projected to
	// JSON, keeping the engine's event type. wesui's reducer turns these into
	// loop_alert parts and owns the copy.
	//
	// Cycle and loop used to be rewritten here into error events with
	// hand-written Chinese text and four invented Kinds
	// (cycle_terminated / cycle_warning / loop_detected / loop_warning), none of
	// which the ErrorKind domain contains — so the webview's kind switch fell
	// through and the bubble showed a red error for a warning the run recovered
	// from. Those branches also read count via data["count"].(int) off a
	// map[string]any: JSON numbers decode as float64, so the assertion always
	// failed and every message said "0 次". The terminal explanation now comes
	// from wesui's done-reason map (cycle_detected / loop_detected), and the
	// in-flight notice from LoopAlertBlock.
	case wire.ErrorStreak, wire.CognitiveRetry, wire.ContextPressure, wire.CacheHints,
		wire.CycleDetected, wire.LoopDetected, wire.LoopWarning:
		out.Observe = observePayload(ev.Data)

	// --- structured output (PC-03/PC-04) ---
	//
	// 这一层原样透传，不解释结构：形状由产出它的工具定义（`ToolResult.StructuredData`），
	// 形态由 wesui 决定（`reduce.ts` → `StructuredOutputPart`）。中间层一旦开始
	// 重塑就成了第三份真相。
	//
	// 此前没有这个 case，于是事件落到 `default: return StreamEvent{}, false` 被
	// 丢弃——而引擎已经在发、wesapp/wire 已经在映射、wesui 已经能渲染。四层里三层
	// 通着，断在这一处，症状是"那个富组件是死的"。
	//
	// 空 payload 不放行：wesui 的 reducer 对 undefined/null 会跳过，但让一个无内容
	// 的事件穿过整条链路到最远端再被丢弃，等于把判断推到离原因最远的地方。
	case wire.StructuredOutput:
		if raw, ok := ev.Data.(json.RawMessage); ok && len(raw) > 0 {
			out.StructuredData = raw
		} else {
			return StreamEvent{}, false
		}

	// --- subagent ---
	case wire.SubagentStart:
		if v, ok := ev.Data.(wesengine.SubagentStartData); ok {
			out.Subagent = &StreamSubagent{
				SubagentID:  v.AgentID,
				Description: v.Description,
				Status:      v.Status,
			}
		}
	case wire.SubagentProgress:
		if v, ok := ev.Data.(wesengine.SubagentProgressData); ok {
			out.Subagent = &StreamSubagent{
				SubagentID: v.AgentID,
				Status:     "running",
			}
		}
	case wire.SubagentDone:
		if v, ok := ev.Data.(wesengine.SubagentCompleteData); ok {
			out.Subagent = &StreamSubagent{
				SubagentID: v.AgentID,
				Status:     v.Status,
				Result:     v.Result,
				ElapsedMS:  v.ElapsedMS,
				Error:      v.Error,
			}
		}

	// --- group member boundaries ---
	// The only events in a group run that name their speaker. Everything
	// between a start and its done belongs to that member, but the engine's
	// stampGroupEvent rewrites RunID / SessionID / ParentRunID and leaves
	// AgentID alone, so the caller must carry the name forward itself
	// (see the consumer loop's currentAgentID). Dropping these here — which
	// the `default` arm did — makes every member's text arrive unattributed.
	case wire.AgentStart, wire.AgentDone:
		// out.AgentID is already ev.AgentID; the type alone is the payload.

	default:
		return StreamEvent{}, false
	}

	if out.Type == "" {
		return StreamEvent{}, false
	}
	return out, true
}

// observePayload copies engine Data into a JSON-object map so the
// webview can put it on WireEvent.data. map[string]any is copied as-is;
// structs (CognitiveRetryData / CacheHintsData) round-trip through JSON
// to keep snake_case tags.
func observePayload(data any) map[string]any {
	if data == nil {
		return nil
	}
	if m, ok := data.(map[string]any); ok {
		return m
	}
	b, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

func mapPlanSteps(steps []wesengine.PlanStep) []StreamPlanStep {
	out := make([]StreamPlanStep, 0, len(steps))
	for _, s := range steps {
		// P1-1: unify step status format with the polling path
		// (handleGetSessionPlan maps "done" → "completed"); both paths
		// must emit identical payloads so new consumers see one protocol.
		status := string(s.Status)
		if status == "done" {
			status = "completed"
		}
		sp := StreamPlanStep{
			ID:     s.ID,
			Title:  s.Description,
			Status: status,
			Detail: s.Result,
		}
		for _, v := range s.Verification {
			sp.Verification = append(sp.Verification, StreamPlanVerifyAction{
				Kind: string(v.Kind), Command: v.Command, Expect: v.Expect,
			})
		}
		for _, vr := range s.VerifyResults {
			sp.VerifyResults = append(sp.VerifyResults, StreamPlanVerifyResult{
				Kind: string(vr.Kind), Command: vr.Command,
				Passed: vr.Passed, Output: vr.Output, Duration: vr.Duration,
			})
		}
		out = append(out, sp)
	}
	return out
}

// shouldSendFallbackDone decides whether the RPC layer must synthesize a
// WireDone after the engine stream ended without one. INV-TERM invariant:
// WireInterrupted is itself a terminal event (the client's useMessageStream
// calls handleDone('interrupted') on it), so no fallback done must be
// fabricated afterwards — a done("missing_terminal_event") racing the real
// interrupted would present the front-end with two conflicting terminals.
// Error paths still get a fallback done so the client can close the bubble
// cleanly; its reason is the terminating error's own code (never "end_turn",
// which would mislabel a failure as a normal completion, and never the
// literal "error", which is in no ErrorCode domain).
func shouldSendFallbackDone(doneSent, errorSent, interruptedSent bool) bool {
	return !doneSent && !interruptedSent
}

// isTerminalStreamError reports whether an EventError ends the run.
//
// `ErrorData.Recoverable` is the engine's contract, not a hint: wesgine
// emits every B-type notice — self-heal retries and control-loop nudges
// such as intermediate_narration_nudge — with Recoverable=true precisely
// so consumers render a subtle hint (or stay silent) and keep listening.
// The loop continues underneath.
//
// Treating those as terminal closed outCh mid-run. Observed 2026-09-02
// (run-8a635d7e0eb7d4c26fe63f69): a narration nudge at turn 0 ended the
// stream, and the engine went on working for another 46 seconds — its
// remaining turns and the real end_turn landed in the log with no route
// to the client, while the user saw a stopped bubble and a red block.
//
// nil is terminal: an error payload we could not parse is not evidence of
// recovery, and the fallback done still closes the bubble.
func isTerminalStreamError(err *StreamError) bool {
	return err == nil || !err.Recoverable
}

// transientProviderCodes is this product's auto-retry policy, not a copy of
// the engine's classification: it names the A-type codes whose cause can pass
// on its own, so a delayed retry (via Resume) has a real chance. The engine's
// own retry/fallback layers already drained before this EventError reached us.
//
// Everything outside this set is deterministic (balance, auth, live-key,
// guardrail, model-not-found) — retrying the same input cannot succeed, so
// those must reach the front-end with the correct action (charge / sign in /
// none) instead of being swallowed by a silent retry.
//
// Keyed by wesengine.ErrorCode with engine constants, so retiring or renaming
// a code upstream breaks this build instead of quietly emptying the set.
var transientProviderCodes = map[wesengine.ErrorCode]bool{
	wesengine.ErrorCodeProviderOverloaded:    true,
	wesengine.ErrorCodeProviderRateLimit:     true,
	wesengine.ErrorCodeProviderStreamStall:   true,
	wesengine.ErrorCodeProviderAllUnhealthy:  true,
	wesengine.ErrorCodeProviderKeysExhausted: true,
}

// isTransientProviderError reports whether a stream error is an A-type
// (provider pass-through) transient failure worth auto-retrying. A-type is
// identified by ProviderMessage being present (the provider's own words), so
// B-type engine faults never match here even if a code overlapped.
func isTransientProviderError(err *StreamError) bool {
	if err == nil || err.ProviderMessage == "" {
		return false
	}
	return transientProviderCodes[err.Code]
}

// synthesizePreRunErrorEvents turns a RunChat setup failure into the same
// EventError (+ EventDone) stream shape as engine-emitted errors
// (INV-ERROR-ATTRIBUTION — single user-visible channel).
//
// Classification is the engine's: wesengine.ClassifyRunError runs the same
// sentinel table, the same A-type/B-type shape decision and the same
// ErrorCode as an in-loop failure. This used to be a local ladder of
// strings.Contains checks that recognised four of twenty sentinels and named
// one of them "wes_token_expired" — a code no engine path emits, so wesui's
// map had a key nothing could reach while the real failure fell through to raw
// wire text.
//
// The done reason is cognitive_error, not end_turn: route select failed, so no
// turn ever ran, and end_turn maps to "任务已完成。" — a failure rendered as
// success.
func synthesizePreRunErrorEvents(err error, sessionID, providerID, model string) <-chan wesengine.Event {
	ch := make(chan wesengine.Event, 2)
	ch <- wesengine.Event{
		Type:      wesengine.EventError,
		Data:      wesgine.ClassifyRunError(err, providerid.Pin(providerID), model),
		SessionID: sessionID,
	}
	ch <- wesengine.Event{
		Type:      wesengine.EventDone,
		Data:      wesengine.StopReasonCognitiveError,
		SessionID: sessionID,
	}
	close(ch)
	return ch
}

func rawIDToString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	var asNumber float64
	if err := json.Unmarshal(raw, &asNumber); err == nil {
		return fmt.Sprintf("%.0f", asNumber)
	}
	return string(raw)
}

func buildEditPreviewFromStart(ev wesengine.Event, pendingEdits map[string]string, workDir string, fp tool.FileProvider) *StreamEditPreview {
	v, ok := ev.Data.(wesengine.ToolStartData)
	if !ok {
		return nil
	}
	if v.Tool != "edit" && v.Tool != "write" && v.Tool != "apply_patch" {
		return nil
	}

	txID := editengine.NewTxID()
	pendingEdits[v.ID] = txID

	if v.Tool == "edit" {
		path, hunks, ok := editengine.ComputeHunksFromEditParams(v.Params, workDir, fp)
		if !ok {
			return nil
		}
		streamHunks := make([]StreamDiffHunk, len(hunks))
		totalLines := 0
		for i, h := range hunks {
			streamHunks[i] = StreamDiffHunk{
				OldStart: h.OldStart,
				OldEnd:   h.OldEnd,
				OldText:  h.OldText,
				NewText:  h.NewText,
			}
			totalLines += strings.Count(h.NewText, "\n") + 1
		}
		streamMode := "full"
		if totalLines > 100 {
			streamMode = "speculative"
		}
		return &StreamEditPreview{
			TxID:       txID,
			Path:       path,
			Paths:      []string{path},
			Status:     "pending",
			Hunks:      streamHunks,
			StreamMode: streamMode,
			TotalLines: totalLines,
		}
	}

	if v.Tool == "write" {
		path, hunks, isNew, ok := editengine.ComputeHunksFromWriteParamsWithDiff(v.Params, workDir, fp)
		if !ok {
			return nil
		}
		streamHunks := make([]StreamDiffHunk, len(hunks))
		totalLines := 0
		for i, h := range hunks {
			streamHunks[i] = StreamDiffHunk{
				OldStart: h.OldStart,
				OldEnd:   h.OldEnd,
				OldText:  h.OldText,
				NewText:  h.NewText,
			}
			totalLines += strings.Count(h.NewText, "\n") + 1
		}
		streamMode := "full"
		if totalLines > 100 {
			streamMode = "speculative"
		}
		return &StreamEditPreview{
			TxID:       txID,
			Path:       path,
			Paths:      []string{path},
			IsNew:      isNew,
			Status:     "pending",
			Hunks:      streamHunks,
			StreamMode: streamMode,
			TotalLines: totalLines,
		}
	}

	if v.Tool == "apply_patch" {
		paths, ok := editengine.ComputePathsFromPatchParams(v.Params, workDir)
		if !ok || len(paths) == 0 {
			return nil
		}
		displayPath := paths[0]
		if len(paths) > 1 {
			displayPath = fmt.Sprintf("%s (+%d files)", paths[0], len(paths)-1)
		}
		return &StreamEditPreview{
			TxID: txID,
			Path: displayPath,
			// 真实路径必须和展示串一起走。此前只发 displayPath，于是
			// editTxPaths 存的是一个不存在的路径，多文件 patch 的 per-file
			// reject 静默失效（EE-10）。
			Paths:      paths,
			Status:     "pending",
			StreamMode: "full",
		}
	}

	return nil
}

func buildEditAppliedFromResult(ev wesengine.Event, pendingEdits map[string]string) *StreamEditPreview {
	v, ok := ev.Data.(wesengine.ToolResultData)
	if !ok {
		return nil
	}
	txID, hasPending := pendingEdits[v.ID]
	if !hasPending {
		return nil
	}
	delete(pendingEdits, v.ID)

	status := "applied"
	if v.IsError {
		status = "failed"
	}

	var path string
	if len(v.OutputFiles) > 0 {
		path = v.OutputFiles[0].Path
	}

	return &StreamEditPreview{
		TxID:   txID,
		Path:   path,
		Status: status,
	}
}

func parseHITLResolution(data any) (wesengine.HITLResolutionData, bool) {
	switch v := data.(type) {
	case wesengine.HITLResolutionData:
		return v, true
	case *wesengine.HITLResolutionData:
		if v == nil {
			return wesengine.HITLResolutionData{}, false
		}
		return *v, true
	default:
		if data == nil {
			return wesengine.HITLResolutionData{}, false
		}
		b, err := json.Marshal(data)
		if err != nil {
			return wesengine.HITLResolutionData{}, false
		}
		var out wesengine.HITLResolutionData
		if json.Unmarshal(b, &out) != nil || out.RequestID == "" {
			return wesengine.HITLResolutionData{}, false
		}
		return out, true
	}
}

func streamHITLFromRequest(v wesengine.HITLRequestData) *StreamHITL {
	return &StreamHITL{
		RequestID:    v.RequestID,
		Kind:         v.Kind,
		HITLKind:     v.HITLKind,
		ToolName:     v.ToolName,
		Reason:       v.Reason,
		Prompt:       v.Prompt,
		CommandText:  v.CommandText,
		Choices:      v.Choices,
		ExpiresAt:    v.ExpiresAt,
		DecisionKind: v.DecisionKind,
		Sensitive:    v.Sensitive,
		Metadata:     v.Metadata,
	}
}

// persistGroupAgentMessage saves a single group member's reply to the
// wesapp/group message store so group chat history survives page refresh.
//
// This mirrors wesclaw's conv_repo.go:onGroupAgentComplete but is called from
// the SSE consumer loop (on agent.done) instead of from ConversationRepository,
// avoiding the full-replacement ConvRepo interface that would take over single-
// agent persistence too.
//
// Errors are logged, not propagated — the stream is already delivered and the
// user sees the reply; losing the history row is a minor degradation, not a
// data-loss crash.
func (h *Handler) persistGroupAgentMessage(ctx context.Context, groupID, agentID, agentName, text string) {
	svc := h.engine.GroupService()
	if svc == nil {
		L(ctx).Warn("[chat] group persist: GroupService nil, skipping",
			"group_id", groupID, "agent_id", agentID)
		return
	}
	cellID := h.engine.CellID()
	if _, err := svc.SaveMessage(ctx, cellID, "local", groupID, agentName, text, nil); err != nil {
		L(ctx).Warn("[chat] group persist: SaveMessage failed",
			"group_id", groupID, "agent_id", agentID, "error", err)
	}
}
