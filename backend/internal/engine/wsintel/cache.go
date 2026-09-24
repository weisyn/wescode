package wsintel

import (
	"log/slog"
	"sync"
	"time"
)

const cacheTTL = 60 * time.Second

type cacheEntry struct {
	ctx       *WorkspaceContext
	expiresAt time.Time
}

var (
	cacheMu sync.Mutex
	cache   = map[string]cacheEntry{}
)

// Build is the main entry point: returns a WorkspaceContext for the given workDir,
// using cached results when available (TTL 60s).
func Build(workDir string, workspaceRoots []string) *WorkspaceContext {
	if workDir == "" {
		return &WorkspaceContext{Primary: ProjectInfo{Type: "unknown"}}
	}

	cacheMu.Lock()
	if e, ok := cache[workDir]; ok && time.Now().Before(e.expiresAt) {
		cacheMu.Unlock()
		return e.ctx
	}
	cacheMu.Unlock()

	ctx := build(workDir, workspaceRoots)

	cacheMu.Lock()
	cache[workDir] = cacheEntry{ctx: ctx, expiresAt: time.Now().Add(cacheTTL)}
	pruneOldEntries()
	cacheMu.Unlock()

	return ctx
}

func build(workDir string, workspaceRoots []string) *WorkspaceContext {
	primary := ProbeL0(workDir)
	bi := ProbeL1(primary.BuildRoot(), primary.Type)
	boundary := ProbeBoundary(workDir)

	var siblings []ProjectInfo
	var siblingPaths []string
	for _, root := range workspaceRoots {
		if root == workDir {
			continue
		}
		sib := ProbeL0(root)
		siblings = append(siblings, sib)
		siblingPaths = append(siblingPaths, root)
	}

	relations := ProbeRelations(workDir, siblingPaths)

	ctx := &WorkspaceContext{
		Primary:   primary,
		Siblings:  siblings,
		Relations: relations,
		BuildInfo: bi,
		Boundary:  boundary,
	}

	slog.Info("[wsintel] workspace probed",
		"path", workDir,
		"type", primary.Type,
		"siblings", len(siblings),
		"relations", len(relations),
		"has_build_info", bi != nil,
		"readonly_dirs", len(boundary.ReadOnlyDirs),
	)

	return ctx
}

// InvalidateCache removes the cache entry for the given workDir.
func InvalidateCache(workDir string) {
	cacheMu.Lock()
	delete(cache, workDir)
	cacheMu.Unlock()
}

const maxCacheEntries = 32

func pruneOldEntries() {
	if len(cache) <= maxCacheEntries {
		return
	}
	now := time.Now()
	for k, e := range cache {
		if now.After(e.expiresAt) {
			delete(cache, k)
		}
	}
}
