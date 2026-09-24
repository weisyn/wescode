package rpc

import (
	"context"
	"encoding/json"

	"github.com/weisyn/wesgine/adapter/mcp"
)

func buildMCPServerConfig(p MCPServerUpsertParams) mcp.ServerConfig {
	return mcp.ServerConfig{
		Name:             p.Name,
		Command:          p.Command,
		Args:             p.Args,
		Env:              p.Env,
		URL:              p.URL,
		Transport:        p.Transport,
		Headers:          p.Headers,
		ConnectTimeoutMs: p.ConnectTimeoutMs,
		ToolTimeoutMs:    p.ToolTimeoutMs,
		Enabled:          p.Enabled,
		ToolsInclude:     p.ToolsInclude,
		ToolsExclude:     p.ToolsExclude,
		ContextVars:      p.ContextVars,
	}
}

func (h *Handler) handleListMCPServers(_ context.Context, _ Request) (any, *RPCError) {
	servers := h.engine.ListMCPServers()
	return servers, nil
}

func (h *Handler) handleGetMCPServer(_ context.Context, req Request) (any, *RPCError) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" {
		return nil, &RPCError{Code: -32602, Message: "name is required"}
	}
	snap, err := h.engine.GetMCPServer(params.Name)
	if err != nil {
		return nil, internalError(err)
	}
	return snap, nil
}

func (h *Handler) handleMCPServerStatus(_ context.Context, req Request) (any, *RPCError) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" {
		return nil, &RPCError{Code: -32602, Message: "name is required"}
	}
	status, err := h.engine.MCPServerStatus(params.Name)
	if err != nil {
		return nil, internalError(err)
	}
	return status, nil
}

func (h *Handler) handleUpsertMCPServer(ctx context.Context, req Request) (any, *RPCError) {
	var params MCPServerUpsertParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" {
		return nil, &RPCError{Code: -32602, Message: "name is required"}
	}
	cfg := buildMCPServerConfig(params)
	if err := h.engine.UpsertMCPServer(ctx, cfg); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[mcp] mcp.upsert", "name", params.Name, "transport", params.Transport)
	servers := h.engine.ListMCPServers()
	for _, s := range servers {
		if s.Config.Name == params.Name {
			return s, nil
		}
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleDeleteMCPServer(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" {
		return nil, &RPCError{Code: -32602, Message: "name is required"}
	}
	if err := h.engine.DeleteMCPServer(ctx, params.Name); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[mcp] mcp.delete", "name", params.Name)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleProbeMCPServer(ctx context.Context, req Request) (any, *RPCError) {
	var params MCPProbeParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	cfg := buildMCPServerConfig(params.MCPServerUpsertParams)
	result := h.engine.ProbeMCPServer(ctx, cfg, params.TimeoutMs)
	return result, nil
}

func (h *Handler) handleConnectMCPServer(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" {
		return nil, &RPCError{Code: -32602, Message: "name is required"}
	}
	if err := h.engine.ConnectMCPServer(ctx, params.Name); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[mcp] mcp.connect", "name", params.Name)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleDisconnectMCPServer(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" {
		return nil, &RPCError{Code: -32602, Message: "name is required"}
	}
	if err := h.engine.DisconnectMCPServer(ctx, params.Name); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[mcp] mcp.disconnect", "name", params.Name)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleEnableMCPServer(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" {
		return nil, &RPCError{Code: -32602, Message: "name is required"}
	}
	if err := h.engine.EnableMCPServer(ctx, params.Name); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[mcp] mcp.enable", "name", params.Name)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleDisableMCPServer(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" {
		return nil, &RPCError{Code: -32602, Message: "name is required"}
	}
	if err := h.engine.DisableMCPServer(ctx, params.Name); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[mcp] mcp.disable", "name", params.Name)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleMCPCallTool(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		ServerName string                 `json:"serverName"`
		ToolName   string                 `json:"toolName"`
		Args       map[string]interface{} `json:"args"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.ServerName == "" {
		return nil, &RPCError{Code: -32602, Message: "serverName is required"}
	}
	if params.ToolName == "" {
		return nil, &RPCError{Code: -32602, Message: "toolName is required"}
	}
	result, err := h.engine.MCPCallTool(ctx, params.ServerName, params.ToolName, params.Args)
	if err != nil {
		return nil, internalError(err)
	}
	return result, nil
}

func (h *Handler) handleMCPSchema(_ context.Context, _ Request) (any, *RPCError) {
	return h.engine.MCPSchema(), nil
}
