package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/weisyn/wesgine/engine"
)

// qualitySignalRequest mirrors the JSON shape accepted by
// POST /api/v1/internal/developer-profile/quality-signal.
// user_id is server-side overwritten from JWT — omitted here.
type qualitySignalRequest struct {
	RunID            string         `json:"run_id"`
	CellID           string         `json:"cell_id"`
	AgentID          string         `json:"agent_id"`
	Model            string         `json:"model"`
	TotalTurns       int            `json:"total_turns"`
	TotalSteps       int            `json:"total_steps"`
	ElapsedMS        int64          `json:"elapsed_ms"`
	StopReason       string         `json:"stop_reason"`
	FilesModified    int            `json:"files_modified"`
	QualityRevisions int            `json:"quality_revisions"`
	QgL1Triggered    bool           `json:"qg_l1_triggered"`
	QgL1Passed       bool           `json:"qg_l1_passed"`
	QgL2Triggered    bool           `json:"qg_l2_triggered"`
	QgL2Passed       bool           `json:"qg_l2_passed"`
	ToolCalls        map[string]int `json:"tool_calls"`
	SkillsUsed       []string       `json:"skills_used"`
	InputTokens      int            `json:"input_tokens"`
	OutputTokens     int            `json:"output_tokens"`
	CacheReads       int            `json:"cache_reads"`
}

// uploadQualitySignal sends a quality signal derived from RunEndData to the
// weisyn platform. Errors are logged and never propagated — this must not
// block or fail the PostRunFn chain.
func (s *Service) uploadQualitySignal(ctx context.Context, agentID string, data engine.RunEndData) {
	if s.weisynURL == "" || s.tokenSource == nil {
		return
	}
	if !s.cfg.DeveloperProfile.QualitySignalUpload {
		return
	}

	token := s.tokenSource.IdentityAccessToken(ctx)
	if token == "" {
		return
	}

	req := qualitySignalRequest{
		RunID:            data.RunID,
		CellID:           s.CellID(),
		AgentID:          agentID,
		Model:            data.Model,
		TotalTurns:       data.TotalTurns,
		TotalSteps:       data.TotalSteps,
		ElapsedMS:        data.ElapsedMS,
		StopReason:       string(data.Reason),
		FilesModified:    len(data.FilesModified),
		QualityRevisions: data.QualityRevisions,
		ToolCalls:        parseToolsUsed(data.ToolsUsed),
		SkillsUsed:       data.SkillsUsed,
		InputTokens:      data.TotalInputTokens,
		OutputTokens:     data.TotalOutputTokens,
		CacheReads:       data.TotalCacheReads,
	}

	body, err := json.Marshal(req)
	if err != nil {
		slog.Warn("[postrun] quality signal marshal failed", "err", err)
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	url := s.weisynURL + "/api/v1/internal/developer-profile/quality-signal"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		slog.Warn("[postrun] quality signal request build failed", "err", err)
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		slog.Warn("[postrun] quality signal upload failed", "err", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Warn("[postrun] quality signal rejected", "status", resp.StatusCode, "run_id", data.RunID)
	}
}

// parseToolsUsed converts ["exec:3", "read:2"] → map[string]int{"exec":3, "read":2}.
func parseToolsUsed(tools []string) map[string]int {
	if len(tools) == 0 {
		return nil
	}
	m := make(map[string]int, len(tools))
	for _, entry := range tools {
		name, countStr, ok := strings.Cut(entry, ":")
		if !ok {
			m[entry] = 1
			continue
		}
		n, err := strconv.Atoi(countStr)
		if err != nil {
			n = 1
		}
		m[name] = n
	}
	return m
}
