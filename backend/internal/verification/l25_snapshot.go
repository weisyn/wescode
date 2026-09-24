package verification

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strconv"
)

// BehavioralSnapshot captures deterministic test output for L2.5 comparison.
// Stored as part of baseline runs; compared against current runs to detect
// behavioral regressions where tests still pass but output has changed.
type BehavioralSnapshot struct {
	TestName   string `json:"test_name"`
	ExitCode   int    `json:"exit_code"`
	StdoutHash string `json:"stdout_hash"`
	StderrHash string `json:"stderr_hash"`
	DurationMs int64  `json:"duration_ms"`
	SourceHash string `json:"source_hash"`
}

// CaptureSnapshot builds a BehavioralSnapshot from streaming test output.
// Uses SHA-256 to avoid storing full test output in memory.
func CaptureSnapshot(testName string, stdout, stderr io.Reader, exitCode int, durationMs int64, sourceHash string) BehavioralSnapshot {
	return BehavioralSnapshot{
		TestName:   testName,
		ExitCode:   exitCode,
		StdoutHash: streamHash(stdout),
		StderrHash: streamHash(stderr),
		DurationMs: durationMs,
		SourceHash: sourceHash,
	}
}

// SnapshotFromOutcome converts a TestOutcome (from go test -json parsing)
// into a BehavioralSnapshot for comparison.
func SnapshotFromOutcome(o TestOutcome) BehavioralSnapshot {
	exitCode := 0
	if o.Status == "fail" {
		exitCode = 1
	}
	return BehavioralSnapshot{
		TestName:   o.TestName,
		ExitCode:   exitCode,
		StdoutHash: o.OutputHash,
		DurationMs: o.Duration.Milliseconds(),
	}
}

// SnapshotFromBaseline converts a stored BaselineResult into a
// BehavioralSnapshot for comparison.
func SnapshotFromBaseline(r BaselineResult) BehavioralSnapshot {
	exitCode := 0
	if r.Status == "fail" {
		exitCode = 1
	}
	return BehavioralSnapshot{
		TestName:   r.TestName,
		ExitCode:   exitCode,
		StdoutHash: r.StdoutHash,
		StderrHash: r.StderrHash,
		DurationMs: r.DurationMs,
		SourceHash: r.SourceHash,
	}
}

// CompareSnapshots detects behavioral regressions between baseline and current.
// Returns nil when there are no differences worth reporting.
func CompareSnapshots(baseline, current BehavioralSnapshot) []OutputRegression {
	var regressions []OutputRegression

	if baseline.ExitCode != current.ExitCode {
		regressions = append(regressions, OutputRegression{
			TestName:      current.TestName,
			Field:         "exit_code",
			BaselineValue: strconv.Itoa(baseline.ExitCode),
			CurrentValue:  strconv.Itoa(current.ExitCode),
			Severity:      "critical",
		})
	}

	if baseline.StdoutHash != "" && current.StdoutHash != "" && baseline.StdoutHash != current.StdoutHash {
		regressions = append(regressions, OutputRegression{
			TestName:      current.TestName,
			Field:         "stdout",
			BaselineValue: truncHash(baseline.StdoutHash),
			CurrentValue:  truncHash(current.StdoutHash),
			Severity:      "warning",
		})
	}

	if baseline.StderrHash != "" && current.StderrHash != "" && baseline.StderrHash != current.StderrHash {
		regressions = append(regressions, OutputRegression{
			TestName:      current.TestName,
			Field:         "stderr",
			BaselineValue: truncHash(baseline.StderrHash),
			CurrentValue:  truncHash(current.StderrHash),
			Severity:      "warning",
		})
	}

	// Duration > 3x baseline is a performance regression.
	if baseline.DurationMs > 0 && current.DurationMs > baseline.DurationMs*3 {
		regressions = append(regressions, OutputRegression{
			TestName:      current.TestName,
			Field:         "duration",
			BaselineValue: strconv.Itoa(int(baseline.DurationMs)) + "ms",
			CurrentValue:  strconv.Itoa(int(current.DurationMs)) + "ms",
			Severity:      "perf",
		})
	}

	return regressions
}

func truncHash(h string) string {
	if len(h) > 16 {
		return h[:16]
	}
	return h
}

func streamHash(r io.Reader) string {
	if r == nil {
		return ""
	}
	h := sha256.New()
	io.Copy(h, r)
	return hex.EncodeToString(h.Sum(nil))
}

// HashString computes SHA-256 of a string value.
func HashString(s string) string {
	if s == "" {
		return ""
	}
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
