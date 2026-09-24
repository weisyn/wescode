package engine

import (
	"sync"
	"sync/atomic"
	"time"
)

// TCRTracker implements Task Completion Rate measurement per design/15-metrics.md §1.1.
//
// Logic: after a Run ends, start a 120s timer. If the user does NOT initiate
// a new edit request (RunChat) touching any file the Run modified within that
// window, the task counts as "completed first-try." If a new request arrives
// within 120s on the same file set, it counts as "incomplete."
//
// This is a product metric (Tier 1), not a billing metric. It lives in
// wescode-app.db and is surfaced via the debug/metrics RPC.
type TCRTracker struct {
	mu       sync.Mutex
	pending  map[string]*tcrEntry // runID → pending evaluation
	stats    TCRStats
	onExpire func(runID string, completed bool) // optional callback for persistence
}

type tcrEntry struct {
	runID         string
	modifiedFiles []string
	timer         *time.Timer
	createdAt     time.Time
}

// TCRStats holds aggregated Task Completion Rate counters.
type TCRStats struct {
	TotalRuns      atomic.Int64
	CompletedFirst atomic.Int64
	Incomplete     atomic.Int64
}

// TCRSnapshot is a serializable point-in-time view.
type TCRSnapshot struct {
	TotalRuns      int64   `json:"totalRuns"`
	CompletedFirst int64   `json:"completedFirst"`
	Incomplete     int64   `json:"incomplete"`
	Rate           float64 `json:"rate"` // CompletedFirst / TotalRuns
}

const tcrEvalWindow = 120 * time.Second

func NewTCRTracker(onExpire func(runID string, completed bool)) *TCRTracker {
	return &TCRTracker{
		pending:  make(map[string]*tcrEntry),
		onExpire: onExpire,
	}
}

// RecordRunEnd starts the TCR evaluation window for a completed Run.
// modifiedFiles is the set of files the Run's tools wrote/edited.
func (t *TCRTracker) RecordRunEnd(runID string, modifiedFiles []string) {
	if len(modifiedFiles) == 0 {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.stats.TotalRuns.Add(1)

	timer := time.AfterFunc(tcrEvalWindow, func() {
		t.expire(runID, true)
	})

	t.pending[runID] = &tcrEntry{
		runID:         runID,
		modifiedFiles: modifiedFiles,
		timer:         timer,
		createdAt:     time.Now(),
	}
}

// RecordNewRequest checks if an incoming request touches files from a
// pending evaluation window. If yes, marks the prior Run as incomplete.
func (t *TCRTracker) RecordNewRequest(touchedFiles []string) {
	if len(touchedFiles) == 0 {
		return
	}
	touchSet := make(map[string]struct{}, len(touchedFiles))
	for _, f := range touchedFiles {
		touchSet[f] = struct{}{}
	}

	t.mu.Lock()
	var toExpire []string
	for runID, entry := range t.pending {
		for _, mf := range entry.modifiedFiles {
			if _, hit := touchSet[mf]; hit {
				toExpire = append(toExpire, runID)
				break
			}
		}
	}
	t.mu.Unlock()

	for _, runID := range toExpire {
		t.expire(runID, false)
	}
}

func (t *TCRTracker) expire(runID string, completed bool) {
	t.mu.Lock()
	entry, ok := t.pending[runID]
	if !ok {
		t.mu.Unlock()
		return
	}
	delete(t.pending, runID)
	entry.timer.Stop()
	t.mu.Unlock()

	if completed {
		t.stats.CompletedFirst.Add(1)
	} else {
		t.stats.Incomplete.Add(1)
	}

	if t.onExpire != nil {
		t.onExpire(runID, completed)
	}
}

// Snapshot returns point-in-time TCR metrics.
func (t *TCRTracker) Snapshot() TCRSnapshot {
	total := t.stats.TotalRuns.Load()
	completed := t.stats.CompletedFirst.Load()
	incomplete := t.stats.Incomplete.Load()
	var rate float64
	if total > 0 {
		rate = float64(completed) / float64(total)
	}
	return TCRSnapshot{
		TotalRuns:      total,
		CompletedFirst: completed,
		Incomplete:     incomplete,
		Rate:           rate,
	}
}

// Close stops all pending timers.
func (t *TCRTracker) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, entry := range t.pending {
		entry.timer.Stop()
	}
	t.pending = make(map[string]*tcrEntry)
}
