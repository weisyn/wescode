package lspbridge

import (
	"sync"
	"time"
)

// Cache provides per-file TTL-based caching for LSP data (symbols + diagnostics).
// INV-LSP-02: degradation only affects precision, not availability.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	ttl     time.Duration
}

type cacheEntry struct {
	symbols     []DocumentSymbol
	diagnostics []Diagnostic
	updatedAt   time.Time
}

// NewCache creates a new per-file cache with the given TTL.
func NewCache(ttl time.Duration) *Cache {
	return &Cache{
		entries: make(map[string]*cacheEntry),
		ttl:     ttl,
	}
}

// GetSymbols returns cached symbols for a file path. Returns nil, false if missing or expired.
func (c *Cache) GetSymbols(path string) ([]DocumentSymbol, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[path]
	if !ok || time.Since(entry.updatedAt) > c.ttl {
		return nil, false
	}
	return entry.symbols, entry.symbols != nil
}

// GetDiagnostics returns cached diagnostics for a file path. Returns nil, false if missing or expired.
func (c *Cache) GetDiagnostics(path string) ([]Diagnostic, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[path]
	if !ok || time.Since(entry.updatedAt) > c.ttl {
		return nil, false
	}
	return entry.diagnostics, entry.diagnostics != nil
}

// SetSymbols stores symbol data for a file, refreshing the TTL.
func (c *Cache) SetSymbols(path string, symbols []DocumentSymbol) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.getOrCreateEntry(path)
	entry.symbols = symbols
	entry.updatedAt = time.Now()
}

// SetDiagnostics stores diagnostics for a file, refreshing the TTL.
func (c *Cache) SetDiagnostics(path string, diags []Diagnostic) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.getOrCreateEntry(path)
	entry.diagnostics = diags
	entry.updatedAt = time.Now()
}

// Invalidate removes all cached data for a specific file.
func (c *Cache) Invalidate(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, path)
}

// Prune removes all expired entries from the cache.
func (c *Cache) Prune() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for path, entry := range c.entries {
		if now.Sub(entry.updatedAt) > c.ttl {
			delete(c.entries, path)
		}
	}
}

func (c *Cache) getOrCreateEntry(path string) *cacheEntry {
	entry, ok := c.entries[path]
	if !ok {
		entry = &cacheEntry{}
		c.entries[path] = entry
	}
	return entry
}
