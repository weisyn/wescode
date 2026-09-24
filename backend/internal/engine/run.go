package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/weisyn/wesapp/providerid"
	"github.com/weisyn/wescode/internal/codeintel"
	"github.com/weisyn/wescode/internal/engine/wsintel"
	wesgine "github.com/weisyn/wesgine"
	wesengine "github.com/weisyn/wesgine/engine"
	"github.com/weisyn/wesgine/message"
)

type ChatOpts struct {
	ProviderID string
	// Model is the BYOK upstream model id. Ignored for wes:/org: — those
	// channels send the weisyn proxy slug from catalog (INV-MODEL-01).
	Model        string
	FilePaths    []string
	FileIDs      []string
	WorkDir      string
	AllowPaths   []string
	TestTimeout  int
	CodeSnippets []CodeSnippet
	Resume       bool // true = continuation of a previous Run (restore checkpoint)
	// ActivatedSkills is the per-turn Skill Activation snapshot (ADR-326).
	// Forwarded to AppRunRequest.ActivatedSkills for a single Turn Overlay.
	ActivatedSkills []string
	ContextItems    []ContextItemResolved
	// Media carries inline media payloads from the frontend (paste/drop).
	// Converted to ContentBlock{Type:"file_ref"} in the user message — PNG and
	// PDF are isomorphic, both are files first; vision expansion happens
	// engine-side per model capability (message.MediaPayloadsToBlocks).
	Media []message.MediaPayload
	// ModelBinding is a legacy observability tag: wesgine v1.0 removed the
	// tri-state fallback gate (T-10; brand-honoring lives on CellSpec
	// AllowedModels + LogicalModelGroups). Kept for request-level logging —
	// written to tags["model_binding"] below, never forwarded to RunParams.
	ModelBinding string
	// ThinkingLevel overrides the RunSettings default reasoning depth for
	// this request ("low"/"medium"/"high"/"max"; empty = RunSettings).
	ThinkingLevel string
	// GroupID selects multi-agent group coordination instead of a single
	// agent. Non-empty routes the Run through the engine's group path
	// (RunParams.GroupID + Scope → RunGroupCoordinated); the agentID argument
	// is then ignored, because a group Run has no single agent identity.
	GroupID      string
	isBackground bool // internal: set by RunChatBackground to bypass foreground lock
}

type CodeSnippet struct {
	FilePath  string
	StartLine int
	EndLine   int
	Code      string
	Language  string
}

func providerConfigByID(providers []ProviderConfig, id string) *ProviderConfig {
	if idx := parseBackendIndex(id); idx >= 0 && idx < len(providers) {
		p := providers[idx]
		if strings.TrimSpace(p.APIKey) == "" {
			return nil
		}
		return &p
	}
	for _, item := range providers {
		if strings.EqualFold(item.Name, id) {
			p := item
			if strings.TrimSpace(p.APIKey) == "" {
				return nil
			}
			return &p
		}
	}
	return nil
}

func (s *Service) RunChat(ctx context.Context, sessionID, agentID, userMessage string, opts ChatOpts) (<-chan wesengine.Event, string, error) {
	slog.Info("[engine.RunChat] enter",
		"session_id", sessionID,
		"agent_id", agentID,
		"message_len", len(userMessage),
		"work_dir", opts.WorkDir,
		"provider_id", opts.ProviderID,
		"is_background", opts.isBackground,
		"fg_in_flight", atomic.LoadInt32(&s.fgRunInFlight),
		"activated_skills_count", len(opts.ActivatedSkills),
	)
	if !opts.isBackground {
		if !atomic.CompareAndSwapInt32(&s.fgRunInFlight, 0, 1) {
			slog.Warn("[engine.RunChat] REJECTED.fg_in_flight",
				"session_id", sessionID, "agent_id", agentID)
			return nil, "", errors.New("engine: another chat is in progress")
		}
	}
	s.mu.Lock()
	initialized := s.initialized
	runtime := s.runtime
	cell := s.cell
	workspace := s.workspace
	providers := append([]ProviderConfig(nil), s.cfg.Providers...)
	providerMgr := s.providers
	// Resolve here, under the same lock as the rest of the snapshot: the number
	// the settings page shows is the number this Run enforces. Reading the raw
	// config instead let an unset TaskBudget skip BudgetCheckFn entirely while
	// the page kept displaying the default.
	runSettings := s.cfg.Run.Resolve()
	configuredWindow := runSettings.MaxTokenEstimate
	runThinkingLevel := runSettings.ThinkingLevel
	globalToolDeny := runSettings.ToolDeny
	codeAsm := s.codeAsm
	qualityGate := s.qualityGate
	constraintReg := s.constraintReg
	codeIdx := s.codeIndex
	topologyDetector := s.topologyDetector
	fileStore := s.fileStore
	editEngine := s.editEngine
	wesCatalog := s.wesCatalog
	s.mu.Unlock()

	// CSP: run decay on each chat to retire expired constraints.
	// Skip decay when readiness is low — freshly inferred constraints
	// shouldn't be retired before they've had a chance to prove useful.
	if constraintReg != nil {
		skipDecay := codeIdx != nil && codeIdx.Readiness().Completeness < codeintel.ReadinessMedium
		if !skipDecay {
			if n := constraintReg.RunDecay(); n > 0 {
				slog.Info("[engine] constraint decay", "retired", n)
				s.PersistConstraints()
			}
		}
	}
	slog.Info("[engine.RunChat] ready",
		"initialized", initialized, "has_runtime", runtime != nil)
	if len(providers) == 0 && providerMgr != nil {
		for _, v := range providerMgr.List() {
			providers = append(providers, ProviderConfig{
				Name:          v.ProviderConfig.Name,
				Type:          string(v.ProviderConfig.Type),
				BaseURL:       v.ProviderConfig.BaseURL,
				APIKey:        v.APIKey,
				Model:         v.ProviderConfig.Model,
				IsDefault:     v.IsDefault,
				NoStreamUsage: v.ProviderConfig.NoStreamUsage,
			})
		}
	}
	if cell != nil {
		if providerid.IsWES(opts.ProviderID) && s.wesGrantWithdrawn(ctx) {
			if !opts.isBackground {
				atomic.StoreInt32(&s.fgRunInFlight, 0)
			}
			return nil, "", fmt.Errorf("%w", wesgine.ErrBillingOverdue)
		}
		s.EnsureWesTokenFresh(ctx)
		if err := cell.EnsureActive(ctx); err != nil {
			if !opts.isBackground {
				atomic.StoreInt32(&s.fgRunInFlight, 0)
			}
			return nil, "", fmt.Errorf("engine: cell not active: %w", err)
		}
	}
	if !initialized || runtime == nil {
		if !opts.isBackground {
			atomic.StoreInt32(&s.fgRunInFlight, 0)
		}
		slog.Warn("[engine.RunChat] REJECTED.not_initialized",
			"initialized", initialized, "has_runtime", runtime != nil)
		return nil, "", errors.New("engine: service is not initialized")
	}
	// Reset per-Run state to prevent cross-Run leakage.
	if codeAsm != nil {
		codeAsm.ResetRunState()
	}
	if qualityGate != nil {
		qualityGate.ResetTaskType()
	}

	if sessionID == "" {
		sessionID = generateSessionID()
	}
	workDir := opts.WorkDir
	if workDir == "" {
		workDir = workspace
	}
	if qualityGate != nil && workDir != "" {
		qualityGate.SetWorkDir(workDir)
	}

	// Take diagnostics baseline at Run start to distinguish pre-existing debt from AI-introduced errors.
	if s.diagBaseline != nil && s.diagCache != nil && !s.diagBaseline.IsValid(30*time.Second) {
		s.diagBaseline.Take(s.diagCache)
		slog.Info("[engine.RunChat] diagnostics baseline captured",
			"errors", len(s.diagCache.FilesWithErrors()))
	}
	userContent := []message.ContentBlock{message.NewTextBlock(userMessage)}
	if len(opts.Media) > 0 {
		userContent = append(userContent, message.MediaPayloadsToBlocks(opts.Media)...)
	}
	if len(opts.CodeSnippets) > 0 {
		var sb strings.Builder
		sb.WriteString("\n\n<attached_code>\n")
		for _, sn := range opts.CodeSnippets {
			fmt.Fprintf(&sb, "File: %s (lines %d-%d, %s)\n```%s\n%s\n```\n",
				sn.FilePath, sn.StartLine, sn.EndLine, sn.Language, sn.Language, sn.Code)
		}
		sb.WriteString("</attached_code>")
		userContent = append(userContent, message.NewTextBlock(sb.String()))
	}
	// D3: inject pending debug overlay (exception/crash) as context for the Agent.
	if debugOverlay := s.GetPendingDebugOverlay(); debugOverlay != "" {
		userContent = append(userContent, message.NewTextBlock(debugOverlay))
	}
	// Inject recent terminal errors (panics, test failures, compile errors from terminal).
	if termOverlay := s.GetPendingTerminalOverlay(); termOverlay != "" {
		userContent = append(userContent, message.NewTextBlock(termOverlay))
	}

	tags := map[string]string{}
	// INV-IO-01: WorkDir resolved by engine from (CellDataDir, Actor).
	// Tags["work_dir"] bridge deleted — engine ignores it.
	if opts.ModelBinding != "" {
		tags["model_binding"] = opts.ModelBinding
	}
	// Workspace Intelligence: unified context probing replaces scattered prompt blocks.
	wsCtx := wsintel.Build(workDir, s.WorkspaceRoots())

	// Inject ActiveArea from AttentionTracker (most recent user editing directory).
	var focusFilePath string
	if s.attentionTracker != nil {
		if topFiles := s.attentionTracker.TopN(1); len(topFiles) > 0 && topFiles[0].WasEdited {
			wsCtx.ActiveArea = filepath.Dir(topFiles[0].Path)
			focusFilePath = topFiles[0].Path
		}
	}

	if codeIdx != nil {
		r := codeIdx.Readiness()
		wsCtx.CKG = &wsintel.CKGStatus{
			TotalFiles:   r.TotalFiles,
			IndexedFiles: r.IndexedFiles,
			StaleFiles:   r.StaleFiles,
			Completeness: r.Completeness,
			Indexing:     r.Indexing,
		}
		codeIdx.SetMindMapHints(&codeintel.MindMapHints{
			BuildSystem: wsCtx.Primary.Type,
			Frameworks:  wsCtx.Primary.Frameworks,
		})

		// Focus file reachability: enrich CKGStatus for system prompt degradation warning.
		if focusFilePath != "" {
			focusRel := focusFilePath
			if workDir != "" && filepath.IsAbs(focusFilePath) {
				if rr, err := filepath.Rel(workDir, focusFilePath); err == nil {
					focusRel = rr
				}
			}
			focusRel = codeintel.IndexPath(focusRel)
			if fp, err := codeIdx.FileReachabilityProfile(focusRel); err == nil && fp.TotalFunctions > 0 {
				wsCtx.CKG.FocusFile = focusRel
				wsCtx.CKG.FocusEdgeResolutionRate = fp.EdgeResolutionRate
				wsCtx.CKG.FocusNameReachableCount = fp.NameReachable
				wsCtx.CKG.FocusIsolatedCount = fp.Isolated
			}
		}
	}

	// Workspace intel only: project shape, index status, unknowns. CSE
	// constraints are deliberately absent here — see the note before the
	// request is dispatched.
	//
	// No behavioural mode line either. It was derived by keyword-matching the
	// user's message and injected as an imperative, so a message matching
	// nothing at all — "你是谁?", "继续" — fell through to the default and told
	// the model to write code. Worse, it landed in AppendSystemPrompt, after
	// the Agent's persona, so an architect persona reading "不编写实现代码" was
	// followed by "[Mode: Implement] Write code." with no precedence rule
	// between them. Guessing the user's intent and then issuing orders from
	// the guess is the shape wesgine removed in anti-patterns 255 and 259.
	var wsPrompt string
	wsPrompt += wsCtx.FormatPrompt()
	wsPrompt += "\n" + wsintel.FormatIndexStatus(wsCtx.CKG)
	if unknowns := wsintel.BuildUnknowns(wsCtx); unknowns != "" {
		wsPrompt += "\n" + unknowns
	}

	// R10: inject Behavioral Baseline readiness so LLM knows regression detection is active.
	if qualityGate != nil && workDir != "" {
		if br := qualityGate.BaselineReadiness(ctx, workDir); br != nil && br.HasBaseline {
			wsPrompt += fmt.Sprintf("\n[Test Baseline]\n%d tests baselined. Regression detection (L2.5) active — modifications that break existing tests will be caught automatically.", br.BaselineTests)
		}
	}

	skillHints := wsintel.BuildSkillHints(wsCtx)

	activatedSkills := opts.ActivatedSkills
	if autoSkills := wsintel.BuildAutoActivateSkills(wsCtx); len(autoSkills) > 0 && len(activatedSkills) == 0 {
		activatedSkills = autoSkills
	}

	req := wesgine.AppRunRequest{
		Actor:     "local",
		SessionID: sessionID,
		Messages: []message.Message{
			{
				Role:    message.RoleUser,
				Content: userContent,
			},
		},
		// The CKG tools (search_symbols / find_callers / find_references /
		// project_map / impact_analysis) are all graph-domain Core, so naming
		// the domain makes them schema-visible from turn 0 without the model
		// having to discover them through tool_search.
		//
		// This used to be a HostToolAllow list, which is where the bugs came
		// from: HostToolAllow is an allowlist, so surfacing five tools meant
		// re-listing every baseline tool we did not intend to remove, and
		// anything left off vanished with no error anywhere. `plan` was missing
		// that way (absent from 91% of runs), and after it was patched in,
		// `cron_manage` was still missing — which is why a user could not get
		// a scheduled task created by asking for one, and the model fell back
		// to exec. Declaring the domain states the actual requirement and
		// leaves the rest of the registry alone, so the next tool registered
		// on this Cell is visible without an edit here.
		ActiveDomains:      []string{"graph"},
		Resume:             opts.Resume,
		ActivatedSkills:    activatedSkills,
		SkillHints:         skillHints,
		AppendSystemPrompt: wsPrompt,
		Tags:               tags,
		// Per-run exec CWD: the IDE's active folder (opts.WorkDir), not the
		// Cell-default folders[0]. Multi-root workspaces: scripts run in the
		// project the user is editing, while Cell identity stays on folders[0].
		PrimaryRoot: workDir,
	}
	if len(globalToolDeny) > 0 {
		req.ToolDeny = append(req.ToolDeny, globalToolDeny...)
	}
	if opts.ThinkingLevel != "" {
		req.ThinkingLevel = opts.ThinkingLevel
	} else if runThinkingLevel != "" {
		req.ThinkingLevel = runThinkingLevel
	}
	// TaskBudget (config unit: k tokens) → engine raw tokens. The engine
	// injects an advisory "converge" note at 85%; the BudgetCheckFn below
	// hard-stops at 100% with a wind-down grace turn (budget_exhausted).
	// Engine never terminates on counters alone — the app declares its
	// commercial limit here (INV-TERM-01 / INV-TERM-04). Fail-open is the
	// engine's concern: a panicking fn is treated as BudgetOK.
	// Unconditional: Resolve guarantees a positive budget, so there is no
	// "unlimited" shape to branch on. The old `if budgetK > 0` guard read the raw
	// config, and an unset budget skipped this whole block — every Run ran with
	// no ceiling while the settings page displayed one.
	budgetTokens := runSettings.TaskBudget * 1000
	req.TaskBudget = budgetTokens
	req.BudgetCheckFn = func(_ context.Context, usage wesengine.TokenSnapshot) wesengine.BudgetStatus {
		if usage.TotalOutputTokens >= budgetTokens {
			return wesengine.BudgetExhausted
		}
		return wesengine.BudgetOK
	}
	// --- TARGET selection: the engine's three-way dispatch (INV-ROUTE-01/02) ---
	//
	// Exactly one shape is sent:
	//   GroupID + Scope → RunGroupCoordinated (serial multi-agent)
	//   AgentID         → direct named-agent run
	//
	// AgentID is left empty for group runs on purpose. engine/params.go calls
	// the two mutually exclusive but its doc ("AgentID takes precedence") and
	// internal/cell/boot.go's dispatch order ("GroupID checked first")
	// disagree, so sending both would make the target depend on which side of
	// that contradiction the linked engine happens to implement.
	isGroupRun := opts.GroupID != ""
	if isGroupRun {
		members, memErr := s.groupMemberIDs(ctx, opts.GroupID)
		if memErr != nil {
			return nil, "", memErr
		}
		// req.GroupID is the group RUN's session identity, not the group
		// entity id: the engine derives per-member sub-sessions as
		// `GroupID + "/" + agentID`. Keying it on sessionID keeps each
		// conversation's member history separate, so talking to the same
		// group in two sessions does not blend the members' context.
		req.GroupID = sessionID
		req.Scope = members
		slog.Info("engine: RunChat group target",
			"session_id", sessionID, "group_id", opts.GroupID, "members", members)
	} else {
		if agentID == "" {
			agentID = "default"
		}
		req.AgentID = agentID
	}

	// namedAgent is the single Agent whose stored config (system prompt, tool
	// ACL, model override) applies to this Run. Empty for group runs — the
	// engine loads each member's own config from the AgentStore — and for the
	// anonymous "default" target, which has no stored row.
	namedAgent := ""
	if !isGroupRun && agentID != "default" {
		namedAgent = agentID
	}

	if namedAgent != "" {
		if agentView, err := s.GetAgent(ctx, namedAgent); err == nil && agentView != nil {
			if agentView.SystemPrompt != "" {
				req.SystemPrompt = agentView.SystemPrompt
			}
			if len(agentView.ToolDeny) > 0 {
				req.ToolDeny = append(req.ToolDeny, agentView.ToolDeny...)
			}
			if len(agentView.ToolAllow) > 0 {
				req.ToolAllow = agentView.ToolAllow
			}
			if agentView.TimeoutSeconds > 0 {
				req.TimeoutSeconds = agentView.TimeoutSeconds
			}
			if agentView.MaxTurns > 0 && req.TaskBudget == 0 {
				req.TaskBudget = agentView.MaxTurns * 2000
			}
			slog.Info("engine: RunChat agent config resolved",
				"agent_id", agentID,
				"has_system_prompt", agentView.SystemPrompt != "",
				"tool_deny", agentView.ToolDeny,
				"model_override", agentView.Model,
			)
		}
	}
	// A mid-session role switch needs saying out loud: SystemPrompt above is
	// already the new persona, but the prepended history still holds the
	// previous persona describing itself, and that wins on proximity.
	// Group runs have no single persona to switch to, and the engine writes
	// its own per-member group prompt (buildGroupSystemPrompt), so a notice
	// here would describe a role nobody is playing.
	if !isGroupRun {
		if notice := s.agentSwitchNotice(ctx, sessionID, agentID); notice != "" {
			req.AppendSystemPrompt += "\n" + notice
			slog.Info("engine: RunChat agent switched mid-session",
				"session_id", sessionID, "agent_id", agentID)
		}
	}
	if len(opts.ActivatedSkills) > 0 {
		slog.Info("engine: RunChat activated_skills",
			"session_id", sessionID,
			"agent_id", req.AgentID,
			"skills", opts.ActivatedSkills,
		)
	}

	// INV-MODEL-01: model must be explicit. Cloud-managed channels (wes:/org:)
	// send the weisyn proxy slug from catalog — never the picker display name.
	catalogModel := ""
	var byokModel string
	if opts.ProviderID != "" {
		if providerid.IsWES(opts.ProviderID) && wesCatalog != nil {
			modelName := providerid.Pin(opts.ProviderID)
			if wesCfg, ok := s.wesConfigForRun(ctx, modelName); ok {
				req.ProviderName = wesCfg.Name
				catalogModel = wesCfg.Model
			} else {
				req.ProviderName = opts.ProviderID
			}
		} else if providerid.IsOrg(opts.ProviderID) && wesCatalog != nil {
			// Org model (company Key via weisyn proxy): same JWT+baseURL
			// transport as wes:, slug org:{orgID}:{providerID} routes on the
			// proxy side (enterprise-org.md §5.6). Keys never land locally.
			if orgCfg, ok := s.orgConfigForRun(ctx, opts.ProviderID); ok {
				req.ProviderName = orgCfg.Name
				catalogModel = orgCfg.Model
			} else {
				req.ProviderName = opts.ProviderID
			}
		} else if pc := providerConfigByID(providers, opts.ProviderID); pc != nil {
			// INV-ERROR-ATTRIBUTION: pin ProviderName for A-type EventError
			// ([Provider] message). Previously only Model was filled, so
			// route-select balance failures emitted ProviderMessage without
			// Provider and the UI dropped the [DeepSeek] prefix.
			if req.ProviderName == "" {
				req.ProviderName = pc.Name
				if req.ProviderName == "" {
					req.ProviderName = opts.ProviderID
				}
			}
			byokModel = pc.Model
		} else if req.ProviderName == "" {
			req.ProviderName = opts.ProviderID
		}
	}
	req.Model = providerid.RunModel(opts.ProviderID, catalogModel, opts.Model)
	if req.Model == "" {
		req.Model = byokModel
	}

	// Agent modelOverride: apply only when user didn't explicitly select a model.
	if req.Model == "" && namedAgent != "" {
		if agentView, err := s.GetAgent(ctx, namedAgent); err == nil && agentView != nil && agentView.Model != "" {
			req.Model = agentView.Model
			slog.Info("engine: RunChat using agent modelOverride",
				"agent_id", namedAgent, "model", agentView.Model)
		}
	}
	// INV-MODEL-01: model must be explicit. If the user didn't select one,
	// the application layer resolves the default — never let the engine
	// guess implicitly.
	if req.Model == "" {
		if defProv, defModel := s.DefaultModel(ctx); defModel != "" {
			req.Model = defModel
			if req.ProviderName == "" {
				req.ProviderName = defProv
			}
			slog.Info("[engine.RunChat] using default model (INV-MODEL-01)",
				"model", defModel, "provider", defProv, "session_id", sessionID)
		}
	}
	if req.Model == "" {
		// Wrap the engine sentinel so ClassifyRunError attaches the same
		// ErrorCode the engine would for the identical condition, instead of
		// this sentence being text-matched back apart downstream.
		return nil, "", fmt.Errorf("configure at least one LLM provider in settings: %w", wesgine.ErrNoProvider)
	}

	req.MaxTokenEstimate = economicWindow(configuredWindow, req.Model)

	// No constraint block goes into AppendSystemPrompt (INV-CSE-15). A system
	// prompt is one static prefix for the whole Run, so a registry-wide dump
	// leaked every root's rules into every edit — in a seven-root workspace an
	// edit to wesclaw arrived carrying wesgine/wescode/wesui invariants. Whole-
	// registry text also breaks prompt caching on every confidence tick.
	// Constraints reach the model through two scoped channels instead: the CSE
	// Overlay (focus file ∩ checker, user message) and PreWrite FAIL advisories.

	// Worker differentiated CycleDetect: inject per-Run override for child runs.
	// wesgine supports AppRunRequest.CycleDetectOverride (per-Run).
	if role := tags["delegation_role"]; role != "" {
		if preset := CyclePresetForRole(role); preset != nil {
			req.CycleDetectOverride = preset
		}
	}

	var allowPaths []string
	if len(opts.AllowPaths) > 0 {
		allowPaths = append(allowPaths, opts.AllowPaths...)
	}
	for _, root := range s.WorkspaceRoots() {
		if root != workDir {
			allowPaths = append(allowPaths, root)
		}
	}
	if topologyDetector != nil {
		discovered := topologyDetector.Detect(ctx, workDir)
		allowPaths = append(allowPaths, discovered...)
	}
	if len(opts.FilePaths) > 0 {
		allowPaths = append(allowPaths, opts.FilePaths...)
		req.MediaFiles = opts.FilePaths
	}
	if len(opts.FileIDs) > 0 && fileStore != nil {
		uc := req.Messages[0].Content
		for _, fid := range opts.FileIDs {
			m := fileStore.Get(fid)
			if m == nil {
				continue
			}
			uc = append(uc, message.ContentBlock{
				Type:     "file_ref",
				ID:       m.ID,
				Name:     m.FileName,
				Source:   m.RawPath,
				MIMEType: m.MIMEType,
			})
			allowPaths = append(allowPaths, m.RawPath)
		}
		req.Messages[0].Content = uc
	}
	// Inject structured context items (from @ context picker) as first-class
	// context_ref blocks (INV-CTX-REF-01/02/03). The user message carries
	// provenance (source/label/uri/detail) plus this-turn-only payload for
	// non-file sources. NO payload is folded into user text blocks — history
	// renders chips, never payload, and the engine strips payload at
	// persistence. File/folder refs keep per-run visibility via AllowPaths +
	// MediaFiles (content enters this turn only, by design).
	for _, ci := range opts.ContextItems {
		meta := message.ContextRefMeta{
			Source: ci.SourceID,
			Label:  ci.Label,
			Detail: ci.Detail,
		}
		blockID := ci.ID
		switch {
		case ci.FilePath != "":
			allowPaths = append(allowPaths, ci.FilePath)
			meta.URI = ci.FilePath
			if ci.SourceID == "file" || ci.SourceID == "folder" {
				req.MediaFiles = append(req.MediaFiles, ci.FilePath)
			}
		case ci.CodeSnippet != nil:
			meta.URI = ci.CodeSnippet.FilePath
			meta.Payload = fmt.Sprintf("File: %s (lines %d-%d, %s)\n```%s\n%s\n```",
				ci.CodeSnippet.FilePath, ci.CodeSnippet.StartLine, ci.CodeSnippet.EndLine,
				ci.CodeSnippet.Language, ci.CodeSnippet.Language, ci.CodeSnippet.Code)
		case ci.Content != "":
			meta.Payload = ci.Content
		}
		if meta.Label == "" {
			meta.Label = defaultContextRefLabel(ci)
		}
		if blockID == "" {
			blockID = meta.Source + ":" + meta.URI
		}
		req.Messages[0].Content = append(req.Messages[0].Content, message.NewContextRefBlock(blockID, meta))
	}

	if len(opts.ContextItems) > 0 {
		var refLabels []string
		for _, ci := range opts.ContextItems {
			refLabels = append(refLabels, ci.SourceID+":"+ci.ID)
		}
		tags["context_refs"] = strings.Join(refLabels, "|")
	}

	// Inject activated skill names as skill_ref blocks into the user message
	// so they persist in wes_messages and render as chips in the user bubble.
	// Only provenance (the name) is stored — skill body is injected separately
	// by the engine's assemble stage and is NOT persisted (same pattern as
	// context_ref: provenance survives, payload does not).
	for _, name := range activatedSkills {
		req.Messages[0].Content = append(req.Messages[0].Content, message.ContentBlock{
			Type: "skill_ref",
			Name: name,
		})
	}

	// Path boundaries go on req.AllowPaths. HostToolAllow is a tool-NAME
	// allowlist, so a path there matches no tool and empties the whole
	// toolset; the engine now refuses such a request at ingress
	// (wesgine.ErrToolNameExpected) rather than running it toolless.
	//
	// File-system path boundaries are enforced by:
	//   1. CellSpec.HostEnvironment (host) — provides WorkDir to ToolContext
	//   2. INV-IO-01: engine resolves WorkDir from (CellDataDir, Actor)
	//   3. CellSpec.DenyPaths / EnginePaths — boot-level path policy
	dedupedPaths := codeintel.DedupPaths(allowPaths)

	// Pass per-run extra paths to the engine for pathaccess authorization.
	// CellSpec.AgentDefaults covers workspace roots (via UpdateWorkspacePaths),
	// but topology-discovered dirs and context-item paths are per-run.
	req.AllowPaths = dedupedPaths

	if codeAsm != nil {
		state := codeAsm.EditorState()
		state.WorkspaceRoots = append([]string{workDir}, dedupedPaths...)
		codeAsm.UpdateEditorState(state)
	}

	if qualityGate != nil {
		if opts.TestTimeout > 0 {
			qualityGate.SetTestTimeout(opts.TestTimeout)
		}
		if strings.HasPrefix(userMessage, "/test") {
			qualityGate.SetForceL2(true)
			msg := strings.TrimSpace(strings.TrimPrefix(userMessage, "/test"))
			if msg == "" {
				msg = "Run tests for the current changes"
			}
			req.Messages = []message.Message{{
				Role:    message.RoleUser,
				Content: []message.ContentBlock{message.NewTextBlock(msg)},
			}}
		} else {
			qualityGate.SetForceL2(false)
		}
	}

	if editEngine != nil {
		editEngine.ResetTracking()

		var snapshotPaths []string
		if codeAsm != nil {
			if state := codeAsm.EditorState(); state.FocusFile != "" {
				snapshotPaths = append(snapshotPaths, state.FocusFile)
			}
		}
		snapshotPaths = append(snapshotPaths, opts.FilePaths...)
		if len(snapshotPaths) > 0 {
			editEngine.SnapshotFiles(snapshotPaths)
		}

		// Create checkpoint + changeset for this Run (INV-EDIT-23).
		if err := editEngine.BeginRun(sessionID); err != nil {
			slog.Warn("[engine] checkpoint creation failed", "err", err)
		}
	}

	slog.Info("[engine.RunChat] runtime.Run.calling",
		"session_id", sessionID, "model", req.Model, "provider_name", req.ProviderName, "agent_id", req.AgentID)
	rawCh, runErr := runtime.Run(ctx, req)
	if runErr != nil {
		if !opts.isBackground {
			atomic.StoreInt32(&s.fgRunInFlight, 0)
		}
		return nil, "", fmt.Errorf("engine: runtime run: %w", runErr)
	}
	slog.Info("[engine.RunChat] runtime.Run.returned",
		"session_id", sessionID, "channel_cap", cap(rawCh))
	wrappedCh := make(chan wesengine.Event, cap(rawCh))
	isBg := opts.isBackground
	go func() {
		defer close(wrappedCh)
		count := 0
		for ev := range rawCh {
			count++
			wrappedCh <- ev
			// G-decouple: release the foreground slot as soon as the
			// terminal event arrives instead of waiting for rawCh to
			// fully drain. Settlement is now asynchronous (Cell-level
			// SettlementWorker): EventRunEnd is emitted immediately
			// after the settlement job is enqueued, so the next chat
			// message enters right away — the engine-side entry barrier
			// (WaitSettlement, SettlementWaitBudget) waits for a fresh
			// anchor and degrades to the previous one on timeout.
			if !isBg && isTerminalRunEvent(ev.Type) {
				atomic.StoreInt32(&s.fgRunInFlight, 0)
			}
		}
		slog.Info("[engine.RunChat] rawCh.drained",
			"session_id", sessionID, "event_count", count, "is_background", isBg)
		if !isBg {
			// Idempotent fallback: covers abnormal paths where no
			// terminal event reached the wrapper (e.g. panic_stop).
			atomic.StoreInt32(&s.fgRunInFlight, 0)
		}
		// Anti-fragile: after Run completes, promote constraints that were
		// referenced (code passed verification) and demote those whose warnings
		// were ignored but code still passed.
		s.applyConstraintLearning()
	}()
	return wrappedCh, sessionID, nil
}

// applyConstraintLearning processes accumulated constraint hits from PreWriteCheck
// after a Run completes. Promotes constraints that were obeyed (warned + no regression).
// Demotes constraints that fire excessively without ever causing regressions
// (INV-CSE-06: self-adaptive decay prevents constraint bloat).
func (s *Service) applyConstraintLearning() {
	hadRegression := atomic.SwapInt32(&s.regressionDetected, 0) != 0

	s.mu.Lock()
	reg := s.constraintReg
	tracker := s.constraintHits
	s.mu.Unlock()

	if reg == nil || tracker == nil {
		return
	}

	hits := tracker.Drain()
	if len(hits) == 0 {
		return
	}

	if hadRegression {
		slog.Info("[engine] constraint learning skipped (regression detected)",
			"hits", len(hits))
		return
	}

	promoted := 0
	demoted := 0
	for _, h := range hits {
		if !h.Warned {
			continue
		}
		c := reg.Get(h.ConstraintID)
		if c == nil {
			continue
		}
		// Promote: constraint was warned, AI obeyed, QG passed → constraint is useful.
		reg.PromoteByID(h.ConstraintID, 0.05)
		promoted++

		// Demote heuristic: if a constraint has been warned (UsageCount) many
		// times but confidence is still low (never confirmed by user or regression),
		// it may be overly noisy. Soft demote when warned > 10 times without
		// ever being confirmed (confidence < 1.0) or triggering regression.
		if c.UsageCount > 10 && c.Confidence < 0.9 && c.Source == "inferred" {
			reg.DemoteByID(h.ConstraintID, 0.02)
			demoted++
		}
	}
	if promoted > 0 || demoted > 0 {
		slog.Info("[engine] constraint learning after run", "promoted", promoted, "demoted", demoted)
		s.PersistConstraints()
	}
}

// defaultContextRefLabel derives a chip label when the picker did not send
// one (server-resolved items carry only id+sourceId).
func defaultContextRefLabel(ci ContextItemResolved) string {
	if p := ci.FilePath; p != "" {
		if base := filepath.Base(p); base != "." && base != string(filepath.Separator) {
			return base
		}
		return p
	}
	if ci.CodeSnippet != nil {
		return filepath.Base(ci.CodeSnippet.FilePath)
	}
	return ci.SourceID + ":" + ci.ID
}

func generateSessionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "ses-" + hex.EncodeToString(b)
}

// isTerminalRunEvent reports whether an event type marks the end of a Run's
// event stream. EventRunEnd is the authoritative terminal signal — the engine
// emits it immediately after the settlement job is enqueued (loop.go), so
// releasing fgRunInFlight at that point lets the next chat message enter
// right away while the engine-side barrier (WaitSettlement + SettlementWaitBudget)
// waits for a fresh anchor. done/error/interrupted are defensive early
// releases for abnormal paths — there the settlement job may not be enqueued
func isTerminalRunEvent(t wesengine.EventType) bool {
	switch t {
	case wesengine.EventRunEnd, wesengine.EventDone, wesengine.EventError, wesengine.EventInterrupted:
		return true
	default:
		return false
	}
}
