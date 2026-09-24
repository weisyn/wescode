package editengine

import "sync/atomic"

// EditMetrics tracks edit tool usage and match tier distribution.
// All fields are atomic — safe for concurrent PostCallHook / MatchFallback use.
type EditMetrics struct {
	TotalEdits   atomic.Int64
	TotalWrites  atomic.Int64
	TotalApplies atomic.Int64

	Tier1Hits atomic.Int64
	Tier2Hits atomic.Int64
	Tier3Hits atomic.Int64
	AllMiss   atomic.Int64

	lastTier  atomic.Int32 // most recently recorded tier (for evidence)
	lastStart atomic.Int64 // byte start of last fallback match
	lastEnd   atomic.Int64 // byte end of last fallback match
}

func NewEditMetrics() *EditMetrics {
	return &EditMetrics{}
}

func (m *EditMetrics) RecordTool(name string) {
	switch name {
	case "edit":
		m.TotalEdits.Add(1)
	case "write":
		m.TotalWrites.Add(1)
	case "apply_patch":
		m.TotalApplies.Add(1)
	}
}

func (m *EditMetrics) RecordTier(tier int) {
	m.lastTier.Store(int32(tier))
	switch tier {
	case 1:
		m.Tier1Hits.Add(1)
	case 2:
		m.Tier2Hits.Add(1)
	case 3:
		m.Tier3Hits.Add(1)
	default:
		m.AllMiss.Add(1)
	}
}

// RecordMatchRange records the byte range of the last successful match.
func (m *EditMetrics) RecordMatchRange(start, end int) {
	m.lastStart.Store(int64(start))
	m.lastEnd.Store(int64(end))
}

// LastTier returns the most recently recorded match tier (1/2/3/0).
func (m *EditMetrics) LastTier() int {
	return int(m.lastTier.Load())
}

// LastMatchRange returns the byte range of the last successful fallback match.
func (m *EditMetrics) LastMatchRange() (int, int) {
	return int(m.lastStart.Load()), int(m.lastEnd.Load())
}

// Snapshot returns a point-in-time copy of all counters.
func (m *EditMetrics) Snapshot() EditMetricsSnapshot {
	return EditMetricsSnapshot{
		TotalEdits:   m.TotalEdits.Load(),
		TotalWrites:  m.TotalWrites.Load(),
		TotalApplies: m.TotalApplies.Load(),
		Tier1Hits:    m.Tier1Hits.Load(),
		Tier2Hits:    m.Tier2Hits.Load(),
		Tier3Hits:    m.Tier3Hits.Load(),
		AllMiss:      m.AllMiss.Load(),
	}
}

type EditMetricsSnapshot struct {
	TotalEdits   int64 `json:"totalEdits"`
	TotalWrites  int64 `json:"totalWrites"`
	TotalApplies int64 `json:"totalApplies"`
	Tier1Hits    int64 `json:"tier1Hits"`
	Tier2Hits    int64 `json:"tier2Hits"`
	Tier3Hits    int64 `json:"tier3Hits"`
	AllMiss      int64 `json:"allMiss"`
}
