package engine

import (
	"sort"
	"sync"
	"time"
)

const (
	attentionWindow   = 5 * time.Minute
	coVisitThreshold  = 3
	coVisitWindow     = 5 * time.Minute
	maxAttentionFiles = 50
)

// FileAttention represents the aggregated attention signal for a single file.
type FileAttention struct {
	Path       string        `json:"path"`
	OpenCount  int           `json:"openCount"`
	TotalDwell time.Duration `json:"totalDwell"`
	LastActive time.Time     `json:"lastActive"`
	WasEdited  bool          `json:"wasEdited"`
	CoVisited  []string      `json:"coVisited,omitempty"`
	Score      float64       `json:"score"`
	Category   string        `json:"category"` // "editing" | "reference" | "glanced"
}

type attentionEvent struct {
	Path      string
	Timestamp time.Time
	Action    string // "focus" | "blur" | "edit"
}

// AttentionTracker maintains a sliding window of file access events and
// computes attention-weighted file priority for AI context selection.
type AttentionTracker struct {
	mu     sync.RWMutex
	events []attentionEvent

	// Per-file accumulated state (rebuilt on query from events)
	// Transition tracking for co-visit detection
	transitions []fileTransition
}

type fileTransition struct {
	From      string
	To        string
	Timestamp time.Time
}

func NewAttentionTracker() *AttentionTracker {
	return &AttentionTracker{}
}

// RecordFocus records that the user focused a file.
func (t *AttentionTracker) RecordFocus(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()

	// Record transition for co-visit detection
	if len(t.events) > 0 {
		lastFocus := ""
		for i := len(t.events) - 1; i >= 0; i-- {
			if t.events[i].Action == "focus" {
				lastFocus = t.events[i].Path
				break
			}
		}
		if lastFocus != "" && lastFocus != path {
			t.transitions = append(t.transitions, fileTransition{
				From: lastFocus, To: path, Timestamp: now,
			})
		}
	}

	t.events = append(t.events, attentionEvent{
		Path: path, Timestamp: now, Action: "focus",
	})
	t.prune()
}

// RecordEdit records that the user edited content in the focused file.
func (t *AttentionTracker) RecordEdit(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, attentionEvent{
		Path: path, Timestamp: time.Now(), Action: "edit",
	})
	t.prune()
}

type fileStats struct {
	openCount  int
	totalDwell time.Duration
	lastActive time.Time
	wasEdited  bool
}

// TopN returns the top N files by attention score.
func (t *AttentionTracker) TopN(n int) []FileAttention {
	t.mu.RLock()
	defer t.mu.RUnlock()

	now := time.Now()
	cutoff := now.Add(-attentionWindow)

	// Aggregate per file
	stats := make(map[string]*fileStats)

	// Process events chronologically to compute dwell
	var focusStack []attentionEvent
	for _, e := range t.events {
		if e.Timestamp.Before(cutoff) {
			continue
		}
		if e.Action == "focus" {
			focusStack = append(focusStack, e)
		}
		if e.Action == "edit" {
			fs := stats[e.Path]
			if fs == nil {
				fs = &fileStats{}
				stats[e.Path] = fs
			}
			fs.wasEdited = true
		}
	}

	// Compute dwell from focus events (each focus ends at next focus)
	for i, e := range focusStack {
		if e.Timestamp.Before(cutoff) {
			continue
		}
		fs := stats[e.Path]
		if fs == nil {
			fs = &fileStats{}
			stats[e.Path] = fs
		}
		fs.openCount++
		if e.Timestamp.After(fs.lastActive) {
			fs.lastActive = e.Timestamp
		}

		// Dwell = time until next focus event (or until now for current)
		var dwell time.Duration
		if i+1 < len(focusStack) {
			dwell = focusStack[i+1].Timestamp.Sub(e.Timestamp)
		} else {
			dwell = now.Sub(e.Timestamp)
		}
		if dwell > 5*time.Minute {
			dwell = 5 * time.Minute // cap single dwell at window size
		}
		fs.totalDwell += dwell
	}

	// Compute co-visits
	coVisitMap := t.computeCoVisits(cutoff)

	// Score and build result
	var results []FileAttention
	for path, fs := range stats {
		score := computeScore(fs, now)
		category := categorize(fs)
		fa := FileAttention{
			Path:       path,
			OpenCount:  fs.openCount,
			TotalDwell: fs.totalDwell,
			LastActive: fs.lastActive,
			WasEdited:  fs.wasEdited,
			Score:      score,
			Category:   category,
		}
		if coFiles, ok := coVisitMap[path]; ok {
			fa.CoVisited = coFiles
		}
		results = append(results, fa)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > n {
		results = results[:n]
	}
	return results
}

// CoVisitedFiles returns files that are frequently co-visited with the given path.
func (t *AttentionTracker) CoVisitedFiles(path string) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	cutoff := time.Now().Add(-coVisitWindow)
	coVisitMap := t.computeCoVisits(cutoff)
	return coVisitMap[path]
}

func (t *AttentionTracker) computeCoVisits(cutoff time.Time) map[string][]string {
	// Count transitions A↔B within window
	type pair struct{ a, b string }
	counts := make(map[pair]int)

	for _, tr := range t.transitions {
		if tr.Timestamp.Before(cutoff) {
			continue
		}
		a, b := tr.From, tr.To
		if a > b {
			a, b = b, a
		}
		counts[pair{a, b}]++
	}

	// Build co-visit map for pairs >= threshold
	result := make(map[string][]string)
	for p, count := range counts {
		if count >= coVisitThreshold {
			result[p.a] = appendUnique(result[p.a], p.b)
			result[p.b] = appendUnique(result[p.b], p.a)
		}
	}
	return result
}

func (t *AttentionTracker) prune() {
	cutoff := time.Now().Add(-attentionWindow)

	// Prune events
	start := 0
	for start < len(t.events) && t.events[start].Timestamp.Before(cutoff) {
		start++
	}
	if start > 0 {
		t.events = t.events[start:]
	}

	// Prune transitions
	tStart := 0
	for tStart < len(t.transitions) && t.transitions[tStart].Timestamp.Before(cutoff) {
		tStart++
	}
	if tStart > 0 {
		t.transitions = t.transitions[tStart:]
	}
}

func computeScore(fs *fileStats, now time.Time) float64 {
	const (
		editWeight    = 30.0
		dwellWeight   = 0.5 // per second
		freqWeight    = 5.0
		recencyWeight = 20.0
	)

	score := 0.0
	if fs.wasEdited {
		score += editWeight
	}
	score += dwellWeight * fs.totalDwell.Seconds()
	score += freqWeight * float64(fs.openCount)

	// Recency: exponential decay, half-life 60s
	elapsed := now.Sub(fs.lastActive).Seconds()
	recencyFactor := 1.0
	if elapsed > 0 {
		recencyFactor = 1.0 / (1.0 + elapsed/60.0)
	}
	score += recencyWeight * recencyFactor

	return score
}

func categorize(fs *fileStats) string {
	if fs.wasEdited {
		return "editing"
	}
	if fs.totalDwell > 10*time.Second {
		return "reference"
	}
	return "glanced"
}

func appendUnique(slice []string, item string) []string {
	for _, s := range slice {
		if s == item {
			return slice
		}
	}
	return append(slice, item)
}
