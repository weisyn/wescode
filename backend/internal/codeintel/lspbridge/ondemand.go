package lspbridge

import (
	"context"
	"time"
)

// Location represents an LSP location result.
type Location struct {
	File string
	Line int
	Col  int
}

// OnDemandQuery provides real-time LSP queries for Agent tool calls.
// INV-LSP-04: timeout 200ms → fallback to CKG baseline.
// INV-LSP-04: never blocks Agent tool call.
type OnDemandQuery struct {
	bridge  LSPBridge
	cache   *Cache
	timeout time.Duration
}

// NewOnDemandQuery creates a query adapter with 200ms default timeout.
func NewOnDemandQuery(bridge LSPBridge, cache *Cache) *OnDemandQuery {
	return &OnDemandQuery{bridge: bridge, cache: cache, timeout: 200 * time.Millisecond}
}

// SetTimeout overrides the default 200ms query timeout.
func (q *OnDemandQuery) SetTimeout(d time.Duration) {
	q.timeout = d
}

// FindReferences queries LSP for references with timeout.
// Returns nil on timeout or error (caller should fallback to CKG).
func (q *OnDemandQuery) FindReferences(ctx context.Context, file string, line, col int) ([]Location, error) {
	ctx, cancel := context.WithTimeout(ctx, q.timeout)
	defer cancel()

	refs, err := q.bridge.References(ctx, file, line, col)
	if err != nil || ctx.Err() != nil {
		return nil, nil
	}

	locs := make([]Location, len(refs))
	for i, r := range refs {
		locs[i] = Location{File: r.Path, Line: r.Line, Col: r.Column}
	}
	return locs, nil
}

// FindImplementations queries LSP for implementations with timeout.
// Returns nil on timeout or error (caller should fallback to CKG).
func (q *OnDemandQuery) FindImplementations(ctx context.Context, file string, line, col int) ([]Location, error) {
	ctx, cancel := context.WithTimeout(ctx, q.timeout)
	defer cancel()

	impls, err := q.bridge.Implementation(ctx, file, line, col)
	if err != nil || ctx.Err() != nil {
		return nil, nil
	}

	locs := make([]Location, len(impls))
	for i, r := range impls {
		locs[i] = Location{File: r.Path, Line: r.Line, Col: r.Column}
	}
	return locs, nil
}

// FindDefinition queries LSP for definition with timeout.
// Returns nil on timeout or error (caller should fallback to CKG).
func (q *OnDemandQuery) FindDefinition(ctx context.Context, file string, line, col int) ([]Location, error) {
	ctx, cancel := context.WithTimeout(ctx, q.timeout)
	defer cancel()

	defs, err := q.bridge.Definition(ctx, file, line, col)
	if err != nil || ctx.Err() != nil {
		return nil, nil
	}

	locs := make([]Location, len(defs))
	for i, r := range defs {
		locs[i] = Location{File: r.Path, Line: r.Line, Col: r.Column}
	}
	return locs, nil
}
