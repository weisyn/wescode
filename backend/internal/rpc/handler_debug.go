package rpc

import (
	"context"
	"encoding/json"
)

func (h *Handler) handleDebugStatus(ctx context.Context, _ Request) (any, *RPCError) {
	status := h.engine.DebugStatus(ctx)
	if data, err := json.Marshal(status); err == nil {
		L(ctx).Info("[debug/status]", "status", string(data))
	}
	return status, nil
}

func (h *Handler) handleDebugMetrics(_ context.Context, _ Request) (any, *RPCError) {
	metrics := h.engine.RetrievalMetrics()
	if metrics == nil {
		return nil, &RPCError{Code: -32603, Message: "retrieval metrics not initialized"}
	}
	return metrics, nil
}

func (h *Handler) handleReadiness(ctx context.Context, _ Request) (any, *RPCError) {
	return h.engine.ReadinessStatus(ctx), nil
}

func (h *Handler) handleVerificationMetrics(_ context.Context, _ Request) (any, *RPCError) {
	return h.engine.VerificationMetrics(), nil
}

func (h *Handler) handleConstraintList(_ context.Context, _ Request) (any, *RPCError) {
	list := h.engine.ListConstraints()
	return map[string]any{"constraints": list}, nil
}

func (h *Handler) handleConstraintConfirm(_ context.Context, req Request) (any, *RPCError) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || params.ID == "" {
		return nil, &RPCError{Code: -32602, Message: "id is required"}
	}
	ok := h.engine.ConfirmConstraint(params.ID)
	if !ok {
		return nil, &RPCError{Code: -32603, Message: "constraint not found"}
	}
	return map[string]any{"confirmed": true}, nil
}

func (h *Handler) handleConstraintDismiss(_ context.Context, req Request) (any, *RPCError) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || params.ID == "" {
		return nil, &RPCError{Code: -32602, Message: "id is required"}
	}
	ok := h.engine.DismissConstraint(params.ID)
	if !ok {
		return nil, &RPCError{Code: -32603, Message: "constraint not found"}
	}
	return map[string]any{"dismissed": true}, nil
}
