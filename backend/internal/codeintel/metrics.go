package codeintel

import (
	"sync"
	"sync/atomic"
	"time"
)

// RetrievalMetrics tracks code intelligence retrieval quality indicators.
// Thread-safe for concurrent access from Assemble calls.
type RetrievalMetrics struct {
	mu sync.RWMutex

	// Counters
	TotalAssembles     atomic.Int64
	TotalFragments     atomic.Int64
	TotalTokensUsed    atomic.Int64
	TotalTokensBudget  atomic.Int64
	SearchSymbolsCalls atomic.Int64
	LSPHoverCalls      atomic.Int64
	LSPHoverHits       atomic.Int64
	LSPDisambiguations atomic.Int64

	// TaskType distribution
	taskCounts map[TaskType]int64

	// Per-assemble timing (rolling window of last 100)
	latencies   []time.Duration
	latencyHead int
}

// NewRetrievalMetrics creates a new metrics collector.
func NewRetrievalMetrics() *RetrievalMetrics {
	return &RetrievalMetrics{
		taskCounts: make(map[TaskType]int64),
		latencies:  make([]time.Duration, 100),
	}
}

// RecordAssemble records metrics for a completed Assemble call.
func (m *RetrievalMetrics) RecordAssemble(taskType TaskType, fragmentCount, tokensUsed, tokensBudget int, elapsed time.Duration) {
	m.TotalAssembles.Add(1)
	m.TotalFragments.Add(int64(fragmentCount))
	m.TotalTokensUsed.Add(int64(tokensUsed))
	m.TotalTokensBudget.Add(int64(tokensBudget))

	m.mu.Lock()
	m.taskCounts[taskType]++
	m.latencies[m.latencyHead%len(m.latencies)] = elapsed
	m.latencyHead++
	m.mu.Unlock()
}

// RecordLSPHover records an LSP Hover call and whether it returned useful info.
func (m *RetrievalMetrics) RecordLSPHover(hit bool) {
	m.LSPHoverCalls.Add(1)
	if hit {
		m.LSPHoverHits.Add(1)
	}
}

// RecordDisambiguation records a successful LSP-based type disambiguation.
func (m *RetrievalMetrics) RecordDisambiguation() {
	m.LSPDisambiguations.Add(1)
}

// Snapshot returns a point-in-time copy of all metrics.
func (m *RetrievalMetrics) Snapshot() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	taskDist := make(map[string]int64, len(m.taskCounts))
	for k, v := range m.taskCounts {
		taskDist[string(k)] = v
	}

	totalAssembles := m.TotalAssembles.Load()
	totalTokensUsed := m.TotalTokensUsed.Load()
	totalTokensBudget := m.TotalTokensBudget.Load()

	var avgUtilization float64
	if totalTokensBudget > 0 {
		avgUtilization = float64(totalTokensUsed) / float64(totalTokensBudget)
	}

	var avgFragments float64
	if totalAssembles > 0 {
		avgFragments = float64(m.TotalFragments.Load()) / float64(totalAssembles)
	}

	// Calculate p50/p95 latency from rolling window
	count := m.latencyHead
	if count > len(m.latencies) {
		count = len(m.latencies)
	}
	var p50, p95 time.Duration
	if count > 0 {
		sorted := make([]time.Duration, count)
		for i := 0; i < count; i++ {
			idx := (m.latencyHead - count + i) % len(m.latencies)
			if idx < 0 {
				idx += len(m.latencies)
			}
			sorted[i] = m.latencies[idx]
		}
		sortDurations(sorted)
		p50 = sorted[count/2]
		p95Idx := count * 95 / 100
		if p95Idx >= count {
			p95Idx = count - 1
		}
		p95 = sorted[p95Idx]
	}

	lspCalls := m.LSPHoverCalls.Load()
	lspHits := m.LSPHoverHits.Load()
	var lspHitRate float64
	if lspCalls > 0 {
		lspHitRate = float64(lspHits) / float64(lspCalls)
	}

	return MetricsSnapshot{
		TotalAssembles:      totalAssembles,
		AvgFragmentsPerRun:  avgFragments,
		AvgTokenUtilization: avgUtilization,
		TaskDistribution:    taskDist,
		LatencyP50Ms:        p50.Milliseconds(),
		LatencyP95Ms:        p95.Milliseconds(),
		LSPHoverHitRate:     lspHitRate,
		LSPDisambiguations:  m.LSPDisambiguations.Load(),
	}
}

// MetricsSnapshot is a serializable point-in-time copy of retrieval metrics.
type MetricsSnapshot struct {
	TotalAssembles      int64            `json:"totalAssembles"`
	AvgFragmentsPerRun  float64          `json:"avgFragmentsPerRun"`
	AvgTokenUtilization float64          `json:"avgTokenUtilization"`
	TaskDistribution    map[string]int64 `json:"taskDistribution"`
	LatencyP50Ms        int64            `json:"latencyP50Ms"`
	LatencyP95Ms        int64            `json:"latencyP95Ms"`
	LSPHoverHitRate     float64          `json:"lspHoverHitRate"`
	LSPDisambiguations  int64            `json:"lspDisambiguations"`
}

func sortDurations(d []time.Duration) {
	for i := 1; i < len(d); i++ {
		key := d[i]
		j := i - 1
		for j >= 0 && d[j] > key {
			d[j+1] = d[j]
			j--
		}
		d[j+1] = key
	}
}
