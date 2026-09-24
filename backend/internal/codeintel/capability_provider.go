package codeintel

import (
	"context"
	"strings"
)

// CodeIntelCapabilityProvider generates a capability surface string describing
// the current codeintel subsystem state. Injected into the model's system
// prompt so it can adapt strategy based on available capabilities.
type CodeIntelCapabilityProvider struct {
	lspAvailable  bool
	lspLanguages  []string
	hasTreeSitter bool
	editTier      int // 1, 2, or 3
}

// CodeIntelCapabilityConfig holds the probed capability state.
type CodeIntelCapabilityConfig struct {
	LSPAvailable  bool
	LSPLanguages  []string
	HasTreeSitter bool
	EditTier      int
}

// NewCapabilityProvider creates a CodeIntelCapabilityProvider from probed state.
func NewCapabilityProvider(cfg CodeIntelCapabilityConfig) *CodeIntelCapabilityProvider {
	tier := cfg.EditTier
	if tier <= 0 {
		tier = 3
	}
	return &CodeIntelCapabilityProvider{
		lspAvailable:  cfg.LSPAvailable,
		lspLanguages:  cfg.LSPLanguages,
		hasTreeSitter: cfg.HasTreeSitter,
		editTier:      tier,
	}
}

// Status returns a JSON-serializable map of capability levels for the frontend.
func (p *CodeIntelCapabilityProvider) Status() map[string]any {
	lspStatus := "unavailable"
	if p.lspAvailable && len(p.lspLanguages) > 0 {
		lspStatus = strings.Join(p.lspLanguages, ", ")
	}
	return map[string]any{
		"lsp":        lspStatus,
		"treeSitter": p.hasTreeSitter,
		"editTier":   p.editTier,
	}
}

// Surface returns a concise capability status line for system prompt injection.
func (p *CodeIntelCapabilityProvider) Surface(_ context.Context) string {
	var parts []string
	parts = append(parts, "search: FTS5 + code graph")
	if p.lspAvailable && len(p.lspLanguages) > 0 {
		parts = append(parts, "LSP: "+strings.Join(p.lspLanguages, ", "))
	}
	return "Code intelligence: " + strings.Join(parts, ", ")
}
