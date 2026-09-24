package codeintel

import (
	"sync"
	"time"
)

// DiagnosticsBaseline captures a snapshot of IDE diagnostics at a point in time.
// Used to distinguish "pre-existing debt" from "AI-introduced errors" during a Run.
type DiagnosticsBaseline struct {
	mu       sync.RWMutex
	snapshot map[string][]IDEDiagnostic // file → diagnostics at baseline
	takenAt  time.Time
}

// NewDiagnosticsBaseline creates an empty baseline (not yet taken).
func NewDiagnosticsBaseline() *DiagnosticsBaseline {
	return &DiagnosticsBaseline{}
}

// Take captures the current state of the DiagnosticsCache as the baseline.
func (b *DiagnosticsBaseline) Take(cache *DiagnosticsCache) {
	snap := cache.Snapshot()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snapshot = snap
	b.takenAt = time.Now()
}

// TakenAt returns when the baseline was captured. Zero means not yet taken.
func (b *DiagnosticsBaseline) TakenAt() time.Time {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.takenAt
}

// IsValid returns true if a baseline has been taken and is recent enough.
func (b *DiagnosticsBaseline) IsValid(maxAge time.Duration) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.takenAt.IsZero() {
		return false
	}
	return time.Since(b.takenAt) < maxAge
}

// BaselineDiff represents the difference between baseline and current diagnostics.
type BaselineDiff struct {
	NewErrors   []IDEDiagnostic // AI-introduced errors (not in baseline)
	NewWarnings []IDEDiagnostic // AI-introduced warnings (not in baseline)
	Resolved    []IDEDiagnostic // Existed in baseline but gone now
	Unchanged   int             // Count of diagnostics unchanged from baseline
}

// TotalNew returns the count of all new diagnostics (errors + warnings).
func (d BaselineDiff) TotalNew() int {
	return len(d.NewErrors) + len(d.NewWarnings)
}

// HasNewErrors returns true if AI introduced any errors.
func (d BaselineDiff) HasNewErrors() bool {
	return len(d.NewErrors) > 0
}

// DiffAgainst compares the baseline against the current cache state.
func (b *DiagnosticsBaseline) DiffAgainst(cache *DiagnosticsCache) BaselineDiff {
	b.mu.RLock()
	baseSnap := b.snapshot
	b.mu.RUnlock()

	currentSnap := cache.Snapshot()

	if baseSnap == nil {
		baseSnap = make(map[string][]IDEDiagnostic)
	}

	baseSet := buildDiagSet(baseSnap)
	currentSet := buildDiagSet(currentSnap)

	var diff BaselineDiff

	for key, diag := range currentSet {
		if _, existed := baseSet[key]; !existed {
			if diag.Severity == 1 {
				diff.NewErrors = append(diff.NewErrors, diag)
			} else if diag.Severity == 2 {
				diff.NewWarnings = append(diff.NewWarnings, diag)
			}
		} else {
			diff.Unchanged++
		}
	}

	for key, diag := range baseSet {
		if _, exists := currentSet[key]; !exists {
			diff.Resolved = append(diff.Resolved, diag)
		}
	}

	return diff
}

// DiffForFiles compares baseline against current cache for specific files only.
func (b *DiagnosticsBaseline) DiffForFiles(cache *DiagnosticsCache, files []string) BaselineDiff {
	b.mu.RLock()
	baseSnap := b.snapshot
	b.mu.RUnlock()

	if baseSnap == nil {
		baseSnap = make(map[string][]IDEDiagnostic)
	}

	fileSet := make(map[string]struct{}, len(files))
	for _, f := range files {
		fileSet[f] = struct{}{}
	}

	filteredBase := make(map[string][]IDEDiagnostic)
	for path, diags := range baseSnap {
		if _, ok := fileSet[path]; ok {
			filteredBase[path] = diags
		}
	}

	filteredCurrent := make(map[string][]IDEDiagnostic)
	for _, path := range files {
		if diags := cache.ForFile(path); len(diags) > 0 {
			filteredCurrent[path] = diags
		}
	}

	baseSetFiltered := buildDiagSet(filteredBase)
	currentSetFiltered := buildDiagSet(filteredCurrent)

	var diff BaselineDiff

	for key, diag := range currentSetFiltered {
		if _, existed := baseSetFiltered[key]; !existed {
			if diag.Severity == 1 {
				diff.NewErrors = append(diff.NewErrors, diag)
			} else if diag.Severity == 2 {
				diff.NewWarnings = append(diff.NewWarnings, diag)
			}
		} else {
			diff.Unchanged++
		}
	}

	for key, diag := range baseSetFiltered {
		if _, exists := currentSetFiltered[key]; !exists {
			diff.Resolved = append(diff.Resolved, diag)
		}
	}

	return diff
}

// diagKey produces a stable identity for a diagnostic (path + line + severity + message).
type diagKey struct {
	Path     string
	Line     int
	Severity int
	Message  string
}

func buildDiagSet(snap map[string][]IDEDiagnostic) map[diagKey]IDEDiagnostic {
	set := make(map[diagKey]IDEDiagnostic)
	for _, diags := range snap {
		for _, d := range diags {
			key := diagKey{
				Path:     d.Path,
				Line:     d.Line,
				Severity: d.Severity,
				Message:  d.Message,
			}
			set[key] = d
		}
	}
	return set
}
