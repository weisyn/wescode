package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/weisyn/wescode/internal/notify"
	wesgine "github.com/weisyn/wesgine"
	"github.com/weisyn/wesgine/adapter/mcp"
	"github.com/weisyn/wesgine/cron"
)

// ─── Memory ───────────────────────────────────────────────────────────────────
//
// The whole memory surface is addressed by layer, not by storage scope. Those
// are different questions: "本地共识" (L2) and "关于我" (L3) both live in the
// global scope, so a scope-addressed page cannot tell a fact the whole cell
// agreed on from a preference that is yours — it shows one bucket labelled with
// whichever of the two the author had in mind. The layer names the distinction
// the user is actually looking at, so it is the only word that crosses the RPC.
//
// wescode is single-actor: every run is stamped "local", so that is the actor
// every call carries. It stays correct the day SSO makes it a real uid.

const memoryActor = "local"

// ListMemory returns memory entries in one layer, or across all layers when
// layer is empty.
func (s *Service) ListMemory(ctx context.Context, layer wesgine.MemoryLayer, namespace, kind string, limit, offset int) ([]wesgine.MemoryEntry, error) {
	if s.memorySvc == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	return s.memorySvc.List(ctx, wesgine.MemoryListOptions{
		Layer:     layer,
		Namespace: namespace,
		Kind:      kind,
		Limit:     limit,
		Offset:    offset,
		Actor:     memoryActor,
	})
}

// ImportMemory bulk-inserts plain-text entries into one layer.
//
// SaveToLayer, not Save: Save takes a scope and would happily accept "global"
// for a row the caller meant as consensus, then hand back a row that is
// indistinguishable from a personal preference. SaveToLayer makes the layer
// decide the scope, the namespace, and whether the row carries an owner at all.
func (s *Service) ImportMemory(ctx context.Context, entries []string, layer wesgine.MemoryLayer, namespace string) (int, error) {
	if s.memorySvc == nil {
		return 0, nil
	}
	imported := 0
	for _, line := range entries {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		_, err := s.memorySvc.SaveToLayer(ctx, wesgine.MemoryWrite{
			Layer:     layer,
			Namespace: namespace,
			Actor:     memoryActor,
			Content:   trimmed,
			Kind:      "fact",
		})
		if err != nil {
			return imported, fmt.Errorf("import entry %d: %w", imported, err)
		}
		imported++
	}
	return imported, nil
}

// SearchMemory performs keyword search over stored memories, optionally within
// one layer.
func (s *Service) SearchMemory(ctx context.Context, query string, layer wesgine.MemoryLayer, namespace string, limit, offset int) ([]wesgine.MemoryEntry, error) {
	if s.memorySvc == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	return s.memorySvc.Search(ctx, query, wesgine.MemorySearchOptions{
		Layer:     layer,
		Namespace: namespace,
		Limit:     limit,
		Actor:     memoryActor,
	})
}

// DeleteMemory removes a memory entry by ID.
func (s *Service) DeleteMemory(ctx context.Context, id string) error {
	if s.memorySvc == nil {
		return nil
	}
	return s.memorySvc.DeleteAs(ctx, id, memoryActor)
}

// ClearMemoryLayer empties one layer, or one address inside it. Returns the
// number of rows removed.
func (s *Service) ClearMemoryLayer(ctx context.Context, layer wesgine.MemoryLayer, namespace string) (int, error) {
	if s.memorySvc == nil {
		return 0, nil
	}
	return s.memorySvc.ClearLayer(ctx, wesgine.MemoryClearOptions{
		Layer:     layer,
		Actor:     memoryActor,
		Namespace: namespace,
	})
}

// MemoryCounts returns one number per layer, zeros included.
//
// This replaced four parallel Stats calls keyed by scope. Stats is an admin
// diagnostic addressed by storage partition, and asking it four times gave the
// page four numbers that answered a question it was not asking — consensus and
// about_me both landed in the "global" call and were reported as one figure.
func (s *Service) MemoryCounts(ctx context.Context) (map[wesgine.MemoryLayer]int, error) {
	if s.memorySvc == nil {
		return nil, nil
	}
	return s.memorySvc.CountByLayer(ctx, memoryActor, "")
}

// ─── Observe (Runs + Tokens) ──────────────────────────────────────────────────
//
// INV-OBS-08: every observe call carries the calling actor, and the engine
// filters at the SQL layer before LIMIT. wescode is single-actor ("local", the
// same identity every run is stamped with), so passing it here is equivalent to
// the whole cell today — but it stays correct the day SSO makes it a real uid.

// ListRunsGlobal returns the most recent runs across all agents.
func (s *Service) ListRunsGlobal(ctx context.Context, limit, offset int) ([]wesgine.RunSummary, error) {
	if s.observeSvc == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	return s.observeSvc.ListRunsGlobal(ctx, "local", limit, offset)
}

// ListRunsByAgent returns the most recent runs for a specific agent.
func (s *Service) ListRunsByAgent(ctx context.Context, agentID string, limit int) ([]wesgine.RunSummary, error) {
	if s.observeSvc == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 30
	}
	return s.observeSvc.ListRunsByAgent(ctx, "local", agentID, limit)
}

// AggregateTokensByAgent returns cumulative token stats for a specific agent.
func (s *Service) AggregateTokensByAgent(ctx context.Context, agentID string, days int) (*wesgine.TokenAggregate, error) {
	if s.observeSvc == nil {
		return nil, nil
	}
	if days <= 0 {
		days = 30
	}
	return s.observeSvc.AggregateTokensByAgent(ctx, "local", agentID, days)
}

// ListMemoryByAgent returns what this actor remembers about one agent (L4).
//
// The layer, not ScopeAgent: that scope also holds the agent sharing channels,
// whose rows carry no owner and belong to the cell rather than to anyone. A
// scope-addressed query returns both and the page presents them as the user's.
func (s *Service) ListMemoryByAgent(ctx context.Context, agentID string, limit int) ([]wesgine.MemoryEntry, error) {
	if s.memorySvc == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	return s.memorySvc.List(ctx, wesgine.MemoryListOptions{
		Layer:     wesgine.LayerAgentMemory,
		Namespace: agentID,
		Limit:     limit,
		Actor:     memoryActor,
	})
}

// GetRunDetail returns a run and its turn breakdown.
func (s *Service) GetRunDetail(ctx context.Context, runID string) (*wesgine.RunDetail, error) {
	if s.observeSvc == nil {
		return nil, nil
	}
	return s.observeSvc.GetRunDetail(ctx, "local", runID)
}

// AggregateTokensAllAgents returns per-agent token aggregates for the past N days.
func (s *Service) AggregateTokensAllAgents(ctx context.Context, days int) ([]wesgine.TokenAggregate, error) {
	if s.observeSvc == nil {
		return nil, nil
	}
	if days <= 0 {
		days = 30
	}
	return s.observeSvc.AggregateTokensAllAgents(ctx, "local", days)
}

// AggregateTokensTimeSeries returns per-day token usage.
// agentID="" means all agents.
func (s *Service) AggregateTokensTimeSeries(ctx context.Context, agentID string, days int) ([]wesgine.DailyTokens, error) {
	if s.observeSvc == nil {
		return nil, nil
	}
	if days <= 0 {
		days = 30
	}
	return s.observeSvc.AggregateTokensTimeSeries(ctx, "local", agentID, days)
}

// ─── Token Usage (wesclaw-compat) ─────────────────────────────────────────────

// TokenUsageSummary returns all-time + today summaries.
func (s *Service) TokenUsageSummary(ctx context.Context) (all *wesgine.TokenUsageSummary, today *wesgine.TokenUsageSummary, err error) {
	if s.observeSvc == nil {
		return &wesgine.TokenUsageSummary{}, &wesgine.TokenUsageSummary{}, nil
	}
	allSum, err := s.observeSvc.TokenUsageSummary(ctx, "local", wesgine.TokenUsageQuery{})
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	todaySum, err := s.observeSvc.TokenUsageSummary(ctx, "local", wesgine.TokenUsageQuery{From: startOfDay})
	if err != nil {
		return allSum, &wesgine.TokenUsageSummary{}, nil
	}
	return allSum, todaySum, nil
}

// TokenUsageGrouped returns token usage grouped by agent/day/model.
func (s *Service) TokenUsageGrouped(ctx context.Context, groupBy string, limit int) ([]wesgine.TokenUsageRecord, error) {
	if s.observeSvc == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	gb := groupBy
	if gb == "date" {
		gb = "day"
	}
	return s.observeSvc.QueryTokenUsage(ctx, "local", wesgine.TokenUsageQuery{
		GroupBy: gb,
		Limit:   limit,
	})
}

// TokenUsageDetails returns individual token usage records for the detail table.
func (s *Service) TokenUsageDetails(ctx context.Context, limit, offset int) ([]wesgine.TokenUsageRecord, error) {
	if s.observeSvc == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	return s.observeSvc.QueryTokenUsage(ctx, "local", wesgine.TokenUsageQuery{
		GroupBy: "session",
		Limit:   limit + 1,
		Offset:  offset,
	})
}

// ─── MCP ─────────────────────────────────────────────────────────────────────

// ListMCPServers returns snapshots of all registered MCP servers.
func (s *Service) ListMCPServers() []mcp.ServerSnapshot {
	if s.cell == nil {
		return nil
	}
	snaps := s.cell.MCPs().List()
	if snaps == nil {
		return []mcp.ServerSnapshot{}
	}
	return snaps
}

// UpsertMCPServer adds or updates an MCP server configuration and persists to config.
func (s *Service) UpsertMCPServer(ctx context.Context, cfg mcp.ServerConfig) error {
	if s.mcpMgr == nil {
		return nil
	}
	return s.mcpMgr.Add(ctx, cfg)
}

// DeleteMCPServer removes a named MCP server and persists to config.
func (s *Service) DeleteMCPServer(ctx context.Context, name string) error {
	if s.mcpMgr == nil {
		return nil
	}
	return s.mcpMgr.Remove(ctx, name)
}

// saveMCPConfig persists current MCP server list to config.yaml.
// Used by browser.go for direct MCP handle modifications that bypass the Manager.
func (s *Service) saveMCPConfig() error {
	if s.cell == nil {
		return nil
	}
	snaps := s.cell.MCPs().List()
	s.mu.Lock()
	s.cfg.MCP = make([]MCPServerConfig, 0, len(snaps))
	for _, snap := range snaps {
		s.cfg.MCP = append(s.cfg.MCP, mcpSnapshotToConfig(snap.Config))
	}
	cfg := s.cfg
	s.mu.Unlock()
	return saveConfig(cfg)
}

// ProbeMCPServer performs a transient connection test without affecting the pool.
func (s *Service) ProbeMCPServer(ctx context.Context, cfg mcp.ServerConfig, timeoutMs int) mcp.ProbeResult {
	if s.cell == nil {
		return mcp.ProbeResult{Error: "engine not initialized"}
	}
	opts := mcp.ProbeOptions{}
	if timeoutMs > 0 {
		opts.TimeoutMs = timeoutMs
	}
	return s.cell.MCPs().Probe(ctx, cfg, opts)
}

// ConnectMCPServer reconnects a named server.
func (s *Service) ConnectMCPServer(ctx context.Context, name string) error {
	if s.mcpMgr == nil {
		return nil
	}
	return s.mcpMgr.Connect(ctx, name)
}

// DisconnectMCPServer disconnects a named server.
func (s *Service) DisconnectMCPServer(ctx context.Context, name string) error {
	if s.mcpMgr == nil {
		return nil
	}
	return s.mcpMgr.Disconnect(ctx, name)
}

// GetMCPServer returns the snapshot of a single named MCP server.
func (s *Service) GetMCPServer(name string) (*mcp.ServerSnapshot, error) {
	if s.cell == nil {
		return nil, fmt.Errorf("engine not initialized")
	}
	snap, err := s.cell.MCPs().Get(name)
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// MCPServerStatus returns a lightweight status view of a named MCP server.
type MCPServerStatusView struct {
	Connected bool             `json:"connected"`
	ToolCount int              `json:"toolCount"`
	Circuit   mcp.CircuitView  `json:"circuit"`
	Sampling  mcp.SamplingView `json:"sampling"`
}

func (s *Service) MCPServerStatus(name string) (*MCPServerStatusView, error) {
	if s.cell == nil {
		return nil, fmt.Errorf("engine not initialized")
	}
	snap, err := s.cell.MCPs().Get(name)
	if err != nil {
		return nil, err
	}
	return &MCPServerStatusView{
		Connected: snap.Connected,
		ToolCount: snap.ToolCount,
		Circuit:   snap.Circuit,
		Sampling:  snap.Sampling,
	}, nil
}

// EnableMCPServer sets the server's Enabled field to true, persists, and reconnects.
func (s *Service) EnableMCPServer(ctx context.Context, name string) error {
	if s.mcpMgr == nil || s.cell == nil {
		return nil
	}
	snap, err := s.cell.MCPs().Get(name)
	if err != nil {
		return err
	}
	enabled := true
	snap.Config.Enabled = &enabled
	return s.mcpMgr.Update(ctx, name, snap.Config)
}

// DisableMCPServer sets the server's Enabled field to false, disconnects, and persists.
func (s *Service) DisableMCPServer(ctx context.Context, name string) error {
	if s.mcpMgr == nil || s.cell == nil {
		return nil
	}
	snap, err := s.cell.MCPs().Get(name)
	if err != nil {
		return err
	}
	disabled := false
	snap.Config.Enabled = &disabled
	_ = s.cell.MCPs().Disconnect(name)
	return s.mcpMgr.Update(ctx, name, snap.Config)
}

// MCPCallTool invokes a specific tool on a named MCP server with given arguments.
func (s *Service) MCPCallTool(ctx context.Context, serverName, toolName string, args map[string]interface{}) (interface{}, error) {
	if s.cell == nil {
		return nil, fmt.Errorf("engine not initialized")
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal args: %w", err)
	}
	result, err := s.cell.MCPs().CallTool(ctx, serverName, toolName, raw)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// MCPServerField describes a single configuration field for the MCP schema form.
type MCPServerField struct {
	Key         string   `json:"key"`
	Type        string   `json:"type"`
	Label       string   `json:"label"`
	LabelEn     string   `json:"labelEn"`
	Placeholder string   `json:"placeholder,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Options     []string `json:"options,omitempty"`
	DependsOn   string   `json:"dependsOn,omitempty"`
	DependsVal  string   `json:"dependsVal,omitempty"`
}

// MCPSchema returns the form schema for creating/editing MCP server configurations.
func (s *Service) MCPSchema() []MCPServerField {
	return []MCPServerField{
		{Key: "name", Type: "string", Label: "Name", LabelEn: "Name", Required: true, Placeholder: "my-server"},
		{Key: "transport_type", Type: "select", Label: "Type", LabelEn: "Type",
			Required: true, Options: []string{"stdio", "http"}},
		{Key: "command", Type: "string", Label: "Command", LabelEn: "Command",
			Placeholder: "npx", DependsOn: "transport_type", DependsVal: "stdio"},
		{Key: "args", Type: "string[]", Label: "Args", LabelEn: "Args",
			Placeholder: "-y @modelcontextprotocol/server-filesystem /tmp",
			DependsOn:   "transport_type", DependsVal: "stdio"},
		{Key: "env", Type: "kv", Label: "Env vars", LabelEn: "Env vars",
			DependsOn: "transport_type", DependsVal: "stdio"},
		{Key: "url", Type: "string", Label: "URL", LabelEn: "URL",
			Placeholder: "https://api.example.com/mcp",
			DependsOn:   "transport_type", DependsVal: "http"},
		{Key: "transport", Type: "select", Label: "Transport", LabelEn: "Transport",
			Options:   []string{"streamable-http", "sse"},
			DependsOn: "transport_type", DependsVal: "http"},
		{Key: "headers", Type: "kv", Label: "Headers", LabelEn: "Headers",
			DependsOn: "transport_type", DependsVal: "http"},
		{Key: "connect_timeout_ms", Type: "integer", Label: "Connect timeout (ms)", LabelEn: "Connect timeout (ms)", Placeholder: "30000"},
		{Key: "tool_timeout_ms", Type: "integer", Label: "Tool timeout (ms)", LabelEn: "Tool timeout (ms)", Placeholder: "120000"},
		{Key: "tools_include", Type: "string[]", Label: "Include tools", LabelEn: "Include tools"},
		{Key: "tools_exclude", Type: "string[]", Label: "Exclude tools", LabelEn: "Exclude tools"},
	}
}

// PutMemory writes one entry into a layer and returns its ID.
func (s *Service) PutMemory(ctx context.Context, w wesgine.MemoryWrite) (string, error) {
	if s.memorySvc == nil {
		return "", fmt.Errorf("engine not initialized")
	}
	w.Actor = memoryActor
	return s.memorySvc.SaveToLayer(ctx, w)
}

// MemoryGC triggers garbage collection on the memory store.
func (s *Service) MemoryGC(ctx context.Context) {
	if s.memorySvc == nil {
		return
	}
	s.memorySvc.TriggerGC(ctx)
}

// ─── Cron ─────────────────────────────────────────────────────────────────────
//
// INV-CRON-10: every cron call carries the calling actor; a job executes as its
// owner. wescode stamps every job with "local" (handler_cron.go), so that is the
// view here. Jobs written before the actor column existed are ownerless and stay
// admin-only by design.

// ListCronJobs returns all cron job entries with their runtime state.
func (s *Service) ListCronJobs(ctx context.Context) ([]cron.Entry, map[string]cron.JobState, error) {
	if s.cronHandle == nil {
		return nil, nil, nil
	}
	entries, err := s.cronHandle.List(ctx, "local")
	if err != nil {
		return nil, nil, err
	}
	states, err := s.cronHandle.ListAllStates(ctx, "local")
	if err != nil {
		return entries, nil, err
	}
	return entries, states, nil
}

// AddCronJob creates a new scheduled job.
func (s *Service) AddCronJob(ctx context.Context, entry cron.Entry) error {
	if s.cronHandle == nil {
		return fmt.Errorf("cron scheduler not initialized")
	}
	return s.cronHandle.Add(ctx, entry, "local")
}

// UpdateCronJob patches an existing job.
func (s *Service) UpdateCronJob(ctx context.Context, id string, patch cron.EntryPatch) error {
	if s.cronHandle == nil {
		return fmt.Errorf("cron scheduler not initialized")
	}
	return s.cronHandle.Update(ctx, id, patch, "local")
}

// DeleteCronJob removes a scheduled job.
func (s *Service) DeleteCronJob(ctx context.Context, id string) error {
	if s.cronHandle == nil {
		return fmt.Errorf("cron scheduler not initialized")
	}
	return s.cronHandle.Remove(ctx, id, "local")
}

// TriggerCronJob fires a job immediately.
func (s *Service) TriggerCronJob(ctx context.Context, id string) error {
	if s.cronHandle == nil {
		return fmt.Errorf("cron scheduler not initialized")
	}
	return s.cronHandle.Trigger(ctx, id, "local")
}

// EnableCronJob enables a disabled job.
func (s *Service) EnableCronJob(ctx context.Context, id string) error {
	if s.cronHandle == nil {
		return fmt.Errorf("cron scheduler not initialized")
	}
	return s.cronHandle.Enable(ctx, id, "local")
}

// DisableCronJob disables a job without removing it.
func (s *Service) DisableCronJob(ctx context.Context, id string) error {
	if s.cronHandle == nil {
		return fmt.Errorf("cron scheduler not initialized")
	}
	return s.cronHandle.Disable(ctx, id, "local")
}

// CronStats returns aggregate scheduler statistics.
func (s *Service) CronStats(ctx context.Context) (cron.SchedulerStats, error) {
	if s.cronHandle == nil {
		return cron.SchedulerStats{}, nil
	}
	return s.cronHandle.Stats(ctx)
}

// ListCronRuns returns recent run records for a job.
func (s *Service) ListCronRuns(ctx context.Context, jobID string, limit int) ([]cron.RunRecord, error) {
	if s.cronHandle == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	return s.cronHandle.ListRuns(ctx, jobID, cron.ListRunsOpts{Limit: limit}, "local")
}

// notifyCronRunFinished tells the IDE a scheduled job produced a result.
//
// Carries the session so the UI can offer to open it: for a job created from a
// conversation that is the conversation the user asked in, and for one created
// in the task page it is the job's own thread. Best-effort — a notification
// that cannot be delivered must not affect the run that already succeeded.
func (s *Service) notifyCronRunFinished(entry cron.Entry, run cron.RunRecord) {
	s.mu.Lock()
	notifier := s.pendingNotifier
	s.mu.Unlock()
	if notifier == nil {
		return
	}
	isOneShot := entry.Schedule.Kind == cron.ScheduleKindAt || entry.DeleteAfterRun
	if err := notifier(notify.CronRunFinished, map[string]any{
		"jobId":        entry.ID,
		"jobName":      entry.Name,
		"runId":        run.RunID,
		"status":       string(run.Status),
		"sessionKey":   run.SessionKey,
		"errorMsg":     run.ErrorMsg,
		"scheduleKind": string(entry.Schedule.Kind),
		"isOneShot":    isOneShot,
	}); err != nil {
		slog.Warn("cron: notify run finished (non-fatal)", "job_id", entry.ID, "err", err)
	}
}

// Note: EnsureCronSession was removed — it was a no-op (the session is
// materialized on first run via the cron adapter's executeJob callback;
// the key convention "cron-"+jobID lives in the adapter, not here).
