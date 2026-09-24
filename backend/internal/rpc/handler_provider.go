package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/weisyn/wesapp/provider"
	appengine "github.com/weisyn/wescode/internal/engine"
	"github.com/weisyn/wescode/internal/failure"
)

// handleProviderCatalog returns the LLM provider catalog (presets + models)
// for the frontend quick-select UI. Data is weisyn.ProviderCatalog() set at boot.
func (h *Handler) handleProviderCatalog(_ context.Context, _ Request) (any, *RPCError) {
	return provider.BuildCatalogResponse(), nil
}

// handleAvailableModels returns {models, orgCatalogError}. The org-catalog
// failure is a structured field, never a sentinel model entry — the model
// list stays clean for any consumer (INV-PROVIDER-VIEW-01).
// Login state is deliberately absent here. This handler used to rewrite every
// platform row to status="billing_blocked" plus the prose "请先登录以使用此模型"
// when no session existed — two fabrications at once: the status conflated "not
// signed in" with "wallet overdue", and no consumer read either field on a
// platform row (the picker maps platform rows through rpcToWesProvider, which
// drops status; GateGuard filters to byok; orgFromAvailable filters to org).
// Whether WES is usable is decided from authMe + the billing snapshot in the
// composer (INV-CHAT-02); the model list answers "what exists", not "may I".
func (h *Handler) handleAvailableModels(ctx context.Context, _ Request) (any, *RPCError) {
	res := h.engine.ListAvailableModelsResult(ctx)
	payload := map[string]any{"models": res.Models}
	if res.OrgCatalogError != "" {
		payload["orgCatalogError"] = res.OrgCatalogError
	}
	// Site base URL for org management / enterprise links — the frontend must
	// never hardcode a deployment hostname (B2: env-aware links).
	if base := h.engine.SiteBaseURL(); base != "" {
		payload["siteBaseUrl"] = base
	}
	return payload, nil
}

// handleRefreshModels clears the upstream WES/org model directory cache and
// returns a freshly fetched merged list. The settings-page "refresh" button
// calls this to bypass the 60s cache TTL.
func (h *Handler) handleRefreshModels(ctx context.Context, _ Request) (any, *RPCError) {
	res := h.engine.RefreshAvailableModels(ctx)
	payload := map[string]any{"models": res.Models}
	if res.OrgCatalogError != "" {
		payload["orgCatalogError"] = res.OrgCatalogError
	}
	if base := h.engine.SiteBaseURL(); base != "" {
		payload["siteBaseUrl"] = base
	}
	return payload, nil
}

func (h *Handler) handleListProviders(ctx context.Context, _ Request) (any, *RPCError) {
	items := h.engine.ListProvidersWithHealth(ctx)
	result := make([]ProviderItem, 0, len(items))
	for _, p := range items {
		result = append(result, ProviderItem{
			Name:          p.Name,
			Type:          p.Type,
			BaseURL:       p.BaseURL,
			Model:         p.Model,
			HasAPIKey:     p.APIKey != "",
			APIKeyPreview: p.APIKey,
			IsDefault:     p.IsDefault,
			NoStreamUsage: p.NoStreamUsage,
			Status:        p.Status,
			ProbeClass:    p.ProbeClass,
		})
	}
	return result, nil
}

func (h *Handler) handleListWesProviders(ctx context.Context, _ Request) (any, *RPCError) {
	providers, err := h.engine.ListWesProviders(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	if providers == nil {
		providers = []appengine.WesProviderInfo{}
	}
	return providers, nil
}

func (h *Handler) handleGetWesBilling(ctx context.Context, _ Request) (any, *RPCError) {
	full := h.engine.CheckWesBillingFull(ctx)
	// Echo both camelCase (legacy AccountTab compat) and snake_case
	// (canonical wesui/wesclaw shape) so consumers can pick.
	if balance, ok := full["balance_mills"]; ok {
		full["balanceMills"] = balance
	}
	return full, nil
}

func (h *Handler) handleAddProvider(ctx context.Context, req Request) (any, *RPCError) {
	var params AddProviderParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" {
		return nil, &RPCError{Code: -32602, Message: "name is required"}
	}
	if params.BaseURL == "" {
		return nil, &RPCError{Code: -32602, Message: "baseUrl is required"}
	}
	if params.Model == "" {
		return nil, &RPCError{Code: -32602, Message: "model is required"}
	}
	if params.APIKey == "" {
		return nil, &RPCError{Code: -32602, Message: "apiKey is required"}
	}
	item, err := h.engine.AddProvider(ctx, appengine.ProviderConfig{
		Name:          params.Name,
		Type:          params.Type,
		BaseURL:       params.BaseURL,
		APIKey:        params.APIKey,
		Model:         params.Model,
		IsDefault:     params.IsDefault,
		NoStreamUsage: params.NoStreamUsage,
		ContextWindow: params.ContextWindow,
		MaxOutput:     params.MaxOutput,
	})
	if err != nil {
		L(ctx).Warn("[provider] provider.add.failed", "name", params.Name, "type", params.Type, "model", params.Model, "error", err)
		if msg, ok := strings.CutPrefix(err.Error(), "provider_conflict:"); ok {
			parts := strings.SplitN(msg, ":", 2)
			existing := parts[0]
			input := existing
			if len(parts) > 1 {
				input = parts[1]
			}
			hint := fmt.Sprintf("已存在同名模型配置「%s」（名称不区分大小写），请更换名称或编辑已有配置", existing)
			if existing == input {
				hint = fmt.Sprintf("已存在名为「%s」的模型配置，请更换名称或编辑已有配置", existing)
			}
			return nil, &RPCError{Code: -32001, Message: hint}
		}
		return nil, internalError(err)
	}
	L(ctx).Info("[provider] provider.add", "name", item.Name, "type", item.Type, "model", item.Model, "is_default", item.IsDefault)
	return ProviderItem{
		Name:          item.Name,
		Type:          item.Type,
		BaseURL:       item.BaseURL,
		Model:         item.Model,
		HasAPIKey:     item.APIKey != "",
		APIKeyPreview: item.APIKey,
		IsDefault:     item.IsDefault,
		NoStreamUsage: item.NoStreamUsage,
	}, nil
}

func (h *Handler) handleUpdateProvider(ctx context.Context, req Request) (any, *RPCError) {
	var params UpdateProviderParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" {
		return nil, &RPCError{Code: -32602, Message: "name is required"}
	}
	if params.BaseURL == "" {
		return nil, &RPCError{Code: -32602, Message: "baseUrl is required"}
	}
	if params.Model == "" {
		return nil, &RPCError{Code: -32602, Message: "model is required"}
	}
	p := appengine.ProviderConfig{
		Name:          params.Name,
		Type:          params.Type,
		BaseURL:       params.BaseURL,
		APIKey:        params.APIKey,
		Model:         params.Model,
		IsDefault:     params.IsDefault,
		NoStreamUsage: params.NoStreamUsage,
		ContextWindow: params.ContextWindow,
		MaxOutput:     params.MaxOutput,
	}
	if err := h.engine.UpdateProvider(ctx, params.Name, p); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[provider] provider.update", "name", params.Name, "type", params.Type, "model", params.Model)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleDeleteProvider(ctx context.Context, req Request) (any, *RPCError) {
	var params DeleteProviderParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" {
		return nil, &RPCError{Code: -32602, Message: "name is required"}
	}

	// Reject deletion when active cron jobs pin this provider. A scheduled
	// run has nobody at the keyboard — silently removing its provider turns
	// every future execution into ErrNoProvider, and the user sees "pinned
	// provider 'X'; no LLM provider configured" with no clue why.
	if entries, _, err := h.engine.ListCronJobs(ctx); err == nil {
		var deps []string
		for _, e := range entries {
			if strings.EqualFold(e.ProviderName, params.Name) {
				deps = append(deps, e.Name)
			}
		}
		if len(deps) > 0 {
			// 任务名走 detail（诊断面），不进句子：句子由渲染器按读者语言写，
			// 而后端不知道那个语言。用户要去的定时任务页会列出全部任务。
			return nil, errFailureDetail(failure.ReasonProviderInUseByCron,
				strings.Join(deps, ", "))
		}
	}

	if err := h.engine.DeleteProvider(ctx, params.Name); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[provider] provider.delete", "name", params.Name)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleSetDefaultProvider(ctx context.Context, req Request) (any, *RPCError) {
	var params SetDefaultProviderParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" {
		return nil, &RPCError{Code: -32602, Message: "name is required"}
	}
	if err := h.engine.SetDefaultProvider(ctx, params.Name); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[provider] provider.set_default", "name", params.Name)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleTestProvider(ctx context.Context, req Request) (any, *RPCError) {
	var params TestProviderParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	p := appengine.ProviderConfig{
		Name:          params.Name,
		Type:          params.Type,
		BaseURL:       params.BaseURL,
		APIKey:        params.APIKey,
		Model:         params.Model,
		NoStreamUsage: params.NoStreamUsage,
	}
	return buildTestResult(h.engine.TestProvider(ctx, p), params.Model), nil
}

func (h *Handler) handleTestProviderByName(ctx context.Context, req Request) (any, *RPCError) {
	var params TestProviderByNameParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" {
		return nil, &RPCError{Code: -32602, Message: "name is required"}
	}
	model := h.engine.ModelForProvider(params.Name)
	outcome, err := h.engine.TestProviderByName(ctx, params.Name)
	if err != nil {
		// Nothing was probed, so there is no verdict. This used to be
		// returned as a successful RPC result whose "class" the settings
		// page had substring-matched out of the message.
		if errors.Is(err, provider.ErrProviderNotFound) {
			return nil, &RPCError{Code: -32602, Message: "provider not found: " + params.Name}
		}
		return nil, internalError(err)
	}
	return buildTestResult(outcome, model), nil
}

func (h *Handler) handleTestWesProvider(ctx context.Context, req Request) (any, *RPCError) {
	var params TestProviderByNameParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" {
		return nil, &RPCError{Code: -32602, Message: "name is required"}
	}
	model := h.engine.ModelForWesProvider(ctx, params.Name)
	outcome, err := h.engine.TestWesProvider(ctx, params.Name)
	if err != nil {
		if errors.Is(err, provider.ErrProviderNotFound) || errors.Is(err, appengine.ErrWesUnavailable) {
			return nil, &RPCError{Code: -32602, Message: err.Error()}
		}
		return nil, internalError(err)
	}
	return buildTestResult(outcome, model), nil
}

// buildTestResult forwards the probe's own classification. It used to call a
// classifyProviderError helper that re-derived the class by substring-matching
// the message — which mapped every "403" to auth_failed (billing 403s
// included), every unrecognized upstream message to unreachable, and invented
// four classes (no_workspace / billing_overdue / wes_token_expired /
// provider_not_found) that the engine's ProbeClass domain does not contain, so
// the settings page fell through to default copy for all of them.
func buildTestResult(o appengine.ProbeOutcome, model string) TestProviderResult {
	r := TestProviderResult{
		OK:         o.OK,
		LatencyMs:  o.LatencyMs,
		Message:    o.Message,
		ProbeClass: o.Class,
	}
	// Context window / max output describe a model the endpoint accepted, so
	// they only travel with a passing probe. The old guard was
	// `ok || ErrorKind == ""`, whose second arm was dead: the classifier's
	// default arm meant a failure always carried a kind.
	if o.OK {
		if spec, found := appengine.LookupModelSpec(model); found {
			r.ContextWindow = spec.ContextWindow
			r.MaxOutput = spec.MaxOutput
		}
	}
	return r
}
