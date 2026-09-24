package rpc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
)

func (h *Handler) handleDesktopSettings(_ context.Context, _ Request) (any, *RPCError) {
	return h.engine.GetDesktopSettings(), nil
}

func (h *Handler) handleDesktopToggle(_ context.Context, req Request) (any, *RPCError) {
	var params struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	restartRequired, err := h.engine.SetDesktopEnabled(params.Enabled)
	if err != nil {
		return nil, internalError(err)
	}
	settings := h.engine.GetDesktopSettings()
	return map[string]any{
		"enabled":          settings.Enabled,
		"platform":         settings.Platform,
		"restart_required": restartRequired,
		"message":          "Desktop automation tools require engine restart to take effect",
	}, nil
}

func (h *Handler) handleBrowserSettings(_ context.Context, _ Request) (any, *RPCError) {
	return h.engine.GetBrowserSettings(), nil
}

func (h *Handler) handleBrowserStatus(ctx context.Context, _ Request) (any, *RPCError) {
	return h.engine.ProbeBrowserStatus(ctx), nil
}

func (h *Handler) handleBrowserConnect(ctx context.Context, _ Request) (any, *RPCError) {
	return h.engine.TriggerBrowserConnect(ctx), nil
}

func (h *Handler) handleBrowserSaveToken(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Token string `json:"token"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
	}
	if err := h.engine.SaveBrowserToken(ctx, params.Token); err != nil {
		return nil, internalError(err)
	}
	return h.engine.GetBrowserSettings(), nil
}

func (h *Handler) handleBrowserToggle(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if err := h.engine.ToggleBrowserEnabled(ctx, params.Enabled); err != nil {
		return nil, internalError(err)
	}
	return h.engine.GetBrowserSettings(), nil
}

func (h *Handler) handleBrowserSetMode(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Mode == "" {
		return nil, &RPCError{Code: -32602, Message: "mode is required (\"user\" or \"sandbox\")"}
	}
	if err := h.engine.SetBrowserMode(ctx, params.Mode); err != nil {
		return nil, internalError(err)
	}
	return h.engine.GetBrowserSettings(), nil
}

func (h *Handler) handleBrowserExtensionDownload(_ context.Context, _ Request) (any, *RPCError) {
	zipPath := h.engine.BrowserExtensionZipPath()
	if zipPath == "" {
		return nil, &RPCError{Code: -32603, Message: "browser extension zip not found"}
	}
	raw, err := os.ReadFile(zipPath)
	if err != nil {
		return nil, internalError(err)
	}
	return map[string]any{
		"data":     base64.StdEncoding.EncodeToString(raw),
		"fileName": filepath.Base(zipPath),
	}, nil
}
