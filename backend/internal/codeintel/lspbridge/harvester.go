package lspbridge

import "sync"

// Harvester receives passive LSP data from the Extension Host.
// INV-LSP-05: Never initiates LSP requests; only consumes push notifications.
type Harvester struct {
	cache *Cache
	mu    sync.RWMutex
}

// NewHarvester creates a Harvester that writes into the given Cache.
func NewHarvester(cache *Cache) *Harvester {
	return &Harvester{cache: cache}
}

// DocumentSymbolUpdate represents a pushed symbol update from Extension Host.
type DocumentSymbolUpdate struct {
	Path    string           `json:"path"`
	Symbols []DocumentSymbol `json:"symbols"`
}

// DiagnosticsUpdate represents a pushed diagnostics update.
type DiagnosticsUpdate struct {
	Path        string       `json:"path"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// OnSymbolUpdate handles the "lsp/symbolUpdate" notification.
// INV-LSP-05: purely passive — stores data pushed by the IDE, never initiates requests.
func (h *Harvester) OnSymbolUpdate(update DocumentSymbolUpdate) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cache.SetSymbols(update.Path, update.Symbols)
}

// OnDiagnosticsUpdate handles the "lsp/diagnosticsUpdate" notification.
// INV-LSP-05: purely passive — stores data pushed by the IDE, never initiates requests.
func (h *Harvester) OnDiagnosticsUpdate(update DiagnosticsUpdate) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cache.SetDiagnostics(update.Path, update.Diagnostics)
}
