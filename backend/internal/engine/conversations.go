package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	wesgine "github.com/weisyn/wesgine"
	wesengine "github.com/weisyn/wesgine/engine"

	"github.com/weisyn/wescode/internal/store"
)

// CrossCellConversation is a session that may belong to any workspace Cell,
// not just the currently active one. This is the type returned by
// ListConversations so the UI can show a unified history across workspaces.
type CrossCellConversation struct {
	wesgine.SessionInfo
	CellID             string `json:"cellId"`
	WorkspaceLabel     string `json:"workspaceLabel"`
	IsCurrentWorkspace bool   `json:"isCurrentWorkspace"`
}

// isGroupMemberSession reports whether a session id is one of the engine's
// per-member sub-sessions from a group run.
//
// The group coordinator gives each member its own run namespaced as
// `groupID + "/" + agentID` (wesgine internal/agent/group.go) so members keep
// independent multi-turn history. Those rows are real sessions in the store
// but they are engine bookkeeping, not conversations the user started: left
// unfiltered, one group turn adds a picker entry per member. wescode's own
// ids are `ses-<hex>` (generateSessionID), so the separator cannot collide.
func isGroupMemberSession(sessionID string) bool {
	return strings.Contains(sessionID, "/")
}

// ListConversations returns recent sessions from ALL workspace Cells,
// ordered by updated_at DESC. The user's conversation history follows the
// user, not the workspace — switching projects must not hide past sessions.
func (s *Service) ListConversations(ctx context.Context) ([]CrossCellConversation, error) {
	s.mu.Lock()
	hyp := s.hyp
	initialized := s.initialized
	currentCellID := s.cellID
	dataDir := s.dataDir
	s.mu.Unlock()
	if !initialized || hyp == nil {
		return nil, fmt.Errorf("engine: not initialized")
	}

	cells, err := hyp.Cells().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("engine: list cells: %w", err)
	}

	var all []CrossCellConversation
	for _, summary := range cells {
		cell, err := hyp.Cells().Get(summary.CellID)
		if err != nil {
			continue
		}
		sessions, err := cell.Sessions().List(ctx, "local", "", 50)
		if err != nil {
			continue
		}
		label := resolveWorkspaceLabel(dataDir, summary.CellID)
		isCurrent := summary.CellID == currentCellID
		for _, sess := range sessions {
			if isGroupMemberSession(sess.ID) {
				continue
			}
			all = append(all, CrossCellConversation{
				SessionInfo:        sess,
				CellID:             summary.CellID,
				WorkspaceLabel:     label,
				IsCurrentWorkspace: isCurrent,
			})
		}
	}

	sort.Slice(all, func(i, j int) bool {
		ti, tj := all[i].UpdatedAt, all[j].UpdatedAt
		if ti.IsZero() {
			ti = all[i].CreatedAt
		}
		if tj.IsZero() {
			tj = all[j].CreatedAt
		}
		return ti.After(tj)
	})
	// all 是元素级截断（结构体切片），本就 rune 安全
	if len(all) > 200 {
		all = all[:200]
	}
	return all, nil
}

// ListUIMessages reads messages for a session.
// When cellID is empty or matches the current Cell, the current Cell is
// tried first; if the session is not found there, ALL Cells are searched so
// cross-workspace history loads transparently (the user's conversation
// history follows the user, not the workspace — INV-CROSSCELL-01). When an
// explicit non-current cellID is given, only that Cell is queried.
func (s *Service) ListUIMessages(ctx context.Context, sessionID, cellID string, limit int) ([]store.UIMessage, error) {
	s.mu.Lock()
	hyp := s.hyp
	currentCell := s.cell
	currentCellID := s.cellID
	initialized := s.initialized
	s.mu.Unlock()
	if !initialized {
		return nil, fmt.Errorf("engine: not initialized")
	}
	if sessionID == "" {
		return nil, nil
	}

	var targetCell *wesgine.Cell
	if cellID == "" || cellID == currentCellID {
		targetCell = currentCell
	} else if hyp != nil {
		c, err := hyp.Cells().Get(cellID)
		if err != nil {
			return nil, fmt.Errorf("engine: cell %s not found: %w", cellID, err)
		}
		targetCell = c
	}
	if targetCell == nil {
		return nil, fmt.Errorf("engine: no cell available")
	}

	raw, err := targetCell.Sessions().Messages(ctx, "local", sessionID, "", limit)
	if err == nil && len(raw) > 0 {
		return store.FoldToUI(raw), nil
	}
	// Session not in the (current) Cell — cross-Cell fallback when the
	// caller did not pin an explicit Cell (INV-CROSSCELL-01).
	if cellID != "" && cellID != currentCellID {
		if err != nil {
			return nil, fmt.Errorf("engine: list messages: %w", err)
		}
		return nil, nil
	}
	if hyp == nil {
		if err != nil {
			return nil, fmt.Errorf("engine: list messages: %w", err)
		}
		return nil, nil
	}
	cells, lerr := hyp.Cells().List(ctx)
	if lerr != nil {
		return nil, fmt.Errorf("engine: list cells: %w", lerr)
	}
	for _, summary := range cells {
		if summary.CellID == currentCellID || summary.CellID == cellID {
			continue // already tried
		}
		c, gerr := hyp.Cells().Get(summary.CellID)
		if gerr != nil {
			continue
		}
		m, merr := c.Sessions().Messages(ctx, "local", sessionID, "", limit)
		if merr == nil && len(m) > 0 {
			return store.FoldToUI(m), nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("engine: list messages: %w", err)
	}
	return nil, nil
}

// GetSessionPlan returns the authoritative Plan state for a session.
// Primary source is the TaskMemory ledger (wes_task_memory in meta.db) —
// the sole cross-run recovery source. The .plans/*.md artifact is
// human-readable and can lag behind the ledger (e.g. Run termination
// paths that never re-write the file), so it must not drive UI state.
// The artifact file is only a fallback for ledger-less sessions
// (cross-cell history or un-wired TaskMemory).
func (s *Service) GetSessionPlan(ctx context.Context, sessionID string) (*wesengine.Plan, error) {
	s.mu.Lock()
	dataDir := s.dataDir
	cellID := s.cellID
	cell := s.cell
	s.mu.Unlock()
	if sessionID == "" || dataDir == "" || cellID == "" {
		return nil, nil
	}
	if cell != nil {
		if p, err := cell.TaskMemory().Plan(ctx, sessionID); err == nil && p != nil {
			return p, nil
		}
	}
	cellDataDir := filepath.Join(dataDir, "cells", cellID)
	return wesengine.ReadPlanFromDir(cellDataDir, "default", sessionID), nil
}

// resolveWorkspaceLabel reads the .workspace marker file written during
// Cell initialization and returns the folder base name (e.g. "wesgine.git").
func resolveWorkspaceLabel(dataDir, cellID string) string {
	data, err := os.ReadFile(filepath.Join(dataDir, "cells", cellID, ".workspace"))
	if err != nil || len(data) == 0 {
		return cellID
	}
	ws := strings.TrimSpace(string(data))
	return filepath.Base(ws)
}
