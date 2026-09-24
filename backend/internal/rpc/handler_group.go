package rpc

import (
	"context"
	"encoding/json"
	"fmt"
)

func (h *Handler) handleListGroups(ctx context.Context, _ Request) (any, *RPCError) {
	svc := h.engine.GroupService()
	if svc == nil {
		// GroupService is initialized in postInitialize (async). If the RPC
		// arrives before postInit completes, wait briefly then retry once.
		h.engine.WaitPostInit(ctx)
		svc = h.engine.GroupService()
	}
	if svc == nil {
		return []GroupItem{}, nil
	}
	groups, err := svc.List(ctx, h.engine.CellID(), "local")
	if err != nil {
		return nil, internalError(err)
	}
	items := make([]GroupItem, 0, len(groups))
	for _, g := range groups {
		items = append(items, GroupItem{
			ID:       g.ID,
			Title:    g.Title,
			Emoji:    g.Emoji,
			AgentIDs: g.AgentIDs,
		})
	}
	return items, nil
}

func (h *Handler) handleCreateGroup(ctx context.Context, req Request) (any, *RPCError) {
	var params CreateGroupParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	svc := h.engine.GroupService()
	if svc == nil {
		return nil, internalError(errGroupNotAvailable)
	}
	g, err := svc.Create(ctx, h.engine.CellID(), "local", params.Title, params.AgentIDs)
	if err != nil {
		return nil, internalError(err)
	}
	return GroupItem{
		ID:       g.ID,
		Title:    g.Title,
		Emoji:    g.Emoji,
		AgentIDs: g.AgentIDs,
	}, nil
}

func (h *Handler) handleDeleteGroup(ctx context.Context, req Request) (any, *RPCError) {
	var params DeleteGroupParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	svc := h.engine.GroupService()
	if svc == nil {
		return nil, internalError(errGroupNotAvailable)
	}
	if err := svc.Delete(ctx, h.engine.CellID(), "local", params.ID); err != nil {
		return nil, internalError(err)
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleRenameGroup(ctx context.Context, req Request) (any, *RPCError) {
	var params RenameGroupParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	svc := h.engine.GroupService()
	if svc == nil {
		return nil, internalError(errGroupNotAvailable)
	}
	if err := svc.Rename(ctx, h.engine.CellID(), "local", params.ID, params.Title); err != nil {
		return nil, internalError(err)
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleUpdateGroupMembers(ctx context.Context, req Request) (any, *RPCError) {
	var params UpdateGroupMembersParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	svc := h.engine.GroupService()
	if svc == nil {
		return nil, internalError(errGroupNotAvailable)
	}
	if err := svc.UpdateMembers(ctx, h.engine.CellID(), "local", params.GroupID, params.AgentIDs); err != nil {
		return nil, internalError(err)
	}
	return map[string]bool{"ok": true}, nil
}

var errGroupNotAvailable = fmt.Errorf("group service not available")
