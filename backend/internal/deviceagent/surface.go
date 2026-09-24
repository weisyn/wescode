package deviceagent

// Capability enumerates the discoverable device-agent capabilities.  The
// wire names are stable identifiers that appear in logs, tool
// registration lists, and (future) MCP tool descriptors.  Consumers
// (auth guards, audit hooks) can pattern-match on the "wescode_" prefix
// to identify device-agent-scoped requests.
type Capability string

const (
	// CapBuffer covers all editor-buffer overlay reads.  Backed by
	// engine.overlayFileProvider; wired through
	// CellSpec.HostEnvironment.Files().
	CapBuffer Capability = "wescode_buffer"

	// CapTerminal covers integrated-terminal exec, verification mirror
	// sessions, and background command execution.  Backed by
	// engine.vscodeShellProvider; wired through
	// CellSpec.HostEnvironment.Shell().
	CapTerminal Capability = "wescode_terminal"

	// CapEditor covers editor state introspection (focus / open list /
	// cursor position).  Backed by engine.editorAdapter; wired through
	// CellSpec.HostEnvironment.Editor().
	CapEditor Capability = "wescode_editor"

	// CapDiff covers diff previews shown as split-view in the IDE.
	// Transported as a private JSON-RPC method "wescode/showDiff".
	CapDiff Capability = "wescode_diff"

	// CapNotify covers user-facing notifications (info / warn / error)
	// and structured diagnostics push.  Transported as a private
	// JSON-RPC method "diagnostics/set" (LSP-compatible) plus custom
	// "wescode/notify".
	CapNotify Capability = "wescode_notify"

	// CapReveal covers "reveal file in explorer" and "open external
	// URL / app" affordances.  Transported as private JSON-RPC methods
	// "wescode/revealInExplorer" and "wescode/openExternal".
	CapReveal Capability = "wescode_reveal"
)

// AllCapabilities returns the full device-agent surface for logging and
// discovery.  Order is stable across calls (documentation-friendly).
func AllCapabilities() []Capability {
	return []Capability{
		CapBuffer,
		CapTerminal,
		CapEditor,
		CapDiff,
		CapNotify,
		CapReveal,
	}
}

// Prefix is the shared string prefix for wire-visible names in this
// surface.  Filter logic:
//
//	if strings.HasPrefix(name, deviceagent.Prefix) { … }
const Prefix = "wescode_"

// RPCMethodPrefix mirrors Prefix on the JSON-RPC transport side (client
// → agent direction, e.g. "wescode/showDiff").
const RPCMethodPrefix = "wescode/"
