package rpc

import (
	"encoding/json"

	"github.com/weisyn/wesapp/file"
	"github.com/weisyn/wescode/internal/editengine"
	"github.com/weisyn/wesgine"
	wesengine "github.com/weisyn/wesgine/engine"
	"github.com/weisyn/wesgine/message"
)

const jsonRPCVersion = "2.0"

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is the JSON-RPC error object. The extension host turns it into a JS
// Error and the webview renders whatever it holds, so Message is display copy
// unless Reason is present.
//
// Reason names a product condition the renderer must phrase itself
// (`internal/failure`). Empty means the failure was propagated from a lower
// layer and Message is the only thing there is to say about it.
//
// Reason exists because its absence forced the code into Message: the
// `no_workspace` arm returned that identifier *as the sentence*, and the
// renderer grew a six-way substring match to read it back out (that module is
// gone; `web/src/lib/failure.ts` now matches on this field).
// Every hop between here and the webview must carry Reason forward — the one
// that did not (`postMessage({error: err.message})`) is what made a string the
// only channel available.
//
// There is no Data field, and adding one back would recreate a fixed defect.
// The old `Data any` was written by six places (including invalidParams and
// internalError, ~300 call sites between them) and read by none: electron-main
// forwards `message` and `reason`, so every cause routed through Data died at
// the process boundary while the user read the literal string "Internal error".
// Diagnostic detail goes in Message; anything the renderer must act on gets a
// Reason.
type RPCError struct {
	Code    int    `json:"code"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message"`
}

type Notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type InitializeParams struct {
	WorkspacePath    string   `json:"workspacePath,omitempty"`
	WorkspaceFolders []string `json:"workspaceFolders,omitempty"`
}

type InitializeResult struct {
	Capabilities map[string]any `json:"capabilities"`
	WorkDir      string         `json:"workDir,omitempty"`

	// CellID is the workspace's wesgine tenancy Cell identifier
	// (deterministic hash of the canonicalised workspace path).  The
	// frontend surfaces this in the status bar / Chat header so the
	// user knows which isolation domain each request runs in.
	CellID string `json:"cellId,omitempty"`

	// ConfigMode is true when no workspace is open. The Go backend runs
	// without Hypervisor/Cell — only auth/provider/settings RPC available.
	ConfigMode bool `json:"configMode,omitempty"`

	// Error is a user-facing error message (e.g. "project locked by
	// another window"). When set, capabilities.chat will be false.
	Error string `json:"error,omitempty"`
}

// CellInfoResult is returned by the "cell/info" RPC method and carries
// the workspace's Cell identity + governance surface for UI display.
// Refreshable on-demand (e.g. after Settings edits) so the status bar
// and Settings page stay in sync.
type CellInfoResult struct {
	CellID     string `json:"cellId"`
	WorkDir    string `json:"workDir"`
	GovernMode string `json:"governMode"` // "open" | "locked"
}

type ChatSendParams struct {
	Message   string `json:"message"`
	SessionID string `json:"sessionId,omitempty"`
	// AgentID and GroupID are the two named shapes of the engine's three-way
	// target dispatch (INV-ROUTE-01/02). They are mutually exclusive; GroupID
	// wins when both arrive, matching internal/cell/boot.go's dispatch order.
	// Neither set = anonymous run.
	AgentID string `json:"agentId,omitempty"`
	// GroupID names an agent group (wc_group_groups.id). The backend resolves
	// its member list into RunParams.Scope — the frontend never sends member
	// ids, so a stale client cannot run a group whose membership it misremembers.
	GroupID    string `json:"groupId,omitempty"`
	ProviderID string `json:"providerId,omitempty"`
	// Model is the explicit model name selected by the user (INV-MODEL-01).
	Model string `json:"model,omitempty"`
	// ModelBinding is a legacy observability tag (wesgine v1.0 removed the
	// tri-state fallback gate, T-10). Forwarded to engine opts → run tags
	// only; brand-honoring lives on CellSpec.AllowedModels. Frontend fills
	// 'strict' when the user explicitly picked a provider, empty otherwise.
	ModelBinding string `json:"modelBinding,omitempty"`
	// ThinkingLevel optionally overrides the RunSettings reasoning depth
	// for this single request ("low"/"medium"/"high"/"max").
	ThinkingLevel string             `json:"thinkingLevel,omitempty"`
	FilePaths     []string           `json:"filePaths,omitempty"`
	FileIDs       []string           `json:"fileIds,omitempty"`
	WorkDir       string             `json:"workDir,omitempty"`
	AllowPaths    []string           `json:"allowPaths,omitempty"`
	TestTimeout   int                `json:"testTimeout,omitempty"`
	CodeSnippets  []CodeSnippetParam `json:"codeSnippets,omitempty"`
	Resume        bool               `json:"resume,omitempty"`
	// ActivatedSkills is the per-turn Skill Activation snapshot (ADR-326).
	// Names selected by the user in the SkillActivationBar are:
	//   1. Written as skill_ref ContentBlocks into the user message (run.go)
	//      — persisted in wes_messages for history/bubble rendering.
	//   2. Forwarded to AppRunRequest.ActivatedSkills for engine turn-0
	//      assemble (skill body injection into system prompt).
	ActivatedSkills []string               `json:"activatedSkills,omitempty"`
	ContextItems    []ContextItemParam     `json:"contextItems,omitempty"`
	Media           []message.MediaPayload `json:"media,omitempty"`
	// TraceID propagates the frontend-generated diagnostic trace id so all
	// backend log fields carry it and one message can be grep'd across
	// Webview / Extension host / Go backend logs
	// (~/.wescode/data/logs/wescode-chat.log).
	TraceID string `json:"_traceId,omitempty"`
}

type CodeSnippetParam struct {
	FilePath  string `json:"filePath"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Code      string `json:"code"`
	Language  string `json:"language"`
}

// ── File management RPC types ────────────────────────────────────────────────
//
// These types are the wire, and they are the *only* declaration of it. The
// handlers must not hand `*file.FileManifest` to a client directly, because
// that struct is the on-disk index record: `loadIndex` reconstructs `m.ID`
// from its `json:"id"` key, so that key can never be renamed without
// orphaning every stored attachment. The wire calls the same value `fileId`
// (an id crossing a boundary is a reference: chat's `fileIds` and the
// `file_ref` content part both say `fileId`; only the manifest's own identity
// field is `id`). Those two names are both correct and they are not the same
// contract — `fileResultOf` is the seam that keeps them apart.
//
// Returning the manifest raw compiles, passes `go vet`, and silently ships a
// payload whose id key nobody reads: that is exactly how the paperclip died
// between 2026-07-27 and 2026-09-03.

type FileUploadParams struct {
	FileName string `json:"fileName"`
	Data     string `json:"data"` // base64-encoded file content
}

type FileImportLocalParams struct {
	Path string `json:"path"`
}

type FileContentParams struct {
	FileID string `json:"fileId"`
}

type FileResult struct {
	FileID   string `json:"fileId"`
	FileName string `json:"fileName"`
	MIMEType string `json:"mimeType,omitempty"`
	Size     int64  `json:"size,omitempty"`
}

// fileResultOf projects a stored manifest onto the wire. Single seam: both
// write paths (upload, importLocal) go through here, so the disk key and the
// wire key can only drift in one place.
func fileResultOf(m *file.FileManifest) FileResult {
	return FileResult{
		FileID:   m.ID,
		FileName: m.FileName,
		MIMEType: m.MIMEType,
		Size:     m.Size,
	}
}

type FileContentResult struct {
	Data     string `json:"data"` // base64
	FileName string `json:"fileName"`
	MIMEType string `json:"mimeType"`
}

type ChatSendResult struct {
	OK        bool   `json:"ok"`
	SessionID string `json:"sessionId,omitempty"`
	RunID     string `json:"runId,omitempty"`
}

type ShutdownResult struct {
	OK bool `json:"ok"`
}

type StreamToolCall struct {
	ID     string            `json:"id,omitempty"`
	Tool   string            `json:"tool"`
	Status string            `json:"status"`
	Args   map[string]string `json:"args,omitempty"`
	Result string            `json:"result,omitempty"`
}

type StreamHITL struct {
	RequestID    string            `json:"requestId"`
	Kind         string            `json:"kind,omitempty"`
	HITLKind     string            `json:"hitlKind,omitempty"`
	ToolName     string            `json:"toolName,omitempty"`
	Reason       string            `json:"reason,omitempty"`
	Prompt       string            `json:"prompt,omitempty"`
	CommandText  string            `json:"commandText,omitempty"`
	Choices      []string          `json:"choices,omitempty"`
	ExpiresAt    string            `json:"expiresAt,omitempty"`
	DecisionKind string            `json:"decisionKind,omitempty"`
	Sensitive    bool              `json:"sensitive,omitempty"`
	Grant        string            `json:"grant,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// StreamError forwards one engine.ErrorData to the webview.
//
// Kind and Code keep the engine's own types rather than widening to string at
// this boundary. They were strings, and the untyped fields let the error branch
// assign the code "no_provider" to Kind — a value the ErrorKind domain does not
// contain — so the webview's kind switch fell through for the one failure it
// most needed to explain. wescode has no error vocabulary of its own to add.
type StreamError struct {
	Text        string              `json:"text,omitempty"`
	Kind        wesengine.ErrorKind `json:"kind,omitempty"`
	Code        wesengine.ErrorCode `json:"code,omitempty"`
	Recoverable bool                `json:"recoverable,omitempty"`

	// Provider error pass-through (A-type only). Mirrors wesgine
	// engine.ErrorData.ProviderMessage/Provider so front-ends render the
	// provider's original error text verbatim ("[Provider] text") instead of
	// a flattened message (INV-ERROR-ATTRIBUTION).
	ProviderMessage string `json:"provider_message,omitempty"`
	Provider        string `json:"provider,omitempty"`
}

type StreamEditPreview struct {
	TxID string `json:"txId"`
	// Path 是**展示用**的单值：多文件 apply_patch 时它是 "a.go (+2 files)"。
	// 不要用它寻址——per-file accept/reject 必须用 Paths（EE-10）。
	Path string `json:"path"`
	// Paths 是这次事务真实触达的全部路径。单文件工具（edit/write）也填，
	// 使 editTxPaths 永远拿到地面真相而不是解析显示串得来的猜测。
	Paths      []string         `json:"paths,omitempty"`
	IsNew      bool             `json:"isNew,omitempty"`
	Status     string           `json:"status"` // "pending" | "applied" | "failed"
	Hunks      []StreamDiffHunk `json:"hunks,omitempty"`
	StreamMode string           `json:"streamMode,omitempty"` // "full" (default) | "incremental" | "speculative"
	// "full": all hunks sent at once (current behavior).
	// "speculative": large edit (>100 changed lines) — frontend uses line-by-line
	//   animation to simulate streaming while the atomic tool executes.
	// "incremental": reserved for future wesgine architecture changes where the
	//   model generates diffs token-by-token and hunks are sent as they complete.
	TotalLines int `json:"totalLines,omitempty"` // for progress tracking
}

type StreamDiffHunk struct {
	OldStart int    `json:"oldStart"`
	OldEnd   int    `json:"oldEnd"`
	OldText  string `json:"oldText"`
	NewText  string `json:"newText"`
}

type StreamTokenUsage struct {
	InputTokens      int `json:"inputTokens"`
	OutputTokens     int `json:"outputTokens"`
	CacheReadTokens  int `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens int `json:"cacheWriteTokens,omitempty"`
	// CacheTokens is the combined read+write total, kept for backward
	// compatibility with older clients; prefer the split fields.
	CacheTokens int   `json:"cacheTokens,omitempty"`
	TotalTurns  int   `json:"totalTurns,omitempty"`
	ElapsedMS   int64 `json:"elapsedMs,omitempty"`
}

type StreamEvent struct {
	RequestID        string                  `json:"requestId,omitempty"`
	Type             string                  `json:"type"`
	Text             string                  `json:"text,omitempty"`
	ToolCall         *StreamToolCall         `json:"toolCall,omitempty"`
	HITL             *StreamHITL             `json:"hitl,omitempty"`
	Error            *StreamError            `json:"error,omitempty"`
	EditPreview      *StreamEditPreview      `json:"editPreview,omitempty"`
	Plan             *StreamPlanData         `json:"plan,omitempty"`
	Subagent         *StreamSubagent         `json:"subagent,omitempty"`
	TokenUsage       *StreamTokenUsage       `json:"tokenUsage,omitempty"`
	ChangesetSummary *StreamChangesetSummary `json:"changesetSummary,omitempty"`
	PlanID           string                  `json:"planId,omitempty"`
	StepID           string                  `json:"stepId,omitempty"`
	SessionID        string                  `json:"sessionId,omitempty"`
	RunID            string                  `json:"runId,omitempty"`
	AgentID          string                  `json:"agentId,omitempty"`
	// AgentName is the speaking member's display name during a group run.
	// It is what the frontend buckets streamed parts by (useStream's
	// `eventAgentKey`), so an event carrying only AgentID lands in the same
	// bubble as every other member. Empty for single-agent runs, where one
	// speaker needs no label.
	AgentName string `json:"agentName,omitempty"`
	// Observe carries engine payloads for observability events
	// (error_streak / cognitive_retry / context_pressure / cache_hints).
	// Keys stay snake_case so wesui reduce matches Pump JSON.
	Observe map[string]any `json:"observe,omitempty"`
	// StructuredData 透传工具的 `tool.ToolResult.StructuredData`（PC-03/PC-04）。
	//
	// 原样转发，这一层不解释结构：形状由产出它的工具定义，形态由 wesui 决定。
	// 中间层一旦开始重塑，就成了第三份真相。用 json.RawMessage 而不是
	// map[string]any，正是为了让"不解释"在类型上成立。
	StructuredData json.RawMessage `json:"structuredData,omitempty"`
}

type StreamSubagent struct {
	SubagentID  string `json:"subagentId,omitempty"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	Result      string `json:"result,omitempty"`
	ElapsedMS   int64  `json:"elapsedMs,omitempty"`
	Error       string `json:"error,omitempty"`
}

type StreamChangesetSummary struct {
	ID           string                     `json:"id"`
	CheckpointID string                     `json:"checkpointId,omitempty"`
	Files        []editengine.ChangesetFile `json:"files"`
	FileCount    int                        `json:"fileCount"`
}

type StreamPlanVerifyAction struct {
	Kind    string `json:"kind"`
	Command string `json:"command"`
	Expect  string `json:"expect,omitempty"`
}

type StreamPlanVerifyResult struct {
	Kind     string `json:"kind"`
	Command  string `json:"command"`
	Passed   bool   `json:"passed"`
	Output   string `json:"output,omitempty"`
	Duration string `json:"duration,omitempty"`
}

type StreamPlanStep struct {
	ID            string                   `json:"id"`
	Title         string                   `json:"title,omitempty"`
	Status        string                   `json:"status,omitempty"`
	Detail        string                   `json:"detail,omitempty"`
	Verification  []StreamPlanVerifyAction `json:"verification,omitempty"`
	VerifyResults []StreamPlanVerifyResult `json:"verifyResults,omitempty"`
}

type StreamPlanData struct {
	ID              string                   `json:"id,omitempty"`
	Title           string                   `json:"title,omitempty"`
	Overview        string                   `json:"overview,omitempty"`
	Analysis        string                   `json:"analysis,omitempty"`
	Steps           []StreamPlanStep         `json:"steps,omitempty"`
	PlanID          string                   `json:"planId,omitempty"`
	SessionID       string                   `json:"sessionId,omitempty"`
	Status          string                   `json:"status,omitempty"`
	Result          string                   `json:"result,omitempty"`
	Detail          string                   `json:"detail,omitempty"`
	Field           string                   `json:"field,omitempty"`
	Reason          string                   `json:"reason,omitempty"`
	Summary         string                   `json:"summary,omitempty"`
	StallCount      *int                     `json:"stallCount,omitempty"`
	LastUpdatedStep string                   `json:"lastUpdatedStep,omitempty"`
	VerifyResults   []StreamPlanVerifyResult `json:"verifyResults,omitempty"`
	// Interrupted mirrors engine.Plan.Interrupted: the plan's run terminated
	// with pending steps (P0-1). The polling path (getSessionPlan) carries it
	// so a reload shows the same terminal state as the SSE plan_interrupted
	// event — the plan must not "resurrect" as in-progress.
	Interrupted       bool   `json:"interrupted,omitempty"`
	InterruptedReason string `json:"interruptedReason,omitempty"`
	// TotalSteps/DoneSteps carry plan_completed statistics (P1-2).
	TotalSteps int `json:"totalSteps,omitempty"`
	DoneSteps  int `json:"doneSteps,omitempty"`
	// INV-PE-12 plan-pending reminder (not an error).
	PendingCount int `json:"pendingCount,omitempty"`
	Attempt      int `json:"attempt,omitempty"`
}

type StreamNotification struct {
	RequestID string      `json:"requestId"`
	Event     StreamEvent `json:"event"`
}

type ConversationItem struct {
	SessionID          string `json:"sessionId"`
	AgentID            string `json:"agentId,omitempty"`
	Title              string `json:"title"`
	LastMessage        string `json:"lastMessage"`
	LastTime           string `json:"lastTime"`
	CellID             string `json:"cellId,omitempty"`
	WorkspaceLabel     string `json:"workspaceLabel,omitempty"`
	IsCurrentWorkspace bool   `json:"isCurrentWorkspace"`
}

// The sidebar/availableModels row shape is wesapp models.Option, serialized
// straight through by handleAvailableModels. A second declaration lived here
// and drifted: it still carried an `error string` field that models.Option
// deliberately dropped, and the frontend DTO copied that field, so wescode's
// picker typed a message where a probe class arrives. Nothing referenced this
// struct — a duplicate wire shape does not have to be reachable to be wrong.

type ListConversationMessagesParams struct {
	SessionID string `json:"sessionId"`
	CellID    string `json:"cellId,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type MessagePart struct {
	Type   string         `json:"type"`
	Text   string         `json:"text,omitempty"`
	Tool   string         `json:"tool,omitempty"`
	Args   map[string]any `json:"args,omitempty"`
	Result string         `json:"result,omitempty"`
	Status string         `json:"status,omitempty"`
}

// ConversationMessageItem carries history back to the webview. Attachments
// ride inside `ContentParts` as `file_ref` parts (see store.buildUserParts) —
// there is deliberately no separate `attachments` field: it existed, no
// producer ever assigned it, and the webview branch reading it was proof for
// a payload that never arrived.
type ConversationMessageItem struct {
	ID           string        `json:"id"`
	Role         string        `json:"role"`
	Content      string        `json:"content"`
	CreatedAt    string        `json:"createdAt,omitempty"`
	AgentID      string        `json:"agentId,omitempty"`
	Parts        []MessagePart `json:"parts,omitempty"`
	ContentParts any           `json:"contentParts,omitempty"`
}

type ProviderItem struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	BaseURL       string `json:"baseUrl"`
	Model         string `json:"model"`
	HasAPIKey     bool   `json:"hasApiKey"`
	APIKeyPreview string `json:"apiKeyPreview"`
	IsDefault     bool   `json:"isDefault"`
	NoStreamUsage bool   `json:"noStreamUsage,omitempty"`
	Status        string `json:"status"` // "ok" | "error" | "unknown"
	// ProbeClass is why the last probe failed, as a closed engine domain.
	// It was `TestError string`, whose name invited the settings page to
	// print it, so a user whose base URL pointed at an HTML page read the
	// literal token `invalid_response`.
	ProbeClass wesgine.ProbeClass `json:"probeClass,omitempty"`
}

type AddProviderParams struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	BaseURL       string `json:"baseUrl"`
	Model         string `json:"model"`
	APIKey        string `json:"apiKey"`
	IsDefault     bool   `json:"isDefault"`
	NoStreamUsage bool   `json:"noStreamUsage,omitempty"`
	ContextWindow int    `json:"context_window,omitempty"`
	MaxOutput     int    `json:"max_output,omitempty"`
}

type UpdateProviderParams struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	BaseURL       string `json:"baseUrl"`
	Model         string `json:"model"`
	APIKey        string `json:"apiKey,omitempty"`
	IsDefault     bool   `json:"isDefault"`
	NoStreamUsage bool   `json:"noStreamUsage,omitempty"`
	ContextWindow int    `json:"context_window,omitempty"`
	MaxOutput     int    `json:"max_output,omitempty"`
}

type DeleteProviderParams struct {
	Name string `json:"name"`
}

type SetDefaultProviderParams struct {
	Name string `json:"name"`
}

type TestProviderParams struct {
	Name          string `json:"name,omitempty"`
	Type          string `json:"type"`
	BaseURL       string `json:"baseUrl"`
	APIKey        string `json:"apiKey"`
	Model         string `json:"model"`
	NoStreamUsage bool   `json:"noStreamUsage,omitempty"`
}

type TestProviderByNameParams struct {
	Name string `json:"name"`
}

// TestProviderResult is one probe verdict.
//
// ProbeClass is the only field a consumer may branch on, and the copy shown to
// the user is the consumer's to write. Message is an upstream diagnostic
// excerpt for the log/details view, not display prose: it was named Error and
// rendered verbatim, which is how raw provider JSON reached the settings page.
// ProbeClass replaced an ErrorKind string that the RPC layer carved out of
// Message by substring matching.
type TestProviderResult struct {
	OK            bool               `json:"ok"`
	LatencyMs     int64              `json:"latencyMs"`
	Message       string             `json:"message,omitempty"`
	ProbeClass    wesgine.ProbeClass `json:"probeClass,omitempty"`
	ContextWindow int                `json:"contextWindow,omitempty"`
	MaxOutput     int                `json:"maxOutput,omitempty"`
}

// AgentItem is the wire type for sidebar/listAgents.
// Unlike wesclaw's AgentWithConv, this does not include conversation summary
// (latest_conv_id, last_message, last_time) because wescode's IDE model
// manages conversations through the chat panel, not the agent list.
type AgentItem struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Description         string   `json:"description,omitempty"`
	Emoji               string   `json:"emoji,omitempty"`
	Figure              string   `json:"figure,omitempty"`
	Hue                 int      `json:"hue,omitempty"`
	Role                string   `json:"role"`
	Goal                string   `json:"goal"`
	Intent              string   `json:"intent,omitempty"`
	Expertise           []string `json:"expertise,omitempty"`
	Tags                []string `json:"tags,omitempty"`
	Suggestions         []string `json:"suggestions,omitempty"`
	SkillBindings       []string `json:"skillBindings,omitempty"`
	ToolAllow           []string `json:"toolAllow,omitempty"`
	ToolDeny            []string `json:"toolDeny,omitempty"`
	MaxTurns            int      `json:"maxTurns,omitempty"`
	TimeoutSeconds      int      `json:"timeoutSeconds,omitempty"`
	ModelOverride       string   `json:"modelOverride,omitempty"`
	WorkspaceAccess     string   `json:"workspaceAccess,omitempty"`
	WorkspaceAllowPaths []string `json:"workspaceAllowPaths,omitempty"`
	DelegationChildDeny []string `json:"delegationChildDenyTools,omitempty"`
	Headless            bool     `json:"headless,omitempty"`
	SystemPrompt        string   `json:"systemPrompt,omitempty"`
	CreatedAt           string   `json:"createdAt,omitempty"`
	IsBuiltin           bool     `json:"isBuiltin"`
	Pinned              bool     `json:"pinned"`
}

type CreateAgentParams struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Role         string   `json:"role"`
	Goal         string   `json:"goal"`
	Intent       string   `json:"intent,omitempty"`
	Expertise    []string `json:"expertise,omitempty"`
	SystemPrompt string   `json:"systemPrompt,omitempty"`
	Model        string   `json:"model,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Suggestions  []string `json:"suggestions,omitempty"`
	Skills       []string `json:"skills,omitempty"`
	Figure       string   `json:"figure,omitempty"`
	Hue          int      `json:"hue,omitempty"`
	Emoji        string   `json:"emoji,omitempty"`
}

// UpdateAgentParams uses pointer fields so an explicit empty value ("" / 0 / false)
// clears the target field, while absent JSON keys leave it unchanged.
type UpdateAgentParams struct {
	ID                  string   `json:"id"`
	Name                *string  `json:"name"`
	Description         *string  `json:"description,omitempty"`
	Role                *string  `json:"role,omitempty"`
	Goal                *string  `json:"goal,omitempty"`
	Intent              *string  `json:"intent,omitempty"`
	Expertise           []string `json:"expertise,omitempty"`
	SystemPrompt        *string  `json:"systemPrompt,omitempty"`
	Suggestions         []string `json:"suggestions,omitempty"`
	Tags                []string `json:"tags,omitempty"`
	SkillBindings       []string `json:"skillBindings,omitempty"`
	ToolAllow           []string `json:"toolAllow,omitempty"`
	ToolDeny            []string `json:"toolDeny,omitempty"`
	MaxTurns            *int     `json:"maxTurns,omitempty"`
	TimeoutSeconds      *int     `json:"timeoutSeconds,omitempty"`
	ModelOverride       *string  `json:"modelOverride,omitempty"`
	WorkspaceAccess     *string  `json:"workspaceAccess,omitempty"`
	WorkspaceAllowPaths []string `json:"workspaceAllowPaths,omitempty"`
	DelegationChildDeny []string `json:"delegationChildDenyTools,omitempty"`
	Headless            *bool    `json:"headless,omitempty"`
	Pinned              *bool    `json:"pinned,omitempty"`
}

type DeleteAgentParams struct {
	ID string `json:"id"`
}

type SkillItem struct {
	Slug                   string   `json:"slug"`
	Name                   string   `json:"name"`
	Description            string   `json:"description,omitempty"`
	Enabled                bool     `json:"enabled"`
	Tags                   []string `json:"tags"`
	Path                   string   `json:"path"`
	Operators              []string `json:"operators,omitempty"`
	Category               string   `json:"category,omitempty"`
	Channel                string   `json:"channel,omitempty"`
	ExecutionMode          string   `json:"executionMode,omitempty"`
	DisableModelInvocation bool     `json:"disableModelInvocation,omitempty"`
	UsageCount             int64    `json:"usageCount"`
}

type CreateSkillParams struct {
	Name string `json:"name"`
}

type ToggleSkillParams struct {
	Slug    string `json:"slug"`
	Enabled bool   `json:"enabled"`
}

type DeleteSkillParams struct {
	Slug string `json:"slug"`
}

type CheckSkillBindingsParams struct {
	Slugs []string `json:"slugs"`
}

type PresetAgentItem struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Emoji        string   `json:"emoji"`
	Role         string   `json:"role"`
	Goal         string   `json:"goal"`
	Intent       string   `json:"intent,omitempty"`
	Category     string   `json:"category"`
	Tags         []string `json:"tags"`
	Suggestions  []string `json:"suggestions,omitempty"`
	SystemPrompt string   `json:"systemPrompt,omitempty"`
}

type HITLRespondParams struct {
	RequestID string `json:"requestId"`
	Grant     string `json:"grant,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Feedback  string `json:"feedback,omitempty"`
	Value     string `json:"value,omitempty"`
	Choice    string `json:"choice,omitempty"`
}

type ChatCancelParams struct {
	SessionID string `json:"sessionId,omitempty"`
}

type EditAcceptParams struct {
	TxID string `json:"txId"`
}

type EditRejectParams struct {
	TxID string `json:"txId"`
}

type ImplicitFeedbackParams struct {
	Type        string `json:"type"` // "undo_immediate" | "post_accept_tweak" | "reject_then_write"
	File        string `json:"file"`
	Lang        string `json:"lang"`
	AIVersion   string `json:"aiVersion"`
	UserVersion string `json:"userVersion"`
	Line        int    `json:"line"`
	Timestamp   int64  `json:"timestamp"`
}

// ── Code Intelligence RPC types ─────────────────────────────────────────────

type EditorDidChangeParams struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type EditorDidCloseParams struct {
	Path string `json:"path"`
}

type EditorStateParams struct {
	FocusFile    string             `json:"focusFile"`
	CursorLine   int                `json:"cursorLine"`
	CursorCol    int                `json:"cursorCol"`
	Selection    *SelectionParam    `json:"selection,omitempty"`
	OpenFiles    []string           `json:"openFiles,omitempty"`
	VisibleRange *VisibleRangeParam `json:"visibleRange,omitempty"`
	RecentEdits  []RecentEditParam  `json:"recentEdits,omitempty"`

	GlobalErrors     []DiagnosticParam `json:"globalErrors,omitempty"`
	TerminalSnapshot string            `json:"terminalSnapshot,omitempty"`
	GitStagedFiles   []string          `json:"gitStagedFiles,omitempty"`
	VisibleEditors   []string          `json:"visibleEditors,omitempty"`
}

type DiagnosticParam struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Message string `json:"message"`
}

type SelectionParam struct {
	StartLine int    `json:"startLine"`
	StartCol  int    `json:"startCol"`
	EndLine   int    `json:"endLine"`
	EndCol    int    `json:"endCol"`
	Text      string `json:"text"`
}

type VisibleRangeParam struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type RecentEditParam struct {
	Path      string `json:"path"`
	Timestamp int64  `json:"timestamp"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
}

type IndexFileParams struct {
	Path string `json:"path"`
}

// ── Context Source RPC types ─────────────────────────────────────────────────

type ContextSourcesResult struct {
	Sources []ContextSourceInfoRPC `json:"sources"`
}

type ContextSourceInfoRPC struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Icon       string `json:"icon"`
	Searchable bool   `json:"searchable"`
	Available  bool   `json:"available"`
}

type ContextSearchParams struct {
	SourceID string `json:"sourceId"`
	Query    string `json:"query"`
	Limit    int    `json:"limit,omitempty"`
}

type ContextSearchResult struct {
	Items []ContextSearchItemRPC `json:"items"`
}

type ContextSearchItemRPC struct {
	ID       string         `json:"id"`
	SourceID string         `json:"sourceId"`
	Label    string         `json:"label"`
	Detail   string         `json:"detail,omitempty"`
	Icon     string         `json:"icon,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

type ContextResolveParams struct {
	Items []ContextResolveItemParam `json:"items"`
}

type ContextResolveItemParam struct {
	ID       string `json:"id"`
	SourceID string `json:"sourceId"`
}

type ContextResolveResult struct {
	Resolved []ContextResolvedRPC `json:"resolved"`
}

type ContextResolvedRPC struct {
	ID          string             `json:"id"`
	Content     string             `json:"content,omitempty"`
	FilePath    string             `json:"filePath,omitempty"`
	CodeSnippet *CodeSnippetRefRPC `json:"codeSnippet,omitempty"`
	Metadata    map[string]string  `json:"metadata,omitempty"`
}

type CodeSnippetRefRPC struct {
	Path      string `json:"path"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Language  string `json:"language"`
	Content   string `json:"content"`
}

// ContextItemParam is a structured context item sent with chat/send.
// Label/Detail are the picker's human strings, persisted into the
// context_ref ContentBlock (INV-CTX-REF-01/03) so the transcript chip
// matches what the user saw in the composer.
type ContextItemParam struct {
	ID          string             `json:"id"`
	SourceID    string             `json:"sourceId"`
	Label       string             `json:"label,omitempty"`
	Detail      string             `json:"detail,omitempty"`
	FilePath    string             `json:"filePath,omitempty"`
	CodeSnippet *CodeSnippetRefRPC `json:"codeSnippet,omitempty"`
	Content     string             `json:"content,omitempty"`
}

// FIM completion types.
type CompletionRequestParams struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Col    int    `json:"col"`
	Prefix string `json:"prefix"`
	Suffix string `json:"suffix"`
}

type CompletionResult struct {
	Completions []CompletionItem `json:"completions"`
}

type CompletionItem struct {
	Text string `json:"text"`
}

// ── Background Agent RPC types ──────────────────────────────────────────────

type BackgroundStartParams struct {
	Message   string `json:"message"`
	SessionID string `json:"sessionId,omitempty"`
	AgentID   string `json:"agentId,omitempty"`
}

type BackgroundStartResult struct {
	RunID string `json:"runId"`
}

type BackgroundTaskItem struct {
	RunID     string `json:"runId"`
	SessionID string `json:"sessionId"`
	AgentID   string `json:"agentId"`
	Status    string `json:"status"`
	StartedAt string `json:"startedAt"`
}

type BackgroundMergeParams struct {
	RunID string `json:"runId"`
}

// ── SCM RPC types ────────────────────────────────────────────────────────────

type SCMStageParams struct {
	Paths []string `json:"paths"`
}

type SCMCommitParams struct {
	Message string `json:"message"`
}

// ─── MCP params ───────────────────────────────────────────────────────────────

type MCPServerUpsertParams struct {
	Name             string            `json:"name"`
	Command          string            `json:"command,omitempty"`
	Args             []string          `json:"args,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	URL              string            `json:"url,omitempty"`
	Transport        string            `json:"transport,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	ConnectTimeoutMs int               `json:"connectTimeoutMs,omitempty"`
	ToolTimeoutMs    int               `json:"toolTimeoutMs,omitempty"`
	Enabled          *bool             `json:"enabled,omitempty"`
	ToolsInclude     []string          `json:"toolsInclude,omitempty"`
	ToolsExclude     []string          `json:"toolsExclude,omitempty"`
	ContextVars      map[string]string `json:"contextVars,omitempty"`
}

type MCPProbeParams struct {
	MCPServerUpsertParams
	TimeoutMs int `json:"timeoutMs,omitempty"`
}

// ── Sidebar extended RPC types ──────────────────────────────────────────────

type ToolDescriptor struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ToolBudgetEstimate struct {
	EstimatedTokens int      `json:"estimatedTokens"`
	MaxTokens       int      `json:"maxTokens"`
	TruncatedTools  []string `json:"truncatedTools"`
	Warnings        []string `json:"warnings"`
}

type AuditEntry struct {
	ID      string `json:"id"`
	Time    string `json:"time"`
	Type    string `json:"type"`
	Tool    string `json:"tool,omitempty"`
	Verdict string `json:"verdict,omitempty"`
	AgentID string `json:"agentId,omitempty"`
	RunID   string `json:"runId,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type WorkspaceFileItem struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
	IsDir   bool   `json:"isDir"`
}

type WorkspaceTreeNode struct {
	Name     string              `json:"name"`
	Path     string              `json:"path"`
	IsDir    bool                `json:"isDir"`
	Size     *int64              `json:"size,omitempty"`
	ModTime  string              `json:"modTime,omitempty"`
	Children []WorkspaceTreeNode `json:"children,omitempty"`
}

// DiagnosticsReport is the payload of the diagnostics/report RPC,
// sent by the Extension when IDE diagnostics change (IMarkerService).
type DiagnosticsReport struct {
	Diagnostics []DiagnosticEntry `json:"diagnostics"`
}

// DiagnosticEntry is a single diagnostic from the IDE (tsserver, ESLint, gopls, etc.).
type DiagnosticEntry struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	EndLine   int    `json:"endLine,omitempty"`
	EndColumn int    `json:"endColumn,omitempty"`
	Severity  int    `json:"severity"` // VSCode MarkerSeverity: 1=Error, 2=Warning, 4=Info, 8=Hint
	Message   string `json:"message"`
	Source    string `json:"source,omitempty"`
	Code      string `json:"code,omitempty"`
}

// ── Group RPC types ─────────────────────────────────────────────────────────

type GroupItem struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Emoji    string   `json:"emoji,omitempty"`
	AgentIDs []string `json:"agentIds"`
}

type CreateGroupParams struct {
	Title    string   `json:"title"`
	AgentIDs []string `json:"agentIds"`
}

type DeleteGroupParams struct {
	ID string `json:"id"`
}

type RenameGroupParams struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type UpdateGroupMembersParams struct {
	GroupID  string   `json:"groupId"`
	AgentIDs []string `json:"agentIds"`
}
