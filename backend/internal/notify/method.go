// Package notify owns the set of JSON-RPC notifications the Go backend pushes
// to the IDE extension.
//
// The set had no owner. Method names were string literals at ~20 call sites and
// the Electron main process matched them with an if-chain; nothing related the
// two, so a name present on one side and absent on the other was not an error —
// it was a dropped notification and a feature that quietly did nothing. That
// shipped twice: `channelEvent` (scan-to-login QR never reached the settings
// page) and `codeintel/index-complete` (call-graph hover/codelens caches never
// globally cleared, hidden by a per-file clear on save).
//
// What closes it is the type, not a list. Method's field is unexported, so a
// Method can only be constructed in this package — every notification the
// backend can send is a var below, by construction rather than by convention.
// The TypeScript half is generated from those vars (see tsgen.go) and dispatched
// with an exhaustive switch, so a var added here without a branch there fails
// tsc. Both halves therefore derive from one source; neither is a copy of the
// other, which is the property a hand-maintained name list cannot have.
//
// Payload shapes are deliberately not modeled here. The defect this package
// prevents is "nobody is listening for this name". A payload contract is a
// larger, separate job, and half-typing it here would leave a surface that
// reads as complete.
package notify

// Method is a JSON-RPC notification method name.
//
// It is a struct rather than `type Method string` on purpose. Go converts
// untyped string constants to any string-kind type, so with a defined string
// type `Notify("codeintel/index-warning", p)` compiles and ships a name that
// exists nowhere else — the exact mistake this package exists to make
// impossible. A struct has no such conversion.
//
// The one state left representable is the zero value, which carries an empty
// name. Nobody writes Method{} meaning to send something, and the funnel
// rejects it (rpc.Server.Notify), so it fails loudly at one place instead of
// silently at twenty.
type Method struct {
	wire string
}

// Wire is the name that goes on the JSON-RPC message.
func (m Method) Wire() string { return m.wire }

// String lets a Method be logged without unwrapping.
func (m Method) String() string { return m.wire }

// IsZero reports whether m was never assigned one of the vars below. Callers do
// not need this; the funnel does.
func (m Method) IsZero() bool { return m.wire == "" }

// Chat.
var (
	// ChatStream carries one agent stream event (delta / tool call / done).
	// Routed to the single window that owns the echoed JSON-RPC request id,
	// never fanned out (INV-WS-06). Sender: rpc.Handler chat stream pump.
	ChatStream = Method{"chat/stream"}

	// ContextSnapshot reports which CKG fragments were assembled for the turn,
	// for the Chat panel's context disclosure. Sender: rpc.Handler assembler hook.
	ContextSnapshot = Method{"context/snapshot"}
)

// Code index.
var (
	// IndexProgress samples a CKG index build: completeness, file/node/edge
	// counts, elapsed. Drives the status-bar progress item.
	// Sender: engine.backgroundIndexAll.
	IndexProgress = Method{"codeintel/index-progress"}

	// IndexComplete fires once, right after the final IndexProgress sample. It
	// is the only global cache invalidation for the call-graph hover and
	// codelens providers, which otherwise clear per-file on save. Do not
	// re-derive it from IndexProgress: completeness is indexed/total, so one
	// unparseable file ends a run below 1.0 and the derived event stops firing.
	// Sender: engine.backgroundIndexAll.
	IndexComplete = Method{"codeintel/index-complete"}

	// IndexError reports a per-root indexing failure. The run continues, so
	// this is a warning surface, not a terminal state.
	// Sender: engine.backgroundIndexAll.
	IndexError = Method{"codeintel/index-error"}
)

// Agent runs and providers.
var (
	// BackgroundComplete announces a finished background agent run.
	// Senders: rpc.Handler background start, engine background task.
	BackgroundComplete = Method{"background/complete"}

	// ProvidersChanged tells the frontend to re-fetch the provider list after
	// the backend mutated it. Carries no payload.
	// Sender: engine provider reload.
	ProvidersChanged = Method{"providers/changed"}

	// VerificationProgress reports the current verification phase for the
	// status bar. Sender: engine.rpcProgressNotifier.
	VerificationProgress = Method{"verification/progress"}

	// DiagnosticsSet replaces the backend-owned diagnostics for the workspace,
	// in an LSP-compatible shape. Sender: engine quality gate.
	DiagnosticsSet = Method{"diagnostics/set"}
)

// IM channels.
var (
	// ChannelEvent pushes IM channel login state (QR payload, scanned,
	// confirmed) to the settings page. It is push-based and replaced polling,
	// so a dropped event stalls the QR flow with no error on any surface.
	//
	// The name is camelCase where every other method is namespace/name. That is
	// the shipped wire name; renaming it here without renaming it in the
	// extension re-breaks the flow this const was added to fix.
	// Sender: rpc.Handler channel subscription loop.
	ChannelEvent = Method{"channelEvent"}
)

// Engine health.
var (
	// EngineHealth samples probe latency on every watchdog tick. Both frontends
	// intentionally ignore it and act on the transitions below; it is declared
	// so that per-tick drop is a decision written down rather than a gap.
	// Sender: engine.EngineHealthWatchdog.
	EngineHealth = Method{"engine/health"}

	// EngineHealthy fires only on the unhealthy→healthy transition and clears
	// the composer block and status-bar warning.
	// Sender: engine.EngineHealthWatchdog.
	EngineHealthy = Method{"engine/healthy"}

	// EngineUnhealthy fires only on the healthy→unhealthy transition and gates
	// the composer. Sender: engine.EngineHealthWatchdog.
	EngineUnhealthy = Method{"engine/unhealthy"}
)

// Language server, driven from the backend through the extension.
var (
	// LSPRequest asks the extension to run a language-server request on the
	// backend's behalf; the answer comes back as a separate RPC call carrying
	// the same id. Sender: engine.Service.LSPRequest.
	LSPRequest = Method{"lsp/request"}
)

// Debugger, driven from agent tools.
var (
	// DebugBreakpoint sets or clears a breakpoint.
	// Sender: engine.Service.RequestDebug, from codeintel's debug tool.
	DebugBreakpoint = Method{"debug/breakpoint"}

	// DebugEvaluate evaluates an expression in the paused frame.
	// Sender: engine.Service.RequestDebug, from codeintel's interactive debug tool.
	DebugEvaluate = Method{"debug/evaluate"}

	// DebugContinue resumes a paused session.
	// Sender: engine.Service.RequestDebug, from codeintel's interactive debug tool.
	DebugContinue = Method{"debug/continue"}
)

// Scheduled tasks.
var (
	// CronRunFinished reports that a scheduled job has produced its result, so
	// the IDE can say so instead of leaving the user to discover it by opening
	// the task page. A cron run has no request behind it and no stream anyone
	// is watching; without this the whole feature is silent by construction,
	// which is precisely how it read to users ("the task says it ran and I
	// received nothing"). Sender: engine.Service cron delivery handler.
	CronRunFinished = Method{"cron/run-finished"}
)

// Mirrored terminal. An agent command that the user should be able to watch is
// echoed into a read-only IDE terminal tab.
var (
	// TerminalCreate opens the tab. Senders: deviceagent terminal manager,
	// engine quality-gate mirror.
	TerminalCreate = Method{"terminal/create"}

	// TerminalOutput appends a chunk. Same senders as TerminalCreate.
	TerminalOutput = Method{"terminal/output"}

	// TerminalExited reports the exit code and closes the tab's live state.
	// Same senders as TerminalCreate.
	TerminalExited = Method{"terminal/exited"}
)
