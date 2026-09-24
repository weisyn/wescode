package engine

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/weisyn/wescode/internal/codeintel/constraints"
	"github.com/weisyn/wescode/internal/editengine"
	wesgine "github.com/weisyn/wesgine"
)

// Interrupt stops ALL running sessions in the cell.
// Prefer InterruptSession for targeted cancellation.
func (s *Service) Interrupt() {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell != nil {
		cell.Sessions().InterruptAll()
	}
}

// InterruptSession stops all runs whose sessionID matches (including agent
// sub-runs that share the same sessionID). Other sessions are unaffected.
func (s *Service) InterruptSession(sessionID string) {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return
	}
	cell.Sessions().Interrupt(sessionID)
}

// AcceptEdits confirms agent edits for the given file paths, clearing backups.
// Pass nil to accept all pending edits.
// M7 S2: detects if the user modified agent-written files before accepting,
// and reports ReportEditModified for behavioral learning.
func (s *Service) AcceptEdits(paths []string) {
	s.mu.Lock()
	ee := s.editEngine
	cell := s.cell
	s.mu.Unlock()
	if ee == nil {
		return
	}

	acceptPaths := paths
	if acceptPaths == nil {
		acceptPaths = ee.PendingBackups()
	}

	// S2: detect user modifications to agent-written files before clearing state.
	if cell != nil {
		s.detectAndReportS2(cell, acceptPaths)
	}

	ee.AcceptEdits(paths)

	// Clear tracked hashes for accepted files.
	for _, p := range acceptPaths {
		s.agentFileHashes.Delete(p)
	}
}

// RejectEdits restores agent-edited files from pre-edit backups.
// Pass nil to reject all pending edits.
// If the agent is still running, it is interrupted first.
// Returns an error if any file was modified by the user after the agent's edit (EE-12).
// M7: rejected file paths are reported to the engine's Learn handle so
// that the BehaviorObserver can capture S1 edit rejection signals.
func (s *Service) RejectEdits(paths []string) error {
	s.mu.Lock()
	ee := s.editEngine
	cell := s.cell
	s.mu.Unlock()
	if ee == nil {
		return nil
	}

	// Collect rejected file info before the restore clears tracking state.
	rejectedPaths := paths
	if rejectedPaths == nil {
		rejectedPaths = ee.PendingBackups()
	}

	// Snapshot agent's version (current file content) BEFORE restore overwrites it.
	agentContent := make(map[string]string, len(rejectedPaths))
	for _, p := range rejectedPaths {
		if data, readErr := os.ReadFile(p); readErr == nil && len(data) < 50_000 {
			agentContent[p] = string(data)
		}
	}

	// M7: report rejection signals BEFORE Interrupt, so they enter the
	// BehaviorObserver buffer before Run exit triggers BehaviorFlushFn.
	// This is synchronous to prevent the flush-before-write race.
	if cell != nil && len(rejectedPaths) > 0 {
		s.reportEditRejections(cell, rejectedPaths, agentContent, nil)
	}

	if cell != nil && cell.Sessions().IsRunning() {
		cell.Sessions().InterruptAll()
	}
	err := ee.RejectEdits(paths)

	// After restore, read the original (pre-agent) file content and update
	// the signals with OldText for richer pattern classification.
	if err == nil && cell != nil && len(rejectedPaths) > 0 {
		origContent := make(map[string]string, len(rejectedPaths))
		for _, p := range rejectedPaths {
			if data, readErr := os.ReadFile(p); readErr == nil && len(data) < 50_000 {
				origContent[p] = string(data)
			}
		}
		s.reportEditRejections(cell, rejectedPaths, agentContent, origContent)
	}
	return err
}

// reportEditRejections synchronously reports edit rejection signals to the
// engine's behavioral learning system (M7 S1 signal). When origContent is
// provided (post-restore pass), it carries the original pre-agent file content.
// Also teaches CSE constraints from the rejection pattern (Path 4).
func (s *Service) reportEditRejections(cell *wesgine.Cell, paths []string, agentContent, origContent map[string]string) {
	learn := cell.Learn()
	if learn == nil {
		return
	}
	ctx := context.Background()

	s.mu.Lock()
	reg := s.constraintReg
	s.mu.Unlock()
	roots := s.WorkspaceRoots()

	for _, p := range paths {
		diff := wesgine.DiffInfo{
			Path:      p,
			AgentText: agentContent[p],
		}
		if origContent != nil {
			diff.OldText = origContent[p]
		}
		if rerr := learn.ReportEditRejected(ctx, p, diff, ""); rerr != nil {
			slog.Debug("[wescode] learn: edit rejection report failed", "path", p, "err", rerr)
		}

		// CSE Path 4: teach constraint from rejection.
		if reg != nil {
			constraints.TeachFromEditRejection(reg, roots, constraints.EditRejection{
				FilePath:     p,
				OriginalCode: agentContent[p],
			})
		}
	}

	if reg != nil {
		s.PersistConstraints()
	}
}

// detectAndReportS2 checks if the user modified agent-written files and
// reports S2 signals to the behavioral learning system.
// Also teaches CSE constraints from modification patterns (Path 4).
func (s *Service) detectAndReportS2(cell *wesgine.Cell, paths []string) {
	learn := cell.Learn()
	if learn == nil {
		return
	}

	s.mu.Lock()
	reg := s.constraintReg
	s.mu.Unlock()

	ctx := context.Background()
	roots := s.WorkspaceRoots()
	taught := false
	for _, p := range paths {
		agentHashVal, ok := s.agentFileHashes.Load(p)
		if !ok {
			continue
		}
		agentHash, _ := agentHashVal.(string)
		currentHash := quickHash(p)
		if currentHash == "" || currentHash == agentHash {
			continue
		}
		// File was modified by user after agent wrote it — S2 signal.
		currentContent, err := os.ReadFile(p)
		if err != nil || len(currentContent) > 50_000 {
			continue
		}
		agentDiff := wesgine.DiffInfo{Path: p}
		userDiff := wesgine.DiffInfo{Path: p, UserText: string(currentContent)}
		if rerr := learn.ReportEditModified(ctx, p, agentDiff, userDiff, ""); rerr != nil {
			slog.Debug("[wescode] learn: edit modified report failed", "path", p, "err", rerr)
		}

		// CSE Path 4: teach constraint from user modification.
		if reg != nil {
			if c := constraints.TeachFromEditModification(reg, roots, constraints.EditModification{
				FilePath: p,
				AICode:   agentDiff.AgentText,
				UserCode: string(currentContent),
			}); c != nil {
				taught = true
			}
		}
	}

	if taught {
		s.PersistConstraints()
	}
}

// ActiveChangeset returns the current changeset snapshot, or nil if none.
func (s *Service) ActiveChangeset() *editengine.Changeset {
	s.mu.Lock()
	ee := s.editEngine
	s.mu.Unlock()
	if ee == nil {
		return nil
	}
	return ee.ActiveChangeset()
}

// EditMetricsSnapshot returns a point-in-time copy of edit tool statistics.
func (s *Service) EditMetricsSnapshot() editengine.EditMetricsSnapshot {
	s.mu.Lock()
	ee := s.editEngine
	s.mu.Unlock()
	if ee == nil {
		return editengine.EditMetricsSnapshot{}
	}
	return ee.Metrics.Snapshot()
}

// HITLSubmit delivers an approval decision for a pending HITL request.
func (s *Service) HITLSubmit(ctx context.Context, reqID, grant, scope, feedback string) error {
	if s.hitlSvc == nil {
		return errors.New("engine: not initialized")
	}
	return s.hitlSvc.Submit(ctx, reqID, grant, scope, feedback)
}

// HITLSubmitInput delivers a free-text answer for a KindInput HITL request.
func (s *Service) HITLSubmitInput(ctx context.Context, reqID, value string) error {
	if s.hitlSvc == nil {
		return errors.New("engine: not initialized")
	}
	return s.hitlSvc.SubmitInput(ctx, reqID, value)
}

// HITLSubmitChoice delivers a selected option for a KindChoice HITL request.
func (s *Service) HITLSubmitChoice(ctx context.Context, reqID, choice string) error {
	if s.hitlSvc == nil {
		return errors.New("engine: not initialized")
	}
	return s.hitlSvc.SubmitChoice(ctx, reqID, choice)
}

// HITLCancel cancels a pending HITL request.
func (s *Service) HITLCancel(ctx context.Context, reqID string) error {
	if s.hitlSvc == nil {
		return errors.New("engine: not initialized")
	}
	return s.hitlSvc.SubmitCancel(ctx, reqID)
}
