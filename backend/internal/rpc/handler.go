package rpc

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/weisyn/weisyn/sdk/auth"
	"github.com/weisyn/wescode/internal/codeintel"
	appengine "github.com/weisyn/wescode/internal/engine"
	"github.com/weisyn/wescode/internal/failure"
	"github.com/weisyn/wescode/internal/notify"
)

type Handler struct {
	engine     *appengine.Service
	auth       *auth.Service
	notifier   *Server
	onShutdown func()
	appCtx     context.Context // process-level context; survives individual RPC requests

	editTxMu sync.Mutex
	// txId → 该事务真实触达的全部路径（跨 chat send 存活，供 accept/reject 用）。
	// 是切片不是单值：多文件 apply_patch 一个 txId 对应 N 个路径，而此前这里存
	// 的是展示串 "a.go (+2 files)"，于是 per-file reject 按一个不存在的路径寻址
	// 并静默失效（EE-10）。
	editTxPaths map[string][]string

	statsCacheMu   sync.Mutex
	statsCacheResp *codeIntelStatsResponse
	statsCacheTime time.Time

	chanSubMu     sync.Mutex
	chanSubCancel context.CancelFunc
}

func NewHandler(engineSvc *appengine.Service, authSvc *auth.Service, notifier *Server, onShutdown func(), opts ...HandlerOption) *Handler {
	h := &Handler{
		engine:      engineSvc,
		auth:        authSvc,
		notifier:    notifier,
		onShutdown:  onShutdown,
		appCtx:      context.Background(),
		editTxPaths: make(map[string][]string),
	}
	for _, o := range opts {
		o(h)
	}

	// Wire context/snapshot notifications from CodeAssembler → Extension.
	engineSvc.SetSnapshotCallback(func(snapshot codeintel.ContextSnapshot) {
		if err := notifier.Notify(notify.ContextSnapshot, snapshot); err != nil {
			slog.Warn("[codeintel] context/snapshot notify failed", "error", err)
		}
		slog.Info("[codeintel] context/snapshot pushed",
			"fragments", len(snapshot.Fragments),
			"tokensUsed", snapshot.TokenUsed,
			"tokenBudget", snapshot.TokenBudget,
			"taskType", snapshot.TaskType,
		)
	})

	// Wire RPC notifier into the Host so VSCodeShellProvider can send
	// terminal/create notifications to the Extension.
	engineSvc.SetNotifier(notifier.Notify)

	return h
}

// HandlerOption configures optional Handler behaviour.
type HandlerOption func(*Handler)

// WithAppContext sets the process-level context used for engine boot.
// Cell supervisors bind to this context so they survive individual RPC
// request lifecycles. Must be called before any initialize RPC arrives.
func WithAppContext(ctx context.Context) HandlerOption {
	return func(h *Handler) { h.appCtx = ctx }
}

// configModeAllowed lists RPC methods available in Config Mode (no Cell).
// These methods only need the Go process running, not a Cell.
var configModeAllowed = map[string]bool{
	"initialize":                 true,
	"auth/me":                    true,
	"auth/login":                 true,
	"auth/register":              true,
	"auth/sendCode":              true,
	"auth/resetPassword":         true,
	"auth/logout":                true,
	"auth/updateProfile":         true,
	"auth/changePassword":        true,
	"auth/deleteAccount":         true,
	"cell/info":                  true,
	"shutdown":                   true,
	"sidebar/getWesBilling":      true,
	"sidebar/listWesProviders":   true,
	"sidebar/testWesProvider":    true,
	"providerCatalog":            true,
	"sidebar/listProviders":      true,
	"sidebar/addProvider":        true,
	"sidebar/updateProvider":     true,
	"sidebar/deleteProvider":     true,
	"sidebar/setDefaultProvider": true,
	"sidebar/testProvider":       true,
	"sidebar/testProviderByName": true,
	// Company / WES catalog. Frontend CONFIG_MODE_SAFE already includes
	// availableModels; without this entry Config Mode returns no_workspace
	// and the settings page mis-renders it as an org-catalog failure.
	"sidebar/availableModels":  true,
	"sidebar/refreshModels":    true,
	"diagnostics/report":       true,
	"_diag":                    true,
	"sidebar/developerProfile": true,
}

// authExempt lists RPC methods that do not require an active login session.
// sidebar/availableModels must be exempt so GateGuard can detect BYOK
// providers and let users with self-hosted keys use Chat without weisyn login.
//
// IDE infrastructure RPCs (editor/*, lsp/*, background/list) are exempt
// because they carry no actor identity — auth-gating them breaks Buffer
// Overlay (Agent reads stale disk instead of editor content) and LSP Bridge
// (CKG loses IDE-provided references/definitions).
var authExempt = map[string]bool{
	"initialize":              true,
	"_diag":                   true,
	"shutdown":                true,
	"auth/me":                 true,
	"auth/login":              true,
	"auth/register":           true,
	"auth/sendCode":           true,
	"auth/resetPassword":      true,
	"auth/logout":             true,
	"cell/info":               true,
	"sidebar/getWesBilling":   true,
	"sidebar/listProviders":   true,
	"sidebar/availableModels": true,
	"sidebar/refreshModels":   true,
	"sidebar/addProvider":     true,
	"sidebar/updateProvider":  true,
	"sidebar/deleteProvider":  true,
	"sidebar/testProvider":    true,
	"providerCatalog":         true,
	"diagnostics/report":      true,
	"editor/fileChanged":      true,
	"editor/didSave":          true,
	"editor/didChange":        true,
	"editor/didClose":         true,
	"lsp/response":            true,
	"background/list":         true,
}

func (h *Handler) Register(server *Server) {
	server.SetMiddleware(func(ctx context.Context, method string, next MethodHandler, req Request) (any, *RPCError) {
		// 1. configModeAllowed methods pass through unconditionally —
		//    their handlers decide behavior in all states.
		if configModeAllowed[method] {
			if !authExempt[method] && h.auth != nil && !h.auth.HasSession(ctx) {
				return nil, errFailure(failure.ReasonLoginRequired)
			}
			return next(ctx, req)
		}
		// 2. Config Mode: no workspace → reject early with actionable error.
		//    "Open a folder" is the fix, regardless of login state.
		if h.engine.ConfigMode() {
			return nil, errFailure(failure.ReasonNoWorkspace)
		}
		// 3. Auth gate: workspace exists but user not logged in.
		if !authExempt[method] && h.auth != nil {
			if !h.auth.HasSession(ctx) {
				return nil, errFailure(failure.ReasonLoginRequired)
			}
		}
		// 4. Cell health: workspace Cell may be booting or degraded. Both arms
		//    are one Reason: the engine can tell "never booted" from "cooled
		//    and warming", the user cannot act on the difference, and the two
		//    sentences here differed only in phrasing the same wait.
		cell := h.engine.Cell()
		if cell == nil {
			return nil, errFailure(failure.ReasonEngineNotReady)
		}
		if err := cell.EnsureActive(ctx); err != nil {
			return nil, errFailureDetail(failure.ReasonEngineNotReady, err.Error())
		}
		return next(ctx, req)
	})

	// Auth
	server.Register("auth/me", h.handleAuthMe)
	server.Register("auth/login", h.handleAuthLogin)
	server.Register("auth/register", h.handleAuthRegister)
	server.Register("auth/sendCode", h.handleAuthSendCode)
	server.Register("auth/resetPassword", h.handleAuthResetPassword)
	server.Register("auth/logout", h.handleAuthLogout)
	server.Register("auth/updateProfile", h.handleAuthUpdateProfile)
	server.Register("auth/changePassword", h.handleAuthChangePassword)
	server.Register("auth/deleteAccount", h.handleAuthDeleteAccount)
	// Engine
	server.Register("initialize", h.handleInitialize)
	server.Register("cell/info", h.handleCellInfo)
	server.Register("chat/send", h.handleChatSend)
	server.Register("shutdown", h.handleShutdown)
	server.Register("sidebar/listConversations", h.handleListConversations)
	server.Register("sidebar/deleteConversation", h.handleDeleteConversation)
	server.Register("sidebar/listConversationMessages", h.handleListConversationMessages)
	server.Register("providerCatalog", h.handleProviderCatalog)
	server.Register("sidebar/availableModels", h.handleAvailableModels)
	server.Register("sidebar/refreshModels", h.handleRefreshModels)
	server.Register("sidebar/listProviders", h.handleListProviders)
	server.Register("sidebar/listWesProviders", h.handleListWesProviders)
	server.Register("sidebar/getWesBilling", h.handleGetWesBilling)
	server.Register("sidebar/addProvider", h.handleAddProvider)
	server.Register("sidebar/updateProvider", h.handleUpdateProvider)
	server.Register("sidebar/deleteProvider", h.handleDeleteProvider)
	server.Register("sidebar/setDefaultProvider", h.handleSetDefaultProvider)
	server.Register("sidebar/testProvider", h.handleTestProvider)
	server.Register("sidebar/testProviderByName", h.handleTestProviderByName)
	server.Register("sidebar/testWesProvider", h.handleTestWesProvider)
	server.Register("sidebar/listAgents", h.handleListAgents)
	server.Register("sidebar/getAgent", h.handleGetAgent)
	server.Register("sidebar/createAgent", h.handleCreateAgent)
	server.Register("sidebar/updateAgent", h.handleUpdateAgent)
	server.Register("sidebar/deleteAgent", h.handleDeleteAgent)
	server.Register("sidebar/listTools", h.handleListTools)
	server.Register("sidebar/agentToolBudget", h.handleAgentToolBudget)
	server.Register("sidebar/listArtifacts", h.handleListArtifacts)
	server.Register("sidebar/workspaceTree", h.handleWorkspaceTree)
	server.Register("sidebar/deleteWorkspaceFile", h.handleDeleteWorkspaceFile)
	server.Register("sidebar/agentAudit", h.handleAgentAudit)
	server.Register("sidebar/listSkills", h.handleListSkills)
	server.Register("sidebar/createSkill", h.handleCreateSkill)
	server.Register("sidebar/getSkill", h.handleGetSkill)
	server.Register("sidebar/updateSkill", h.handleUpdateSkill)
	server.Register("sidebar/createSkillFull", h.handleCreateSkillFull)
	server.Register("sidebar/toggleSkill", h.handleToggleSkill)
	server.Register("sidebar/deleteSkill", h.handleDeleteSkill)
	server.Register("sidebar/reloadSkills", h.handleReloadSkills)
	// Skill Workshop (multi-file + repair)
	server.Register("sidebar/uploadSkillFile", h.handleUploadSkillFile)
	server.Register("sidebar/listSkillResources", h.handleListSkillResources)
	server.Register("sidebar/repairSkill", h.handleRepairSkill)
	// Skill Health & Status
	server.Register("sidebar/skillHealth", h.handleSkillHealth)
	server.Register("sidebar/skillStatus", h.handleSkillStatus)
	server.Register("sidebar/checkSkillBindings", h.handleCheckSkillBindings)
	server.Register("sidebar/validateSkill", h.handleValidateSkill)
	server.Register("sidebar/skillCatalog", h.handleSkillCatalog)
	server.Register("sidebar/skillHubInstall", h.handleSkillHubInstall)
	server.Register("sidebar/visibleSkills", h.handleVisibleSkills)
	server.Register("sidebar/listPresetAgents", h.handleListPresetAgents)
	server.Register("sidebar/getRunSettings", h.handleGetRunSettings)
	server.Register("sidebar/updateRunSettings", h.handleUpdateRunSettings)
	server.Register("chat/cancel", h.handleChatCancel)
	server.Register("hitl/respond", h.handleHITLRespond)
	server.Register("chat/getSessionPlan", h.handleGetSessionPlan)
	// Edit accept/reject + stream preview + metrics
	server.Register("edit/accept", h.handleEditAccept)
	server.Register("edit/reject", h.handleEditReject)
	server.Register("edit/metrics", h.handleEditMetrics)
	server.Register("edit/changeset", h.handleEditChangeset)
	server.Register("feedback/implicit", h.handleImplicitFeedback)
	// Code Intelligence
	server.Register("editor/state", h.handleEditorState)
	server.Register("editor/didSave", h.handleEditorDidSave)
	server.Register("editor/didChange", h.handleEditorDidChange)
	server.Register("editor/didClose", h.handleEditorDidClose)
	server.Register("editor/fileChanged", h.handleEditorFileChanged)
	server.Register("index/file", h.handleIndexFile)
	server.Register("completion/request", h.handleCompletionRequest)
	// Files
	server.Register("files/upload", h.handleFileUpload)
	server.Register("files/importLocal", h.handleFileImportLocal)
	server.Register("files/content", h.handleFileContent)
	// SCM
	server.Register("scm/info", h.handleSCMInfo)
	server.Register("scm/stage", h.handleSCMStage)
	server.Register("scm/commit", h.handleSCMCommit)
	// Context Search
	server.Register("context/sources", h.handleContextSources)
	server.Register("context/search", h.handleContextSearch)
	server.Register("context/resolve", h.handleContextResolve)
	// Diagnostics
	server.Register("debug/status", h.handleDebugStatus)
	server.Register("debug/metrics", h.handleDebugMetrics)
	server.Register("debug/readiness", h.handleReadiness)
	server.Register("verification/metrics", h.handleVerificationMetrics)
	// Constraint management (CSP Phase 2)
	server.Register("constraint/list", h.handleConstraintList)
	server.Register("constraint/confirm", h.handleConstraintConfirm)
	server.Register("constraint/dismiss", h.handleConstraintDismiss)
	// Background Agent
	server.Register("background/start", h.handleBackgroundStart)
	server.Register("background/list", h.handleBackgroundList)
	server.Register("background/merge", h.handleBackgroundMerge)
	server.Register("background/discard", h.handleBackgroundDiscard)
	server.Register("background/cancel", h.handleBackgroundCancel)

	// Groups
	server.Register("sidebar/listGroups", h.handleListGroups)
	server.Register("sidebar/createGroup", h.handleCreateGroup)
	server.Register("sidebar/deleteGroup", h.handleDeleteGroup)
	server.Register("sidebar/renameGroup", h.handleRenameGroup)
	server.Register("sidebar/updateGroupMembers", h.handleUpdateGroupMembers)

	// Knowledge Base (auto-indexed workspace documents via DocSync)
	server.Register("sidebar/listKBFiles", h.handleListKBFiles)
	server.Register("sidebar/kbRescan", h.handleKBRescan)
	server.Register("sidebar/kbSearch", h.handleKBSearch)
	server.Register("sidebar/kbStats", h.handleKBStats)
	server.Register("sidebar/kbClear", h.handleKBClear)
	server.Register("sidebar/kbReindexQuarantined", h.handleKBReindexQuarantined)
	server.Register("sidebar/kbReindexFile", h.handleKBReindexFile)
	server.Register("sidebar/kbDeleteFile", h.handleKBDeleteFile)
	server.Register("sidebar/kbReindexErrors", h.handleKBReindexErrors)

	// Memory management
	server.Register("sidebar/importMemory", h.handleImportMemory)
	server.Register("sidebar/listMemory", h.handleListMemory)
	server.Register("sidebar/searchMemory", h.handleSearchMemory)
	server.Register("sidebar/deleteMemory", h.handleDeleteMemory)
	server.Register("sidebar/memoryCounts", h.handleMemoryCounts)
	server.Register("sidebar/clearMemoryLayer", h.handleClearMemoryLayer)
	server.Register("sidebar/putMemory", h.handlePutMemory)
	server.Register("sidebar/memoryGC", h.handleMemoryGC)

	// Observe: runs + tokens
	server.Register("sidebar/listRuns", h.handleListRuns)
	server.Register("sidebar/listRunsByAgent", h.handleListRunsByAgent)
	server.Register("sidebar/getRunDetail", h.handleGetRunDetail)
	server.Register("sidebar/tokenAggregates", h.handleTokenAggregates)
	server.Register("sidebar/tokenTimeSeries", h.handleTokenTimeSeries)
	server.Register("sidebar/tokenAggregateByAgent", h.handleTokenAggregateByAgent)
	server.Register("sidebar/tokenSummary", h.handleTokenUsageSummary)
	server.Register("sidebar/tokenGrouped", h.handleTokenUsageGrouped)
	server.Register("sidebar/tokenDetails", h.handleTokenUsageDetails)
	server.Register("sidebar/listMemoryByAgent", h.handleListMemoryByAgent)

	// Developer profile (proxied to weisyn platform)
	server.Register("sidebar/developerProfile", h.handleDeveloperProfile)

	// Cron management
	server.Register("sidebar/listCronJobs", h.handleListCronJobs)
	server.Register("sidebar/addCronJob", h.handleAddCronJob)
	server.Register("sidebar/updateCronJob", h.handleUpdateCronJob)
	server.Register("sidebar/deleteCronJob", h.handleDeleteCronJob)
	server.Register("sidebar/triggerCronJob", h.handleTriggerCronJob)
	server.Register("sidebar/enableCronJob", h.handleEnableCronJob)
	server.Register("sidebar/disableCronJob", h.handleDisableCronJob)
	server.Register("sidebar/listCronRuns", h.handleListCronRuns)
	server.Register("sidebar/cronStats", h.handleCronStats)

	// IM Channels
	server.Register("sidebar/listChannels", h.handleListChannels)
	server.Register("sidebar/getChannel", h.handleGetChannel)
	server.Register("sidebar/getChannelConfig", h.handleGetChannelConfig)
	server.Register("sidebar/upsertChannel", h.handleUpsertChannel)
	server.Register("sidebar/deleteChannel", h.handleDeleteChannel)
	server.Register("sidebar/connectChannel", h.handleConnectChannel)
	server.Register("sidebar/disconnectChannel", h.handleDisconnectChannel)
	server.Register("sidebar/channelEvents", h.handleChannelEvents)
	server.Register("sidebar/subscribeChannelEvents", h.handleSubscribeChannelEvents)
	server.Register("sidebar/channelBindings", h.handleChannelBindings)
	server.Register("sidebar/updateChannelBindings", h.handleUpdateChannelBindings)
	server.Register("sidebar/channelPreflight", h.handleChannelPreflight)
	server.Register("sidebar/channelSchema", h.handleChannelSchema)
	server.Register("sidebar/validateChannelBindings", h.handleValidateChannelBindings)

	// Email
	server.Register("sidebar/listEmailAccounts", h.handleListEmailAccounts)
	server.Register("sidebar/getEmailAccount", h.handleGetEmailAccount)
	server.Register("sidebar/saveEmailAccount", h.handleSaveEmailAccount)
	server.Register("sidebar/deleteEmailAccount", h.handleDeleteEmailAccount)
	server.Register("sidebar/testEmailAccount", h.handleTestEmailAccount)

	// Desktop automation
	server.Register("sidebar/desktopSettings", h.handleDesktopSettings)
	server.Register("sidebar/desktopToggle", h.handleDesktopToggle)

	// Browser automation
	server.Register("sidebar/browserSettings", h.handleBrowserSettings)
	server.Register("sidebar/browserStatus", h.handleBrowserStatus)
	server.Register("sidebar/browserConnect", h.handleBrowserConnect)
	server.Register("sidebar/browserSaveToken", h.handleBrowserSaveToken)
	server.Register("sidebar/browserToggle", h.handleBrowserToggle)
	server.Register("sidebar/browserSetMode", h.handleBrowserSetMode)
	server.Register("sidebar/browserExtensionZip", h.handleBrowserExtensionDownload)

	// Cell trust policy (command palette shortcut → governance mode)
	server.Register("cell/setTrustPolicy", h.handleSetTrustPolicy)

	// Engine Settings
	server.Register("sidebar/getGovernanceSettings", h.handleGetGovernanceSettings)
	server.Register("sidebar/updateGovernanceSettings", h.handleUpdateGovernanceSettings)
	server.Register("sidebar/getMemoryPolicySettings", h.handleGetMemoryPolicySettings)
	server.Register("sidebar/updateMemoryPolicySettings", h.handleUpdateMemoryPolicySettings)
	server.Register("sidebar/getRunLimitsSettings", h.handleGetRunLimitsSettings)
	server.Register("sidebar/updateRunLimitsSettings", h.handleUpdateRunLimitsSettings)
	server.Register("sidebar/getCycleDetectSettings", h.handleGetCycleDetectSettings)
	server.Register("sidebar/updateCycleDetectSettings", h.handleUpdateCycleDetectSettings)
	server.Register("sidebar/getContextBudgetSettings", h.handleGetContextBudgetSettings)
	server.Register("sidebar/updateContextBudgetSettings", h.handleUpdateContextBudgetSettings)
	server.Register("sidebar/getResilienceSettings", h.handleGetResilienceSettings)
	server.Register("sidebar/updateResilienceSettings", h.handleUpdateResilienceSettings)
	server.Register("sidebar/listPlugins", h.handleListPlugins)
	server.Register("sidebar/getProviderStrategySettings", h.handleGetProviderStrategySettings)
	server.Register("sidebar/updateProviderStrategySettings", h.handleUpdateProviderStrategySettings)

	// MCP management
	server.Register("sidebar/listMCPServers", h.handleListMCPServers)
	server.Register("sidebar/getMCPServer", h.handleGetMCPServer)
	server.Register("sidebar/mcpServerStatus", h.handleMCPServerStatus)
	server.Register("sidebar/upsertMCPServer", h.handleUpsertMCPServer)
	server.Register("sidebar/deleteMCPServer", h.handleDeleteMCPServer)
	server.Register("sidebar/probeMCPServer", h.handleProbeMCPServer)
	server.Register("sidebar/connectMCPServer", h.handleConnectMCPServer)
	server.Register("sidebar/disconnectMCPServer", h.handleDisconnectMCPServer)
	server.Register("sidebar/enableMCPServer", h.handleEnableMCPServer)
	server.Register("sidebar/disableMCPServer", h.handleDisableMCPServer)
	server.Register("sidebar/mcpCallTool", h.handleMCPCallTool)
	server.Register("sidebar/mcpSchema", h.handleMCPSchema)

	// CodeIntel (Project Overview panel v2)
	server.Register("codeintel/stats", h.handleCodeIntelStats)
	server.Register("codeintel/project-tree", h.handleCodeIntelProjectTree)
	server.Register("codeintel/callgraph", h.handleCodeIntelCallgraph)
	server.Register("codeintel/expand-node", h.handleCodeIntelExpandNode)
	server.Register("codeintel/module-deps", h.handleCodeIntelModuleDeps)
	server.Register("codeintel/coverage", h.handleCodeIntelCoverage)
	server.Register("codeintel/affected-tests", h.handleCodeIntelAffectedTests)
	server.Register("codeintel/reindex", h.handleCodeIntelReindex)
	server.Register("codeintel/clear", h.handleCodeIntelClear)

	// Terminal (Extension → Backend data flow for VSCode integrated terminal)
	server.Register("terminal/output", h.handleTerminalOutput)
	server.Register("terminal/exited", h.handleTerminalExited)

	// LSP Bridge (Extension → Backend: IDE language server responses)
	server.Register("lsp/response", h.handleLSPResponse)

	// IDE Diagnostics (Extension → Backend: push from IMarkerService)
	server.Register("diagnostics/report", h.handleDiagnosticsReport)

	// Debug Bridge (Extension → Backend: unified debug events)
	server.Register("debug/hit", h.handleDebugHit) // legacy compat
	server.Register("debug/event", h.handleDebugEvent)
	server.Register("debug/response", h.handleDebugResponse)
	server.Register("crash/report", h.handleCrashReport)

	// Test Bridge (Extension → Backend: test execution results)
	server.Register("test/result", h.handleTestResult)
}

func (h *Handler) handleInitialize(_ context.Context, req Request) (any, *RPCError) {
	var params InitializeParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
	}
	workDir, err := h.engine.Initialize(h.appCtx, params.WorkspacePath, params.WorkspaceFolders)
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "locked by another process") {
			return InitializeResult{
				Capabilities: map[string]any{
					"chat":      false,
					"streaming": false,
					"tools":     []string{},
				},
				WorkDir: "",
				CellID:  "",
				Error:   "该项目已在另一个窗口打开。同一项目同时只能有一个活跃窗口。",
			}, nil
		}
		return nil, internalError(err)
	}
	isConfigMode := h.engine.ConfigMode()
	return InitializeResult{
		Capabilities: map[string]any{
			"chat":      !isConfigMode,
			"streaming": !isConfigMode,
			"tools":     []string{"read", "write", "exec"},
		},
		WorkDir:    workDir,
		CellID:     h.engine.CellID(),
		ConfigMode: isConfigMode,
	}, nil
}

// handleCellInfo returns the current workspace's Cell identity + governance
// surface for UI display.  Read-only; safe to poll.
func (h *Handler) handleCellInfo(_ context.Context, _ Request) (any, *RPCError) {
	if h.engine.ConfigMode() {
		return map[string]any{"configMode": true}, nil
	}
	cellID := h.engine.CellID()
	if cellID == "" {
		return map[string]any{"configMode": true}, nil
	}
	return CellInfoResult{
		CellID:     cellID,
		WorkDir:    h.engine.WorkspacePath(),
		GovernMode: h.engine.CurrentGovernMode(),
	}, nil
}

func (h *Handler) handleShutdown(_ context.Context, _ Request) (any, *RPCError) {
	if err := h.engine.Close(); err != nil {
		return nil, internalError(err)
	}
	if h.onShutdown != nil {
		h.onShutdown()
	}
	return ShutdownResult{OK: true}, nil
}

// invalidParams and internalError put the cause in Message, not in a side
// channel.
//
// Both used to write `Message: "Invalid params"` with err.Error() in Data. The
// electron-main bridge reads `msg.error.message` and `msg.error.reason` and
// nothing else, so across ~300 call sites the entire cause was dropped at the
// process boundary and the user got the literal string "Internal error". Data
// is gone from RPCError for that reason: a field written by six places and read
// by none is not a channel, it is a leak with a schema.
//
// These carry no failure.Reason on purpose. A reason names a condition the
// product asserts and the renderer has a sentence for; these two are the
// fallthrough for everything unclassified, so a reason here would be a promise
// the renderer cannot keep. Their text stays diagnostic and English.
func invalidParams(err error) *RPCError {
	msg := "invalid params"
	if err != nil {
		msg += ": " + err.Error()
	}
	return &RPCError{Code: -32602, Message: msg}
}

func internalError(err error) *RPCError {
	msg := "internal error"
	if err != nil {
		msg += ": " + err.Error()
	}
	return &RPCError{Code: -32603, Message: msg}
}
