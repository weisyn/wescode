package engine

import (
	"context"
	"fmt"

	wesgine "github.com/weisyn/wesgine"
	"github.com/weisyn/wesgine/governance"
)

// Every Update*Settings method returns the field names that persisted to the
// registry but did not reach the running Cell, straight from
// wesgine.SpecPatch.RestartRequired(). All seven return it even though four of
// them build patches whose fields all hot-apply today — the alternative is a
// second, hand-maintained list of "which endpoints can need a restart", and a
// list that must be kept in agreement with the engine's is exactly how
// ReadMaxChars stayed configurable-looking while its mapping was missing. A
// restart-requiring field added to any of these patches later surfaces in the
// UI without anyone remembering to widen the signature.
//
// The caller must render a non-empty list. "Saved, nothing happened, no
// signal" is indistinguishable from a dead setting.

// ---------------------------------------------------------------------------
// Governance Settings
// ---------------------------------------------------------------------------

// GovernanceSettings carries only fields the page can both show and change.
// Compliance used to ride along here: loaded, serialised, never rendered, and
// absent from the Update struct. A read-only field on a settings surface is
// the same failure as a dead toggle read backwards — it invites a UI that
// displays a value nobody can act on.
type GovernanceSettings struct {
	GovernMode         string   `json:"governMode"`
	DenyPaths          []string `json:"denyPaths"`
	RedactorRules      []string `json:"redactorRules"`
	ThinkingVisibility string   `json:"thinkingVisibility"`
	NetworkPolicy      string   `json:"networkPolicy"`
}

type GovernanceSettingsUpdate struct {
	GovernMode         *string   `json:"governMode,omitempty"`
	DenyPaths          *[]string `json:"denyPaths,omitempty"`
	RedactorRules      *[]string `json:"redactorRules,omitempty"`
	ThinkingVisibility *string   `json:"thinkingVisibility,omitempty"`
	NetworkPolicy      *string   `json:"networkPolicy,omitempty"`
}

func (s *Service) GetGovernanceSettings() (GovernanceSettings, error) {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return GovernanceSettings{}, fmt.Errorf("workspace Cell not initialised")
	}
	view, err := cell.Policy().Get()
	if err != nil {
		return GovernanceSettings{}, err
	}
	spec := cell.Spec()
	mode := "open"
	if spec.Governance != nil && spec.Governance.Mode != "" {
		mode = string(spec.Governance.Mode)
	}
	// Empty network policy enforces exactly as "allow" (the sandbox switch has
	// no default arm), so report it as allow rather than inventing an "unset"
	// state the page would have to explain.
	netPolicy := string(governance.NetworkAllow)
	if spec.Governance != nil && spec.Governance.Sandbox.NetworkPolicy != "" {
		netPolicy = string(spec.Governance.Sandbox.NetworkPolicy)
	}
	denyPaths := append([]string{}, view.DenyPaths...)
	redactorRules := append([]string{}, view.RedactorRules...)
	return GovernanceSettings{
		GovernMode:         mode,
		DenyPaths:          denyPaths,
		RedactorRules:      redactorRules,
		ThinkingVisibility: spec.ThinkingVisibility,
		NetworkPolicy:      netPolicy,
	}, nil
}

func (s *Service) UpdateGovernanceSettings(ctx context.Context, gs GovernanceSettingsUpdate) ([]string, error) {
	s.mu.Lock()
	cell := s.cell
	hyp := s.hyp
	cellID := s.cellID
	s.mu.Unlock()
	if cell == nil || hyp == nil {
		return nil, fmt.Errorf("workspace Cell not initialised")
	}

	if gs.DenyPaths != nil || gs.RedactorRules != nil {
		policyPatch := wesgine.PolicyPatch{}
		if gs.DenyPaths != nil {
			policyPatch.DenyPaths = gs.DenyPaths
		}
		if gs.RedactorRules != nil {
			policyPatch.RedactorRules = gs.RedactorRules
		}
		if err := cell.Policy().Update(ctx, policyPatch); err != nil {
			return nil, fmt.Errorf("update policy: %w", err)
		}
	}

	if gs.GovernMode != nil || gs.NetworkPolicy != nil || gs.ThinkingVisibility != nil {
		var patch wesgine.SpecPatch
		if gs.GovernMode != nil || gs.NetworkPolicy != nil {
			// Spec() copies the struct but Governance is a pointer, so writing
			// through it would mutate the running Cell before UpdateSpec has
			// validated anything — a rejected patch would still have landed.
			gov := governance.CellGovernance{}
			if cur := cell.Spec().Governance; cur != nil {
				gov = *cur
			}
			if gs.GovernMode != nil {
				gov.Mode = governance.GovernMode(*gs.GovernMode)
			}
			if gs.NetworkPolicy != nil {
				// Domain check is the engine's (ValidateGovernance runs on the
				// patch). Copying its value table here would give us a second
				// one to keep in sync, and the sandbox switch has no default
				// arm — an unknown value that slipped past enforces as allow.
				gov.Sandbox.NetworkPolicy = governance.NetworkPolicy(*gs.NetworkPolicy)
			}
			patch.Governance = &gov
		}
		if gs.ThinkingVisibility != nil {
			patch.ThinkingVisibility = gs.ThinkingVisibility
		}
		if err := hyp.Cells().UpdateSpec(ctx, cellID, patch); err != nil {
			return nil, fmt.Errorf("update governance spec: %w", err)
		}
		return patch.RestartRequired(), nil
	}

	return nil, nil
}

// ---------------------------------------------------------------------------
// Memory Policy Settings
// ---------------------------------------------------------------------------

// MemoryPolicySettings exposes the two entry caps the user can meaningfully
// reason about: role memory (long-lived, survives sessions) and session memory
// (this conversation's runtime state). The engine has five scope caps; the
// other three are engine-populated (environment sensor, consensus, working
// cache) and have no user decision behind them.
//
// The two are separate fields because the engine keeps them separate, with
// different defaults (10k / 5k). A single collapsed "maxEntries" used to feed
// both: opening the page showed one of the two, saving overwrote the other
// with it, and nothing in the UI said the 2:1 ratio had just been destroyed.
type MemoryPolicySettings struct {
	WritePolicy       string `json:"writePolicy"`
	MaxEntriesAgent   int    `json:"maxEntriesAgent"`
	MaxEntriesSession int    `json:"maxEntriesSession"`
}

type MemoryPolicySettingsUpdate struct {
	WritePolicy       *string `json:"writePolicy,omitempty"`
	MaxEntriesAgent   *int    `json:"maxEntriesAgent,omitempty"`
	MaxEntriesSession *int    `json:"maxEntriesSession,omitempty"`
}

// GetMemoryPolicySettings reads the persisted CellSpec, and nothing else.
//
// It used to prefer config.yaml over the spec, which made the page *look*
// correct in exactly the case where it was wrong: the write landed in the
// yaml, the read came back from the yaml, and the engine — which only ever
// consulted the spec — kept the old policy. Reading the single store the
// engine reads means a stale value now shows as stale.
func (s *Service) GetMemoryPolicySettings() (MemoryPolicySettings, error) {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return MemoryPolicySettings{}, fmt.Errorf("workspace Cell not initialised")
	}
	spec := cell.Spec()
	ml := resolveMemoryLimits(spec.MemoryLimits)
	wp := string(spec.MemoryWritePolicy)
	if wp == "" {
		wp = string(wesgine.MemoryWriteSilent)
	}
	// resolveMemoryLimits already applied the engine defaults, so these are the
	// caps the GC enforces, not the raw zeros. That matters here: zero on the
	// wire would read as "no cap" to the page, while the engine reads it as
	// "use the default" — the one direction where a wrong guess silently
	// promises unlimited memory.
	return MemoryPolicySettings{
		WritePolicy:       wp,
		MaxEntriesAgent:   ml.MaxEntriesAgent,
		MaxEntriesSession: ml.MaxEntriesSession,
	}, nil
}

func (s *Service) UpdateMemoryPolicySettings(ctx context.Context, ms MemoryPolicySettingsUpdate) ([]string, error) {
	s.mu.Lock()
	cell := s.cell
	hyp := s.hyp
	cellID := s.cellID
	s.mu.Unlock()
	if cell == nil || hyp == nil {
		return nil, fmt.Errorf("workspace Cell not initialised")
	}
	spec := cell.Spec()

	var memLimits *wesgine.MemoryLimitsConfig
	if ms.MaxEntriesAgent != nil || ms.MaxEntriesSession != nil {
		ml := resolveMemoryLimits(spec.MemoryLimits)
		if ms.MaxEntriesAgent != nil {
			ml.MaxEntriesAgent = *ms.MaxEntriesAgent
		}
		if ms.MaxEntriesSession != nil {
			ml.MaxEntriesSession = *ms.MaxEntriesSession
		}
		memLimits = &ml
	}

	// WritePolicy rides the same SpecPatch as the entry caps: the engine
	// reads it per write (INV-MEM-27), so writing the spec *is* the update.
	//
	// It previously went to config.yaml instead, and the author left a
	// `restartRequired` flag behind, computed and then discarded with
	// `_ = restartRequired`. Neither half worked: the flag never reached the
	// UI, and a restart would not have applied the value either — an
	// existing Cell keeps its persisted spec, so the yaml was read on first
	// boot and never again. Both are gone rather than one being fixed;
	// "tell the user to restart" is the right answer only when a restart is.
	var writePolicy *wesgine.MemoryWritePolicy
	if ms.WritePolicy != nil {
		wp := wesgine.MemoryWritePolicy(*ms.WritePolicy)
		writePolicy = &wp
	}

	patch := wesgine.SpecPatch{
		MemoryLimits:      memLimits,
		MemoryWritePolicy: writePolicy,
	}
	if err := hyp.Cells().UpdateSpec(ctx, cellID, patch); err != nil {
		return nil, fmt.Errorf("update spec: %w", err)
	}
	return patch.RestartRequired(), nil
}

// ---------------------------------------------------------------------------
// Run Limits Settings
// ---------------------------------------------------------------------------

type RunLimitsSettings struct {
	TimeoutSeconds     int `json:"timeoutSeconds"`
	PanicStopTurns     int `json:"panicStopTurns"`
	ExecTimeoutSeconds int `json:"execTimeoutSeconds"`
	ReadMaxChars       int `json:"readMaxChars"`
	GrepMaxResults     int `json:"grepMaxResults"`
	MaxConcurrentRuns  int `json:"maxConcurrentRuns"`
	TokensPerMinute    int `json:"tokensPerMinute"`
	TokensPerDay       int `json:"tokensPerDay"`
}

type RunLimitsSettingsUpdate struct {
	TimeoutSeconds     *int `json:"timeoutSeconds,omitempty"`
	PanicStopTurns     *int `json:"panicStopTurns,omitempty"`
	ExecTimeoutSeconds *int `json:"execTimeoutSeconds,omitempty"`
	ReadMaxChars       *int `json:"readMaxChars,omitempty"`
	GrepMaxResults     *int `json:"grepMaxResults,omitempty"`
	MaxConcurrentRuns  *int `json:"maxConcurrentRuns,omitempty"`
	TokensPerMinute    *int `json:"tokensPerMinute,omitempty"`
	TokensPerDay       *int `json:"tokensPerDay,omitempty"`
}

func (s *Service) GetRunLimitsSettings() (RunLimitsSettings, error) {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return RunLimitsSettings{}, fmt.Errorf("workspace Cell not initialised")
	}
	spec := cell.Spec()
	rl := resolveRunLimits(spec.RunLimits)
	tl := resolveToolLimits(spec.ToolLimits)
	return RunLimitsSettings{
		TimeoutSeconds:     rl.MaxRunTimeoutSeconds,
		PanicStopTurns:     rl.PanicStopTurns,
		ExecTimeoutSeconds: tl.ExecForegroundTimeoutSeconds,
		ReadMaxChars:       tl.ReadMaxChars,
		GrepMaxResults:     tl.GrepMaxResults,
		MaxConcurrentRuns:  spec.Quotas.MaxConcurrentRuns,
		TokensPerMinute:    spec.Quotas.MaxLLMTokensPerMinute,
		TokensPerDay:       spec.Quotas.MaxLLMTokensPerDay,
	}, nil
}

func (s *Service) UpdateRunLimitsSettings(ctx context.Context, rl RunLimitsSettingsUpdate) ([]string, error) {
	s.mu.Lock()
	cell := s.cell
	hyp := s.hyp
	cellID := s.cellID
	s.mu.Unlock()
	if cell == nil || hyp == nil {
		return nil, fmt.Errorf("workspace Cell not initialised")
	}
	spec := cell.Spec()

	var runLimits *wesgine.RunLimitsConfig
	if rl.TimeoutSeconds != nil || rl.PanicStopTurns != nil {
		r := resolveRunLimits(spec.RunLimits)
		if rl.TimeoutSeconds != nil {
			r.MaxRunTimeoutSeconds = *rl.TimeoutSeconds
		}
		if rl.PanicStopTurns != nil {
			r.PanicStopTurns = *rl.PanicStopTurns
		}
		runLimits = &r
	}

	var toolLimits *wesgine.ToolLimitsConfig
	if rl.ExecTimeoutSeconds != nil || rl.ReadMaxChars != nil || rl.GrepMaxResults != nil {
		t := resolveToolLimits(spec.ToolLimits)
		if rl.ExecTimeoutSeconds != nil {
			t.ExecForegroundTimeoutSeconds = *rl.ExecTimeoutSeconds
		}
		if rl.ReadMaxChars != nil {
			t.ReadMaxChars = *rl.ReadMaxChars
		}
		if rl.GrepMaxResults != nil {
			t.GrepMaxResults = *rl.GrepMaxResults
		}
		toolLimits = &t
	}

	var quotas *wesgine.CellQuotas
	if rl.MaxConcurrentRuns != nil || rl.TokensPerMinute != nil || rl.TokensPerDay != nil {
		q := spec.Quotas
		if rl.MaxConcurrentRuns != nil {
			q.MaxConcurrentRuns = *rl.MaxConcurrentRuns
		}
		if rl.TokensPerMinute != nil {
			q.MaxLLMTokensPerMinute = *rl.TokensPerMinute
		}
		if rl.TokensPerDay != nil {
			q.MaxLLMTokensPerDay = *rl.TokensPerDay
		}
		quotas = &q
	}

	patch := wesgine.SpecPatch{
		RunLimits:  runLimits,
		ToolLimits: toolLimits,
		Quotas:     quotas,
	}
	if err := hyp.Cells().UpdateSpec(ctx, cellID, patch); err != nil {
		return nil, fmt.Errorf("update spec: %w", err)
	}
	return patch.RestartRequired(), nil
}

// ---------------------------------------------------------------------------
// Cycle Detect Settings
// ---------------------------------------------------------------------------

type CycleDetectSettings struct {
	ExplorationWarn int               `json:"explorationWarn"`
	ExplorationTerm int               `json:"explorationTerm"`
	ActionWarn      int               `json:"actionWarn"`
	ActionTerm      int               `json:"actionTerm"`
	IdenticalWarn   int               `json:"identicalWarn"`
	IdenticalTerm   int               `json:"identicalTerm"`
	ToolOverrides   map[string][2]int `json:"toolOverrides,omitempty"`
}

func (s *Service) GetCycleDetectSettings() (CycleDetectSettings, error) {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return CycleDetectSettings{}, fmt.Errorf("workspace Cell not initialised")
	}
	spec := cell.Spec()
	var cd wesgine.CycleDetectConfig
	if spec.CycleDetect != nil {
		cd = *spec.CycleDetect
	}
	return CycleDetectSettings{
		ExplorationWarn: cd.ExplorationWarnThreshold,
		ExplorationTerm: cd.ExplorationTermThreshold,
		ActionWarn:      cd.ActionWarnThreshold,
		ActionTerm:      cd.ActionTermThreshold,
		IdenticalWarn:   cd.IdenticalCallWarnThreshold,
		IdenticalTerm:   cd.IdenticalCallTermThreshold,
		ToolOverrides:   cd.ToolOverrides,
	}, nil
}

func (s *Service) UpdateCycleDetectSettings(ctx context.Context, cd CycleDetectSettings) ([]string, error) {
	s.mu.Lock()
	cell := s.cell
	hyp := s.hyp
	cellID := s.cellID
	s.mu.Unlock()
	if cell == nil || hyp == nil {
		return nil, fmt.Errorf("workspace Cell not initialised")
	}
	cfg := &wesgine.CycleDetectConfig{
		ExplorationWarnThreshold:   cd.ExplorationWarn,
		ExplorationTermThreshold:   cd.ExplorationTerm,
		ActionWarnThreshold:        cd.ActionWarn,
		ActionTermThreshold:        cd.ActionTerm,
		IdenticalCallWarnThreshold: cd.IdenticalWarn,
		IdenticalCallTermThreshold: cd.IdenticalTerm,
		ToolOverrides:              cd.ToolOverrides,
	}
	patch := wesgine.SpecPatch{CycleDetect: cfg}
	if err := hyp.Cells().UpdateSpec(ctx, cellID, patch); err != nil {
		return nil, fmt.Errorf("update cycle detect: %w", err)
	}
	return patch.RestartRequired(), nil
}

// ---------------------------------------------------------------------------
// Context Budget Settings
// ---------------------------------------------------------------------------

type ContextBudgetSettings struct {
	AppIdentity                   string  `json:"appIdentity"`
	ProactiveCompressTriggerRatio float64 `json:"proactiveCompressTriggerRatio"`
	ProactiveCompressRatio        float64 `json:"proactiveCompressRatio"`
	AutoCompactBufferTokens       int     `json:"autoCompactBufferTokens"`
	MaxPinnedMessages             int     `json:"maxPinnedMessages"`
	SystemPromptMaxRatio          float64 `json:"systemPromptMaxRatio"`
}

type ContextBudgetSettingsUpdate struct {
	AppIdentity                   *string  `json:"appIdentity,omitempty"`
	ProactiveCompressTriggerRatio *float64 `json:"proactiveCompressTriggerRatio,omitempty"`
	ProactiveCompressRatio        *float64 `json:"proactiveCompressRatio,omitempty"`
	AutoCompactBufferTokens       *int     `json:"autoCompactBufferTokens,omitempty"`
	MaxPinnedMessages             *int     `json:"maxPinnedMessages,omitempty"`
	SystemPromptMaxRatio          *float64 `json:"systemPromptMaxRatio,omitempty"`
}

// GetContextBudgetSettings reads AppIdentity from config.yaml, not from the
// CellSpec — the one place in this file where the yaml is the right answer.
// The textarea edits the user's fragment; the spec holds the merged prompt,
// which starts with wescode's own multi-page constitution. Reading the spec
// here would dump that into the editor and the next save would persist it as
// "the user's custom text", permanently duplicating the built-in prompt.
func (s *Service) GetContextBudgetSettings() (ContextBudgetSettings, error) {
	s.mu.Lock()
	cell := s.cell
	customIdentity := s.cfg.AppIdentity
	s.mu.Unlock()
	if cell == nil {
		return ContextBudgetSettings{}, fmt.Errorf("workspace Cell not initialised")
	}
	spec := cell.Spec()
	cb := resolveContextBudget(spec.ContextBudget)
	return ContextBudgetSettings{
		AppIdentity:                   customIdentity,
		ProactiveCompressTriggerRatio: cb.ProactiveCompressTriggerRatio,
		ProactiveCompressRatio:        cb.ProactiveCompressRatio,
		AutoCompactBufferTokens:       cb.AutoCompactBufferTokens,
		MaxPinnedMessages:             cb.MaxPinnedMessages,
		SystemPromptMaxRatio:          cb.SystemPromptMaxRatio,
	}, nil
}

func (s *Service) UpdateContextBudgetSettings(ctx context.Context, cb ContextBudgetSettingsUpdate) ([]string, error) {
	s.mu.Lock()
	cell := s.cell
	hyp := s.hyp
	cellID := s.cellID
	s.mu.Unlock()
	if cell == nil || hyp == nil {
		return nil, fmt.Errorf("workspace Cell not initialised")
	}

	// AppIdentity travels in two directions because it has two values: the
	// user's fragment (config.yaml — the only place it exists) and the merged
	// prompt (CellSpec — derived, recomputed here and on every boot).
	//
	// Persisting the fragment alone is what this used to do, on the theory
	// that boot would merge it. Boot does merge it — into a spec that
	// GetOrCreate keeps for an existing Cell, so the merged value was
	// written once at Cell creation and the edit landed nowhere. The spec
	// patch below is the half that was missing; the engine reads
	// AppIdentity per assembly, so it takes effect on the next turn.
	var appIdentity *string
	if cb.AppIdentity != nil {
		s.mu.Lock()
		s.cfg.AppIdentity = *cb.AppIdentity
		cfgCopy := s.cfg
		s.mu.Unlock()
		if err := saveConfig(cfgCopy); err != nil {
			return nil, fmt.Errorf("save appIdentity: %w", err)
		}
		merged := s.resolveAppIdentity()
		appIdentity = &merged
	}

	spec := cell.Spec()
	budget := resolveContextBudget(spec.ContextBudget)
	budgetChanged := false
	if cb.ProactiveCompressTriggerRatio != nil {
		budget.ProactiveCompressTriggerRatio = *cb.ProactiveCompressTriggerRatio
		budgetChanged = true
	}
	if cb.ProactiveCompressRatio != nil {
		budget.ProactiveCompressRatio = *cb.ProactiveCompressRatio
		budgetChanged = true
	}
	if cb.AutoCompactBufferTokens != nil {
		budget.AutoCompactBufferTokens = *cb.AutoCompactBufferTokens
		budgetChanged = true
	}
	if cb.MaxPinnedMessages != nil {
		budget.MaxPinnedMessages = *cb.MaxPinnedMessages
		budgetChanged = true
	}
	if cb.SystemPromptMaxRatio != nil {
		budget.SystemPromptMaxRatio = *cb.SystemPromptMaxRatio
		budgetChanged = true
	}
	// One patch for both: SpecPatch fields are independently optional, and
	// two calls would leave a window where the identity moved and the budget
	// had not (or the reverse, on error).
	patch := wesgine.SpecPatch{AppIdentity: appIdentity}
	if budgetChanged {
		patch.ContextBudget = &budget
	}
	if patch.AppIdentity != nil || patch.ContextBudget != nil {
		if err := hyp.Cells().UpdateSpec(ctx, cellID, patch); err != nil {
			return nil, fmt.Errorf("update context budget: %w", err)
		}
	}
	return patch.RestartRequired(), nil
}

// ---------------------------------------------------------------------------
// Resilience Settings
// ---------------------------------------------------------------------------

type ResilienceSettings struct {
	RetryMaxAttempts         int `json:"retryMaxAttempts"`
	HealthThreshold          int `json:"healthThreshold"`
	FirstByteTimeoutSeconds  int `json:"firstByteTimeoutSeconds"`
	InterChunkTimeoutSeconds int `json:"interChunkTimeoutSeconds"`
	MaxOverloadRetries       int `json:"maxOverloadRetries"`
}

type ResilienceSettingsUpdate struct {
	RetryMaxAttempts         *int `json:"retryMaxAttempts,omitempty"`
	HealthThreshold          *int `json:"healthThreshold,omitempty"`
	FirstByteTimeoutSeconds  *int `json:"firstByteTimeoutSeconds,omitempty"`
	InterChunkTimeoutSeconds *int `json:"interChunkTimeoutSeconds,omitempty"`
	MaxOverloadRetries       *int `json:"maxOverloadRetries,omitempty"`
}

func (s *Service) GetResilienceSettings() (ResilienceSettings, error) {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return ResilienceSettings{}, fmt.Errorf("workspace Cell not initialised")
	}
	spec := cell.Spec()
	r := resolveResilience(spec.Resilience)
	return ResilienceSettings{
		RetryMaxAttempts:         r.RetryMaxAttempts,
		HealthThreshold:          r.HealthThreshold,
		FirstByteTimeoutSeconds:  r.FirstByteTimeoutSeconds,
		InterChunkTimeoutSeconds: r.InterChunkTimeoutSeconds,
		MaxOverloadRetries:       r.MaxOverloadRetries,
	}, nil
}

func (s *Service) UpdateResilienceSettings(ctx context.Context, rs ResilienceSettingsUpdate) ([]string, error) {
	s.mu.Lock()
	cell := s.cell
	hyp := s.hyp
	cellID := s.cellID
	s.mu.Unlock()
	if cell == nil || hyp == nil {
		return nil, fmt.Errorf("workspace Cell not initialised")
	}
	spec := cell.Spec()
	r := resolveResilience(spec.Resilience)
	if rs.RetryMaxAttempts != nil {
		r.RetryMaxAttempts = *rs.RetryMaxAttempts
	}
	if rs.HealthThreshold != nil {
		r.HealthThreshold = *rs.HealthThreshold
	}
	if rs.FirstByteTimeoutSeconds != nil {
		r.FirstByteTimeoutSeconds = *rs.FirstByteTimeoutSeconds
	}
	if rs.InterChunkTimeoutSeconds != nil {
		r.InterChunkTimeoutSeconds = *rs.InterChunkTimeoutSeconds
	}
	if rs.MaxOverloadRetries != nil {
		r.MaxOverloadRetries = *rs.MaxOverloadRetries
	}
	patch := wesgine.SpecPatch{Resilience: &r}
	if err := hyp.Cells().UpdateSpec(ctx, cellID, patch); err != nil {
		return nil, fmt.Errorf("update resilience: %w", err)
	}
	return patch.RestartRequired(), nil
}

// ---------------------------------------------------------------------------
// Plugins
// ---------------------------------------------------------------------------

type PluginInfo struct {
	Name      string `json:"name"`
	Dir       string `json:"dir"`
	Version   string `json:"version"`
	ToolCount int    `json:"toolCount"`
	Status    string `json:"status"`
}

func (s *Service) ListPlugins() ([]PluginInfo, error) {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return nil, fmt.Errorf("workspace Cell not initialised")
	}
	spec := cell.Spec()
	var plugins []PluginInfo
	for _, src := range spec.Plugins {
		name := src.Dir
		version := "1.0.0"
		toolCount := 0
		status := "running"

		if mcpHandle := cell.MCPs(); mcpHandle != nil {
			if snap, err := mcpHandle.Get(name); err == nil {
				toolCount = snap.ToolCount
				if snap.Connected {
					status = "running"
				} else if snap.LastError != "" {
					status = "error"
				} else {
					status = "stopped"
				}
			}
		}

		plugins = append(plugins, PluginInfo{
			Name:      name,
			Dir:       src.Dir,
			Version:   version,
			ToolCount: toolCount,
			Status:    status,
		})
	}
	return plugins, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func resolveRunLimits(cfg *wesgine.RunLimitsConfig) wesgine.RunLimitsConfig {
	var r wesgine.RunLimitsConfig
	if cfg != nil {
		r = *cfg
	}
	r.Resolve()
	return r
}

func resolveToolLimits(cfg *wesgine.ToolLimitsConfig) wesgine.ToolLimitsConfig {
	var t wesgine.ToolLimitsConfig
	if cfg != nil {
		t = *cfg
	}
	t.Resolve()
	return t
}

func resolveContextBudget(cfg *wesgine.CellContextBudgetConfig) wesgine.CellContextBudgetConfig {
	var c wesgine.CellContextBudgetConfig
	if cfg != nil {
		c = *cfg
	}
	c.Resolve()
	return c
}

func resolveResilience(cfg *wesgine.ResilienceLimitsConfig) wesgine.ResilienceLimitsConfig {
	var r wesgine.ResilienceLimitsConfig
	if cfg != nil {
		r = *cfg
	}
	r.Resolve()
	return r
}

func resolveMemoryLimits(cfg *wesgine.MemoryLimitsConfig) wesgine.MemoryLimitsConfig {
	var m wesgine.MemoryLimitsConfig
	if cfg != nil {
		m = *cfg
	}
	m.Resolve()
	return m
}

// ---------------------------------------------------------------------------
// Provider Strategy Settings
// ---------------------------------------------------------------------------

type ProviderStrategySettings struct {
	Strategy      string   `json:"strategy"`
	AllowedModels []string `json:"allowedModels"`
}

func (s *Service) GetProviderStrategySettings() (ProviderStrategySettings, error) {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return ProviderStrategySettings{}, fmt.Errorf("Cell not initialized")
	}
	spec := cell.Spec()
	return ProviderStrategySettings{
		Strategy:      string(spec.ProviderStrategy),
		AllowedModels: spec.AllowedModels,
	}, nil
}

func (s *Service) UpdateProviderStrategySettings(ctx context.Context, ps ProviderStrategySettings) ([]string, error) {
	s.mu.Lock()
	cell := s.cell
	hyp := s.hyp
	s.mu.Unlock()
	if cell == nil || hyp == nil {
		return nil, fmt.Errorf("Cell not initialized")
	}
	strategy := wesgine.ProviderStrategy(ps.Strategy)
	patch := wesgine.SpecPatch{
		ProviderStrategy: &strategy,
		AllowedModels:    ps.AllowedModels,
	}
	if err := hyp.Cells().UpdateSpec(ctx, cell.Spec().ID, patch); err != nil {
		return nil, err
	}
	return patch.RestartRequired(), nil
}
