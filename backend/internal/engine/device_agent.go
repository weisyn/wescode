package engine

import "github.com/weisyn/wescode/internal/deviceagent"

// deviceAgentCaps flattens the deviceagent.AllCapabilities() list to a
// []string suitable for slog attribute dumps.  Small helper kept here so
// engine.go does not depend on the deviceagent package's Capability
// type directly (log-only coupling).
func deviceAgentCaps() []string {
	caps := deviceagent.AllCapabilities()
	out := make([]string, len(caps))
	for i, c := range caps {
		out[i] = string(c)
	}
	return out
}
