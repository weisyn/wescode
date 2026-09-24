package engine

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	maxBufferEntries    = 200
	maxBufferTotalBytes = 100 * 1024 * 1024 // 100 MB
)

// BufferStore maintains an in-memory overlay of editor dirty buffers.
// When a file is open and modified (unsaved) in the editor, its content
// is stored here so the AI read tool returns the latest editor state
// instead of the stale disk version.
//
// Capacity is bounded by maxBufferEntries and maxBufferTotalBytes;
// oldest entries are evicted FIFO when limits are exceeded.
//
// All paths are normalized to absolute cleaned paths to ensure consistent
// key matching between editor (fsPath) and tool (relative/joined paths).
type bufferEntry struct {
	content string
	setTime time.Time // stable timestamp for overlay Stat (set once per Set call)
}

type BufferStore struct {
	mu         sync.RWMutex
	buffers    map[string]bufferEntry
	order      []string // FIFO order for eviction
	totalBytes int

	// frozen tracks paths that should not accept new overlay content
	// because the agent recently wrote to them. This prevents the editor
	// from re-pushing stale dirty buffers after agent writes.
	frozen map[string]struct{}
}

func NewBufferStore() *BufferStore {
	return &BufferStore{
		buffers: make(map[string]bufferEntry),
		frozen:  make(map[string]struct{}),
	}
}

// normalizePath returns a cleaned absolute path with symlinks resolved for
// consistent key matching. On macOS, /var → /private/var symlink causes
// editor fsPath and tool paths to diverge without EvalSymlinks.
//
// When the path itself does not exist yet (e.g. a newly created file), the
// deepest existing ancestor is resolved instead — otherwise Set/Get keys for
// a fresh file under /var would never match an existing /private/var entry.
func normalizePath(path string) string {
	cleaned := filepath.Clean(path)
	resolved := cleaned
	if r, err := filepath.EvalSymlinks(cleaned); err == nil {
		resolved = r
	} else if r, err := resolveExistingAncestor(cleaned); err == nil {
		resolved = r
	}
	if abs, err := filepath.Abs(resolved); err == nil {
		resolved = abs
	}
	// Windows filesystems are case-insensitive, so the editor's fsPath
	// ("f:\proj\Main.go") and a tool-side joined path ("F:\proj\main.go") name
	// one file but hash to two map keys. The overlay then misses and `read`
	// returns disk content — silently, and it is the exact failure this store
	// exists to prevent. Same fold as normalizeWorkspaceRoot (D-9); the two
	// must agree, since callers cross between workspace paths and buffer keys.
	if runtime.GOOS == "windows" {
		resolved = strings.ToLower(resolved)
	}
	return resolved
}

// resolveExistingAncestor walks up from p until it finds a directory that
// exists, resolves its symlinks, and re-appends the remaining segments.
func resolveExistingAncestor(p string) (string, error) {
	dir := filepath.Dir(p)
	base := filepath.Base(p)
	for {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, base), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return p, fmt.Errorf("normalizePath: no resolvable ancestor for %q", p)
		}
		base = filepath.Join(filepath.Base(dir), base)
		dir = parent
	}
}

// Set updates (or creates) the overlay for the given path.
// If the path is frozen (agent recently wrote to it), the update is ignored
// to prevent stale editor buffers from overwriting fresh disk content.
func (bs *BufferStore) Set(path, content string) {
	path = normalizePath(path)

	bs.mu.Lock()
	defer bs.mu.Unlock()

	if _, frozen := bs.frozen[path]; frozen {
		// Check if the new content differs from disk — if so, the user made a
		// real edit after the agent wrote. Auto-thaw and accept the new overlay.
		diskContent, err := os.ReadFile(path)
		if err == nil && content != string(diskContent) {
			delete(bs.frozen, path)
			slog.Info("[buffer] auto-thaw: user edited frozen file", "path", path)
		} else {
			slog.Debug("[buffer] ignored set on frozen path", "path", path)
			return
		}
	}

	if old, exists := bs.buffers[path]; exists {
		bs.totalBytes -= len(old.content)
		bs.removeFromOrder(path)
	}
	bs.order = append(bs.order, path)

	bs.buffers[path] = bufferEntry{content: content, setTime: time.Now()}
	bs.totalBytes += len(content)
	bs.evictLocked()
}

// Remove deletes the overlay for the given path (e.g. on save or close).
func (bs *BufferStore) Remove(path string) {
	path = normalizePath(path)
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if old, exists := bs.buffers[path]; exists {
		bs.totalBytes -= len(old.content)
		delete(bs.buffers, path)
		bs.removeFromOrder(path)
	}
}

// Freeze marks a path as frozen (agent wrote to it). The editor's stale
// buffer will be ignored until Thaw is called (typically on editor/didSave
// or a subsequent editor/didChange after the editor reloads from disk).
func (bs *BufferStore) Freeze(path string) {
	path = normalizePath(path)
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.frozen[path] = struct{}{}
	// Also remove any existing stale overlay
	if old, exists := bs.buffers[path]; exists {
		bs.totalBytes -= len(old.content)
		delete(bs.buffers, path)
		bs.removeFromOrder(path)
	}
	slog.Debug("[buffer] path frozen", "path", path)
}

// Thaw removes the freeze on a path, allowing future editor changes to
// update the overlay again.
func (bs *BufferStore) Thaw(path string) {
	path = normalizePath(path)
	bs.mu.Lock()
	defer bs.mu.Unlock()
	delete(bs.frozen, path)
}

// Get returns the overlay content and true if the path has a dirty buffer.
// Returns ("", false) if no overlay exists (caller should read from disk).
func (bs *BufferStore) Get(path string) (string, bool) {
	path = normalizePath(path)
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	entry, ok := bs.buffers[path]
	if ok {
		slog.Debug("[buffer] overlay hit", "path", path)
	}
	return entry.content, ok
}

// GetEntry returns the overlay entry (content + setTime) for the given path.
// Returns nil if no overlay exists.
func (bs *BufferStore) GetEntry(path string) *bufferEntry {
	path = normalizePath(path)
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	entry, ok := bs.buffers[path]
	if !ok {
		return nil
	}
	return &entry
}

func (bs *BufferStore) removeFromOrder(path string) {
	for i, p := range bs.order {
		if p == path {
			bs.order = append(bs.order[:i], bs.order[i+1:]...)
			return
		}
	}
}

// Clear removes all overlay entries and frozen state. Called on Cell
// Close/switch to prevent stale data from leaking across workspaces.
func (bs *BufferStore) Clear() {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.buffers = make(map[string]bufferEntry)
	bs.order = nil
	bs.totalBytes = 0
	bs.frozen = make(map[string]struct{})
}

func (bs *BufferStore) evictLocked() {
	evicted := 0
	for (len(bs.buffers) > maxBufferEntries || bs.totalBytes > maxBufferTotalBytes) && len(bs.order) > 0 {
		oldest := bs.order[0]
		bs.order = bs.order[1:]
		if old, exists := bs.buffers[oldest]; exists {
			bs.totalBytes -= len(old.content)
			delete(bs.buffers, oldest)
			evicted++
		}
	}
	if evicted > 0 {
		slog.Warn("[buffer] overlay evicted entries (overlay may be stale for evicted files)",
			"evicted", evicted, "remaining", len(bs.buffers), "totalBytes", bs.totalBytes)
	}
}
