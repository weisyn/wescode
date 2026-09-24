package engine

import (
	"context"
	"log/slog"

	"github.com/weisyn/wesgine/engine"
)

// buildPostRunFn constructs the application-level PostRunFn that chains:
//  1. TCR tracker (120s completion judgment)
//  2. Tool token attribution logging (for Tier 2 Token Efficiency metrics)
//  3. Run summary logging for observability
//  4. Quality signal upload to weisyn platform (developer profile)
func (s *Service) buildPostRunFn() func(ctx context.Context, agentID, sessionID string, data engine.RunEndData) {
	return func(ctx context.Context, agentID, sessionID string, data engine.RunEndData) {
		// 1. TCR: record run end with modified files → start 120s timer
		if s.tcrTracker != nil && len(data.FilesModified) > 0 {
			s.tcrTracker.RecordRunEnd(data.RunID, data.FilesModified)
		}

		// 2. Tool token attribution: log per-tool result token consumption
		if len(data.ToolResultTokens) > 0 {
			for tool, tokens := range data.ToolResultTokens {
				slog.Debug("[postrun] tool_token_attribution",
					"run_id", data.RunID,
					"tool", tool,
					"result_tokens", tokens,
				)
			}
		}

		// 3. Log run summary for observability
		slog.Info("[postrun] run_complete",
			"run_id", data.RunID,
			"agent_id", agentID,
			"session_id", sessionID,
			"turns", data.TotalTurns,
			"input_tokens", data.TotalInputTokens,
			"output_tokens", data.TotalOutputTokens,
			"reason", data.Reason,
			"files_modified", len(data.FilesModified),
			"quality_revisions", data.QualityRevisions,
		)

		// 4. Quality signal upload to weisyn platform (developer profile)
		s.uploadQualitySignal(ctx, agentID, data)
	}
}
