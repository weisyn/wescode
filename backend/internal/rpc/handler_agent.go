package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	appagent "github.com/weisyn/wesapp/agent"
	appengine "github.com/weisyn/wescode/internal/engine"
)

func (h *Handler) handleListAgents(ctx context.Context, _ Request) (any, *RPCError) {
	agents, err := h.engine.ListAgents(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	items := make([]AgentItem, 0, len(agents))
	for _, a := range agents {
		items = append(items, agentViewToItem(a))
	}
	return items, nil
}

func (h *Handler) handleGetAgent(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.ID == "" {
		return nil, &RPCError{Code: -32602, Message: "id is required"}
	}
	a, err := h.engine.GetAgent(ctx, params.ID)
	if err != nil {
		return nil, internalError(err)
	}
	return agentViewToItem(*a), nil
}

func agentViewToItem(a appagent.AgentView) AgentItem {
	return AgentItem{
		ID:                  a.ID,
		Name:                a.Name,
		Description:         a.Description,
		Emoji:               a.Emoji,
		Figure:              a.Figure,
		Hue:                 a.Hue,
		Role:                a.Role,
		Goal:                a.Goal,
		Intent:              a.Intent,
		Expertise:           a.Expertise,
		Tags:                a.Tags,
		Suggestions:         a.Suggestions,
		SkillBindings:       a.Skills,
		SystemPrompt:        a.SystemPrompt,
		ToolAllow:           a.ToolAllow,
		ToolDeny:            a.ToolDeny,
		MaxTurns:            a.MaxTurns,
		TimeoutSeconds:      a.TimeoutSeconds,
		ModelOverride:       a.Model,
		WorkspaceAccess:     a.WorkspaceAccess,
		WorkspaceAllowPaths: a.WorkspaceAllowPaths,
		DelegationChildDeny: a.DelegationChildDeny,
		Headless:            a.Headless,
		CreatedAt:           a.CreatedAt,
		IsBuiltin:           a.IsBuiltin,
		Pinned:              a.Pinned,
	}
}

func (h *Handler) handleCreateAgent(ctx context.Context, req Request) (any, *RPCError) {
	var params CreateAgentParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" {
		return nil, &RPCError{Code: -32602, Message: "name is required"}
	}
	view, err := h.engine.CreateAgent(ctx, appengine.CreateAgentParams{
		Name:         params.Name,
		Description:  params.Description,
		Role:         params.Role,
		Goal:         params.Goal,
		Intent:       params.Intent,
		Expertise:    params.Expertise,
		SystemPrompt: params.SystemPrompt,
		Model:        params.Model,
		Tags:         params.Tags,
		Suggestions:  params.Suggestions,
		Skills:       params.Skills,
		Figure:       params.Figure,
		Hue:          params.Hue,
		Emoji:        params.Emoji,
	})
	if err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[agent] agent.create", "id", view.ID, "name", params.Name)
	return agentViewToItem(*view), nil
}

func (h *Handler) handleUpdateAgent(ctx context.Context, req Request) (any, *RPCError) {
	var params UpdateAgentParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.ID == "" {
		return nil, &RPCError{Code: -32602, Message: "id is required"}
	}
	if err := h.engine.UpdateAgent(ctx, appengine.UpdateAgentParams{
		ID:                  params.ID,
		Name:                params.Name,
		Description:         params.Description,
		Role:                params.Role,
		Goal:                params.Goal,
		Intent:              params.Intent,
		Expertise:           params.Expertise,
		SystemPrompt:        params.SystemPrompt,
		Suggestions:         params.Suggestions,
		Tags:                params.Tags,
		SkillBindings:       params.SkillBindings,
		ToolAllow:           params.ToolAllow,
		ToolDeny:            params.ToolDeny,
		MaxTurns:            params.MaxTurns,
		TimeoutSeconds:      params.TimeoutSeconds,
		ModelOverride:       params.ModelOverride,
		WorkspaceAccess:     params.WorkspaceAccess,
		WorkspaceAllowPaths: params.WorkspaceAllowPaths,
		DelegationChildDeny: params.DelegationChildDeny,
		Headless:            params.Headless,
		Pinned:              params.Pinned,
	}); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[agent] agent.update", "id", params.ID, "name", params.Name)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleDeleteAgent(ctx context.Context, req Request) (any, *RPCError) {
	var params DeleteAgentParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.ID == "" {
		return nil, &RPCError{Code: -32602, Message: "id is required"}
	}
	if err := h.engine.DeleteAgent(ctx, params.ID); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[agent] agent.delete", "id", params.ID)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleListTools(_ context.Context, _ Request) (any, *RPCError) {
	tools := h.engine.ListTools()
	if tools == nil {
		return []ToolDescriptor{}, nil
	}
	result := make([]ToolDescriptor, 0, len(tools))
	for _, t := range tools {
		result = append(result, ToolDescriptor{Name: t.Name, Description: t.Description})
	}
	return result, nil
}

func (h *Handler) handleAgentToolBudget(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		AgentID string `json:"agentId"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	est, err := h.engine.EstimateToolBudget(ctx, params.AgentID)
	if err != nil {
		return nil, internalError(err)
	}
	return est, nil
}

func (h *Handler) handleListArtifacts(_ context.Context, req Request) (any, *RPCError) {
	var params struct {
		AgentID string `json:"agentId"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
	}
	wsDir := h.engine.AgentWorkspaceDir(params.AgentID)
	if wsDir == "" {
		return []WorkspaceFileItem{}, nil
	}
	entries, err := os.ReadDir(wsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []WorkspaceFileItem{}, nil
		}
		return nil, internalError(fmt.Errorf("listArtifacts: %w", err))
	}
	items := make([]WorkspaceFileItem, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, WorkspaceFileItem{
			Name:    e.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime().Format(time.RFC3339),
			IsDir:   e.IsDir(),
		})
	}
	return items, nil
}

func (h *Handler) handleWorkspaceTree(_ context.Context, req Request) (any, *RPCError) {
	var params struct {
		AgentID string `json:"agentId"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
	}
	wsDir := h.engine.AgentWorkspaceDir(params.AgentID)
	if wsDir == "" {
		return []WorkspaceTreeNode{}, nil
	}
	root, err := buildWorkspaceTree(wsDir, "")
	if err != nil {
		if os.IsNotExist(err) {
			return []WorkspaceTreeNode{}, nil
		}
		return nil, internalError(fmt.Errorf("workspaceTree: %w", err))
	}
	return root, nil
}

func buildWorkspaceTree(baseDir, relPath string) ([]WorkspaceTreeNode, error) {
	fullPath := baseDir
	if relPath != "" {
		fullPath = filepath.Join(baseDir, relPath)
	}
	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return nil, err
	}
	nodes := make([]WorkspaceTreeNode, 0, len(entries))
	for _, e := range entries {
		childRel := e.Name()
		if relPath != "" {
			childRel = filepath.Join(relPath, e.Name())
		}
		node := WorkspaceTreeNode{
			Name:  e.Name(),
			Path:  childRel,
			IsDir: e.IsDir(),
		}
		if info, err := e.Info(); err == nil {
			node.ModTime = info.ModTime().Format(time.RFC3339)
			if !e.IsDir() {
				sz := info.Size()
				node.Size = &sz
			}
		}
		if e.IsDir() {
			children, _ := buildWorkspaceTree(baseDir, childRel)
			node.Children = children
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func (h *Handler) handleDeleteWorkspaceFile(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		AgentID string `json:"agentId"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Path == "" {
		return nil, &RPCError{Code: -32602, Message: "path is required"}
	}
	wsDir := h.engine.AgentWorkspaceDir(params.AgentID)
	if wsDir == "" {
		return nil, &RPCError{Code: -32603, Message: "workspace not available"}
	}
	target := filepath.Join(wsDir, filepath.Clean(params.Path))
	if !strings.HasPrefix(target, wsDir) {
		return nil, &RPCError{Code: -32602, Message: "path traversal not allowed"}
	}
	if err := os.Remove(target); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[agent] workspace.file_delete", "agent_id", params.AgentID, "path", params.Path)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleAgentAudit(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		AgentID string `json:"agentId"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	entries, err := h.engine.ListAgentAudit(ctx, params.AgentID, params.Limit)
	if err != nil {
		return nil, internalError(err)
	}
	if entries == nil {
		entries = make([]appengine.AgentAuditEntry, 0)
	}
	return entries, nil
}

func (h *Handler) handleListPresetAgents(_ context.Context, _ Request) (any, *RPCError) {
	items := h.engine.ListPresetAgents()
	result := make([]PresetAgentItem, 0, len(items))
	for _, p := range items {
		tags := p.Tags
		if tags == nil {
			tags = []string{}
		}
		result = append(result, PresetAgentItem{
			ID:           p.ID,
			Name:         p.Name,
			Emoji:        p.Emoji,
			Role:         p.Role,
			Goal:         p.Goal,
			Intent:       p.Intent,
			Category:     p.Category,
			Tags:         tags,
			Suggestions:  p.Suggestions,
			SystemPrompt: p.SystemPrompt,
		})
	}
	return result, nil
}
