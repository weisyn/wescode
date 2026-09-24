package codeintel

// Re-exports from lspbridge/ for package-internal convenience.
// Consumers within codeintel/ use these directly without qualifying lspbridge.

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/weisyn/wescode/internal/codeintel/lspbridge"
)

// --- Type aliases (allow existing codeintel code to compile unchanged) ---

type Diagnostic = lspbridge.Diagnostic
type DiagSeverity = lspbridge.DiagSeverity
type DefinitionLocation = lspbridge.DefinitionLocation
type CodeActionResult = lspbridge.CodeActionResult
type TextEdit = lspbridge.TextEdit
type FileEdit = lspbridge.FileEdit
type DocumentSymbol = lspbridge.DocumentSymbol
type CallHierarchyItem = lspbridge.CallHierarchyItem
type IncomingCall = lspbridge.IncomingCall
type OutgoingCall = lspbridge.OutgoingCall
type LSPRange = lspbridge.LSPRange
type LSPBridge = lspbridge.LSPBridge
type LSPRequestFn = lspbridge.LSPRequestFn

// Severity constants re-exported for package-level use.
const (
	DiagError   = lspbridge.DiagError
	DiagWarning = lspbridge.DiagWarning
	DiagInfo    = lspbridge.DiagInfo
	DiagHint    = lspbridge.DiagHint
)

// NoopLSP re-exported.
type NoopLSP = lspbridge.NoopLSP

// IDELSPBridge re-exported.
type IDELSPBridge = lspbridge.IDELSPBridge

// NewIDELSPBridge delegates to lspbridge.
var NewIDELSPBridge = lspbridge.NewIDELSPBridge

// LangForPath delegates to lspbridge.
var LangForPath = lspbridge.LangForPath

// --- LSPCache preserved for existing consumers (thin wrapper over lspbridge.Cache) ---

// LSPCache provides per-Run caching for LSP Definition and References results.
// INV-CI-23: TTL = Run lifecycle; cleared by ResetRunState.
type LSPCache struct {
	mu   sync.RWMutex
	defs map[string][]DefinitionLocation
	refs map[string][]DefinitionLocation
}

// NewLSPCache creates an empty cache.
func NewLSPCache() *LSPCache {
	return &LSPCache{
		defs: make(map[string][]DefinitionLocation),
		refs: make(map[string][]DefinitionLocation),
	}
}

// Reset clears all cached results (called at Run start).
func (c *LSPCache) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.defs = make(map[string][]DefinitionLocation)
	c.refs = make(map[string][]DefinitionLocation)
}

func lspCacheKey(path string, line, col int) string {
	return fmt.Sprintf("%s:%d:%d", path, line, col)
}

// Definition returns a cached definition result, or calls the LSP bridge and caches it.
func (c *LSPCache) Definition(ctx context.Context, lsp LSPBridge, path string, line, col int) ([]DefinitionLocation, error) {
	key := lspCacheKey(path, line, col)

	c.mu.RLock()
	if cached, ok := c.defs[key]; ok {
		c.mu.RUnlock()
		return cached, nil
	}
	c.mu.RUnlock()

	result, err := lsp.Definition(ctx, path, line, col)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.defs[key] = result
	c.mu.Unlock()

	slog.Debug("[lsp-cache] definition cached", "key", key, "results", len(result))
	return result, nil
}

// References returns cached reference results, or calls the LSP bridge and caches it.
func (c *LSPCache) References(ctx context.Context, lsp LSPBridge, path string, line, col int) ([]DefinitionLocation, error) {
	key := lspCacheKey(path, line, col)

	c.mu.RLock()
	if cached, ok := c.refs[key]; ok {
		c.mu.RUnlock()
		return cached, nil
	}
	c.mu.RUnlock()

	result, err := lsp.References(ctx, path, line, col)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.refs[key] = result
	c.mu.Unlock()

	slog.Debug("[lsp-cache] references cached", "key", key, "results", len(result))
	return result, nil
}
