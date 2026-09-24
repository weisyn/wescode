package rpc

import (
	"context"
	"encoding/json"

	appengine "github.com/weisyn/wescode/internal/engine"
)

func (h *Handler) handleGetRunSettings(_ context.Context, _ Request) (any, *RPCError) {
	return h.engine.GetRunSettings(), nil
}

// validThinkingLevels is the closed domain the settings panel offers. Empty
// means "let the model decide".
var validThinkingLevels = map[string]bool{"": true, "low": true, "medium": true, "high": true, "max": true}

// handleUpdateRunSettings applies a field-level patch. Params are pointers so an
// omitted key stays untouched: the panel saves the tool denylist, the thinking
// level, and the two budgets from three independent buttons, each sending only
// its own field.
func (h *Handler) handleUpdateRunSettings(_ context.Context, req Request) (any, *RPCError) {
	var params appengine.RunSettingsPatch
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.TaskBudget != nil && (*params.TaskBudget < 0 || *params.TaskBudget > 10000) {
		return nil, &RPCError{Code: -32602, Message: "taskBudget must be in [0, 10000]"}
	}
	if params.MaxTokenEstimate != nil {
		// 0 means "use the economic default". Anything between 1 and the floor is
		// rejected rather than silently raised: a budget under the fixed per-turn
		// overhead can never be met by compression (CE-16).
		if *params.MaxTokenEstimate < 0 || *params.MaxTokenEstimate > 4000000 {
			return nil, &RPCError{Code: -32602, Message: "maxTokenEstimate must be in [0, 4000000]"}
		}
		if *params.MaxTokenEstimate > 0 && *params.MaxTokenEstimate < appengine.MinEconomicWindow {
			return nil, &RPCError{Code: -32602, Message: "maxTokenEstimate must be 0 (default) or at least 64000"}
		}
	}
	if params.ThinkingLevel != nil && !validThinkingLevels[*params.ThinkingLevel] {
		return nil, &RPCError{Code: -32602, Message: "thinkingLevel must be one of: low, medium, high, max, or empty"}
	}
	if err := h.engine.UpdateRunSettings(params); err != nil {
		return nil, internalError(err)
	}
	return map[string]bool{"ok": true}, nil
}
