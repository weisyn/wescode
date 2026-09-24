package codeintel

import (
	"sync/atomic"
	"time"
)

// ReadinessReport describes how "ready" a constraint source is for consumption.
// Every CKG query, constraint check, and baseline comparison attaches one.
//
// CI-24: queries MUST check Completeness; < threshold → degrade or annotate.
type ReadinessReport struct {
	Source       string    `json:"source"`       // "ckg" / "constraint" / "baseline"
	Completeness float64   `json:"completeness"` // 0.0 ~ 1.0
	Freshness    float64   `json:"freshness"`    // 0.0 ~ 1.0 (indexed-and-current / indexed)
	IndexedFiles int       `json:"indexed_files"`
	TotalFiles   int       `json:"total_files"`
	StaleFiles   int       `json:"stale_files"`
	LastUpdated  time.Time `json:"last_updated"`
	Indexing     bool      `json:"indexing"`
}

// Completeness thresholds for degradation behavior.
const (
	ReadinessLow    = 0.3 // below: skip entirely (CI-24 greenfield)
	ReadinessMedium = 0.5 // below: heavy degradation
	ReadinessHigh   = 0.9 // above: full fidelity
)

// Readiness returns a point-in-time ReadinessReport for the CKG index.
func (ci *CodeIndex) Readiness() ReadinessReport {
	total := int(atomic.LoadInt32(&ci.totalFiles))
	indexed := int(atomic.LoadInt32(&ci.indexedCount))
	stale := int(atomic.LoadInt32(&ci.staleCount))
	indexing := atomic.LoadInt32(&ci.indexing) != 0

	var completeness float64
	if total > 0 {
		completeness = float64(indexed) / float64(total)
		if completeness > 1.0 {
			completeness = 1.0
		}
	} else if indexed > 0 {
		// totalFiles not yet set (background scan hasn't run); indexed files
		// exist from PostWriteIndex or manual calls → treat as fully complete
		// to avoid false degradation before the first full scan.
		completeness = 1.0
	}

	var freshness float64
	if indexed > 0 {
		freshness = 1.0 - float64(stale)/float64(indexed)
		if freshness < 0 {
			freshness = 0
		}
	}

	var lastScan time.Time
	if ns := ci.lastFullScanNano.Load(); ns > 0 {
		lastScan = time.Unix(0, ns)
	}

	return ReadinessReport{
		Source:       "ckg",
		Completeness: completeness,
		Freshness:    freshness,
		IndexedFiles: indexed,
		TotalFiles:   total,
		StaleFiles:   stale,
		LastUpdated:  lastScan,
		Indexing:     indexing,
	}
}

// SetIndexedCount explicitly sets the indexed file count.
func (ci *CodeIndex) SetIndexedCount(n int) {
	atomic.StoreInt32(&ci.indexedCount, int32(n))
}

// IsIndexing returns true when backgroundIndex is actively writing to the DB.
// Callers on the chat hot path should skip ci.mu.RLock()-dependent operations
// to avoid being blocked by the exclusive write lock held per-file.
func (ci *CodeIndex) IsIndexing() bool {
	return atomic.LoadInt32(&ci.indexing) != 0
}

// BufferProvider supplies dirty editor buffer content for freshness-aware
// PreWriteCheck. Implemented by engine.BufferStore.
type BufferProvider interface {
	Get(path string) []byte
}
