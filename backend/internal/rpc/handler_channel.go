package rpc

import (
	"context"
	"encoding/json"
	"fmt"

	appengine "github.com/weisyn/wescode/internal/engine"
	"github.com/weisyn/wescode/internal/notify"
	wesconfig "github.com/weisyn/wesgine/config"
)

func resolveChannelAccountParams(raw json.RawMessage) (platform, accountID string, err error) {
	if len(raw) == 0 {
		return "", "", fmt.Errorf("missing params")
	}
	var params struct {
		Platform       string `json:"platform"`
		AccountID      string `json:"accountId"`
		AccountIDSnake string `json:"account_id"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return "", "", err
	}
	accountID = params.AccountID
	if accountID == "" {
		accountID = params.AccountIDSnake
	}
	return params.Platform, accountID, nil
}

func jsonStringField(raw json.RawMessage, key string) (string, error) {
	var params map[string]json.RawMessage
	if err := json.Unmarshal(raw, &params); err != nil {
		return "", err
	}
	v, ok := params[key]
	if !ok {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", err
	}
	return s, nil
}

func (h *Handler) handleListChannels(ctx context.Context, _ Request) (any, *RPCError) {
	channels := h.engine.ListChannels()
	if channels == nil {
		return []any{}, nil
	}
	return channels, nil
}

func (h *Handler) handleGetChannel(ctx context.Context, req Request) (any, *RPCError) {
	platform, accountID, err := resolveChannelAccountParams(req.Params)
	if err != nil {
		return nil, invalidParams(err)
	}
	if accountID == "" {
		return nil, &RPCError{Code: -32602, Message: "accountId is required"}
	}
	snap, err := h.engine.GetChannel(platform, accountID)
	if err != nil {
		return nil, internalError(err)
	}
	return snap, nil
}

func (h *Handler) handleGetChannelConfig(ctx context.Context, req Request) (any, *RPCError) {
	platform, accountID, err := resolveChannelAccountParams(req.Params)
	if err != nil {
		return nil, invalidParams(err)
	}
	if accountID == "" {
		return nil, &RPCError{Code: -32602, Message: "accountId is required"}
	}
	view, err := h.engine.GetChannelConfig(platform, accountID)
	if err != nil {
		return nil, internalError(err)
	}
	return view, nil
}

func (h *Handler) handleUpsertChannel(ctx context.Context, req Request) (any, *RPCError) {
	var params appengine.ChannelUpsertParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.AccountID == "" {
		if alt, _ := jsonStringField(req.Params, "accountId"); alt != "" {
			params.AccountID = alt
		}
	}
	if params.AccountID == "" {
		return nil, &RPCError{Code: -32602, Message: "accountId is required"}
	}
	snap, err := h.engine.UpsertChannel(ctx, params)
	if err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[channel] channel.upsert", "platform", params.Platform, "account_id", params.AccountID)
	return snap, nil
}

func (h *Handler) handleDeleteChannel(ctx context.Context, req Request) (any, *RPCError) {
	platform, accountID, err := resolveChannelAccountParams(req.Params)
	if err != nil {
		return nil, invalidParams(err)
	}
	if accountID == "" {
		return nil, &RPCError{Code: -32602, Message: "accountId is required"}
	}
	if err := h.engine.DeleteChannel(ctx, platform, accountID); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[channel] channel.delete", "platform", platform, "account_id", accountID)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleConnectChannel(ctx context.Context, req Request) (any, *RPCError) {
	platform, accountID, err := resolveChannelAccountParams(req.Params)
	if err != nil {
		return nil, invalidParams(err)
	}
	if accountID == "" {
		return nil, &RPCError{Code: -32602, Message: "accountId is required"}
	}
	snap, err := h.engine.ConnectChannel(ctx, platform, accountID)
	if err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[channel] channel.connect", "platform", platform, "account_id", accountID)
	return snap, nil
}

func (h *Handler) handleDisconnectChannel(ctx context.Context, req Request) (any, *RPCError) {
	platform, accountID, err := resolveChannelAccountParams(req.Params)
	if err != nil {
		return nil, invalidParams(err)
	}
	if accountID == "" {
		return nil, &RPCError{Code: -32602, Message: "accountId is required"}
	}
	if err := h.engine.DisconnectChannel(ctx, platform, accountID); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[channel] channel.disconnect", "platform", platform, "account_id", accountID)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleChannelEvents(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Since uint64 `json:"since"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			L(ctx).Warn("[channel] unmarshal events params failed", "error", err)
		}
	}
	events, latestSeq := h.engine.PollChannelEvents(params.Since)
	if events == nil {
		events = []appengine.ChannelEventView{}
	}
	return map[string]any{
		"events":     events,
		"latest_seq": latestSeq,
	}, nil
}

func (h *Handler) handleChannelBindings(ctx context.Context, _ Request) (any, *RPCError) {
	return map[string]any{"bindings": h.engine.GetChannelBindings()}, nil
}

func (h *Handler) handleUpdateChannelBindings(ctx context.Context, req Request) (any, *RPCError) {
	// Accept either snake_case (new wire format, matches @wesui/connections
	// and the wesclaw/channel shared package) or camelCase (legacy, still
	// emitted by some VS Code webview state shipped before this iteration).
	var params struct {
		Bindings []struct {
			Platform          string   `json:"platform"`
			AccountID         string   `json:"account_id"`
			AccountIDCamel    string   `json:"accountId"`
			AgentScope        []string `json:"agent_scope"`
			AgentScopeCamel   []string `json:"agentScope"`
			FixedAgentID      string   `json:"fixed_agent_id"`
			FixedAgentIDCamel string   `json:"fixedAgentId"`
		} `json:"bindings"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	var bindings []wesconfig.ChannelBinding
	for _, b := range params.Bindings {
		account := b.AccountID
		if account == "" {
			account = b.AccountIDCamel
		}
		scope := b.AgentScope
		if len(scope) == 0 {
			scope = b.AgentScopeCamel
		}
		fixed := b.FixedAgentID
		if fixed == "" {
			fixed = b.FixedAgentIDCamel
		}
		bindings = append(bindings, wesconfig.ChannelBinding{
			Platform:     b.Platform,
			AccountID:    account,
			AgentScope:   scope,
			FixedAgentID: fixed,
		})
	}
	if err := h.engine.UpdateChannelBindings(ctx, bindings); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[channel] channel.update_bindings", "count", len(bindings))
	return map[string]any{"bindings": bindings}, nil
}

func (h *Handler) handleChannelPreflight(ctx context.Context, req Request) (any, *RPCError) {
	platform, accountID, err := resolveChannelAccountParams(req.Params)
	if err != nil {
		return nil, invalidParams(err)
	}
	if accountID == "" {
		return nil, &RPCError{Code: -32602, Message: "accountId is required"}
	}
	var params struct {
		Config  json.RawMessage                `json:"config"`
		Secrets map[string]wesconfig.SecretRef `json:"secrets,omitempty"`
	}
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
	}
	result := h.engine.PreflightChannel(ctx, platform, accountID, params.Config, params.Secrets)
	return result, nil
}

func (h *Handler) handleChannelSchema(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Platform string `json:"platform"`
	}
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			L(ctx).Warn("[channel] unmarshal schema params failed", "error", err)
		}
	}
	if params.Platform == "" {
		return h.engine.ListChannelSchemas(), nil
	}
	schema, err := h.engine.GetChannelSchema(params.Platform)
	if err != nil {
		return nil, &RPCError{Code: -32602, Message: err.Error()}
	}
	return schema, nil
}

func (h *Handler) handleValidateChannelBindings(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Bindings []wesconfig.ChannelBinding `json:"bindings"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	result := h.engine.ValidateChannelBindings(ctx, params.Bindings)
	return result, nil
}

func (h *Handler) handleListEmailAccounts(ctx context.Context, _ Request) (any, *RPCError) {
	accounts, err := h.engine.ListEmailAccounts(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	if accounts == nil {
		return []any{}, nil
	}
	return accounts, nil
}

func (h *Handler) handleGetEmailAccount(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		AccountID string `json:"accountId"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	detail, err := h.engine.GetEmailAccount(ctx, params.AccountID)
	if err != nil {
		return nil, internalError(err)
	}
	return detail, nil
}

func (h *Handler) handleSaveEmailAccount(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		AccountID string `json:"accountId"`
		appengine.EmailAccountWrite
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.AccountID == "" {
		return nil, &RPCError{Code: -32602, Message: "accountId is required"}
	}
	detail, err := h.engine.SaveEmailAccount(ctx, params.AccountID, params.EmailAccountWrite)
	if err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[channel] email.save", "account_id", params.AccountID)
	return detail, nil
}

func (h *Handler) handleDeleteEmailAccount(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		AccountID string `json:"accountId"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if err := h.engine.DeleteEmailAccount(ctx, params.AccountID); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[channel] email.delete", "account_id", params.AccountID)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleTestEmailAccount(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		AccountID string `json:"accountId"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	to, err := h.engine.TestEmailAccount(ctx, params.AccountID)
	if err != nil {
		return map[string]any{"status": "error", "error": err.Error()}, nil
	}
	return map[string]any{"status": "sent", "to": to}, nil
}

// handleSubscribeChannelEvents starts a push-based subscription that sends
// `channelEvent` notifications to the Extension webview. Replaces the
// polling-based sidebar/channelEvents for real-time QR code flows.
//
// After subscribing, it replays QR content for any channel currently in
// qr_pending state. This covers the race where the qr_code event fired
// before the webview subscribed (wesclaw handler_channels.go:62-69 does
// the same replay).
func (h *Handler) handleSubscribeChannelEvents(ctx context.Context, _ Request) (any, *RPCError) {
	h.chanSubMu.Lock()
	if h.chanSubCancel != nil {
		h.chanSubCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.chanSubCancel = cancel
	h.chanSubMu.Unlock()

	go h.channelSubscriptionLoop(ctx)

	// Replay QR content for channels already in qr_pending state so
	// late subscribers see the QR code without waiting for a new event.
	for _, snap := range h.engine.ListChannels() {
		if snap.Status == "qr_pending" && snap.QRContent != "" {
			_ = h.notifier.Notify(notify.ChannelEvent, map[string]any{
				"kind":       "qr_code",
				"platform":   snap.Platform,
				"account_id": snap.AccountID,
				"data":       map[string]string{"qr_content": snap.QRContent, "qr": snap.QRContent},
				"error":      "",
			})
		}
	}

	return map[string]bool{"ok": true}, nil
}

func (h *Handler) channelSubscriptionLoop(ctx context.Context) {
	ch, unsub := h.engine.SubscribeChannelEvents()
	if ch == nil {
		return
	}
	defer unsub()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			errStr := ""
			if ev.Error != nil {
				errStr = ev.Error.Error()
			}
			data := ev.Data
			if data == nil {
				data = map[string]string{}
			}
			if err := h.notifier.Notify(notify.ChannelEvent, map[string]any{
				"kind":       string(ev.Kind),
				"platform":   ev.Platform,
				"account_id": ev.AccountID,
				"data":       data,
				"error":      errStr,
			}); err != nil {
				L(ctx).Warn("[channel] notify event failed", "platform", ev.Platform, "kind", ev.Kind, "error", err)
			}
		}
	}
}
