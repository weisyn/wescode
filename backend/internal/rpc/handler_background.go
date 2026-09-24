package rpc

import (
	"context"
	"encoding/json"
	"time"

	appengine "github.com/weisyn/wescode/internal/engine"
	"github.com/weisyn/wescode/internal/notify"
)

func (h *Handler) handleBackgroundStart(ctx context.Context, req Request) (any, *RPCError) {
	var params BackgroundStartParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Message == "" {
		return nil, &RPCError{Code: -32602, Message: "message is required"}
	}
	runID, err := h.engine.RunChatBackground(ctx, params.SessionID, params.AgentID, params.Message, appengine.ChatOpts{})
	if err != nil {
		return nil, internalError(err)
	}
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		deadline := time.After(30 * time.Minute)
		for {
			select {
			case <-ticker.C:
				tasks := h.engine.ListBackgroundTasks()
				found := false
				for _, t := range tasks {
					if t.RunID == runID {
						found = true
						if t.Status != "running" {
							if err := h.notifier.Notify(notify.BackgroundComplete, map[string]string{
								"runId":  runID,
								"status": t.Status,
							}); err != nil {
								L(ctx).Warn("[background] notify complete failed", "run_id", runID, "error", err)
							}
							return
						}
						break
					}
				}
				if !found {
					if err := h.notifier.Notify(notify.BackgroundComplete, map[string]string{
						"runId":  runID,
						"status": "disappeared",
					}); err != nil {
						L(ctx).Warn("[background] notify disappeared failed", "run_id", runID, "error", err)
					}
					return
				}
			case <-deadline:
				return
			}
		}
	}()
	return BackgroundStartResult{RunID: runID}, nil
}

func (h *Handler) handleBackgroundList(_ context.Context, _ Request) (any, *RPCError) {
	tasks := h.engine.ListBackgroundTasks()
	items := make([]BackgroundTaskItem, 0, len(tasks))
	for _, t := range tasks {
		items = append(items, BackgroundTaskItem{
			RunID:     t.RunID,
			SessionID: t.SessionID,
			AgentID:   t.AgentID,
			Status:    t.Status,
			StartedAt: t.StartedAt.Format(time.RFC3339),
		})
	}
	return items, nil
}

func (h *Handler) handleBackgroundMerge(ctx context.Context, req Request) (any, *RPCError) {
	var params BackgroundMergeParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.RunID == "" {
		return nil, &RPCError{Code: -32602, Message: "runId is required"}
	}
	if err := h.engine.MergeBackgroundTask(ctx, params.RunID); err != nil {
		return nil, internalError(err)
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleBackgroundDiscard(ctx context.Context, req Request) (any, *RPCError) {
	var params BackgroundMergeParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.RunID == "" {
		return nil, &RPCError{Code: -32602, Message: "runId is required"}
	}
	if err := h.engine.DiscardBackgroundTask(ctx, params.RunID); err != nil {
		return nil, internalError(err)
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleBackgroundCancel(_ context.Context, req Request) (any, *RPCError) {
	var params BackgroundMergeParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.RunID == "" {
		return nil, &RPCError{Code: -32602, Message: "runId is required"}
	}
	if err := h.engine.CancelBackgroundTask(params.RunID); err != nil {
		return nil, internalError(err)
	}
	return map[string]bool{"ok": true}, nil
}
