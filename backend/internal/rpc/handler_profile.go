package rpc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

type developerProfileParams struct {
	View string `json:"view"` // "radar" | "trends" | "suggestions" | "summary"
}

// handleDeveloperProfile proxies the developer profile API from the weisyn
// platform. The frontend sends {view: "radar"|"trends"|"suggestions"} and
// receives the corresponding JSON payload.
//
// This handler runs in Config Mode (no Cell required) because profile data
// is a platform-level concept, not workspace-scoped.
func (h *Handler) handleDeveloperProfile(ctx context.Context, req Request) (any, *RPCError) {
	var params developerProfileParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			L(ctx).Warn("[profile] unmarshal params failed", "err", err)
		}
	}
	if params.View == "" {
		params.View = "radar"
	}

	baseURL := h.engine.SiteBaseURL()
	if baseURL == "" {
		return map[string]any{}, nil
	}
	if h.auth == nil {
		return nil, &RPCError{Code: -32001, Message: "auth service unavailable"}
	}
	token := h.auth.IdentityAccessToken(ctx)
	if token == "" {
		return nil, &RPCError{Code: -32001, Message: "not logged in"}
	}

	var apiPath string
	switch params.View {
	case "radar":
		apiPath = "/api/me/developer-profile/radar"
	case "trends":
		apiPath = "/api/me/developer-profile/trends"
	case "suggestions":
		apiPath = "/api/me/developer-profile/suggestions"
	case "summary":
		apiPath = "/api/me/developer-profile/summary"
	default:
		return nil, invalidParams(nil)
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+apiPath, nil)
	if err != nil {
		return nil, internalError(err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		L(ctx).Warn("[profile] weisyn API request failed", "view", params.View, "err", err)
		return nil, internalError(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, internalError(err)
	}

	if resp.StatusCode != http.StatusOK {
		L(ctx).Warn("[profile] weisyn API error", "view", params.View, "status", resp.StatusCode)
		return nil, &RPCError{Code: -32000, Message: "platform returned " + resp.Status}
	}

	var result any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, internalError(err)
	}
	return result, nil
}
