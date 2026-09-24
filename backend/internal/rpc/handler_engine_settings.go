package rpc

import (
	"context"
	"encoding/json"

	appengine "github.com/weisyn/wescode/internal/engine"
)

// settingsUpdateResult is the reply shape for every settings mutation.
//
// RestartRequired names the CellSpec fields that were persisted but are still
// being served from the values the Cell resolved at boot (wesgine
// SpecPatch.RestartRequired). The UI must show it: a saved setting that changes
// nothing and says nothing is indistinguishable from a dead setting, and that
// shape is what the whole dead-settings sweep was about.
type settingsUpdateResult struct {
	OK              bool     `json:"ok"`
	RestartRequired []string `json:"restartRequired,omitempty"`
}

func settingsSaved(restartRequired []string) settingsUpdateResult {
	return settingsUpdateResult{OK: true, RestartRequired: restartRequired}
}

// ---------------------------------------------------------------------------
// Trust Policy (command palette shortcut for governance mode)
// ---------------------------------------------------------------------------

func (h *Handler) handleSetTrustPolicy(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Policy string `json:"policy"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Policy == "" {
		return nil, &RPCError{Code: -32602, Message: "policy is required"}
	}
	mode := params.Policy
	update := appengine.GovernanceSettingsUpdate{GovernMode: &mode}
	if _, err := h.engine.UpdateGovernanceSettings(ctx, update); err != nil {
		return nil, internalError(err)
	}
	// Governance mode hot-applies, so there is no restart list to relay here.
	return map[string]string{"applied": mode}, nil
}

// ---------------------------------------------------------------------------
// Governance
// ---------------------------------------------------------------------------

func (h *Handler) handleGetGovernanceSettings(_ context.Context, _ Request) (any, *RPCError) {
	result, err := h.engine.GetGovernanceSettings()
	if err != nil {
		return nil, internalError(err)
	}
	return result, nil
}

func (h *Handler) handleUpdateGovernanceSettings(ctx context.Context, req Request) (any, *RPCError) {
	var params appengine.GovernanceSettingsUpdate
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	restart, err := h.engine.UpdateGovernanceSettings(ctx, params)
	if err != nil {
		return nil, internalError(err)
	}
	return settingsSaved(restart), nil
}

// ---------------------------------------------------------------------------
// Memory Policy
// ---------------------------------------------------------------------------

func (h *Handler) handleGetMemoryPolicySettings(_ context.Context, _ Request) (any, *RPCError) {
	result, err := h.engine.GetMemoryPolicySettings()
	if err != nil {
		return nil, internalError(err)
	}
	return result, nil
}

// maxMemoryEntriesCeiling bounds the two entry caps. The engine has no upper
// bound of its own; this one exists so a mistyped digit is refused at the
// boundary instead of becoming a cap the GC will never reach.
const maxMemoryEntriesCeiling = 1_000_000

// checkMaxEntries rejects values the engine cannot honour as written.
//
// Zero is rejected rather than passed through: the engine reads a zero cap as
// "use the default" (MemoryLimitsConfig.Resolve), so accepting it would let
// the page offer an unlimited option that silently resolves to 10k/5k. The
// page used to advertise exactly that ("0 = 不限").
func checkMaxEntries(field string, v *int) *RPCError {
	if v == nil {
		return nil
	}
	if *v < 1 || *v > maxMemoryEntriesCeiling {
		return &RPCError{Code: -32602, Message: field + " must be in [1, 1000000]"}
	}
	return nil
}

func (h *Handler) handleUpdateMemoryPolicySettings(ctx context.Context, req Request) (any, *RPCError) {
	var params appengine.MemoryPolicySettingsUpdate
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if e := checkMaxEntries("maxEntriesAgent", params.MaxEntriesAgent); e != nil {
		return nil, e
	}
	if e := checkMaxEntries("maxEntriesSession", params.MaxEntriesSession); e != nil {
		return nil, e
	}
	// writePolicy is deliberately not validated here: wesgine's
	// ValidateSpecPatch owns that closed domain and rejects out-of-domain
	// values at UpdateSpec. A copy of the enum on this side would be a second
	// judgement that drifts the day the engine adds a value.
	restart, err := h.engine.UpdateMemoryPolicySettings(ctx, params)
	if err != nil {
		return nil, internalError(err)
	}
	return settingsSaved(restart), nil
}

// ---------------------------------------------------------------------------
// Run Limits
// ---------------------------------------------------------------------------

func (h *Handler) handleGetRunLimitsSettings(_ context.Context, _ Request) (any, *RPCError) {
	result, err := h.engine.GetRunLimitsSettings()
	if err != nil {
		return nil, internalError(err)
	}
	return result, nil
}

func (h *Handler) handleUpdateRunLimitsSettings(ctx context.Context, req Request) (any, *RPCError) {
	var params appengine.RunLimitsSettingsUpdate
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	restart, err := h.engine.UpdateRunLimitsSettings(ctx, params)
	if err != nil {
		return nil, internalError(err)
	}
	return settingsSaved(restart), nil
}

// ---------------------------------------------------------------------------
// Cycle Detect
// ---------------------------------------------------------------------------

func (h *Handler) handleGetCycleDetectSettings(_ context.Context, _ Request) (any, *RPCError) {
	result, err := h.engine.GetCycleDetectSettings()
	if err != nil {
		return nil, internalError(err)
	}
	return result, nil
}

func (h *Handler) handleUpdateCycleDetectSettings(ctx context.Context, req Request) (any, *RPCError) {
	var params appengine.CycleDetectSettings
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	restart, err := h.engine.UpdateCycleDetectSettings(ctx, params)
	if err != nil {
		return nil, internalError(err)
	}
	return settingsSaved(restart), nil
}

// ---------------------------------------------------------------------------
// Context Budget
// ---------------------------------------------------------------------------

func (h *Handler) handleGetContextBudgetSettings(_ context.Context, _ Request) (any, *RPCError) {
	result, err := h.engine.GetContextBudgetSettings()
	if err != nil {
		return nil, internalError(err)
	}
	return result, nil
}

func (h *Handler) handleUpdateContextBudgetSettings(ctx context.Context, req Request) (any, *RPCError) {
	var params appengine.ContextBudgetSettingsUpdate
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	restart, err := h.engine.UpdateContextBudgetSettings(ctx, params)
	if err != nil {
		return nil, internalError(err)
	}
	return settingsSaved(restart), nil
}

// ---------------------------------------------------------------------------
// Resilience
// ---------------------------------------------------------------------------

func (h *Handler) handleGetResilienceSettings(_ context.Context, _ Request) (any, *RPCError) {
	result, err := h.engine.GetResilienceSettings()
	if err != nil {
		return nil, internalError(err)
	}
	return result, nil
}

func (h *Handler) handleUpdateResilienceSettings(ctx context.Context, req Request) (any, *RPCError) {
	var params appengine.ResilienceSettingsUpdate
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	restart, err := h.engine.UpdateResilienceSettings(ctx, params)
	if err != nil {
		return nil, internalError(err)
	}
	return settingsSaved(restart), nil
}

// ---------------------------------------------------------------------------
// Plugins
// ---------------------------------------------------------------------------

func (h *Handler) handleListPlugins(_ context.Context, _ Request) (any, *RPCError) {
	result, err := h.engine.ListPlugins()
	if err != nil {
		return nil, internalError(err)
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Provider Strategy
// ---------------------------------------------------------------------------

func (h *Handler) handleGetProviderStrategySettings(_ context.Context, _ Request) (any, *RPCError) {
	result, err := h.engine.GetProviderStrategySettings()
	if err != nil {
		return nil, internalError(err)
	}
	return result, nil
}

func (h *Handler) handleUpdateProviderStrategySettings(ctx context.Context, req Request) (any, *RPCError) {
	var params appengine.ProviderStrategySettings
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	restart, err := h.engine.UpdateProviderStrategySettings(ctx, params)
	if err != nil {
		return nil, internalError(err)
	}
	return settingsSaved(restart), nil
}
