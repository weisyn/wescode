package engine

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// planStepCheckpointFn returns a callback for WithPlanStepCheckpoint that
// creates a lightweight git stash checkpoint when a plan step completes.
// This enables rollback to the state before a failed step (ORCH-GAP-02).
//
// The checkpoint uses `git stash create` (zero-footprint: no HEAD/branch
// modification, no stash ref entry). The SHA is logged for manual recovery.
func (s *Service) planStepCheckpointFn() func(ctx context.Context, stepID string, outcome tool.StepOutcome) {
	return func(ctx context.Context, stepID string, outcome tool.StepOutcome) {
		s.mu.Lock()
		ws := s.workspace
		s.mu.Unlock()
		if ws == "" {
			return
		}

		sp := &tool.LocalShellProvider{}
		shellCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		msg := fmt.Sprintf("wescode-plan-step-%s-%s", stepID, outcome)
		session, err := sp.Start(shellCtx, tool.ShellRequest{
			Command: "git stash create -m " + shellQuote(msg),
			WorkDir: ws,
		})
		if err != nil {
			slog.Debug("[plan-checkpoint] git stash create failed", "step", stepID, "err", err)
			return
		}
		outBytes, _ := io.ReadAll(session.Output())
		session.Wait()
		sha := strings.TrimSpace(string(outBytes))
		if sha == "" {
			return
		}

		slog.Info("[plan-checkpoint] created", "step", stepID, "outcome", outcome, "sha", sha[:min(len(sha), 12)])
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
