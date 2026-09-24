package engine

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"

	"github.com/weisyn/weisyn/sdk/wes"
	"github.com/weisyn/wesapp/file"
	"github.com/weisyn/wescode/internal/codeintel"
	"github.com/weisyn/wescode/internal/codeintel/constraints"
	"github.com/weisyn/wescode/internal/deviceagent"
	"github.com/weisyn/wescode/internal/editengine"
	"github.com/weisyn/wescode/internal/notify"
	wescoderuntime "github.com/weisyn/wescode/internal/runtime"
	"github.com/weisyn/wescode/internal/treesitter"
	"github.com/weisyn/wescode/internal/verification"
	wesgine "github.com/weisyn/wesgine"
	wesmcp "github.com/weisyn/wesgine/adapter/mcp"
	wesagent "github.com/weisyn/wesgine/agent"
	wesconfig "github.com/weisyn/wesgine/config"
	wcron "github.com/weisyn/wesgine/cron"
	gevent "github.com/weisyn/wesgine/engine"
	"github.com/weisyn/wesgine/governance"
	"github.com/weisyn/wesgine/tool"

	appchannel "github.com/weisyn/wesapp/channel"
	"github.com/weisyn/wesapp/group"
	"github.com/weisyn/wesapp/providerid"
	apptunnel "github.com/weisyn/wesapp/tunnel"

	"github.com/weisyn/wesapp/agent"
	wesengine "github.com/weisyn/wesapp/engine"
	appmcp "github.com/weisyn/wesapp/mcp"
	"github.com/weisyn/wesapp/provider"
	appruntime "github.com/weisyn/wesapp/runtime"
	"github.com/weisyn/wesapp/stop"
)

// Products inject *tunnel.Manager as channel.Tunnel; channel must not import wesapp/tunnel.
var _ appchannel.Tunnel = (*apptunnel.Manager)(nil)

// Service is the main engine entry point that owns wesgine, codeintel, and
// all subsystems. It coordinates initialization, runtime, and shutdown.
type Service struct {
	mu             sync.Mutex
	cfg            AppConfig
	workspace      string
	workspaceRoots []string
	dataDir        string

	initialized    bool
	initializing   bool          // true while engine start is in flight
	initDone       chan struct{} // closed when the first engine start completes (success or fail)
	initErr        error         // non-nil if the first engine start failed
	configMode     bool          // true = no workspace, no Cell, only auth/provider/settings RPC
	bgLoopCancel   context.CancelFunc
	indexCancel    context.CancelFunc // cancels the current backgroundIndexAll run
	indexDone      chan struct{}      // closed when backgroundIndexAll exits; Close() waits on this before releasing tsPool
	postInitCancel context.CancelFunc // cancels the postInitialize goroutine
	postInitDone   chan struct{}      // closed when postInitialize exits
	eng            *wesengine.Engine
	memorySvc      *wesgine.MemoryHandle
	observeSvc     *wesgine.CellObserveHandle
	hitlSvc        *wesgine.HITLHandle
	agents         *agent.Service
	stops          *stop.Registry
	providers      *provider.CellClient
	mcpMgr         *appmcp.Manager
	hyp            *wesgine.Hypervisor
	cell           *wesgine.Cell
	runtime        *wesgine.RuntimeHandle
	cellID         string
	editEngine     *editengine.EditEngine
	fileStore      *file.FileStore
	bufferStore    *BufferStore
	tsPool         *treesitter.ParserPool
	codeIndex      *codeintel.CodeIndex
	ckgTools       *codeintel.CKGToolSet
	codeAsm        *codeintel.CodeAssembler
	qualityGate    *verification.CodeQualityGate
	lsp            codeintel.LSPBridge
	diagCache      *codeintel.DiagnosticsCache
	diagBaseline   *codeintel.DiagnosticsBaseline
	watcher        *codeintel.FileWatcher

	debugStore       *DebugEventStore
	logWatcher       *LogWatcher
	termDiagCache    *TerminalDiagCache
	termDiagParser   *TerminalDiagParser
	attentionTracker *AttentionTracker
	feedbackHandler  *ImplicitFeedbackHandler
	tcrTracker       *TCRTracker
	metricsPersister *MetricsPersister

	host             *deviceagent.Host
	pendingNotifier  deviceagent.NotifyFn
	topologyDetector *codeintel.ProjectTopologyDetector
	wesCatalog       *wes.Catalog
	wesHandler       *wes.Handler
	weisynURL        string
	tokenSource      TokenSource
	codeintelCap     *codeintel.CodeIntelCapabilityProvider
	constraintReg    *constraints.Registry
	constraintHits   *codeintel.ConstraintHitTracker
	preWritePrePtr   *atomic.Pointer[tool.PreCallHook]
	preWritePostPtr  *atomic.Pointer[tool.PostCallHook]
	conventions      []codeintel.Convention
	historySource    *historyContextSource

	snapshotMu sync.RWMutex
	onSnapshot codeintel.SnapshotCallback

	agentFileHashes sync.Map // path → hash (string); tracks agent-written file versions for S2 detection

	// sessionAgents tracks sessionID → agentID of the last Run so a mid-session
	// role switch can be announced to the model (see agent_switch.go).
	sessionAgents sync.Map

	skillCacheMu   sync.RWMutex
	skillCacheData []SkillInfo
	skillCacheTime time.Time

	fgRunInFlight      int32 // atomic: 1 = foreground chat active, 0 = idle
	persistPending     int32 // atomic: singleflight for PersistConstraints
	regressionDetected int32 // atomic: 1 = current Run had L2.5 regression (skip promote)

	lspMu              sync.Mutex
	pendingLSPRequests map[string]chan lspResponseEntry
	lspNextID          int64

	debugMu          sync.Mutex
	pendingDebugReqs map[string]chan json.RawMessage
	debugNextID      int64

	bgTasksMu sync.Mutex
	bgTasks   map[string]*BackgroundTask

	cronHandle         *wesgine.CronHandle
	channelEventBuf    *channelEventBuffer
	channelLockRelease func()
	// channelBoot delegates per-instance adapter attach to the
	// wesclaw/channel shared package. This eliminates the 6-platform
	// switch that used to live in this package (channels.go::bootChannelInstance).
	channelBoot    appchannel.BootFn
	channelPersist *appchannel.Persister
	watchdog       *EngineHealthWatchdog

	groupSvc   *group.Service
	groupDB    *sql.DB         // per-workspace wescode-app.db (v1.0 P1 follow-up)
	userAgents *userAgentStore // durable shadow of the volatile engine agent store
	docSync    *DocSync        // workspace document → Knowledge auto-sync
}

type lspResponseEntry struct {
	Result json.RawMessage
	Error  string
}

// WES capability states returned by TokenSource.WESAuthState. Single source
// of truth for the cross-repo protocol string (weisyn auth.Service emits
// these; INV-BILLING-02).
const (
	WESStateOK             = "ok"
	WESStateBillingOverdue = "billing_overdue"
	WESStateTokenExpired   = "token_expired"
	WESStateAnonymous      = "anonymous"
	WESStateTransient      = "transient"
)

// Withdrawn wes: grant (INV-BILLING-02) is raised as
// fmt.Errorf("%w", wesgine.ErrBillingOverdue) at the RunChat gate, not as a
// local string constant. The string constant that used to live here existed
// only so a downstream strings.Contains ladder could recognise it again;
// wrapping the engine sentinel makes ClassifyRunError attach
// provider_billing_overdue directly, and there is no second copy of the
// sentence to drift.

// TokenSource provides access tokens for weisyn API calls.
type TokenSource interface {
	ValidAccessToken(ctx context.Context) string
	// IdentityAccessToken is the weisyn identity JWT for /api/me/* and org:
	// catalog. Personal WES overdue must not blank this token.
	IdentityAccessToken(ctx context.Context) string
	// WESAuthState returns the current WES access token together with the
	// account-level capability state (ok / billing_overdue / token_expired /
	// anonymous / transient). Implemented by weisyn's auth.Service; the state
	// is authoritative for WES capability and does not depend on how much JWT
	// lifetime remains on this machine.
	WESAuthState(ctx context.Context) (token string, state string)
}

func NewService(cfg AppConfig) *Service {
	return &Service{
		cfg:         cfg,
		bufferStore: NewBufferStore(),
		ckgTools:    codeintel.NewCKGToolSet(),
	}
}

// SetTokenSource wires the weisyn auth token provider for WES LLM access.
func (s *Service) SetTokenSource(ts TokenSource, weisynURL string) {
	s.tokenSource = ts
	s.weisynURL = weisynURL
	s.wesCatalog = wes.NewAuthCatalog(weisynURL, ts.IdentityAccessToken, ts.ValidAccessToken, nil)
	s.wesHandler = wes.NewHandler(weisynURL, s.wesCatalog, nil)
}

// SiteBaseURL returns the weisyn deployment the engine is wired to (from
// WESCODE_WEISYN_URL). Frontends build org management / enterprise links from
// this instead of hardcoding a hostname.
func (s *Service) SiteBaseURL() string { return s.weisynURL }

// WESAuthState exposes the current WES access token and the account-level
// capability state to RPC/UI consumers. state is one of the WESState*
// constants (ok / billing_overdue / token_expired / anonymous / transient).
func (s *Service) WESAuthState(ctx context.Context) (token string, state string) {
	if s.tokenSource == nil {
		return "", "anonymous"
	}
	return s.tokenSource.WESAuthState(ctx)
}

// wesGrantWithdrawn reports INV-BILLING-02: identity JWT may still work
// (org: / /api/me) but the wes: grant is empty on purpose.
func (s *Service) wesGrantWithdrawn(ctx context.Context) bool {
	_, state := s.WESAuthState(ctx)
	return state == WESStateBillingOverdue
}

// SetSnapshotCallback registers a callback for context/snapshot notifications.
func (s *Service) SetSnapshotCallback(fn codeintel.SnapshotCallback) {
	s.snapshotMu.Lock()
	defer s.snapshotMu.Unlock()
	s.onSnapshot = fn
}

// Runtime returns the wesgine RuntimeHandle instance (nil if not initialized).
func (s *Service) Runtime() *wesgine.RuntimeHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runtime
}

// Cell returns the active workspace Cell (nil if not initialized).
func (s *Service) Cell() *wesgine.Cell {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cell
}

// Hypervisor returns the wesgine Hypervisor instance (nil if not initialized).
func (s *Service) Hypervisor() *wesgine.Hypervisor {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hyp
}

// CellID returns the current workspace Cell ID (empty if not initialized).
func (s *Service) CellID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cellID
}

// WorkspacePath returns the absolute workspace root the current Cell
// was materialised for.  Empty when Initialize has not run yet.
// (v1.0 P3 follow-up — used by the frontend workspace / Cell badge.)
func (s *Service) WorkspacePath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workspace
}

// CurrentGovernMode returns the workspace Cell's governance mode
// ("open" / "locked"). Used by cell/info RPC and Settings UI.
func (s *Service) CurrentGovernMode() string {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return "unknown"
	}
	spec := cell.Spec()
	if spec.Governance == nil {
		return "open"
	}
	if spec.Governance.Mode != "" {
		return string(spec.Governance.Mode)
	}
	return "open"
}

// normalizeWorkspaceRoot canonicalizes a workspace root path so every consumer
// (Cell ID, WorkspaceRoots, RPC stats, codeintel SQL prefixes) agrees on ONE
// representation: Clean → Abs → symlink-resolve → Windows ToLower.
// D-9: a path must compare equal whether it entered via TS folders, the
// initialize RPC, or was re-read from the index DB — keep this in sync with
// the RPC boundary (handler_codeintel stats root matching).
func normalizeWorkspaceRoot(rootPath string) string {
	cleaned := filepath.Clean(rootPath)
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		abs = cleaned
	}
	// Resolve symlinks so that different path representations of the same
	// directory produce the same identity (e.g. macOS /var → /private/var,
	// or user symlinks in the workspace path).
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	// Windows filesystems are case-insensitive: "C:\Foo" and "c:\foo" are the
	// same directory, so normalize case before hashing (POSIX stays as-is).
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	return abs
}

// normalizeWorkspaceRoots applies normalizeWorkspaceRoot to a list and
// deduplicates (multi-root workspaces may repeat a folder under two casings).
func normalizeWorkspaceRoots(roots []string) []string {
	if len(roots) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(roots))
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		n := normalizeWorkspaceRoot(r)
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

// workspaceCellID derives a deterministic Cell ID from the workspace identity
// path. INV-WS-09: TypeScript always passes the folders[0] DIRECTORY path —
// never a .code-workspace file (that is a file path; using it as CWD fails
// with ENOTDIR, and hashing it would produce a different Cell than hashing
// the folder directory). This function hashes the exact path it receives, so
// callers must pass the same canonical directory path.
// INV-WS-02 (v3): empty path = Config Mode (caller must not call this function).
// INV-WS-03: same path always produces the same CellID.
func workspaceCellID(rootPath string) string {
	abs := normalizeWorkspaceRoot(rootPath)
	sum := sha256.Sum256([]byte(abs))
	return "ws-" + hex.EncodeToString(sum[:4])
}

// engineOwnedDenyPaths lists the DenyPaths entries wescode owns rather than
// the user: the login-session DB and the config file holding provider API
// keys. Both the create path (specOverride) and the existing-Cell reconcile
// derive from here, so a correction lands on installed users too.
//
// hypervisor.db* is also covered by the engine's own computeIsolationDenyPaths;
// it stays listed so this does not silently depend on engine internals.
func engineOwnedDenyPaths(dataDir string) []string {
	return []string{
		filepath.Join(dataDir, "db"),
		// Not dataDir: the config file lives in the XDG *config* dir (or
		// wherever $WESCODE_CONFIG points). `dataDir/config.yaml` denied a
		// path that never exists while the real file — provider api_key
		// values included — stayed readable by every agent tool.
		defaultConfigPath(),
		filepath.Join(dataDir, "hypervisor.db"),
		filepath.Join(dataDir, "hypervisor.db-wal"),
		filepath.Join(dataDir, "hypervisor.db-shm"),
	}
}

// unionPaths returns existing plus whichever add entries it lacks, and
// whether anything was added. Comparison is on cleaned paths so a persisted
// `/a/b/` does not re-add `/a/b`. Existing order is preserved (a settings-UI
// diff stays readable across boots) and the input slice is never mutated —
// it may be the live spec's backing array.
func unionPaths(existing []string, add ...string) ([]string, bool) {
	seen := make(map[string]struct{}, len(existing)+len(add))
	for _, p := range existing {
		seen[filepath.Clean(p)] = struct{}{}
	}
	out := append([]string(nil), existing...)
	for _, p := range add {
		c := filepath.Clean(p)
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, p)
	}
	return out, len(out) != len(existing)
}

// ConfigMode returns true when the engine is in Config Mode (no workspace,
// no Cell, only auth/provider/settings RPC available).
func (s *Service) ConfigMode() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.configMode
}

// PrimaryRoot returns the engine's primary workspace root — the base every
// relative tool path resolves against at execution time (ToolContext.PrimaryRoot).
// Consumers deriving tool paths (edit previews, diff positioning) MUST use this,
// never the per-request Chat WorkDir: the two can diverge and a naive
// join against the wrong root produces phantom paths the engine never wrote.
func (s *Service) PrimaryRoot() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workspace
}

// FileProvider returns the Host's FileProvider for overlay-aware file I/O.
func (s *Service) FileProvider() tool.FileProvider {
	if s.host != nil {
		return s.host.Files()
	}
	return &tool.LocalFileProvider{}
}

// TerminalManager returns the terminal session manager for RPC handlers.
func (s *Service) TerminalManager() *deviceagent.TerminalManager {
	if s.host != nil {
		return s.host.TermMgr
	}
	return nil
}

// SetNotifier saves the RPC notification function for later use by
// Initialize (host construction) and QualityGate (diagnostics push).
func (s *Service) SetNotifier(fn deviceagent.NotifyFn) {
	s.pendingNotifier = fn
	if s.qualityGate != nil {
		s.qualityGate.SetDiagnosticsPush(func(errors []verification.CompileError) {
			type diagnostic struct {
				Path    string `json:"path"`
				Line    int    `json:"line"`
				Column  int    `json:"column"`
				Message string `json:"message"`
			}
			diags := make([]diagnostic, len(errors))
			for i, e := range errors {
				diags[i] = diagnostic{Path: e.Path, Line: e.Line, Column: e.Column, Message: e.Message}
			}
			if err := fn(notify.DiagnosticsSet, map[string]any{"diagnostics": diags}); err != nil {
				slog.Warn("[engine] diagnostics notification failed", "error", err)
			}
		})
		s.qualityGate.SetProgressNotifier(&rpcProgressNotifier{fn: fn})
	}
}

// rpcProgressNotifier adapts deviceagent.NotifyFn to verification.ProgressNotifier.
type rpcProgressNotifier struct {
	fn deviceagent.NotifyFn
}

func (n *rpcProgressNotifier) Notify(phase string, detail string) error {
	return n.fn(notify.VerificationProgress, map[string]string{
		"phase":  phase,
		"detail": detail,
	})
}

// LSPRequest sends an LSP request to the IDE extension via the notifier and
// waits for the response.
func (s *Service) LSPRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if s.pendingNotifier == nil {
		return nil, fmt.Errorf("no notifier (not running in IDE mode)")
	}

	s.lspMu.Lock()
	if s.pendingLSPRequests == nil {
		s.pendingLSPRequests = make(map[string]chan lspResponseEntry)
	}
	s.lspNextID++
	reqID := fmt.Sprintf("lsp-%d", s.lspNextID)
	ch := make(chan lspResponseEntry, 1)
	s.pendingLSPRequests[reqID] = ch
	s.lspMu.Unlock()

	defer func() {
		s.lspMu.Lock()
		delete(s.pendingLSPRequests, reqID)
		s.lspMu.Unlock()
	}()

	slog.Debug("[lsp] request send", "requestId", reqID, "method", method)
	if err := s.pendingNotifier(notify.LSPRequest, map[string]any{
		"requestId": reqID,
		"method":    method,
		"params":    params,
	}); err != nil {
		return nil, fmt.Errorf("lsp request notify: %w", err)
	}

	timeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case <-timeout.Done():
		slog.Warn("[lsp] request timeout", "requestId", reqID, "method", method)
		return nil, fmt.Errorf("lsp request timeout: %s", method)
	case resp := <-ch:
		if resp.Error != "" {
			slog.Warn("[lsp] request error", "requestId", reqID, "method", method, "error", resp.Error)
			return nil, fmt.Errorf("lsp error: %s", resp.Error)
		}
		slog.Debug("[lsp] request result", "requestId", reqID, "method", method, "result_len", len(resp.Result))
		return resp.Result, nil
	}
}

// HandleLSPResponse delivers a response from the IDE extension for a pending
// LSP request.
func (s *Service) HandleLSPResponse(reqID string, result json.RawMessage, errMsg string) {
	s.lspMu.Lock()
	ch, ok := s.pendingLSPRequests[reqID]
	s.lspMu.Unlock()
	if !ok {
		slog.Warn("[lsp] no pending request for response", "requestId", reqID)
		return
	}
	ch <- lspResponseEntry{Result: result, Error: errMsg}
}

// HandleDiagnosticsReport receives IDE diagnostics pushed by the Extension
// (via IMarkerService) and updates the diagnostics cache.
func (s *Service) HandleDiagnosticsReport(diags []codeintel.IDEDiagnostic) {
	if s.diagCache == nil {
		return
	}
	s.diagCache.Update(diags)
	slog.Debug("[diagnostics] IDE diagnostics updated", "count", len(diags), "files", len(s.diagCache.FilesWithErrors()))
}

// FeedTerminalOutput passes terminal output through the diagnostics parser.
func (s *Service) FeedTerminalOutput(terminalID string, data string) {
	if s.termDiagParser != nil {
		s.termDiagParser.Feed(terminalID, data)
	}
}

// ClearTerminalDiag clears terminal diagnostics for a specific terminal.
func (s *Service) ClearTerminalDiag(terminalID string) {
	if s.termDiagParser != nil {
		s.termDiagParser.ClearTerminal(terminalID)
	}
}

// HandleImplicitFeedback processes an implicit feedback event from the Extension.
func (s *Service) HandleImplicitFeedback(ctx context.Context, event ImplicitFeedbackEvent) {
	if s.feedbackHandler == nil {
		return
	}
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return
	}
	s.feedbackHandler.Handle(ctx, cell, memoryActor, event)
}

// Namespaces the two edit patrols write into. They are addresses inside L4,
// not agent ids — the patrols fire on file edits and have no run to ask which
// agent was speaking, so their rows are keyed by what the fact is about (the
// workspace's invariants, the workspace's compile failures) rather than by who
// produced it. The reader is memory(search) over the layer plus the digest's
// regression→convention promotion, both of which scan the layer rather than
// one agent's namespace.
const (
	invariantNamespace  = "default/invariants"
	compileBugNamespace = "default/bugs"
	correctionNamespace = "default/corrections"
	regressionNamespace = "default/regressions"
)

// recordInvariantViolation persists an engineering-invariant breach into
// memory L4 (agent_memory, KindRegression) so later sessions recall this
// file's violation history when it is edited again (cross-session discipline
// feedback). Stored as a *regression* — a factual engineering event ("this
// discipline has been broken before"), NOT a correction (user preference:
// "I like tabs"). digest promotes 2+ same-pattern regressions to a risk
// warning convention (INV-LEARN-11).
//
// Actor is not decoration: L4 is the private domain, so an ownerless row is
// refused outright (INV-MEM-38) and the promotion above only works because the
// digest carries that same owner onto the convention it mints. wescode is
// N=1, hence memoryActor.
//
// Best-effort: a nil Cell or a save failure only degrades to a log line.
func (s *Service) recordInvariantViolation(ctx context.Context, v wesgine.InvariantViolation) {
	slog.Warn("[invariant] violation", "rule", v.RuleID, "path", v.Path, "kind", v.Kind)
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return
	}
	mem := cell.Memory()
	if mem == nil {
		return
	}
	_, err := mem.SaveToLayer(ctx, wesgine.MemoryWrite{
		Layer:     wesgine.LayerAgentMemory,
		Namespace: invariantNamespace,
		Actor:     memoryActor,
		Kind:      "regression",
		Key:       v.RuleID + ":" + v.Path,
		Content: fmt.Sprintf(
			"INV %s violated in %s (%s): %s. This invariant exists because the same breach caused a real regression in the past — review before re-editing.",
			v.RuleID, v.Path, v.Kind, v.Reason),
		Metadata: map[string]string{
			"source":   "invariant_patrol",
			"rule":     v.RuleID,
			"path":     v.Path,
			"kind":     v.Kind,
			"pattern":  v.RuleID, // digest regression→convention groups by this key (INV-LEARN-11)
			"severity": v.Severity,
			"commits":  invariantCommits(v.Reason), // regression evidence trail
		},
	})
	if err != nil {
		slog.Warn("[invariant] memory save failed", "err", err, "rule", v.RuleID)
	}
}

// recordCompileBug persists a compile failure on an edited file into memory
// L4 (agent_memory) as KindBug — the engine's most direct evidence that the
// file carries an engineering bug (M8 KindBug writer #1: compile patrol).
// Key is "compile:<path>" so repeated failures on the same file collapse onto
// one row; without it the line number rides in Content and every re-edit
// mints a near-identical row.
// Best-effort: a nil Cell or a save failure only degrades to a log line.
func (s *Service) recordCompileBug(ctx context.Context, b verification.BugEvent) {
	slog.Warn("[bug] compile failure on edited file", "path", b.Path, "line", b.Line)
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return
	}
	mem := cell.Memory()
	if mem == nil {
		return
	}
	_, err := mem.SaveToLayer(ctx, wesgine.MemoryWrite{
		Layer:     wesgine.LayerAgentMemory,
		Namespace: compileBugNamespace,
		Actor:     memoryActor,
		Kind:      "bug",
		Key:       "compile:" + b.Path,
		Content: fmt.Sprintf(
			"Compile bug in %s:%d: %s. The agent edited this file and broke compilation — verify the fix before moving on.",
			b.Path, b.Line, b.Message),
		Metadata: map[string]string{
			"source": "compile_patrol",
			"path":   b.Path,
			"kind":   "compile",
		},
	})
	if err != nil {
		slog.Warn("[bug] memory save failed", "err", err)
	}
}

// commitHashRe matches abbreviated (7-12 hex) commit hashes cited in
// invariant rule reasons, e.g. "f158071c removed → 61fa7a17 regressed".
// The \b anchors alone are not enough: words like "deadbeef" are entirely
// a-f letters and would match. invariantCommits additionally requires at
// least one digit, which real hashes almost always contain (reduces false
// positives on prose; a fully-letter hash is astronomically rare).
var commitHashRe = regexp.MustCompile(`\b[0-9a-f]{7,12}\b`)

// invariantCommits extracts the commit hashes cited in the rule reason
// (e.g. "f158071c removed → 61fa7a17 regressed → b16173b0 re-removed") so the
// regression entry carries its evidence trail for recall.
func invariantCommits(reason string) string {
	var hashes []string
	for _, m := range commitHashRe.FindAllString(reason, -1) {
		if !strings.ContainsAny(m, "0123456789") {
			continue // "abcdef"/"deadbeef" style prose false positive
		}
		hashes = append(hashes, m)
		if len(hashes) >= 8 {
			break
		}
	}
	return strings.Join(hashes, ",")
}

// RecentTerminalErrors implements codeintel.TerminalDiagProvider.
func (s *Service) RecentTerminalErrors(within time.Duration) []codeintel.TerminalError {
	if s.termDiagCache == nil {
		return nil
	}
	raw := s.termDiagCache.Recent(within)
	out := make([]codeintel.TerminalError, len(raw))
	for i, r := range raw {
		out[i] = codeintel.TerminalError{
			TerminalID: r.TerminalID,
			Timestamp:  r.Timestamp,
			Pattern:    r.Pattern,
			Lang:       r.Lang,
			File:       r.File,
			Line:       r.Line,
			Column:     r.Column,
			Message:    r.Message,
			Context:    r.Context,
		}
	}
	return out
}

// AllTerminalErrors implements codeintel.TerminalDiagProvider.
func (s *Service) AllTerminalErrors() []codeintel.TerminalError {
	if s.termDiagCache == nil {
		return nil
	}
	raw := s.termDiagCache.All()
	out := make([]codeintel.TerminalError, len(raw))
	for i, r := range raw {
		out[i] = codeintel.TerminalError{
			TerminalID: r.TerminalID,
			Timestamp:  r.Timestamp,
			Pattern:    r.Pattern,
			Lang:       r.Lang,
			File:       r.File,
			Line:       r.Line,
			Column:     r.Column,
			Message:    r.Message,
			Context:    r.Context,
		}
	}
	return out
}

// GetPendingTerminalOverlay returns a formatted overlay for recent terminal errors.
// Used for auto-injection into chat context at Run start.
func (s *Service) GetPendingTerminalOverlay() string {
	if s.termDiagCache == nil || s.termDiagCache.IsEmpty() {
		return ""
	}
	recent := s.termDiagCache.Recent(60 * time.Second)
	if len(recent) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n<terminal_errors>\nRecent terminal errors detected:\n")
	for i, e := range recent {
		if i >= 5 {
			fmt.Fprintf(&sb, "... and %d more\n", len(recent)-5)
			break
		}
		fmt.Fprintf(&sb, "- [%s] %s", e.Pattern, e.Message)
		if e.File != "" {
			fmt.Fprintf(&sb, " at %s:%d", e.File, e.Line)
		}
		sb.WriteByte('\n')
	}
	sb.WriteString("</terminal_errors>")
	return sb.String()
}

// makeEmbeddingFunc returns an EmbeddingFunc that calls the Cell's Provider embedding
// endpoint. Returns nil if the Cell is not available (CLI fallback → RI only).
func (s *Service) makeEmbeddingFunc() codeintel.EmbeddingFunc {
	return func(ctx context.Context, texts []string) ([][]float64, error) {
		s.mu.Lock()
		cell := s.cell
		s.mu.Unlock()
		if cell == nil {
			return nil, fmt.Errorf("embedding: cell not available")
		}
		inputJSON, err := json.Marshal(texts)
		if err != nil {
			return nil, err
		}
		resp, err := cell.Providers().Embed(ctx, wesgine.EmbedRequest{
			Input: json.RawMessage(inputJSON),
		})
		if err != nil {
			return nil, fmt.Errorf("embedding provider: %w", err)
		}
		vectors := make([][]float64, len(resp.Data))
		for _, item := range resp.Data {
			if item.Index < len(vectors) {
				vectors[item.Index] = item.Embedding
			}
		}
		return vectors, nil
	}
}

// RequestDebug sends a debug command to the IDE extension and waits for a response.
// INV-DEBUG-02: best-effort, 5s timeout.
func (s *Service) RequestDebug(ctx context.Context, method notify.Method, params any) (json.RawMessage, error) {
	if s.pendingNotifier == nil {
		return nil, fmt.Errorf("debug: notifier not connected")
	}

	s.debugMu.Lock()
	if s.pendingDebugReqs == nil {
		s.pendingDebugReqs = make(map[string]chan json.RawMessage)
	}
	s.debugNextID++
	reqID := fmt.Sprintf("dbg-%d", s.debugNextID)
	ch := make(chan json.RawMessage, 1)
	s.pendingDebugReqs[reqID] = ch
	s.debugMu.Unlock()

	defer func() {
		s.debugMu.Lock()
		delete(s.pendingDebugReqs, reqID)
		s.debugMu.Unlock()
	}()

	if err := s.pendingNotifier(method, map[string]any{
		"requestId": reqID,
		"params":    params,
	}); err != nil {
		return nil, fmt.Errorf("debug request notify: %w", err)
	}

	timeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case <-timeout.Done():
		return nil, fmt.Errorf("debug request timeout: %s", method)
	case resp := <-ch:
		return resp, nil
	}
}

// HandleDebugResponse delivers a response from the IDE extension for a pending
// debug command.
func (s *Service) HandleDebugResponse(reqID string, result json.RawMessage) {
	s.debugMu.Lock()
	ch, ok := s.pendingDebugReqs[reqID]
	s.debugMu.Unlock()
	if !ok {
		slog.Warn("[debug] no pending request for response", "requestId", reqID)
		return
	}
	ch <- result
}

// startBackgroundIndex cancels any in-flight background indexing and starts
// a fresh run with the current WorkspaceRoots().
// IMPORTANT: caller must NOT hold s.mu — this method manages its own locking.
func (s *Service) startBackgroundIndex() {
	s.mu.Lock()
	if s.indexCancel != nil {
		s.indexCancel()
	}
	// Wait for the previous goroutine to exit before spawning a new one,
	// so the old run does not read tsPool/codeIndex after they are swapped.
	prevDone := s.indexDone
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.indexCancel = cancel
	s.indexDone = done
	s.mu.Unlock()
	if prevDone != nil {
		<-prevDone
	}
	go func() {
		defer close(done)
		s.backgroundIndexAll(ctx)
	}()
}

// startBackgroundIndexLocked is like startBackgroundIndex but for callers
// that already hold s.mu. It releases the lock before starting the goroutine.
func (s *Service) startBackgroundIndexLocked() {
	if s.indexCancel != nil {
		s.indexCancel()
	}
	// Wait for the previous goroutine to exit before spawning a new one,
	// so the old run does not read tsPool/codeIndex after they are swapped.
	prevDone := s.indexDone
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.indexCancel = cancel
	s.indexDone = done
	// Must unlock before spawning goroutine that will re-acquire s.mu
	s.mu.Unlock()
	if prevDone != nil {
		<-prevDone
	}
	go func() {
		defer close(done)
		s.backgroundIndexAll(ctx)
	}()
}

// WorkspaceRoots returns all workspace folder roots known to the engine.
func (s *Service) workspaceStatusLine(idx *codeintel.CodeIndex) string {
	s.mu.Lock()
	ws := s.workspace
	ds := s.docSync
	s.mu.Unlock()

	if ws == "" {
		return "[workspace: empty — no project files. You can help the user create a new project using write/exec tools.]"
	}

	r := idx.Readiness()
	if r.Indexing {
		return fmt.Sprintf("[workspace: indexing %d%% — code search tools unavailable, use read/grep instead.]",
			int(r.Completeness*100))
	}

	symbolCount, _ := idx.SymbolCount(context.Background())
	if symbolCount == 0 && r.IndexedFiles == 0 {
		return "[workspace: index unavailable — code search tools may fail, use read/grep instead.]"
	}

	status := fmt.Sprintf("[workspace: ready — %d files, %d symbols indexed.", r.IndexedFiles, symbolCount)
	if ds != nil {
		kbFiles, _ := ds.ListFiles(context.Background())
		if len(kbFiles) > 0 {
			status += fmt.Sprintf(" Knowledge base: %d documents indexed — use knowledge_search for documentation queries.", len(kbFiles))
		}
	}
	status += "]"
	return status
}

func (s *Service) WorkspaceRoots() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.workspaceRoots) > 0 {
		return s.workspaceRoots
	}
	if s.workspace != "" {
		return []string{s.workspace}
	}
	return nil
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// GetCodeIndex returns the code index for external consumers.
func (s *Service) GetCodeIndex() *codeintel.CodeIndex {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.codeIndex
}

// ReindexProject clears a single project's indexed data and triggers a
// background re-scan of all roots (the scanner is idempotent on unchanged
// files, so only the cleared project does real work).
func (s *Service) ReindexProject(projectRoot string) {
	s.mu.Lock()
	if s.codeIndex != nil {
		s.codeIndex.ClearProject(projectRoot)
	}
	s.startBackgroundIndexLocked()
}

// ClearProject drops a single project's indexed data without re-scanning.
func (s *Service) ClearProject(projectRoot string) {
	s.mu.Lock()
	if s.codeIndex != nil {
		s.codeIndex.ClearProject(projectRoot)
	}
	s.mu.Unlock()
}

// GetTopologyDetector returns the project topology detector for external consumers.
func (s *Service) GetTopologyDetector() *codeintel.ProjectTopologyDetector {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.topologyDetector
}

// DefaultDataDir returns the default wescode data directory path.
func DefaultDataDir() string {
	return defaultDataDir()
}

// wescodeAppIdentity is the L1 application-level identity injected into every
// agent's system prompt via WithAppIdentity.
func wescodeAppIdentity() string {
	return baseAppIdentity + platformPrompt()
}

// resolveAppIdentity merges the built-in identity with user-configured custom
// system prompt from config.yaml. Custom prompt appends after built-in.
func (s *Service) resolveAppIdentity() string {
	base := wescodeAppIdentity()
	custom := s.cfg.AppIdentity
	if custom == "" {
		return base
	}
	return base + "\n\n" + custom
}

const baseAppIdentity = `You are an AI programming assistant embedded in a code editor.
The user watches you work in real time. In coding, your reasoning is more valuable than the execution result.

Interaction style:
- Before editing, briefly explain what you will change and why — the user needs to understand the reasoning, not just the diff. One or two sentences suffice; do not write a design doc.
- For trivial, single-location fixes (typo, missing import, obvious rename), apply directly without preamble.
- When a change spans multiple files, state the scope upfront so the user knows what to expect.
- After completing work, summarise what changed and highlight anything the user should review. Do not repeat code the user can already see in the diff.
- Do not execute more than 3 tool calls in a row without outputting reasoning text. The user cannot follow silent tool chains.`

// platformPrompt returns OS-specific constraints appended to the agent
// identity so generated commands run in the actual environment. Without this,
// the model's POSIX training data produces commands that fail on Windows.
func platformPrompt() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	return `

Platform (Windows):
- Shell commands execute via cmd.exe. Avoid POSIX-only syntax: single quotes, heredocs, chmod, ./script.sh, unix pipes with grep/find/awk, and && chains with > redirection quirks.
- Use cmd/PowerShell-compatible commands and tool names with their Windows extensions (python, rg, where, dir, type instead of cat).
- File paths use backslashes and drive letters (C:\Users\...). Use %TEMP% instead of /tmp.`
}

// applyWorkspaceConfigLocked updates workspace path, roots, and triggers
// re-indexing if anything changed. Caller MUST hold s.mu. This method
// ALWAYS releases s.mu before returning (either via Unlock or startBackgroundIndexLocked).
// This is the SINGLE place where workspace config is applied — no other
// code path should duplicate this logic.
func (s *Service) applyWorkspaceConfigLocked(workspacePath string, workspaceFolders []string) string {
	rootsChanged := false

	// D-9: normalize every root through the same pipeline as workspaceCellID
	// (Clean → Abs → symlink-resolve → Windows ToLower) BEFORE storing, so
	// WorkspaceRoots()/stats return ONE representation and the RPC boundary
	// matches the Cell ID hash input. All downstream consumers (doc-sync,
	// LSP pool, pathaccess, codeintel) receive the normalized roots.
	normalizedFolders := normalizeWorkspaceRoots(workspaceFolders)
	if len(workspaceFolders) > 0 && !stringSlicesEqual(s.workspaceRoots, normalizedFolders) {
		slog.Info("[engine] workspace roots changed", "old", s.workspaceRoots, "new", normalizedFolders)
		s.workspaceRoots = normalizedFolders
		rootsChanged = true
	}
	if workspacePath != "" && workspacePath != s.workspace {
		slog.Info("[engine] workspace path changed", "old", s.workspace, "new", workspacePath)
		s.workspace = workspacePath
		if s.qualityGate != nil {
			s.qualityGate.SetWorkDir(workspacePath)
		}
		rootsChanged = true
	}
	if s.codeAsm != nil {
		s.codeAsm.UpdateEditorState(codeintel.EditorState{})
		s.codeAsm.ResetRunState()
	}
	ws := s.workspace
	ds := s.docSync
	cell := s.cell
	if rootsChanged && s.codeIndex != nil {
		slog.Info("[engine] re-indexing after workspace config change", "roots", len(s.workspaceRoots))
		s.startBackgroundIndexLocked() // releases s.mu
	} else {
		s.mu.Unlock()
	}

	if rootsChanged && ds != nil {
		go ds.SyncRoots(context.Background(), normalizedFolders)
	}

	// Sync Cell's AgentDefaults.Workspace.AllowPaths so the pathaccess
	// checker allows reads from all multi-root workspace folders.
	if rootsChanged && cell != nil {
		cell.UpdateWorkspacePaths(ws, normalizedFolders)
	}

	return ws
}

func (s *Service) Initialize(ctx context.Context, workspacePath string, workspaceFolders []string) (resolvedWorkDir string, err error) {
	s.mu.Lock()

	// Fast path: already initialized — apply workspace config or no-op.
	//
	// INV-WS-05: Cell ID is immutable for the lifetime of the Go process.
	// Each Go process serves exactly ONE workspace. The TypeScript layer
	// (WescodeBackendMainService) spawns separate Go processes per workspace.
	// Cell switching within a single process is architecturally impossible.
	if s.initialized {
		ws := strings.TrimSpace(workspacePath)

		// Config Mode → Cell Mode transition (e.g. drag folder to empty window).
		if s.configMode && ws != "" {
			slog.Info("[engine] Config Mode → Cell Mode transition",
				"new_workspace", ws)
			s.initialized = false
			s.configMode = false
			s.mu.Unlock()
			return s.Initialize(ctx, workspacePath, workspaceFolders)
		}

		// Cell Mode: workspace roots may have changed (multi-root add/remove).
		// The Cell stays the same; only exec CWD and roots update.
		if !s.configMode {
			return s.applyWorkspaceConfigLocked(workspacePath, workspaceFolders), nil
		}

		// Config Mode → Config Mode: no-op.
		s.mu.Unlock()
		return "", nil
	}

	// Wait path: another goroutine is booting — wait, then apply config.
	if s.initializing {
		ch := s.initDone
		s.mu.Unlock()
		slog.Info("[engine] Initialize: waiting for in-flight boot to complete", "workspace", workspacePath)
		select {
		case <-ch:
		case <-ctx.Done():
			return "", fmt.Errorf("engine: initialize wait cancelled: %w", ctx.Err())
		}
		s.mu.Lock()
		if s.initialized {
			return s.applyWorkspaceConfigLocked(workspacePath, workspaceFolders), nil
		}
		s.mu.Unlock()
		if s.initErr != nil {
			return "", fmt.Errorf("engine: boot failed (waited): %w", s.initErr)
		}
		return "", fmt.Errorf("engine: boot completed but service not initialized")
	}

	s.initializing = true
	s.initDone = make(chan struct{})
	s.initErr = nil

	ws := strings.TrimSpace(workspacePath)

	// Config Mode: no workspace → no Cell, no Hypervisor.
	// Only auth/provider/settings RPC available via configModeAllowed whitelist.
	if ws == "" {
		slog.Info("[engine] entering Config Mode (no workspace)")
		s.configMode = true
		s.initialized = true
		s.initializing = false
		s.dataDir = strings.TrimSpace(os.Getenv("WESCODE_DATA_DIR"))
		if s.dataDir == "" {
			s.dataDir = defaultDataDir()
		}
		close(s.initDone)
		s.mu.Unlock()
		return "", nil
	}

	// Cell Mode: resolve workspace path and proceed with full boot.
	var wsAbs string
	abs, err := filepath.Abs(ws)
	if err != nil {
		s.mu.Unlock()
		return "", fmt.Errorf("engine: resolve workspace path: %w", err)
	}
	wsAbs = abs

	dataDir := strings.TrimSpace(os.Getenv("WESCODE_DATA_DIR"))
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		s.mu.Unlock()
		return "", fmt.Errorf("engine: create data dir %q: %w", dataDir, err)
	}
	s.configMode = false

	// Provider seed: read from config.yaml on disk (the user's original
	// BYOK configuration). s.cfg.Providers must NOT be used here — in
	// Cell Mode it only contains Config Mode leftovers and is never
	// synced from Cell state (no sync = no masked-key pollution).
	var pendingSeedProviders []wesconfig.ProviderConfig
	if diskCfg, err := LoadConfig(); err == nil {
		for _, p := range diskCfg.Providers {
			if strings.TrimSpace(p.APIKey) != "" {
				pendingSeedProviders = append(pendingSeedProviders, toWesProviderConfigs([]ProviderConfig{p})...)
			}
		}
	}

	// v1.0: community license is engine.CommunityLicense inside StartEngine.
	skillsDir := filepath.Join(dataDir, "cells", workspaceCellID(wsAbs), "skills")
	if s.cfg.SkillsOverrideDir != "" {
		skillsDir = s.cfg.SkillsOverrideDir
	}
	// Skill seeding moved to after Cell creation (line ~1175) via cell.SeedSkills.

	imGatewayCfg := wesconfig.DefaultIMGatewayConfig()
	imGatewayCfg.Bindings = append([]wesconfig.ChannelBinding(nil), s.cfg.IMGateway.Bindings...)

	tsPool := treesitter.NewParserPoolN(4)
	ee := editengine.New(func(path string, content []byte) []editengine.SyntaxWarning {
		lang, ok := treesitter.DetectLang(path)
		if !ok {
			return nil
		}
		tree, err := tsPool.Parse(lang, content, nil)
		if err != nil {
			return nil
		}
		defer tree.Close()
		errs := treesitter.CollectErrors(tree, content)
		warnings := make([]editengine.SyntaxWarning, len(errs))
		for i, e := range errs {
			warnings[i] = editengine.SyntaxWarning{Line: e.Line, Column: e.Column, Message: e.Message}
		}
		return warnings
	})

	// v1.0 P2 device-agent surface: BufferOverlayHost is the concrete
	// implementation of the capabilities enumerated in
	// internal/deviceagent.  Logged here so ops can grep "device_agent"
	// to see which capabilities are attached at Cell boot.  Cell ID
	// materialises further down (workspaceCellID) — log with workspace
	// path here and re-log with the resolved cell id after boot.
	slog.Info("[engine] device_agent surface init",
		"workspace", wsAbs,
		"capabilities", deviceAgentCaps(),
		"transport", "cellspec.HostEnvironment + private JSON-RPC")
	host := deviceagent.NewHost(s.bufferStore, s.pendingNotifier, func() codeintel.EditorState {
		s.mu.Lock()
		asm := s.codeAsm
		s.mu.Unlock()
		if asm == nil {
			return codeintel.EditorState{}
		}
		return asm.EditorState()
	})
	s.host = host
	engineReadPaths := []string{
		skillsDir,
		filepath.Join(dataDir, ".plans"),
	}
	wrappedShell := &engineShellProvider{base: host.Shell(), enginePaths: engineReadPaths}
	ee.SetFileProvider(host.Files())
	// 影子仓库按 cell 分区，随 cell 一起被清理（EE-15）。
	shadowRoot := filepath.Join(dataDir, "cells", workspaceCellID(wsAbs), "shadow")
	ee.SetCheckpointManager(editengine.NewCheckpointManager(shadowRoot, wsAbs, wrappedShell))

	vCfg := verification.MergeConfig(verification.DefaultConfig(), s.cfg.Verification)
	vCfg.BenchMode = s.cfg.BenchMode
	qgSP := newQualityMirrorProvider(&engineShellProvider{base: &tool.LocalShellProvider{}, enginePaths: engineReadPaths}, s.pendingNotifier)
	qualityGate := verification.NewCodeQualityGate(vCfg, wsAbs, qgSP)
	qualityGate.SetModifiedFilesFn(ee.ModifiedFiles)

	cellID := workspaceCellID(wsAbs)
	// Deferred CodeIndex: no disk I/O until Cell directory is stable.
	// BindPath is called after Cells.GetOrCreate (see below).
	codeIdx := codeintel.NewCodeIndexDeferred(tsPool)
	codeIdx.SetWorkDir(wsAbs)
	codeIdx.EmbeddingFn = s.makeEmbeddingFunc()
	ee.SetCKGNotifier(func(path string) { codeIdx.IndexFile(context.Background(), path) })
	// InitReadinessCounts deferred to postInitialize — 626MB index DB makes
	// this query take 30-50s. Boot can proceed without readiness stats.

	// INV-LSP-09: language servers belong to editor extensions.
	// Window → ask the running IDE LSP. No window → NoopLSP, CKG only.
	// The backend must not spawn gopls / typescript-language-server / etc.
	var lspBridge codeintel.LSPBridge
	if s.pendingNotifier != nil {
		lspBridge = codeintel.NewIDELSPBridge(s.LSPRequest)
		slog.Info("[codeintel] using IDE LSP bridge (extension-owned language servers)")
	} else {
		lspBridge = codeintel.NoopLSP{}
		slog.Info("[codeintel] using NoopLSP (no window; CKG only)")
	}

	diagCache := codeintel.NewDiagnosticsCache()
	diagBaseline := codeintel.NewDiagnosticsBaseline()
	termDiagCache := NewTerminalDiagCache()
	termDiagParser := NewTerminalDiagParser(termDiagCache)
	attentionTracker := NewAttentionTracker()
	feedbackHandler := NewImplicitFeedbackHandler()
	tcrTracker := NewTCRTracker(nil)

	codeAsm := codeintel.NewCodeAssembler(nil)
	retriever := codeintel.NewRetriever(codeIdx, tsPool,
		codeintel.WithLSP(lspBridge),
		codeintel.WithMetrics(codeAsm.Metrics()),
		codeintel.WithFileProvider(host.Files()),
	)
	codeAsm.SetRetriever(retriever)
	codeAsm.SetGitProvider(codeintel.NewGitContextProvider(host.Shell()))
	codeAsm.SetTypeContextProvider(codeintel.NewTypeContextProvider(lspBridge, tsPool, host.Files()))
	codeAsm.BuildDefaultOverlays(codeIdx)
	codeAsm.SetWorkspaceStatusFn(func() string {
		return s.workspaceStatusLine(codeIdx)
	})

	codeSegmenter := codeintel.NewCodeSegmenter(tsPool)

	debugStore := NewDebugEventStore(50)
	logWatcher := NewLogWatcher(debugStore)

	_, lspIsNoop := lspBridge.(codeintel.NoopLSP)
	capProvider := codeintel.NewCapabilityProvider(codeintel.CodeIntelCapabilityConfig{
		LSPAvailable:  !lspIsNoop,
		LSPLanguages:  detectLSPLanguages(lspBridge),
		HasTreeSitter: tsPool != nil,
		EditTier:      3,
	})
	s.codeintelCap = capProvider
	_ = capProvider

	// A2 P1 follow-up: build the wescode-specific edit MatchFallback
	// (Tier 2 Normalized + Tier 3 tree-sitter Structural). Installed
	// on the builtin.Edit tool inside cell.Boot via
	// CellSpec.EditMatchFallback.
	editFallback := editengine.NewCombinedFallback(tsPool, ee.Metrics)

	var constraintReg *constraints.Registry
	cycleCfg := wesgine.CycleDetectConfig{
		ExplorationWarnThreshold:   50,
		ExplorationTermThreshold:   100,
		ActionWarnThreshold:        25,
		ActionTermThreshold:        50,
		IdenticalCallWarnThreshold: 4,
		IdenticalCallTermThreshold: 7,
		ToolOverrides: map[string][2]int{
			"exec":          {30, 60},
			"exec:readonly": {50, 100},
			"edit":          {30, 60},
			"write":         {30, 60},
			"apply_patch":   {30, 60},
			// AGENTS.md:498 — v1.1: [10,20]→[15,30]. Plan is a planning
			// capability, not exploration: it gets its own tighter cycle
			// thresholds (matches the documented contract).
			"plan": {15, 30},
		},
	}

	specOverride := func(spec *wesgine.CellSpec) error {
		spec.ID = cellID
		gov := governance.DefaultGovernance()
		spec.Governance = &gov
		// Union, not "only when empty": the previous guard also read as
		// "respect the user's list", but this spec is rebuilt from zero on
		// every boot, so the guard was always true and never protected
		// anything. What actually keeps a settings-UI edit is that
		// GetOrCreate ignores DenyPaths for an existing Cell — which is also
		// why the reconcile after GetOrCreate exists.
		spec.DenyPaths, _ = unionPaths(spec.DenyPaths, engineOwnedDenyPaths(dataDir)...)
		spec.EnginePaths = append(spec.EnginePaths,
			filepath.Join(dataDir, "skills"),
			filepath.Join(dataDir, ".plans"),
		)
		spec.RuntimeDir = filepath.Join(dataDir, "runtime")
		spec.QualityGate = qualityGate
		spec.AgentRouter = wesgine.NewLLMSelectorWithConfig(nil, wesgine.LLMSelectorConfig{
			EnableGroup: true,
		})
		spec.EditPatrol = verification.NewCompilePatrolWithInvariants(wsAbs, host.Shell(), s.recordInvariantViolation).WithBugRecorder(s.recordCompileBug)
		spec.PlanStepCheckpointFn = s.planStepCheckpointFn()
		spec.ContextAssemblerWrapper = codeAsm.WrapperFn()
		spec.CycleDetect = &cycleCfg
		// [MEM-CONT-01] wescode 需要跨 Session 连续性：一个 workspace 是一个
		// 长期项目，用户昨天纠正过的事今天仍然成立。引擎默认 isolated
		// （白板模式）会让 Lazy recall 只召回本会话产出的行，所以这里必须
		// 显式声明 full。
		//
		// full 只是**打开**跨会话豁免，豁免本身按 Kind 判定（INV-MEM-54）：
		// durable kind 跨会话，episodic kind（context / plan）仍绑会话。
		// 这一条曾经写的是「Correction 无 session_id 标签所以对新 Session
		// 不可见」，那个描述里有两个已经不成立的东西——豁免曾按 Formation
		// 判定（而生产写入路径不填它，于是 correction 实际从未跨会话，契约
		// 只在 fixture 里成立），而 session 曾是 metadata 键（现在是列）。
		//
		// ColdStart 窗口仍默认 0：跨会话召回要的是「结论还有效」，不是把
		// 上一段对话的摘要重放进这一段。
		spec.MemoryLimits = &wesgine.MemoryLimitsConfig{
			MemoryContinuity: "full",
		}
		// MemoryWritePolicy is deliberately absent: it is a plain user
		// setting with no derivation, so CellSpec (which wesgine persists) is
		// its only store. Setting it here as well would reach first boot only
		// — GetOrCreate keeps the persisted spec for an existing Cell — and
		// the settings UI now patches the spec directly.
		//
		// A2 P1 follow-up: wescode tool-runtime hooks. wesgine forwards
		// these onto loop.Deps (Host/FileSegmenter/AppIdentity) and
		// installs EditMatchFallback onto builtin.Edit inside cell.Boot.
		spec.HostEnvironment = host
		spec.AppIdentity = s.resolveAppIdentity()
		spec.FileSegmenter = codeSegmenter
		spec.EditMatchFallback = editFallback
		spec.FileCardSymbolExtractor = codeIdx.TopSymbols
		spec.PostRunFn = s.buildPostRunFn()
		spec.TokenRefreshFn = func(ctx context.Context) bool {
			if s.wesGrantWithdrawn(ctx) {
				return false
			}
			return s.EnsureWesTokenFresh(ctx)
		}
		spec.BillingBlockedFn = func(ctx context.Context) bool {
			return s.wesGrantWithdrawn(ctx)
		}
		// Live-token path: wes:/org: providers resolve their identity JWT
		// per HTTP request (sliding renewal lives inside the token source),
		// so a 15-minute TTL is never frozen into the provider at
		// construction time and cannot expire mid-Run. org: uses the
		// identity token (INV-BILLING-03: org channel bypasses the personal
		// WES wallet, so a withdrawn personal grant must not block org
		// refresh); wes: uses the WES grant token, empty when withdrawn.
		// Fail-closed: any other provider name yields "" (no credential).
		spec.ProviderLiveKeyFn = func(ctx context.Context, providerName string) string {
			if s.tokenSource == nil {
				return ""
			}
			switch {
			case providerid.IsOrg(providerName):
				return s.tokenSource.IdentityAccessToken(ctx)
			case providerid.IsWES(providerName):
				return s.tokenSource.ValidAccessToken(ctx)
			default:
				return ""
			}
		}
		// Phase 4: wire all Pre/PostCallHooks into the per-Cell Executor.
		// This fixes the critical bug where ModifiedFiles() was always empty
		// (PostCallHook.TrackModified never ran), causing QualityGate to
		// always early-return Pass.
		// ToolExecutorHooks: EditEngine pre/post hooks + CSE PreWriteCheck.
		// PreWriteCheck uses a lazy delegate: specOverride registers closures
		// that forward to atomic pointers filled by postInitialize (after
		// InferFromProject completes and constraintReg is populated).
		var preWritePre atomic.Pointer[tool.PreCallHook]
		var preWritePost atomic.Pointer[tool.PostCallHook]
		lazyCSEPre := func(ctx context.Context, call tool.ToolCall, tc *tool.ToolContext) (*tool.ToolResult, error) {
			if fn := preWritePre.Load(); fn != nil {
				return (*fn)(ctx, call, tc)
			}
			return nil, nil
		}
		lazyCSEPost := func(ctx context.Context, call tool.ToolCall, result *tool.ToolResult, tc *tool.ToolContext) {
			if fn := preWritePost.Load(); fn != nil {
				(*fn)(ctx, call, result, tc)
			}
		}
		s.preWritePrePtr = &preWritePre
		s.preWritePostPtr = &preWritePost
		spec.ToolExecutorHooks = tool.Hooks{
			PreCalls: []tool.PreCallHook{
				ee.PreCallHook(),
				lazyCSEPre,
			},
			PostCalls: []tool.PostCallHook{
				ee.PostCallHook(),
				s.bufferInvalidateHook(),
				s.agentWriteTrackHook(),
				codeintel.NewPostWriteIndex(codeIdx),
				codeintel.NewConfidenceEnrichHook(codeIdx, s.ckgTools),
				s.crashDetectHook(),
				lazyCSEPost,
			},
		}
		// ADR-323: assign low-frequency builtin tools to domains so they
		// are excluded from per-turn schema unless explicitly activated.
		// High-frequency coding tools (grep/glob/edit) stay domain-free
		// to avoid extra tool_search RTT on every coding session.
		// Combined with progressive schema degradation (turn 1+ How-Only),
		// this reduces tool schema tokens from ~18.6K to ~8K.
		spec.ToolDomainOverrides = map[string]string{
			"apply_patch": "code_edit",
			"fetch_url":   "network",
			"memory":      "knowledge",
			"skill":       "knowledge",
			"session":     "session_mgmt",
		}
		// v1.0 INV-PROVIDER-03: strategy 必显式声明。wescode workspace
		// 场景采用 shared_first（平台账号优先，用户 BYOK 备用），既契
		// 合 wesgine AGENTS §15 编程 workspace 建议，又能覆盖 BYOK 用
		// 户从 config.yaml migrate 过来的 seed provider。
		spec.ProviderStrategy = wesgine.ProviderStrategySharedFirst
		// PrimaryRoot = VS Code primary workspace folder (the "project I'm working in").
		// AllowPaths = additional multi-root workspace folders (HostPaths authorization).
		// ArtifactDir is engine-resolved from (CellDataDir, Actor) — not set here.
		spec.AgentDefaults = &wesagent.AgentDefaults{
			Workspace: wesagent.WorkspaceConfig{
				PrimaryRoot: wsAbs,
				AllowPaths:  workspaceFolders,
			},
		}
		// v1.0 破铸：从 config.yaml 收集来的可用 provider（用户已在
		// yaml 中填了 API key 的）seed 到 CellSpec.PrivateProviders，
		// 由 wesgine 首次 boot 时经 hypervisor.db 持久化；之后 CRUD
		// 走 providerMgr → UpdateSpec，config.yaml 不再回读。
		if len(pendingSeedProviders) > 0 {
			spec.PrivateProviders = append(spec.PrivateProviders, pendingSeedProviders...)
		}
		if err := appruntime.SetupRuntime(ctx, spec, appruntime.Opts{
			TargetDir: filepath.Join(dataDir, "runtime"),
			PythonFS:  wescoderuntime.PythonFS(),
		}); err != nil {
			slog.Warn("runtime setup failed (non-fatal)", "error", err)
		}
		return nil
	}

	// PostBootFn re-seeds all volatile runtime state on every Cell.Start
	// (including Cool→Warm). Agents, Tier C tools, and disk-loaded agents
	// are all lost when the temperature scheduler Cools the Cell.
	postBootFn := func(bootCtx context.Context, c *wesgine.Cell) error {
		seedBuiltinAgentsToCell(bootCtx, c)

		// Presets replay from the embedded YAML; agents the user created have
		// no copy but wc_agents, so this is what keeps them alive across the
		// Cool→Warm that rebuilds the engine's store. Restoring after the
		// presets leaves the user's row authoritative on an id collision.
		// First boot finds this nil — Initialize restores there instead.
		s.mu.Lock()
		userAgents := s.userAgents
		s.mu.Unlock()
		if userAgents != nil {
			if n, err := userAgents.RestoreInto(bootCtx, c.Agents()); err != nil {
				slog.Error("[engine] user agent re-seed failed", "error", err)
			} else if n > 0 {
				slog.Info("[engine] user agents re-seeded", "count", n)
			}
		}

		reg := c.Tools()
		// Observe runs on every registered tool, not just the CKG ones: the
		// caller deciding which tools to declare is how the stale allowlist
		// came about. CKGToolSet asks the type instead.
		doReg := func(t tool.Tool) {
			s.ckgTools.Observe(t)
			if err := reg.Register(t); err != nil {
				slog.Warn("[engine] re-register tool failed", "name", t.Name(), "error", err)
			}
		}

		if !s.cfg.DisableCKG {
			doReg(codeintel.NewSearchSymbolsTool(codeIdx,
				func() codeintel.EditorState { return codeAsm.EditorState() }))
			doReg(codeintel.NewFindReferencesTool(lspBridge, codeIdx))
			doReg(codeintel.NewProjectMapTool(codeIdx, wsAbs))
			doReg(codeintel.NewGetDiagnosticsTool(diagCache, lspBridge, diagBaseline))
			doReg(codeintel.NewFixDiagnosticsTool(diagCache, lspBridge, diagBaseline))
			doReg(codeintel.NewApplyCodeActionTool(lspBridge))
			doReg(codeintel.NewLSPRenameTool(lspBridge))
			doReg(codeintel.NewLSPOrganizeImportsTool(lspBridge))
			doReg(codeintel.NewGetTerminalErrorsTool(s))
			doReg(codeintel.NewGetAttentionContextTool(s))
			doReg(codeintel.NewTraceVariableTool(tsPool, lspBridge, host.Files()))
			doReg(codeintel.NewControlFlowTool(tsPool, host.Files()))
			doReg(codeintel.NewCrossLanguageTool(codeIdx))
			doReg(codeintel.NewDebugContextTool(codeintel.DebugToolsConfig{State: s}))
			doReg(codeintel.NewDebugStacktraceTool(codeintel.DebugToolsConfig{State: s}))
			doReg(codeintel.NewSetBreakpointTool(codeintel.DebugToolsConfig{
				State: s, RequestDebug: s.RequestDebug,
			}))
			doReg(codeintel.NewAnalyzeCrashTool(codeintel.DebugToolsConfig{State: s}))
			doReg(codeintel.NewSuggestBreakpointTool(codeintel.DebugToolsConfig{State: s}))
			doReg(codeintel.NewEvaluateTool(codeintel.DebugToolsConfig{
				State: s, RequestDebug: s.RequestDebug,
			}))
			doReg(codeintel.NewDebugContinueTool(codeintel.DebugToolsConfig{
				State: s, RequestDebug: s.RequestDebug,
			}))
			doReg(codeintel.NewRunProfileTool())
			doReg(codeintel.NewAnalyzeProfileTool())
			doReg(codeintel.NewWatchLogsTool())
			doReg(codeintel.NewCIStatusTool())
			doReg(codeintel.NewOrphansTool(codeIdx))
			doReg(codeintel.NewImpactTool(codeIdx))
			doReg(codeintel.NewDuplicatesTool(codeIdx))
			doReg(codeintel.NewCallersTool(codeIdx, lspBridge))
			doReg(codeintel.NewCalleesTool(codeIdx, lspBridge))
			doReg(codeintel.NewVerifyCallersTool(codeIdx, lspBridge))
			doReg(codeintel.NewReportFindingTool())
			doReg(codeintel.NewTracePathTool(codeIdx))
			doReg(codeintel.NewSemanticSearchTool(codeIdx))
			doReg(codeintel.NewCoChangeTool(codeIdx))
			doReg(codeintel.NewCircularDepsTool(codeIdx))
			doReg(codeintel.NewHotspotTool(codeIdx))
			doReg(codeintel.NewImplementationsTool(codeIdx, lspBridge))
			doReg(codeintel.NewChangeFreqTool(wsAbs))
			doReg(codeintel.NewAPIConsumersTool(codeIdx))
			doReg(NewLogMonitorTool(LogMonitorToolConfig{Watcher: logWatcher}))
			doReg(codeintel.NewReadSymbolsTool(codeIdx, host.Files()))

			// The 10 tools that need postInitialize-built state. On first boot
			// s.constraintReg is still nil and postInitialize registers them;
			// on every re-Warm it is set and this is the only thing that puts
			// them back, because Boot handed us an empty ToolRegistry. See
			// registerDeferredCKGTools for why omitting this was silent.
			s.mu.Lock()
			deferredReg := s.constraintReg
			s.mu.Unlock()
			if deferredReg != nil {
				s.registerDeferredCKGTools(c, codeIdx, deferredReg)
			}
		} // end if !cfg.DisableCKG

		// Tool edge registration deferred to postInitialize — the 626MB
		// code.db makes DELETE+INSERT operations here take 30-50s on cold start.

		// MCP servers from config (only on re-activation, not first boot).
		if mh := c.MCPs(); mh != nil && s.mcpMgr != nil {
			for _, mc := range s.cfg.MCP {
				if err := mh.Upsert(bootCtx, wesMCPServerConfig(mc)); err != nil {
					slog.Warn("[engine] mcp server upsert failed", "name", mc.Name, "error", err)
				}
			}
		}

		// IM channel re-boot (only on re-activation when persister is set).
		if s.channelPersist != nil && s.channelLockRelease != nil {
			gateway := s.channelPersist.Source()
			if gateway != nil {
				appchannel.BootChannelsWithOptions(bootCtx, c, gateway, s.channelPersist, slog.Default(), appchannel.BootOptions{})
			}
		}

		return nil
	}

	// Engine startup can take tens of seconds; release s.mu around it.
	// initDone + initializing flag ensure concurrent callers wait rather
	// than starting a second Hypervisor on the same dataDir.
	s.mu.Unlock()

	// Layer 1: start Hypervisor (or reuse existing from Cell identity change).
	s.mu.Lock()
	eng := s.eng
	s.mu.Unlock()
	if eng == nil {
		var startErr error
		eng, startErr = wesengine.StartEngine(ctx, wesengine.Config{
			DataDir: dataDir,
			Logger:  slog.Default(),
			// INV-WS-05: one process = one workspace Cell. The shared
			// hypervisor.db may list neighbour ws-{hash} entries; only
			// attach the current workspace so neighbours are never
			// booted, cooled by the temperature scheduler, or their
			// data plane opened in this process.
			AttachCellIDs: []string{cellID},
		})
		if startErr != nil {
			s.mu.Lock()
			s.initErr = startErr
			s.initializing = false
			close(s.initDone)
			s.mu.Unlock()
			codeIdx.Close()
			tsPool.Close()
			return "", fmt.Errorf("engine: start: %w", startErr)
		}
	}

	slog.Default().Info("boot: engine version", "sdk_version", eng.Hypervisor.Version())

	// Layer 2: assemble CellSpec and create the Cell.
	spec := wesgine.CellSpec{
		ID: cellID,
		CellCoreSpec: wesgine.CellCoreSpec{
			Locale:    "zh-CN",
			OnStarted: postBootFn,
		},
	}
	if err := specOverride(&spec); err != nil {
		if closeErr := eng.Close(ctx); closeErr != nil {
			slog.Warn("[engine] close after spec-override failure", "error", closeErr)
		}
		s.mu.Lock()
		s.initErr = err
		s.initializing = false
		close(s.initDone)
		s.mu.Unlock()
		codeIdx.Close()
		tsPool.Close()
		return "", fmt.Errorf("engine: spec override: %w", err)
	}

	// Provider seed from sibling Cell: when this Cell has no providers
	// (neither from config.yaml nor from CellSpec), scan existing Cells
	// in the same Hypervisor for one that has providers and clone them.
	// This handles the "Cell ID drift" scenario: the user configured
	// providers on Cell A (old workspace root), but the workspace root
	// changed (e.g. activeEditor in a different folder before the fix
	// above, or .code-workspace identity change) → new Cell B has no
	// providers. Cloning is a one-time seed (AX-1: after copy, the two
	// Cells' providers are fully independent).
	if len(spec.PrivateProviders) == 0 {
		if siblings, listErr := eng.Hypervisor.Cells().List(ctx); listErr == nil {
			for _, sib := range siblings {
				if sib.CellID == spec.ID {
					continue
				}
				sibCell, getErr := eng.Hypervisor.Cells().Get(sib.CellID)
				if getErr != nil {
					continue
				}
				sibSpec := sibCell.Spec()
				if len(sibSpec.PrivateProviders) > 0 {
					spec.PrivateProviders = make([]wesconfig.ProviderConfig, len(sibSpec.PrivateProviders))
					copy(spec.PrivateProviders, sibSpec.PrivateProviders)
					slog.Info("[engine] provider seed from sibling cell",
						"sibling", sib.CellID, "target", spec.ID,
						"providers", len(spec.PrivateProviders))
					break
				}
			}
		}
	}

	cell, cellErr := eng.Hypervisor.Cells().GetOrCreate(ctx, spec)
	if cellErr != nil {
		if closeErr := eng.Close(ctx); closeErr != nil {
			slog.Warn("[engine] close after cell-create failure", "error", closeErr)
		}
		s.mu.Lock()
		s.initErr = cellErr
		s.initializing = false
		close(s.initDone)
		s.mu.Unlock()
		codeIdx.Close()
		tsPool.Close()
		return "", fmt.Errorf("engine: cell create: %w", cellErr)
	}

	// Reconcile engine-owned DenyPaths onto an already-persisted Cell.
	// GetOrCreate merges only the process hooks for an existing Cell and
	// drops the incoming DenyPaths, so specOverride above reaches first boot
	// only: without this, the config-path correction never lands on any
	// installed user, and a settings-UI edit that dropped an entry keeps it
	// dropped forever. Non-fatal — a Cell that boots with a stale list is
	// still better than no Cell, and the log says which paths are exposed.
	if merged, changed := unionPaths(cell.Spec().DenyPaths, engineOwnedDenyPaths(dataDir)...); changed {
		if err := eng.Hypervisor.Cells().UpdateSpec(ctx, cellID, wesgine.SpecPatch{DenyPaths: &merged}); err != nil {
			slog.Error("[engine] deny paths reconcile failed — credential files stay readable by agent tools",
				"cell", cellID, "want", engineOwnedDenyPaths(dataDir), "error", err)
		} else {
			slog.Info("[engine] deny paths reconciled", "cell", cellID, "count", len(merged))
		}
	}

	// Same reconcile, same reason, different failure shape: AppIdentity is
	// *derived* (baseAppIdentity + platformPrompt + config.yaml fragment),
	// and two of those three are constants of this binary. Reaching first
	// boot only means the prompt an installed workspace runs is whichever
	// wescode version created it — every later edit to the constitution
	// ships to new workspaces and to nobody else, and nothing reports the
	// gap because the identity section is still there, just old.
	//
	// Unconditional string compare rather than "did the user edit it":
	// the value moves when the user edits the fragment *and* when wescode
	// upgrades, and only the current spec can tell those apart from a no-op.
	if want := s.resolveAppIdentity(); cell.Spec().AppIdentity != want {
		if err := eng.Hypervisor.Cells().UpdateSpec(ctx, cellID, wesgine.SpecPatch{AppIdentity: &want}); err != nil {
			slog.Error("[engine] app identity reconcile failed — agent runs with the prompt persisted at Cell creation",
				"cell", cellID, "error", err)
		} else {
			slog.Info("[engine] app identity reconciled", "cell", cellID, "chars", len(want))
		}
	}

	// Cell directory is now stable. Bind CodeIndex to its permanent path.
	// Config Mode (wsAbs=="") never reaches here — it returns early above.
	if wsAbs != "" {
		indexDBPath := filepath.Join(dataDir, "cells", cellID, "index", "code.db")
		if err := codeIdx.BindPath(indexDBPath); err != nil {
			slog.Error("[engine] code index bind failed", "path", indexDBPath, "error", err)
		}
	}

	// Seed builtin skills after cell creation.
	if s.cfg.SkillsOverrideDir == "" {
		seedBuiltinSkills(ctx, cell)
	}

	// wescode-app.db opens here rather than in postInitialize because this is
	// the first boot's only chance to put user-created agents back: postBootFn
	// has already run once, during GetOrCreate above, with no store to read
	// from. Later Cool→Warm cycles find s.userAgents set and restore from
	// postBootFn itself. Both callers are needed for the same reason
	// registerDeferredCKGTools has two.
	//
	// It runs before s.initialized, so no RPC can observe the window where the
	// engine holds presets but not the user's agents.
	appDB, appDBErr := openAppDB(ctx, dataDir, cellID, slog.Default())
	var userAgents *userAgentStore
	if appDBErr != nil {
		slog.Error("[engine] app db unavailable — agents created this session are lost on the next Cool→Warm",
			"cell", cellID, "error", appDBErr)
	} else if store, storeErr := newUserAgentStore(ctx, appDB); storeErr != nil {
		slog.Error("[engine] user agent store disabled — agents created this session are lost on the next Cool→Warm",
			"cell", cellID, "error", storeErr)
	} else {
		userAgents = store
		if n, rErr := store.RestoreInto(ctx, cell.Agents()); rErr != nil {
			slog.Error("[engine] user agent restore failed", "cell", cellID, "error", rErr)
		} else if n > 0 {
			slog.Info("[engine] user agents restored", "cell", cellID, "count", n)
		}
	}

	// Hold Cell + Handle wrappers locally. Six L0 packages stay until D3–D8.
	s.mu.Lock()
	s.eng = eng
	s.memorySvc = cell.Memory()
	s.observeSvc = cell.Observe()
	s.hitlSvc = cell.HITL()
	s.agents = agent.NewService(cell.Agents(), slog.Default())
	s.groupDB = appDB
	s.userAgents = userAgents
	s.stops = stop.NewRegistry()
	if eng.Hypervisor != nil {
		s.providers = provider.NewCellClient(eng.Hypervisor, cell, slog.Default())
	}
	s.hyp = eng.Hypervisor
	s.cell = cell
	s.cellID = cellID

	// Wire KBOverlay: let the assembler reach the Cell for Knowledge queries.
	codeAsm.SetCellGetter(func() *wesgine.Cell {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.cell
	})

	// Write workspace marker so cross-Cell session listing can resolve
	// CellID → workspace folder name for display.
	if cellDir := filepath.Join(s.dataDir, "cells", cellID); s.workspace != "" {
		if err := os.WriteFile(filepath.Join(cellDir, ".workspace"), []byte(s.workspace), 0644); err != nil {
			slog.Warn("[engine] write workspace marker failed", "cell_dir", cellDir, "error", err)
		}
	}

	s.editEngine = ee
	s.tsPool = tsPool
	s.codeIndex = codeIdx
	s.codeAsm = codeAsm
	s.qualityGate = qualityGate
	s.constraintReg = constraintReg
	s.lsp = lspBridge
	s.diagCache = diagCache
	s.diagBaseline = diagBaseline
	s.historySource = &historyContextSource{
		getWorkspace:   func() string { s.mu.Lock(); defer s.mu.Unlock(); return s.workspace },
		getModified:    ee.ModifiedFiles,
		getRecentEdits: func() []recentEditRecord { return nil },
		getAllRoots:    s.WorkspaceRoots,
	}
	s.termDiagCache = termDiagCache
	s.termDiagParser = termDiagParser
	s.attentionTracker = attentionTracker
	s.feedbackHandler = feedbackHandler
	s.tcrTracker = tcrTracker
	s.runtime = cell.Runtime()
	s.topologyDetector = codeintel.NewProjectTopologyDetector()
	s.workspace = wsAbs
	if len(workspaceFolders) > 0 {
		s.workspaceRoots = workspaceFolders
	}
	slog.Info("[engine] Initialize: path config",
		"workspace", wsAbs,
		"workspaceFolders", workspaceFolders,
		"primaryRoot", wsAbs,
		"allowPaths_count", len(workspaceFolders))
	s.dataDir = dataDir
	s.debugStore = debugStore
	s.logWatcher = logWatcher

	_ = codeSegmenter
	_ = editFallback

	// Mark ready — all RPC handlers can now proceed.
	s.initialized = true
	s.initializing = false
	close(s.initDone)
	// Start initial indexing while still holding the lock. This ensures any
	// waiter (id:2 with real workspace folders) that calls applyWorkspaceConfigLocked
	// will find indexCancel already set, properly cancel THIS run, and restart
	// with the correct roots — eliminating the race where postInitialize's
	// deferred startBackgroundIndex would cancel the waiter's good indexing run.
	if s.codeIndex != nil {
		s.startBackgroundIndexLocked() // releases s.mu
	} else {
		s.mu.Unlock()
	}

	// Provider state is read directly from CellClient (s.providers)
	// in all Cell Mode operations. No sync to s.cfg.Providers needed.

	// ── Phase 2: async enhancement (runs in background after RPC response) ──
	postCtx, postCancel := context.WithCancel(context.Background())
	postDone := make(chan struct{})
	s.mu.Lock()
	s.postInitCancel = postCancel
	s.postInitDone = postDone
	s.mu.Unlock()

	go func() {
		defer close(postDone)
		s.postInitialize(postCtx, wsAbs, dataDir, cellID,
			ee, qualityGate, codeIdx, codeAsm, tsPool, lspBridge, diagCache, diagBaseline,
			constraintReg, debugStore, logWatcher)
	}()

	return wsAbs, nil
}

// deferredCKGToolNames is the set registerDeferredCKGTools registers. It exists
// so a test can assert the set without booting a Cell — the failure this guards
// is silent (a tool that is never registered produces no error), so counting the
// call sites is the only thing that can fail loudly.
var deferredCKGToolNames = []string{
	"check_constraints", "suggest_constraints",
	"get_effects", "trace_data_flow", "find_taint_paths",
	"get_trend", "detect_drift", "predict_co_changes",
	"list_conventions", "check_convention",
	"find_doc_references",
}

// registerDeferredCKGTools registers the 11 Tier C tools that need state
// postInitialize builds (the CSE registry and an opened code.db).
//
// It has two callers and needs both. postInitialize covers first boot, where
// constraintReg does not exist yet when OnStarted first runs — that ordering is
// why these were deferred in the first place. postBootFn covers every later
// Cell.Start: icell.Boot mints a fresh ToolRegistry via BuildSubsystems, so
// after the temperature scheduler Cools an idle Cell (15m by default, since
// wesapp/engine.StartEngine leaves CellTemperature at its defaults) the next
// request re-Warms into a registry holding only what OnStarted just put there.
// Registering these solely from postInitialize — which runs once per process —
// therefore made the whole CSE and temporal tool surface disappear for the rest
// of the process, with no error and no log line, because Register was simply
// never called again.
func (s *Service) registerDeferredCKGTools(c *wesgine.Cell, codeIdx *codeintel.CodeIndex, constraintReg *constraints.Registry) {
	if c == nil || codeIdx == nil || constraintReg == nil {
		return
	}
	doReg := func(t tool.Tool) {
		s.ckgTools.Observe(t)
		if err := c.Tools().Register(t); err != nil {
			slog.Error("[engine] ckg tool register failed", "tool", t.Name(), "error", err)
		}
	}

	doReg(codeintel.NewCheckConstraintsTool(constraintReg))
	doReg(codeintel.NewSuggestConstraintsTool(codeIdx, constraintReg))
	doReg(codeintel.NewEffectsTool(codeIdx))
	doReg(codeintel.NewDataFlowTool(codeIdx))
	doReg(codeintel.NewTaintPathsTool(codeIdx))
	doReg(codeintel.NewGetTrendTool(func() *sql.DB { return codeIdx.DB() }))
	doReg(codeintel.NewDetectDriftTool(func() *sql.DB { return codeIdx.DB() }))
	doReg(codeintel.NewPredictCoChangesTool(func() *sql.DB { return codeIdx.DB() }))
	doReg(codeintel.NewListConventionsTool(func() []codeintel.Convention { return s.latestConventions() }))
	doReg(codeintel.NewCheckConventionTool(func() []codeintel.Convention { return s.latestConventions() }))

	// Doc↔Code cross-reference tool: bridges Knowledge documents with CKG symbols.
	docRefIdx := codeintel.NewDocRefIndex(codeIdx.DB(), c, slog.Default())
	doReg(codeintel.NewDocReferencesTool(docRefIdx))

	// Wire DocSync → DocRefIndex so doc saves trigger cross-reference refresh.
	s.mu.Lock()
	ds := s.docSync
	s.mu.Unlock()
	if ds != nil {
		ds.SetOnRefresh(func(ctx context.Context, kbFileID, docPath string) {
			if err := docRefIdx.RefreshFile(ctx, kbFileID, docPath); err != nil {
				slog.Warn("[engine] doc-ref index refresh failed", "kb_file_id", kbFileID, "path", docPath, "error", err)
			}
		})
	}

	// Asynchronously build initial doc references from Knowledge entities.
	go func() {
		if err := docRefIdx.RefreshAll(context.Background()); err != nil {
			slog.Debug("[doc-ref] initial refresh failed", "error", err)
		}
	}()
}

// postInitialize runs non-critical setup after the engine is already serving
// RPCs. Failures here degrade features (code intelligence, channels, etc.)
// but never prevent the engine from being usable.
func (s *Service) postInitialize(
	ctx context.Context,
	wsAbs, dataDir, cellID string,
	ee *editengine.EditEngine,
	qualityGate *verification.CodeQualityGate,
	codeIdx *codeintel.CodeIndex,
	codeAsm *codeintel.CodeAssembler,
	tsPool *treesitter.ParserPool,
	lspBridge codeintel.LSPBridge,
	diagCache *codeintel.DiagnosticsCache,
	diagBaseline *codeintel.DiagnosticsBaseline,
	constraintReg *constraints.Registry,
	debugStore *DebugEventStore,
	logWatcher *LogWatcher,
) {
	if ctx.Err() != nil {
		return
	}

	// DocSync: auto-index workspace document files into Knowledge (INV-KB-WS-01).
	// Runs first so sidebar/listKBFiles returns data before CKG/CSE finish.
	if postCell := s.cell; postCell != nil {
		ds := NewDocSync(postCell, cellID, slog.Default())
		s.mu.Lock()
		s.docSync = ds
		s.mu.Unlock()
		roots := make([]string, len(s.workspaceRoots))
		copy(roots, s.workspaceRoots)
		if len(roots) == 0 && wsAbs != "" {
			roots = []string{wsAbs}
		}
		ds.Start(ctx, roots)
	}

	if ctx.Err() != nil {
		return
	}

	// Code index readiness counts + external entry points require a bound DB.
	// Config Mode never reaches postInitialize — wsAbs is always non-empty here.
	if wsAbs != "" {
		codeIdx.InitReadinessCounts(context.Background())

		// These CodeIndex methods have no in-repo caller: the LLM invokes them
		// through the tool layer, so the CKG sees zero inbound call edges and
		// FindOrphans would report every one of them as dead. Declaring them
		// as external entry points is what keeps that from happening.
		//
		// This used to be modeled as `tool_wrap` edge rows with source_id=0.
		// symbols.id is AUTOINCREMENT from 1, so every one of those INSERTs
		// violated the FK and was silently dropped — the suppression never
		// worked. An entry-point set says the same thing without pretending
		// there is a caller.
		codeIdx.SetExternalEntryPoints([]string{
			"SearchSymbols", "FindReferences", "ProjectMap", "GetDiagnostics",
			"TraceVariable", "ControlFlow", "CrossLanguage",
			"FindOrphans", "ImpactAnalysis", "FindSimilar",
			"SearchFiles", "ListFileSymbols", "ListImports", "ReverseImports",
			"PackageOfFile", "SymbolCount", "SymbolIDs",
			"PromoteByID", "DemoteByID",
			"FindSymbolInLang", "WrapperFn",
		})
	}

	if ctx.Err() != nil {
		return
	}

	// Constraint registry: infer from project structure (can scan workspace).
	constraintReg = constraints.NewRegistry()
	if wsAbs != "" {
		for _, c := range constraints.InferFromProject(wsAbs, codeIdx.Readiness().Completeness) {
			constraintReg.Add(c)
		}
		if constraintReg.CountForRoot(wsAbs) == 0 {
			seeded := constraints.SeedGoConstraints(constraintReg, wsAbs)
			if seeded > 0 {
				slog.Info("[codeintel] seeded Go constraints (cold start)", "root", wsAbs, "count", seeded)
			}
		}
	}

	s.mu.Lock()
	s.constraintReg = constraintReg
	s.mu.Unlock()

	// Wire CSE registry into CodeAssembler so the CSEOverlay can find it.
	codeAsm.SetConstraintRegistry(constraintReg)

	s.constraintHits = &codeintel.ConstraintHitTracker{}

	// Activate PreWriteCheck: fill the lazy delegates registered in specOverride.
	// From this point, every write/edit/apply_patch will check constraints.
	if s.preWritePrePtr != nil && s.preWritePostPtr != nil {
		preHook, postHook := codeintel.NewPreWriteCheck(codeIdx, tsPool, codeintel.PreWriteCheckOpts{
			Constraints: constraintReg,
			Buffers:     &bufferProviderAdapter{store: s.bufferStore},
			HitTracker:  s.constraintHits,
		})
		s.preWritePrePtr.Store(&preHook)
		s.preWritePostPtr.Store(&postHook)
		slog.Info("[codeintel] PreWriteCheck activated",
			"constraints", constraintReg.Count(), "active", len(constraintReg.Active()))
	}

	if ctx.Err() != nil {
		return
	}

	// Register deferred CKG tools that depend on postInitialize-created state.
	if c := s.cell; c != nil {
		s.registerDeferredCKGTools(c, codeIdx, constraintReg)
	}

	// Load persisted constraints from code.db (merges with inferred).
	go s.LoadPersistedConstraints()

	// One-shot cleanup: remove legacy constraint_data / exploration_path
	// entries from wes_memories (they now live in code.db).
	if s.cell != nil {
		go s.cleanupLegacyConstraintMemory()
	}

	// QualityGate diagnostic push wiring
	if s.pendingNotifier != nil && qualityGate != nil {
		fn := s.pendingNotifier
		qualityGate.SetDiagnosticsPush(func(errors []verification.CompileError) {
			type diagnostic struct {
				Path    string `json:"path"`
				Line    int    `json:"line"`
				Column  int    `json:"column"`
				Message string `json:"message"`
			}
			diags := make([]diagnostic, len(errors))
			for i, e := range errors {
				diags[i] = diagnostic{Path: e.Path, Line: e.Line, Column: e.Column, Message: e.Message}
			}
			if err := fn(notify.DiagnosticsSet, map[string]any{"diagnostics": diags}); err != nil {
				slog.Warn("[engine] diagnostics notification failed", "error", err)
			}
		})
		qualityGate.SetProgressNotifier(&rpcProgressNotifier{fn: fn})
	}
	if s.pendingNotifier != nil && qualityGate != nil {
		qualityGate.SetDiagnosticsFn(func(ctx context.Context, path string) ([]verification.CompileError, error) {
			cached := diagCache.ForFile(path)
			var errs []verification.CompileError
			for _, d := range cached {
				if d.Severity == 1 {
					errs = append(errs, verification.CompileError{
						Path:    d.Path,
						Line:    d.Line,
						Column:  d.Column,
						Message: d.Message,
					})
				}
			}
			return errs, nil
		})
	}

	if qualityGate != nil && diagBaseline != nil {
		qualityGate.SetBaselineDiffFn(func(files []string) *verification.BaselineDiffResult {
			if diagBaseline.TakenAt().IsZero() {
				return nil
			}
			diff := diagBaseline.DiffForFiles(diagCache, files)
			result := &verification.BaselineDiffResult{
				Resolved: len(diff.Resolved),
			}
			for _, d := range diff.NewErrors {
				result.NewErrors = append(result.NewErrors, verification.CompileError{
					Path:    d.Path,
					Line:    d.Line,
					Column:  d.Column,
					Message: d.Message,
				})
			}
			// Unchanged errors are pre-existing
			allCurrent := diagCache.ErrorsAndWarnings()
			for _, d := range allCurrent {
				if d.Severity == 1 {
					isNew := false
					for _, ne := range diff.NewErrors {
						if ne.Path == d.Path && ne.Line == d.Line && ne.Message == d.Message {
							isNew = true
							break
						}
					}
					if !isNew {
						result.Preexisting = append(result.Preexisting, verification.CompileError{
							Path:    d.Path,
							Line:    d.Line,
							Column:  d.Column,
							Message: d.Message,
						})
					}
				}
			}
			return result
		})
	}

	if qualityGate != nil {
		_, isNoop := lspBridge.(codeintel.NoopLSP)
		if !isNoop {
			qualityGate.SetLSPAutoFixFn(func(ctx context.Context, errors []verification.CompileError) *verification.LSPAutoFixResult {
				fixed := 0
				for _, ce := range errors {
					actions, err := lspBridge.CodeActions(ctx, ce.Path, ce.Line, ce.Column, ce.Line, ce.Column+1)
					if err != nil || len(actions) == 0 {
						continue
					}
					for _, a := range actions {
						if a.IsPreferred {
							_, applyErr := lspBridge.ApplyCodeAction(ctx, ce.Path, ce.Line, ce.Column, ce.Line, ce.Column+1, a.Title)
							if applyErr == nil {
								fixed++
							}
							break
						}
					}
				}
				if fixed == 0 {
					return nil
				}
				return &verification.LSPAutoFixResult{
					Fixed:     fixed,
					Remaining: len(errors) - fixed,
				}
			})
		}
	}

	if ctx.Err() != nil {
		return
	}

	// Group service (degrades to nil on failure). The app db was opened in
	// Initialize — see the user-agent restore there for why it cannot wait
	// until here.
	s.mu.Lock()
	groupDB := s.groupDB
	s.mu.Unlock()
	if groupDB == nil {
		slog.Warn("[engine] group service disabled: app db unavailable")
	} else if groupSvc, gErr := initGroupService(ctx, groupDB, s.cell, slog.Default()); gErr != nil {
		slog.Warn("[engine] group service disabled", "error", gErr)
	} else {
		s.mu.Lock()
		s.groupSvc = groupSvc
		s.mu.Unlock()

		if qualityGate != nil {
			if bsStore, bsErr := verification.NewBaselineStore(groupDB); bsErr != nil {
				slog.Warn("[engine] baseline store disabled", "error", bsErr)
			} else {
				qualityGate.SetBaselineStore(bsStore)
				wsDir := wsAbs
				qualityGate.SetGitCommitFn(func() string {
					cmd := exec.Command("git", "rev-parse", "HEAD")
					cmd.Dir = wsDir
					out, err := cmd.Output()
					if err != nil {
						return ""
					}
					return strings.TrimSpace(string(out))
				})
			}
		}
	}

	// Metrics persistence — flush to wescode-app.db every 5 minutes.
	if s.groupDB != nil {
		var rm *codeintel.RetrievalMetrics
		if s.codeAsm != nil {
			rm = s.codeAsm.Metrics()
		}
		mp := NewMetricsPersister(s.groupDB, rm, s.tcrTracker)
		if err := mp.Start(ctx); err != nil {
			slog.Warn("[engine] metrics persister disabled", "error", err)
		} else {
			s.mu.Lock()
			s.metricsPersister = mp
			s.mu.Unlock()
		}
	}

	// FileStore
	if fs, err := file.NewFileStore(dataDir); err == nil {
		s.mu.Lock()
		s.fileStore = fs
		s.mu.Unlock()
	} else {
		slog.Warn("[engine] file store init failed", "error", err)
	}

	// Clean up the legacy "main" Cell from D-60 era (1-install-1-cell).
	s.mu.Lock()
	hyp := s.hyp
	s.mu.Unlock()
	if hyp != nil {
		if list, err := hyp.Cells().List(ctx); err == nil {
			for _, info := range list {
				if info.CellID == "main" {
					// Delete purges the bytes too. That is what we want
					// here: no UI reaches a D-60 "main" Cell after the
					// move to ws-{hash}, so keeping its directory only
					// produces a tree that no gauge measures and no code
					// path can reopen. Installs that ran the earlier
					// keep-the-bytes version of this cleanup still have
					// cells/main/ on disk; the engine's boot sweep warns
					// about it but will not delete an unregistered
					// directory, so that one is an operator call.
					slog.Info("[engine] removing legacy D-60 'main' cell", "cell_id", info.CellID)
					if err := hyp.Cells().Stop(ctx, info.CellID); err != nil {
						slog.Warn("[engine] legacy cell stop failed", "cell_id", info.CellID, "error", err)
					}
					if err := hyp.Cells().Delete(ctx, info.CellID); err != nil {
						slog.Warn("[engine] legacy cell delete failed", "cell_id", info.CellID, "error", err)
					}
				}
			}
		}
	}

	if ctx.Err() != nil {
		return
	}

	// Convention scan (guard against Close racing with postInitialize)
	go func() {
		s.mu.Lock()
		cell := s.cell
		s.mu.Unlock()
		if cell == nil {
			return
		}
		scanCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if wsAbs != "" {
			if _, serr := cell.Learn().ScanConventions(scanCtx, wsAbs); serr != nil {
				slog.Debug("[engine] initial convention scan failed", "err", serr)
			}
		}
	}()

	go s.probeAllProviders()

	// QualityGate advanced wiring
	qualityGate.SetSymbolFinder(&codeIndexSymbolAdapter{idx: codeIdx})
	qualityGate.SetImportGraph(codeIdx)
	symbolLister := codeIndexSymbolLister(codeIdx)
	qualityGate.SetFailureCorrelator(&verification.CallGraphCorrelator{
		SymbolsInFile: symbolLister,
	})
	qualityGate.SetTestSelector(&verification.FunctionTestSelector{
		SymbolsInFile:       symbolLister,
		TestFunctionsInFile: symbolLister,
	})
	qualityGate.SetTestFailureHook(func(failures []verification.TestFailure) {
		infos := make([]TestFailureInfo, len(failures))
		for i, f := range failures {
			infos[i] = TestFailureInfo{
				Name:    f.TestName,
				File:    f.Package,
				Message: f.Output,
			}
		}
		s.HandleTestFailures(infos)
	})
	qualityGate.SetRegressionLearnFn(func(regressions []verification.RegressionInfo) {
		s.learnFromRegressions(regressions)
	})

	codeAsm.SetSnapshotCallback(func(snapshot codeintel.ContextSnapshot) {
		if s.qualityGate != nil && snapshot.TaskType != "" {
			s.qualityGate.SetTaskType(snapshot.TaskType)
		}
		s.snapshotMu.RLock()
		fn := s.onSnapshot
		s.snapshotMu.RUnlock()
		if fn != nil {
			go fn(snapshot)
		}
	})

	// File watcher — recursive fsnotify on large repos can take 30-60s
	if wsAbs != "" {
		go func() {
			fw := codeintel.NewFileWatcher(codeIdx, tsPool, wsAbs)
			// One watcher serves every root, and boundary classification is
			// root-relative, so it needs the live set — not the boot-time
			// primary. Folders added later would otherwise classify as
			// outside the workspace, i.e. never indexed incrementally.
			fw.SetRootProvider(s.WorkspaceRoots)
			fw.Start(context.Background())
			s.mu.Lock()
			s.watcher = fw
			s.mu.Unlock()
		}()
	}

	bgCtx, bgCancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.bgLoopCancel = bgCancel
	s.mu.Unlock()
	// NOTE: startBackgroundIndex is NOT called here — it has been moved to
	// the boot completion path (Initialize, before close(initDone)) to
	// eliminate the race where this deferred call would cancel an in-flight
	// indexing run that applyWorkspaceConfigLocked already started with the
	// correct workspace roots.
	go s.backgroundAnchorLoop(bgCtx)

	hasByokKey := s.providers != nil && s.providers.HasBYOK()
	if !hasByokKey {
		go func() {
			s.BootstrapWesProvider(context.Background())
			if s.pendingNotifier != nil {
				if err := s.pendingNotifier(notify.ProvidersChanged, nil); err != nil {
					slog.Warn("[engine] providers-changed notification failed", "error", err)
				}
			}
		}()
	} else {
		if s.pendingNotifier != nil {
			if err := s.pendingNotifier(notify.ProvidersChanged, nil); err != nil {
				slog.Warn("[engine] providers-changed notification failed", "error", err)
			}
		}
	}
	s.startWesTokenSyncLoop(bgCtx)

	// MCP servers from config (guard: Close may nil s.cell concurrently)
	s.mu.Lock()
	postCell := s.cell
	s.mu.Unlock()
	if postCell == nil {
		slog.Info("[engine] postInitialize: cell nil'd (closed?), skipping remaining setup")
		return
	}
	if mh := postCell.MCPs(); mh != nil && len(s.cfg.MCP) > 0 {
		mcpCfgs := append([]MCPServerConfig(nil), s.cfg.MCP...)
		go func() {
			for _, mc := range mcpCfgs {
				mctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				if err := mh.Upsert(mctx, wesMCPServerConfig(mc)); err != nil {
					slog.Warn("[engine] MCP upsert failed", "server", mc.Name, "error", err)
				}
				cancel()
			}
		}()
	}

	// MCP Manager for CRUD + persistence
	s.mu.Lock()
	postCell = s.cell // re-check after goroutine spawns
	if postCell == nil {
		s.mu.Unlock()
		slog.Info("[engine] postInitialize: cell nil'd before MCP manager, aborting")
		return
	}
	s.mcpMgr = appmcp.NewManager(postCell.MCPs(), func(configs []wesmcp.ServerConfig) error {
		s.mu.Lock()
		s.cfg.MCP = make([]MCPServerConfig, 0, len(configs))
		for _, c := range configs {
			s.cfg.MCP = append(s.cfg.MCP, mcpSnapshotToConfig(c))
		}
		cfg := s.cfg
		s.mu.Unlock()
		return saveConfig(cfg)
	}, slog.Default())
	s.mu.Unlock()

	// Channel persistence + boot
	s.channelPersist = &appchannel.Persister{
		Source: func() *wesconfig.IMGatewayConfig {
			return &wesconfig.IMGatewayConfig{
				Channels: s.cfg.Channels,
				Bindings: s.cfg.IMGateway.Bindings,
			}
		},
		Save: func() error { return saveConfig(s.cfg) },
	}
	tunnelMgr := apptunnel.NewManager(apptunnel.NewCloudflaredFactory(""))
	bootOpts := appchannel.BootOptions{Tunnel: tunnelMgr}
	s.mu.Lock()
	postCell = s.cell
	s.mu.Unlock()
	if postCell == nil {
		slog.Info("[engine] postInitialize: cell nil'd before channel boot, aborting")
		return
	}
	s.channelBoot = appchannel.MakeBootFnWithOptions(postCell, s.channelPersist, bootOpts)

	s.mu.Lock()
	s.ensureDefaultChannelBindings()
	s.syncChannelBindings(context.Background())
	s.mu.Unlock()

	releaseChannelLock, bootChannels := acquireChannelBootLock(dataDir)
	if !bootChannels {
		slog.Warn("[channels] IM channels not started in this process",
			"orphan_pid", staleChannelLockPID(dataDir),
		)
	} else {
		s.mu.Lock()
		s.channelLockRelease = releaseChannelLock
		s.mu.Unlock()
		go func() {
			s.mu.Lock()
			c := s.cell
			s.mu.Unlock()
			if c == nil {
				return
			}
			gateway := &wesconfig.IMGatewayConfig{
				Channels: s.cfg.Channels,
				Bindings: s.cfg.IMGateway.Bindings,
			}
			appchannel.BootChannelsWithOptions(context.Background(), c, gateway, s.channelPersist, slog.Default(), bootOpts)
		}()
	}
	s.ensureChannelEventBuffer()

	// Cron scheduler (guard: Close may nil s.cell concurrently)
	s.mu.Lock()
	postCell = s.cell
	s.mu.Unlock()
	if postCell == nil {
		slog.Info("[engine] postInitialize: cell nil'd before cron, aborting")
		return
	}
	s.cronHandle = postCell.Cron()
	if s.cronHandle != nil {
		// Jobs written before the actor column carried meaning have no owner:
		// invisible to List, unreachable by every by-ID call, and still fired
		// by the tick loop (INV-CRON-10). wescode has exactly one actor, so
		// "local" is the owner by construction, not a guess.
		if n, err := s.cronHandle.ClaimOwnerless(ctx, "local"); err != nil {
			slog.Warn("[engine] cron: adopt ownerless jobs", "err", err)
		} else if n > 0 {
			slog.Info("[engine] cron: adopted ownerless jobs", "count", n)
		}
		s.cronHandle.SetRunCallback(func(_ context.Context, _ string, events <-chan gevent.Event) {
			var sessionID string
			var errMsg string
			for ev := range events {
				if ev.SessionID != "" && sessionID == "" {
					sessionID = ev.SessionID
				}
				if ev.Type == gevent.EventError {
					ed := gevent.ParseErrorData(ev.Data)
					if ed.Code == "timeout" {
						errMsg = "[定时] 执行超时"
					} else {
						errMsg = "[定时] 执行失败"
						if ed.Message != "" {
							errMsg += ": " + ed.Message
						}
					}
				}
			}
			if sessionID == "" || !strings.HasPrefix(sessionID, "cron-") {
				return
			}
			if errMsg != "" {
				slog.Warn("cron: run failed", "session_id", sessionID, "error", errMsg)
			}
		})
		s.cronHandle.SetParamsHook(func(_ context.Context, req *wcron.JobRequest, entry *wcron.Entry) {
			if req.Actor == "" {
				req.Actor = entry.Actor
			}
			if req.Actor == "" {
				req.Actor = "local"
			}
			if req.Metadata == nil {
				req.Metadata = map[string]string{}
			}
			req.Metadata["actor"] = req.Actor
			roots := s.WorkspaceRoots()
			if len(roots) > 0 {
				req.Metadata["work_dir"] = roots[0]
			}
			// No model resolution here on purpose. The model a scheduled job
			// runs on is chosen when the job is created — inherited from the
			// conversation for cron_manage, picked in the form for the UI —
			// and travels on the Entry. Resolving it per run would mean the
			// user cannot tell which model their task uses and the answer
			// could change under them, which is the thing INV-MODEL-01 exists
			// to prevent.
		})
		// Without a delivery handler the engine's routing event has no
		// subscriber and a finished job is silent: the result sits in a
		// conversation the user has no reason to open, which is what "the task
		// ran and I got nothing" actually was. The reply itself already landed
		// in the right conversation (an origin target makes the run itself use
		// it); this is the part that says so.
		s.cronHandle.SetDeliveryHandler(func(_ context.Context, entry wcron.Entry, run wcron.RunRecord) {
			s.notifyCronRunFinished(entry, run)
		})
	}

	// Health watchdog
	if s.pendingNotifier != nil {
		wd := newEngineHealthWatchdog(s, s.pendingNotifier)
		s.mu.Lock()
		s.watchdog = wd
		s.mu.Unlock()
		wd.Start(context.Background())
	}

	slog.Info("[engine] postInitialize complete", "workspace", wsAbs)
}

func (s *Service) Close() error {
	slog.Info("[engine] closing")

	// Cancel postInitialize and wait for it to finish before tearing down state.
	// This prevents nil-pointer panics from async goroutines referencing torn-down objects.
	s.mu.Lock()
	piCancel := s.postInitCancel
	piDone := s.postInitDone
	s.postInitCancel = nil
	s.postInitDone = nil
	s.mu.Unlock()
	if piCancel != nil {
		piCancel()
	}
	if piDone != nil {
		<-piDone
	}

	if s.bgLoopCancel != nil {
		s.bgLoopCancel()
		s.bgLoopCancel = nil
	}
	if s.indexCancel != nil {
		s.indexCancel()
		s.indexCancel = nil
	}
	// Wait for the backgroundIndexAll goroutine to exit before releasing
	// tsPool/codeIndex — the goroutine reads both without holding s.mu
	// (background.go:148), so closing them while it runs is a data race.
	indexDone := s.indexDone
	s.indexDone = nil
	if indexDone != nil {
		<-indexDone
	}
	if s.channelLockRelease != nil {
		s.channelLockRelease()
		s.channelLockRelease = nil
	}
	if s.logWatcher != nil {
		s.logWatcher.Stop()
		s.logWatcher = nil
	}
	if s.watchdog != nil {
		s.watchdog.Stop()
		s.watchdog = nil
	}
	s.bgTasksMu.Lock()
	for _, task := range s.bgTasks {
		if task.cancel != nil {
			task.cancel()
		}
	}
	s.bgTasksMu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized {
		return nil
	}
	s.initialized = false
	s.configMode = false

	if s.bufferStore != nil {
		s.bufferStore.Clear()
	}

	var firstErr error
	s.runtime = nil
	if s.eng != nil {
		firstErr = s.eng.Close(context.Background())
		s.eng = nil
	}
	s.memorySvc = nil
	s.observeSvc = nil
	s.hitlSvc = nil
	s.agents = nil
	s.stops = nil
	s.providers = nil
	s.mcpMgr = nil
	s.hyp = nil
	s.cell = nil
	if s.watcher != nil {
		s.watcher.Stop()
		s.watcher = nil
	}
	// IDE LSP bridge has no process to close (VSCode manages language servers).
	s.lsp = nil
	if s.codeIndex != nil {
		s.codeIndex.Close()
		s.codeIndex = nil
	}
	if s.tsPool != nil {
		s.tsPool.Close()
		s.tsPool = nil
	}
	s.editEngine = nil
	s.codeAsm = nil
	s.groupSvc = nil
	if s.metricsPersister != nil {
		s.metricsPersister.Close()
		s.metricsPersister = nil
	}
	if s.tcrTracker != nil {
		s.tcrTracker.Close()
		s.tcrTracker = nil
	}
	if s.groupDB != nil {
		if err := s.groupDB.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.groupDB = nil
	}
	return firstErr
}

// MemoryService returns the Cell memory handle.
func (s *Service) MemoryService() *wesgine.MemoryHandle {
	return s.memorySvc
}

// ObserveService returns the Cell observe handle.
func (s *Service) ObserveService() *wesgine.CellObserveHandle {
	return s.observeSvc
}

// HITLService returns the Cell HITL handle.
func (s *Service) HITLService() *wesgine.HITLHandle {
	return s.hitlSvc
}

// ProviderManager returns the provider manager.
func (s *Service) ProviderManager() *provider.CellClient { return s.providers }

// AgentManager returns the agent service.
func (s *Service) AgentManager() *agent.Service { return s.agents }

// GroupService returns the group service.
func (s *Service) GroupService() *group.Service { return s.groupSvc }

// WaitPostInit blocks until postInitialize completes (or ctx is cancelled).
// Callers that need services initialized in postInitialize (e.g. GroupService)
// should call this when the service is nil.
func (s *Service) WaitPostInit(ctx context.Context) {
	s.mu.Lock()
	ch := s.postInitDone
	s.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case <-ch:
	case <-ctx.Done():
	}
}

// MCPManager returns the MCP manager.
func (s *Service) MCPManager() *appmcp.Manager { return s.mcpMgr }

// StopRegistry returns the chat stop registry.
func (s *Service) StopRegistry() *stop.Registry {
	return s.stops
}

// groupSessionCleaner adapts *wesgine.Cell to group.SessionCleaner.
type groupSessionCleaner struct {
	cell *wesgine.Cell
}

func (c *groupSessionCleaner) DeleteByPrefix(ctx context.Context, prefix string) error {
	if c.cell == nil {
		return nil
	}
	sessions := c.cell.Sessions()
	if sessions == nil {
		return nil
	}
	return sessions.DeleteByPrefix(ctx, prefix)
}

func (c *groupSessionCleaner) ClearSession(ctx context.Context, sessionID string) error {
	if c.cell == nil {
		return nil
	}
	mem := c.cell.Memory()
	if mem == nil {
		return nil
	}
	return mem.ClearSession(ctx, sessionID)
}

// latestConventions returns the most recently mined project conventions.
// Populated by the background Pipeline run (pass6Analyze → MineConventions).
func (s *Service) latestConventions() []codeintel.Convention {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conventions
}
