package verification

import "sync/atomic"

// VerificationMetrics tracks aggregate verification statistics across runs.
// All counters are atomic — safe for concurrent QualityGate.Evaluate calls.
type VerificationMetrics struct {
	L0Triggered atomic.Int64
	L0Errors    atomic.Int64

	L1Triggered atomic.Int64
	L1Passed    atomic.Int64
	L1Failed    atomic.Int64

	L2Triggered atomic.Int64
	L2Passed    atomic.Int64
	L2Failed    atomic.Int64

	L25Triggered     atomic.Int64
	L25Passed        atomic.Int64
	L25Failed        atomic.Int64
	BaselineCaptured atomic.Int64
	FlakyDetected    atomic.Int64

	L3Triggered atomic.Int64
	L3Passed    atomic.Int64
	L3Failed    atomic.Int64

	RevisionAttempts atomic.Int64
	RevisionSuccess  atomic.Int64
}

func NewVerificationMetrics() *VerificationMetrics {
	return &VerificationMetrics{}
}

// Snapshot returns a point-in-time copy of all counters.
func (m *VerificationMetrics) Snapshot() VerificationStatsSnapshot {
	return VerificationStatsSnapshot{
		L0Triggered: m.L0Triggered.Load(),
		L0Errors:    m.L0Errors.Load(),

		L1Triggered: m.L1Triggered.Load(),
		L1Passed:    m.L1Passed.Load(),
		L1Failed:    m.L1Failed.Load(),

		L2Triggered: m.L2Triggered.Load(),
		L2Passed:    m.L2Passed.Load(),
		L2Failed:    m.L2Failed.Load(),

		L25Triggered:     m.L25Triggered.Load(),
		L25Passed:        m.L25Passed.Load(),
		L25Failed:        m.L25Failed.Load(),
		BaselineCaptured: m.BaselineCaptured.Load(),
		FlakyDetected:    m.FlakyDetected.Load(),

		L3Triggered: m.L3Triggered.Load(),
		L3Passed:    m.L3Passed.Load(),
		L3Failed:    m.L3Failed.Load(),

		RevisionAttempts: m.RevisionAttempts.Load(),
		RevisionSuccess:  m.RevisionSuccess.Load(),
	}
}

// VerificationStatsSnapshot is a JSON-serializable point-in-time view.
type VerificationStatsSnapshot struct {
	L0Triggered int64 `json:"l0Triggered"`
	L0Errors    int64 `json:"l0Errors"`

	L1Triggered int64 `json:"l1Triggered"`
	L1Passed    int64 `json:"l1Passed"`
	L1Failed    int64 `json:"l1Failed"`

	L2Triggered int64 `json:"l2Triggered"`
	L2Passed    int64 `json:"l2Passed"`
	L2Failed    int64 `json:"l2Failed"`

	L25Triggered     int64 `json:"l25Triggered"`
	L25Passed        int64 `json:"l25Passed"`
	L25Failed        int64 `json:"l25Failed"`
	BaselineCaptured int64 `json:"baselineCaptured"`
	FlakyDetected    int64 `json:"flakyDetected"`

	L3Triggered int64 `json:"l3Triggered"`
	L3Passed    int64 `json:"l3Passed"`
	L3Failed    int64 `json:"l3Failed"`

	RevisionAttempts int64 `json:"revisionAttempts"`
	RevisionSuccess  int64 `json:"revisionSuccess"`
}
