package codeintel

import (
	"sync"
	"time"
)

// IDEDiagnostic is a diagnostic entry reported by the IDE extension
// via IMarkerService (aggregating tsserver, ESLint, gopls, etc.).
type IDEDiagnostic struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	EndLine  int    `json:"endLine,omitempty"`
	EndCol   int    `json:"endColumn,omitempty"`
	Severity int    `json:"severity"` // 1=Error 2=Warning 4=Info 8=Hint (VSCode MarkerSeverity)
	Message  string `json:"message"`
	Source   string `json:"source,omitempty"` // "ts", "eslint", "gopls", etc.
	Code     string `json:"code,omitempty"`
}

// DiagnosticsCache stores IDE diagnostics pushed by the Extension.
// Thread-safe. Used by `get_diagnostics` tool and QualityGate LSP path.
type DiagnosticsCache struct {
	mu        sync.RWMutex
	byFile    map[string][]IDEDiagnostic
	updatedAt time.Time
}

func NewDiagnosticsCache() *DiagnosticsCache {
	return &DiagnosticsCache{
		byFile: make(map[string][]IDEDiagnostic),
	}
}

// Update replaces the entire diagnostics set.
// Called on each debounced push from the Extension.
func (c *DiagnosticsCache) Update(diags []IDEDiagnostic) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.byFile = make(map[string][]IDEDiagnostic, len(diags)/2+1)
	for _, d := range diags {
		c.byFile[d.Path] = append(c.byFile[d.Path], d)
	}
	c.updatedAt = time.Now()
}

// ForFile returns diagnostics for a specific file.
func (c *DiagnosticsCache) ForFile(path string) []IDEDiagnostic {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.byFile[path]
}

// All returns all cached diagnostics across all files.
func (c *DiagnosticsCache) All() []IDEDiagnostic {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var all []IDEDiagnostic
	for _, diags := range c.byFile {
		all = append(all, diags...)
	}
	return all
}

// FilesWithErrors returns file paths that have at least one Error-severity diagnostic.
func (c *DiagnosticsCache) FilesWithErrors() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var files []string
	for path, diags := range c.byFile {
		for _, d := range diags {
			if d.Severity == 1 {
				files = append(files, path)
				break
			}
		}
	}
	return files
}

// Snapshot returns a deep copy of all diagnostics grouped by file.
// Used by DiagnosticsBaseline.Take() to capture point-in-time state.
func (c *DiagnosticsCache) Snapshot() map[string][]IDEDiagnostic {
	c.mu.RLock()
	defer c.mu.RUnlock()

	snap := make(map[string][]IDEDiagnostic, len(c.byFile))
	for path, diags := range c.byFile {
		copied := make([]IDEDiagnostic, len(diags))
		copy(copied, diags)
		snap[path] = copied
	}
	return snap
}

// ErrorsAndWarnings returns only Error (1) and Warning (2) severity diagnostics.
func (c *DiagnosticsCache) ErrorsAndWarnings() []IDEDiagnostic {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var result []IDEDiagnostic
	for _, diags := range c.byFile {
		for _, d := range diags {
			if d.Severity <= 2 {
				result = append(result, d)
			}
		}
	}
	return result
}

// UpdatedAt returns the time of the last update.
func (c *DiagnosticsCache) UpdatedAt() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.updatedAt
}
