package rpc

import (
	"context"
	"encoding/json"

	wesgine "github.com/weisyn/wesgine"
)

// parseLayerParam turns a wire layer string into a layer. Empty means "no layer
// filter" on read paths, which is why it is not an error here — the write paths
// below require a layer and say so themselves.
//
// An unrecognized word is refused rather than dropped: dropping it widens the
// query to every layer, and a page asking for 本地共识 that silently receives
// 关于我 as well looks like it worked.
func parseLayerParam(raw string) (wesgine.MemoryLayer, *RPCError) {
	if raw == "" {
		return "", nil
	}
	layer, err := wesgine.ParseMemoryLayer(raw)
	if err != nil {
		return "", &RPCError{Code: -32602, Message: err.Error()}
	}
	return layer, nil
}

func (h *Handler) handleImportMemory(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Entries   []string `json:"entries"`
		Layer     string   `json:"layer"`
		Namespace string   `json:"namespace"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if len(params.Entries) == 0 {
		return nil, &RPCError{Code: -32602, Message: "entries is required"}
	}
	if params.Layer == "" {
		return nil, &RPCError{Code: -32602, Message: "layer is required"}
	}
	layer, rpcErr := parseLayerParam(params.Layer)
	if rpcErr != nil {
		return nil, rpcErr
	}
	imported, err := h.engine.ImportMemory(ctx, params.Entries, layer, params.Namespace)
	if err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[memory] import",
		"layer", params.Layer, "namespace", params.Namespace,
		"input_count", len(params.Entries), "imported", imported)
	return map[string]int{"imported": imported}, nil
}

func (h *Handler) handleListMemory(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Layer     string `json:"layer"`
		Namespace string `json:"namespace"`
		Kind      string `json:"kind"`
		Limit     int    `json:"limit"`
		Offset    int    `json:"offset"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
	}
	layer, rpcErr := parseLayerParam(params.Layer)
	if rpcErr != nil {
		return nil, rpcErr
	}
	entries, err := h.engine.ListMemory(ctx, layer, params.Namespace, params.Kind, params.Limit, params.Offset)
	if err != nil {
		return nil, internalError(err)
	}
	if entries == nil {
		return []any{}, nil
	}
	return entries, nil
}

func (h *Handler) handleSearchMemory(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Query     string `json:"query"`
		Layer     string `json:"layer"`
		Namespace string `json:"namespace"`
		Limit     int    `json:"limit"`
		Offset    int    `json:"offset"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Query == "" {
		return nil, &RPCError{Code: -32602, Message: "query is required"}
	}
	layer, rpcErr := parseLayerParam(params.Layer)
	if rpcErr != nil {
		return nil, rpcErr
	}
	entries, err := h.engine.SearchMemory(ctx, params.Query, layer, params.Namespace, params.Limit, params.Offset)
	if err != nil {
		return nil, internalError(err)
	}
	if entries == nil {
		return []any{}, nil
	}
	return entries, nil
}

func (h *Handler) handleDeleteMemory(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.ID == "" {
		return nil, &RPCError{Code: -32602, Message: "id is required"}
	}
	if err := h.engine.DeleteMemory(ctx, params.ID); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[memory] delete", "entry_id", params.ID)
	return map[string]bool{"ok": true}, nil
}

// handleMemoryCounts answers "how many rows in each layer", zeros included.
//
// It replaced four scope-keyed Stats calls the page made in parallel. Two of
// those layers share the global scope, so the page could not have separated
// them however it summed the answers.
func (h *Handler) handleMemoryCounts(ctx context.Context, _ Request) (any, *RPCError) {
	counts, err := h.engine.MemoryCounts(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	out := make(map[string]int, len(counts))
	for layer, n := range counts {
		out[string(layer)] = n
	}
	return out, nil
}

// handleClearMemoryLayer empties one layer, or one address inside it.
//
// Namespace is meaningful only where the layer does not address itself:
// agent_memory is per-agent, while about_me is the actor's own partition and
// consensus is cell-wide. The engine refuses a namespace on those two rather
// than ignoring it — ignoring turns "delete this one" into "empty the layer".
func (h *Handler) handleClearMemoryLayer(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Layer     string `json:"layer"`
		Namespace string `json:"namespace"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Layer == "" {
		return nil, &RPCError{Code: -32602, Message: "layer is required"}
	}
	layer, rpcErr := parseLayerParam(params.Layer)
	if rpcErr != nil {
		return nil, rpcErr
	}
	removed, err := h.engine.ClearMemoryLayer(ctx, layer, params.Namespace)
	if err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[memory] clear_layer",
		"layer", params.Layer, "namespace", params.Namespace, "removed", removed)
	return map[string]int{"removed": removed}, nil
}

func (h *Handler) handlePutMemory(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Content   string `json:"content"`
		Layer     string `json:"layer"`
		Namespace string `json:"namespace"`
		Kind      string `json:"kind"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Content == "" {
		return nil, &RPCError{Code: -32602, Message: "content is required"}
	}
	if params.Layer == "" {
		return nil, &RPCError{Code: -32602, Message: "layer is required"}
	}
	layer, rpcErr := parseLayerParam(params.Layer)
	if rpcErr != nil {
		return nil, rpcErr
	}
	kind := params.Kind
	if kind == "" {
		kind = "fact"
	}
	id, err := h.engine.PutMemory(ctx, wesgine.MemoryWrite{
		Layer:     layer,
		Namespace: params.Namespace,
		Content:   params.Content,
		Kind:      kind,
	})
	if err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[memory] put",
		"layer", params.Layer, "namespace", params.Namespace,
		"kind", kind, "entry_id", id)
	return map[string]string{"id": id}, nil
}

func (h *Handler) handleMemoryGC(ctx context.Context, _ Request) (any, *RPCError) {
	h.engine.MemoryGC(ctx)
	L(ctx).Info("[memory] gc_triggered")
	return map[string]string{"status": "gc triggered"}, nil
}

func (h *Handler) handleListRuns(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
	}
	runs, err := h.engine.ListRunsGlobal(ctx, params.Limit, params.Offset)
	if err != nil {
		return nil, internalError(err)
	}
	if runs == nil {
		return []any{}, nil
	}
	return h.enrichRunsWithAgentName(ctx, runs), nil
}

func (h *Handler) handleListRunsByAgent(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		AgentID string `json:"agentId"`
		Limit   int    `json:"limit"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
	}
	runs, err := h.engine.ListRunsByAgent(ctx, params.AgentID, params.Limit)
	if err != nil {
		return nil, internalError(err)
	}
	if runs == nil {
		return []any{}, nil
	}
	return h.enrichRunsWithAgentName(ctx, runs), nil
}

func (h *Handler) handleGetRunDetail(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		RunID string `json:"runId"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.RunID == "" {
		return nil, &RPCError{Code: -32602, Message: "runId is required"}
	}
	detail, err := h.engine.GetRunDetail(ctx, params.RunID)
	if err != nil {
		return nil, internalError(err)
	}
	return detail, nil
}

func (h *Handler) handleTokenAggregates(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Days int `json:"days"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
	}
	aggs, err := h.engine.AggregateTokensAllAgents(ctx, params.Days)
	if err != nil {
		return nil, internalError(err)
	}
	if aggs == nil {
		return []any{}, nil
	}
	return aggs, nil
}

func (h *Handler) handleTokenTimeSeries(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		AgentID string `json:"agentId"`
		Days    int    `json:"days"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
	}
	series, err := h.engine.AggregateTokensTimeSeries(ctx, params.AgentID, params.Days)
	if err != nil {
		return nil, internalError(err)
	}
	if series == nil {
		return []any{}, nil
	}
	return series, nil
}

func (h *Handler) handleTokenAggregateByAgent(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		AgentID string `json:"agentId"`
		Days    int    `json:"days"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
	}
	agg, err := h.engine.AggregateTokensByAgent(ctx, params.AgentID, params.Days)
	if err != nil {
		return nil, internalError(err)
	}
	return agg, nil
}

func (h *Handler) handleTokenUsageSummary(ctx context.Context, _ Request) (any, *RPCError) {
	allSum, todaySum, err := h.engine.TokenUsageSummary(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	return map[string]any{
		"totalInput":  allSum.TotalInputTokens,
		"totalOutput": allSum.TotalOutputTokens,
		"totalTokens": allSum.TotalTokens,
		"totalCalls":  allSum.CallCount,
		"todayInput":  todaySum.TotalInputTokens,
		"todayOutput": todaySum.TotalOutputTokens,
		"todayTokens": todaySum.TotalTokens,
		"todayCalls":  todaySum.CallCount,
	}, nil
}

func (h *Handler) handleTokenUsageGrouped(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		GroupBy string `json:"groupBy"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
	}
	if params.GroupBy == "" {
		params.GroupBy = "agent"
	}
	records, err := h.engine.TokenUsageGrouped(ctx, params.GroupBy, 20)
	if err != nil {
		return nil, internalError(err)
	}
	var agentNames map[string]string
	if params.GroupBy == "agent" || params.GroupBy == "" {
		agentNames = h.engine.AgentNameMap(ctx)
	}
	groups := make([]map[string]any, 0, len(records))
	for _, r := range records {
		label := r.Key
		if label == "" {
			if r.Model != "" {
				label = r.Model
			} else if r.AgentID != "" {
				label = r.AgentID
			} else {
				label = "(unknown)"
			}
		}
		if agentNames != nil && r.AgentID != "" {
			if name, ok := agentNames[r.AgentID]; ok {
				label = name
			}
		}
		groups = append(groups, map[string]any{
			"key":          r.Key,
			"label":        label,
			"inputTokens":  r.InputTokens,
			"outputTokens": r.OutputTokens,
			"totalTokens":  r.TotalTokens,
			"calls":        r.CallCount,
		})
	}
	return groups, nil
}

func (h *Handler) handleTokenUsageDetails(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
	}
	if params.Limit <= 0 {
		params.Limit = 10
	}
	records, err := h.engine.TokenUsageDetails(ctx, params.Limit, params.Offset)
	if err != nil {
		return nil, internalError(err)
	}
	hasMore := len(records) > params.Limit
	if hasMore {
		records = records[:params.Limit]
	}
	items := make([]map[string]any, 0, len(records))
	for _, r := range records {
		date := ""
		if !r.LastUpdated.IsZero() {
			date = r.LastUpdated.Format("2006-01-02")
		} else if r.Key != "" {
			date = r.Key
		}
		items = append(items, map[string]any{
			"model":        r.Model,
			"provider":     r.Provider,
			"date":         date,
			"inputTokens":  r.InputTokens,
			"outputTokens": r.OutputTokens,
			"totalTokens":  r.TotalTokens,
		})
	}
	return map[string]any{"items": items, "hasMore": hasMore}, nil
}

func (h *Handler) handleListMemoryByAgent(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		AgentID string `json:"agentId"`
		Limit   int    `json:"limit"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
	}
	entries, err := h.engine.ListMemoryByAgent(ctx, params.AgentID, params.Limit)
	if err != nil {
		return nil, internalError(err)
	}
	if entries == nil {
		return []any{}, nil
	}
	return entries, nil
}

func (h *Handler) enrichRunsWithAgentName(ctx context.Context, runs []wesgine.RunSummary) []map[string]any {
	agentNames := map[string]string{}
	if agents, err := h.engine.ListAgents(ctx); err == nil {
		for _, a := range agents {
			agentNames[a.ID] = a.Name
		}
	}
	items := make([]map[string]any, 0, len(runs))
	for _, r := range runs {
		item := map[string]any{
			"runId":        r.RunID,
			"agentId":      r.AgentID,
			"sessionId":    r.SessionID,
			"model":        r.Model,
			"status":       r.Status,
			"startedAt":    r.StartedAt,
			"endedAt":      r.EndedAt,
			"totalTurns":   r.TotalTurns,
			"stepCount":    r.StepCount,
			"elapsedMs":    r.ElapsedMS,
			"inputTokens":  r.InputTokens,
			"outputTokens": r.OutputTokens,
			"stopReason":   r.StopReason,
			"agentName":    agentNames[r.AgentID],
		}
		items = append(items, item)
	}
	return items
}
