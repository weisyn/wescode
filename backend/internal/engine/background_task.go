package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/weisyn/wescode/internal/notify"
)

// BackgroundTask represents an Agent run executing in a git worktree.
type BackgroundTask struct {
	RunID     string
	SessionID string
	AgentID   string
	WorkDir   string // worktree path
	Status    string // "running" | "completed" | "failed"
	Error     string // error message when Status == "failed"
	StartedAt time.Time
	cancel    context.CancelFunc
}

// RunChatBackground starts a chat in a separate git worktree (goroutine).
func (s *Service) RunChatBackground(ctx context.Context, sessionID, agentID, message string, opts ChatOpts) (string, error) {
	s.mu.Lock()
	workspace := s.workspace
	initialized := s.initialized
	s.mu.Unlock()
	if !initialized {
		return "", errors.New("engine: not initialized")
	}
	if workspace == "" {
		return "", errors.New("engine: workspace not set")
	}

	runID := generateRunID()
	branchName := "wescode-bg-" + runID
	worktreePath := filepath.Join(workspace, "."+branchName)

	// P0-6 fix: create a named branch (not detached HEAD) so merge works correctly.
	if _, err := execGit(ctx, workspace, "worktree", "add", "-b", branchName, worktreePath); err != nil {
		return "", fmt.Errorf("engine: create worktree: %w", err)
	}

	bgCtx, bgCancel := context.WithCancel(context.Background())

	task := &BackgroundTask{
		RunID:     runID,
		SessionID: sessionID,
		AgentID:   agentID,
		WorkDir:   worktreePath,
		Status:    "running",
		StartedAt: time.Now(),
		cancel:    bgCancel,
	}

	s.bgTasksMu.Lock()
	if s.bgTasks == nil {
		s.bgTasks = make(map[string]*BackgroundTask)
	}
	s.bgTasks[runID] = task
	s.bgTasksMu.Unlock()

	go func() {
		defer bgCancel()
		bgOpts := opts
		bgOpts.WorkDir = worktreePath
		bgOpts.isBackground = true // P0-5: bypass foreground lock

		eventCh, sid, err := s.RunChat(bgCtx, sessionID, agentID, message, bgOpts)
		if err != nil {
			s.bgTasksMu.Lock()
			task.Status = "failed"
			task.Error = err.Error()
			s.bgTasksMu.Unlock()
			slog.Warn("[background] RunChat failed", "runId", runID, "error", err)
			return
		}
		if sid != "" {
			s.bgTasksMu.Lock()
			task.SessionID = sid
			s.bgTasksMu.Unlock()
		}

		for range eventCh {
		}

		s.bgTasksMu.Lock()
		task.Status = "completed"
		s.bgTasksMu.Unlock()

		if s.pendingNotifier != nil {
			if err := s.pendingNotifier(notify.BackgroundComplete, map[string]string{
				"runId":  runID,
				"status": "completed",
			}); err != nil {
				slog.Warn("[background] notify BackgroundComplete failed", "runId", runID, "error", err)
			}
		}

		slog.Info("[background] task completed", "runId", runID)
	}()

	return runID, nil
}

// ListBackgroundTasks returns all tracked background tasks.
func (s *Service) ListBackgroundTasks() []BackgroundTask {
	s.bgTasksMu.Lock()
	defer s.bgTasksMu.Unlock()
	result := make([]BackgroundTask, 0, len(s.bgTasks))
	for _, t := range s.bgTasks {
		result = append(result, BackgroundTask{
			RunID:     t.RunID,
			SessionID: t.SessionID,
			AgentID:   t.AgentID,
			WorkDir:   t.WorkDir,
			Status:    t.Status,
			Error:     t.Error,
			StartedAt: t.StartedAt,
		})
	}
	return result
}

// CancelBackgroundTask cancels a running background task.
func (s *Service) CancelBackgroundTask(runID string) error {
	s.bgTasksMu.Lock()
	task, ok := s.bgTasks[runID]
	s.bgTasksMu.Unlock()
	if !ok {
		return fmt.Errorf("engine: background task %q not found", runID)
	}
	if task.Status != "running" {
		return fmt.Errorf("engine: background task %q not running (status: %s)", runID, task.Status)
	}
	task.cancel()
	return nil
}

// MergeBackgroundTask commits worktree changes and merges the branch back.
// P0-6 fix: properly commit in the worktree, then merge by branch name.
func (s *Service) MergeBackgroundTask(ctx context.Context, runID string) error {
	s.bgTasksMu.Lock()
	task, ok := s.bgTasks[runID]
	s.bgTasksMu.Unlock()
	if !ok {
		return fmt.Errorf("engine: background task %q not found", runID)
	}
	if task.Status == "running" {
		return fmt.Errorf("engine: background task %q still running", runID)
	}

	s.mu.Lock()
	workspace := s.workspace
	s.mu.Unlock()

	// Get the branch name from the worktree.
	branch, err := execGit(ctx, task.WorkDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fmt.Errorf("engine: get worktree branch: %w", err)
	}
	branch = strings.TrimSpace(branch)

	// P0-6 fix: commit all changes in the worktree before merging.
	// Without a commit, `git merge` has nothing to merge.
	if _, err := execGit(ctx, task.WorkDir, "add", "-A"); err != nil {
		slog.Warn("[background] git add failed", "error", err, "workDir", task.WorkDir)
	}
	// Check if there are staged changes to commit.
	if diffOut, _ := execGit(ctx, task.WorkDir, "diff", "--cached", "--quiet"); diffOut != "" || true {
		_, commitErr := execGit(ctx, task.WorkDir, "commit", "-m",
			fmt.Sprintf("wescode background agent: %s", runID))
		if commitErr != nil {
			// No changes to commit is OK — might have no modifications.
			slog.Debug("[background] commit (may be empty)", "error", commitErr)
		}
	}

	// Merge the branch back to the main workspace.
	if _, err := execGit(ctx, workspace, "merge", "--no-ff", branch, "-m",
		fmt.Sprintf("Merge background agent %s", runID)); err != nil {
		_, _ = execGit(ctx, workspace, "merge", "--abort")
		return fmt.Errorf("engine: merge worktree failed (conflicts?): %w", err)
	}

	s.removeWorktree(ctx, workspace, task)

	// Clean up the temporary branch after merge.
	_, _ = execGit(ctx, workspace, "branch", "-d", branch)

	return nil
}

// DiscardBackgroundTask removes a background task's worktree without merging.
func (s *Service) DiscardBackgroundTask(ctx context.Context, runID string) error {
	s.bgTasksMu.Lock()
	task, ok := s.bgTasks[runID]
	s.bgTasksMu.Unlock()
	if !ok {
		return fmt.Errorf("engine: background task %q not found", runID)
	}
	if task.Status == "running" {
		task.cancel()
	}

	s.mu.Lock()
	workspace := s.workspace
	s.mu.Unlock()

	s.removeWorktree(ctx, workspace, task)
	return nil
}

func (s *Service) removeWorktree(ctx context.Context, workspace string, task *BackgroundTask) {
	if _, err := execGit(ctx, workspace, "worktree", "remove", "--force", task.WorkDir); err != nil {
		slog.Warn("[background] worktree remove failed, forcing", "error", err)
		_ = os.RemoveAll(task.WorkDir)
		_, _ = execGit(ctx, workspace, "worktree", "prune")
	}

	s.bgTasksMu.Lock()
	delete(s.bgTasks, task.RunID)
	s.bgTasksMu.Unlock()
}

func generateRunID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
