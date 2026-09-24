package rpc

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/weisyn/wescode/internal/editengine"
	appengine "github.com/weisyn/wescode/internal/engine"
)

func (h *Handler) handleEditAccept(ctx context.Context, req Request) (any, *RPCError) {
	var params EditAcceptParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.TxID == "" {
		return nil, &RPCError{Code: -32602, Message: "txId is required"}
	}
	L(ctx).Debug("[edit/accept]", "txId", params.TxID)

	if params.TxID == "*" {
		h.engine.AcceptEdits(nil)
		h.editTxMu.Lock()
		h.editTxPaths = make(map[string][]string)
		h.editTxMu.Unlock()
	} else {
		h.editTxMu.Lock()
		paths := h.editTxPaths[params.TxID]
		delete(h.editTxPaths, params.TxID)
		h.editTxMu.Unlock()
		if len(paths) > 0 {
			h.engine.AcceptEdits(paths)
		}
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleEditReject(ctx context.Context, req Request) (any, *RPCError) {
	var params EditRejectParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.TxID == "" {
		return nil, &RPCError{Code: -32602, Message: "txId is required"}
	}
	L(ctx).Debug("[edit/reject]", "txId", params.TxID)

	var rejectErr error
	if params.TxID == "*" {
		rejectErr = h.engine.RejectEdits(nil)
		h.editTxMu.Lock()
		h.editTxPaths = make(map[string][]string)
		h.editTxMu.Unlock()
	} else {
		h.editTxMu.Lock()
		paths := h.editTxPaths[params.TxID]
		delete(h.editTxPaths, params.TxID)
		h.editTxMu.Unlock()
		if len(paths) > 0 {
			rejectErr = h.engine.RejectEdits(paths)
		}
	}
	if rejectErr != nil {
		if errors.Is(rejectErr, editengine.ErrFileModifiedByUser) {
			return map[string]any{"ok": false, "conflict": true, "error": rejectErr.Error()}, nil
		}
		return nil, internalError(rejectErr)
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleEditMetrics(ctx context.Context, _ Request) (any, *RPCError) {
	return h.engine.EditMetricsSnapshot(), nil
}

func (h *Handler) handleImplicitFeedback(ctx context.Context, req Request) (any, *RPCError) {
	var params ImplicitFeedbackParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Type == "" {
		return nil, &RPCError{Code: -32602, Message: "type is required"}
	}
	L(ctx).Info("[feedback/implicit]", "type", params.Type, "file", params.File, "lang", params.Lang)

	h.engine.HandleImplicitFeedback(ctx, appengine.ImplicitFeedbackEvent{
		Type:        params.Type,
		File:        params.File,
		Lang:        params.Lang,
		AIVersion:   params.AIVersion,
		UserVersion: params.UserVersion,
		Line:        params.Line,
		Timestamp:   params.Timestamp,
	})
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleEditChangeset(_ context.Context, _ Request) (any, *RPCError) {
	cs := h.engine.ActiveChangeset()
	if cs == nil {
		return map[string]any{"active": false}, nil
	}
	snap := cs.Snapshot()
	return map[string]any{
		"active":        true,
		"id":            snap.ID,
		"checkpoint_id": snap.CheckpointID,
		"status":        string(snap.Status),
		"files":         snap.Files,
		"file_count":    len(snap.Files),
	}, nil
}
