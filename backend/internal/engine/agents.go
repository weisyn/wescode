package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	appagent "github.com/weisyn/wesapp/agent"
	"github.com/weisyn/wesgine/agent"
	"github.com/weisyn/wesgine/engine"
)

// ListAgents returns all registered agents as AgentView.
func (s *Service) ListAgents(ctx context.Context) ([]appagent.AgentView, error) {
	if s.agents == nil {
		return nil, nil
	}
	return s.agents.List(ctx)
}

// AgentNameMap returns a map of agentID → display name for label enrichment.
func (s *Service) AgentNameMap(ctx context.Context) map[string]string {
	agents, err := s.ListAgents(ctx)
	if err != nil || len(agents) == 0 {
		return nil
	}
	m := make(map[string]string, len(agents))
	for _, a := range agents {
		m[a.ID] = a.Name
	}
	return m
}

// GetAgent returns a single agent by ID.
func (s *Service) GetAgent(ctx context.Context, id string) (*appagent.AgentView, error) {
	if s.agents == nil {
		return nil, errors.New("engine: not initialized")
	}
	return s.agents.Get(ctx, id)
}

// CreateAgentParams holds all parameters for creating a new agent.
type CreateAgentParams struct {
	Name         string
	Description  string
	Role         string
	Goal         string
	Intent       string
	Expertise    []string
	SystemPrompt string
	Model        string
	Tags         []string
	Suggestions  []string
	Skills       []string // nil = all skills visible; empty slice = no skills
	Figure       string
	Hue          int
	Emoji        string
}

// CreateAgent registers a new user-defined agent and returns it.
func (s *Service) CreateAgent(ctx context.Context, params CreateAgentParams) (*appagent.AgentView, error) {
	if s.agents == nil {
		return nil, errors.New("engine: not initialized")
	}
	view, err := s.agents.Create(ctx, appagent.CreateParams{
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
		return nil, err
	}
	// An agent exists only if it is durable. The engine's store is rebuilt on
	// every Cell.Start, so a definition that never reached wc_agents is one
	// the user loses at the next Cool→Warm without being told — report the
	// failure now and take the registration back out.
	if mErr := s.mirrorAgent(ctx, view.ID); mErr != nil {
		if rmErr := s.agents.Delete(ctx, view.ID); rmErr != nil {
			slog.Error("[engine] agent rollback failed after persist error",
				"id", view.ID, "persist_error", mErr, "rollback_error", rmErr)
		}
		return nil, fmt.Errorf("persist agent: %w", mErr)
	}
	return view, nil
}

// mirrorAgent copies the engine's current definition of id into wc_agents.
//
// The config is read back from the engine instead of rebuilt from the request:
// wesapp fills in defaults (Role←Name, Goal←"帮助用户") and packs Metadata on
// the way in, and a row assembled here would be a second encoder that drifts
// from the one a restore has to reproduce.
func (s *Service) mirrorAgent(ctx context.Context, id string) error {
	s.mu.Lock()
	store, cell := s.userAgents, s.cell
	s.mu.Unlock()
	if store == nil || cell == nil {
		return errors.New("agent persistence unavailable (wescode-app.db not open)")
	}
	cfg, err := cell.Agents().Get(ctx, id)
	if err != nil {
		return fmt.Errorf("read back agent %q: %w", id, err)
	}
	return store.Put(ctx, *cfg)
}

// UpdateAgentParams holds all parameters for updating an existing agent.
// Pointer fields: nil means "don't change"; slices keep nil = don't change semantics
// so callers can pass an explicit empty slice to clear a list field.
type UpdateAgentParams struct {
	ID                  string
	Name                *string
	Description         *string
	Role                *string
	Goal                *string
	Intent              *string
	Expertise           []string
	SystemPrompt        *string
	Suggestions         []string
	Tags                []string
	SkillBindings       []string
	ToolAllow           []string
	ToolDeny            []string
	MaxTurns            *int
	TimeoutSeconds      *int
	ModelOverride       *string
	WorkspaceAccess     *string
	WorkspaceAllowPaths []string
	DelegationChildDeny []string
	Headless            *bool
	Pinned              *bool
}

// UpdateAgent patches an existing agent definition via the Service.
func (s *Service) UpdateAgent(ctx context.Context, params UpdateAgentParams) error {
	if s.agents == nil {
		return errors.New("engine: not initialized")
	}
	if appagent.IsBuiltinID(params.ID) {
		return errors.New("cannot modify a builtin agent")
	}

	up := appagent.UpdateParams{
		Suggestions:         params.Suggestions,
		Tags:                params.Tags,
		Skills:              params.SkillBindings,
		ToolAllow:           params.ToolAllow,
		ToolDeny:            params.ToolDeny,
		Expertise:           params.Expertise,
		WorkspaceAllowPaths: params.WorkspaceAllowPaths,
		DelegationChildDeny: params.DelegationChildDeny,
	}
	up.Name = params.Name
	up.Description = params.Description
	up.Role = params.Role
	up.Goal = params.Goal
	up.Intent = params.Intent
	up.SystemPrompt = params.SystemPrompt
	up.MaxTurns = params.MaxTurns
	up.TimeoutSeconds = params.TimeoutSeconds
	up.Model = params.ModelOverride
	up.WorkspaceAccess = params.WorkspaceAccess
	up.Headless = params.Headless
	up.Pinned = params.Pinned
	if _, err := s.agents.Update(ctx, params.ID, up); err != nil {
		return err
	}
	// Same reason as CreateAgent: an edit that only reached the in-memory
	// store reverts to the persisted version at the next Cool→Warm, so a
	// silent success here would be a silent rollback later.
	if err := s.mirrorAgent(ctx, params.ID); err != nil {
		return fmt.Errorf("persist agent: %w", err)
	}
	return nil
}

// ListTools returns descriptors for all registered tools in the engine.
func (s *Service) ListTools() []ToolDescriptor {
	s.mu.Lock()
	cell := s.cell
	initialized := s.initialized
	s.mu.Unlock()
	if !initialized || cell == nil {
		return nil
	}
	descs := cell.Tools().Descriptions()
	result := make([]ToolDescriptor, 0, len(descs))
	for name, desc := range descs {
		result = append(result, ToolDescriptor{Name: name, Description: desc})
	}
	return result
}

// ToolDescriptor is a lightweight tool name + description pair.
type ToolDescriptor struct {
	Name        string
	Description string
}

// AgentWorkspaceDir returns the workspace directory for a given agent.
// Returns empty string if the engine is not initialized or no data directory is set.
func (s *Service) AgentWorkspaceDir(agentID string) string {
	s.mu.Lock()
	dataDir := s.dataDir
	initialized := s.initialized
	s.mu.Unlock()
	if !initialized || dataDir == "" {
		return ""
	}
	dir := filepath.Join(dataDir, "workspace", agentID)
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	return dir
}

// DeleteAgent removes a user-defined agent.
func (s *Service) DeleteAgent(ctx context.Context, id string) error {
	if appagent.IsBuiltinID(id) {
		return errors.New("cannot delete a builtin agent")
	}
	if s.agents == nil {
		return errors.New("engine: not initialized")
	}
	if err := s.agents.Delete(ctx, id); err != nil {
		return err
	}
	s.mu.Lock()
	store := s.userAgents
	s.mu.Unlock()
	if store == nil {
		// No store means nothing was ever persisted (CreateAgent refuses
		// without one), so there is no row to leave behind.
		return nil
	}
	// A surviving row would re-register the agent at the next Cool→Warm, so
	// the user has to hear about this one rather than watch it come back.
	if err := store.Delete(ctx, id); err != nil {
		return fmt.Errorf("persist agent removal: %w", err)
	}
	return nil
}

// ── Tool Budget Estimation ──────────────────────────────────────────────────

// ToolBudgetEstimate describes estimated token cost of an agent's tool set.
type ToolBudgetEstimate struct {
	EstimatedTokens int      `json:"estimatedTokens"`
	MaxTokens       int      `json:"maxTokens"`
	TruncatedTools  []string `json:"truncatedTools"`
	Warnings        []string `json:"warnings"`
}

// EstimateToolBudget computes the estimated token cost of all tools visible to an agent.
func (s *Service) EstimateToolBudget(ctx context.Context, agentID string) (ToolBudgetEstimate, error) {
	s.mu.Lock()
	cell := s.cell
	initialized := s.initialized
	s.mu.Unlock()
	if !initialized || cell == nil {
		return ToolBudgetEstimate{MaxTokens: 128000}, errors.New("engine: not initialized")
	}

	const maxTokens = 128000

	allSchemas := cell.Tools().ListAll()

	var agentCfg *agent.AgentConfig
	if agentID != "" {
		view, err := s.agents.Get(ctx, agentID)
		if err == nil && view != nil {
			cfg := viewToAgentConfig(*view)
			agentCfg = &cfg
		}
	}

	var totalTokens int
	var truncated []string
	var warnings []string

	for _, schema := range allSchemas {
		if agentCfg != nil {
			if !toolAllowed(schema.Name, agentCfg.ToolPolicy) {
				continue
			}
		}
		est := estimateSchemaTokens(schema.Name, schema.Description, schema.InputSchema)
		totalTokens += est
	}

	if totalTokens > maxTokens/4 {
		warnings = append(warnings, fmt.Sprintf("tool schemas use %d tokens (%.0f%% of context window)", totalTokens, float64(totalTokens)/float64(maxTokens)*100))
	}

	return ToolBudgetEstimate{
		EstimatedTokens: totalTokens,
		MaxTokens:       maxTokens,
		TruncatedTools:  truncated,
		Warnings:        warnings,
	}, nil
}

// viewToAgentConfig converts an AgentView to an AgentConfig for internal use
// (e.g., tool budget estimation).
func viewToAgentConfig(v appagent.AgentView) agent.AgentConfig {
	cfg := agent.AgentConfig{
		ID:   v.ID,
		Name: v.Name,
	}
	cfg.ToolPolicy.Allow = v.ToolAllow
	cfg.ToolPolicy.Deny = v.ToolDeny
	return cfg
}

func toolAllowed(name string, policy agent.ToolPolicy) bool {
	if len(policy.Allow) > 0 {
		for _, a := range policy.Allow {
			if a == name {
				return true
			}
		}
		return false
	}
	for _, d := range policy.Deny {
		if d == name {
			return false
		}
	}
	return true
}

func estimateSchemaTokens(name, description string, inputSchema json.RawMessage) int {
	return len(name)/3 + len(description)/4 + len(inputSchema)/4 + 18
}

// ── Audit Trail ─────────────────────────────────────────────────────────────

// AgentAuditEntry represents a run-based audit record for an agent.
//
// Status keeps the engine's RunStatus type rather than widening to string at
// this boundary: the audit view has no status of its own to invent, and a
// string field here would let a future writer put anything in it while still
// compiling.
type AgentAuditEntry struct {
	ID         string           `json:"id"`
	Time       string           `json:"time"`
	Type       string           `json:"type"`
	AgentID    string           `json:"agentId,omitempty"`
	RunID      string           `json:"runId,omitempty"`
	Model      string           `json:"model,omitempty"`
	Status     engine.RunStatus `json:"status,omitempty"`
	TotalTurns int              `json:"totalTurns,omitempty"`
	ElapsedMS  int64            `json:"elapsedMs,omitempty"`
}

// ListAgentAudit returns recent run history for the given agent as audit entries.
func (s *Service) ListAgentAudit(ctx context.Context, agentID string, limit int) ([]AgentAuditEntry, error) {
	if s.observeSvc == nil {
		return nil, errors.New("engine: not initialized")
	}
	if limit <= 0 {
		limit = 50
	}

	// INV-OBS-08: actor rides along; wescode is single-actor ("local").
	runs, err := s.observeSvc.ListRunsByAgent(ctx, "local", agentID, limit)
	if err != nil {
		return nil, fmt.Errorf("list agent audit: %w", err)
	}

	entries := make([]AgentAuditEntry, 0, len(runs))
	for _, r := range runs {
		entries = append(entries, AgentAuditEntry{
			ID:         r.RunID,
			Time:       r.StartedAt.Format(time.RFC3339),
			Type:       "run",
			AgentID:    r.AgentID,
			RunID:      r.RunID,
			Model:      r.Model,
			Status:     r.Status,
			TotalTurns: r.TotalTurns,
			ElapsedMS:  r.ElapsedMS,
		})
	}
	return entries, nil
}
