package rpc

import (
	"context"
	"encoding/json"
	"errors"
)

func (h *Handler) handleSCMInfo(ctx context.Context, _ Request) (any, *RPCError) {
	info, err := h.engine.GetGitInfo(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	return info, nil
}

func (h *Handler) handleSCMStage(ctx context.Context, req Request) (any, *RPCError) {
	var params SCMStageParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if len(params.Paths) == 0 {
		return nil, invalidParams(errors.New("paths is required"))
	}
	if err := h.engine.GitStage(ctx, params.Paths); err != nil {
		return nil, internalError(err)
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleSCMCommit(ctx context.Context, req Request) (any, *RPCError) {
	var params SCMCommitParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Message == "" {
		return nil, invalidParams(errors.New("message is required"))
	}
	if err := h.engine.GitCommit(ctx, params.Message); err != nil {
		return nil, internalError(err)
	}
	return map[string]bool{"ok": true}, nil
}
