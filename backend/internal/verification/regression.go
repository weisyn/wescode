package verification

import (
	"context"
	"fmt"
	"strings"

	"github.com/weisyn/wesgine/message"
)

// RegressionInfo describes a test that was passing in the baseline
// but is now failing — a true behavioral regression.
type RegressionInfo struct {
	Package        string
	TestName       string
	BaselineStatus string // "pass" — was passing in baseline
	CurrentOutput  string
	IsFlaky        bool
}

// DetectRegressions compares current test failures against the latest
// baseline to identify true regressions (was-pass, now-fail).
// Returns nil if no baseline exists or no regressions are detected.
func DetectRegressions(ctx context.Context, bs *BaselineStore, workDirHash string, failures []TestFailure) ([]RegressionInfo, error) {
	latest, err := bs.LatestRun(ctx, workDirHash)
	if err != nil || latest == nil {
		return nil, err
	}

	results, err := bs.LoadResults(ctx, latest.ID)
	if err != nil {
		return nil, err
	}

	// Build a set of tests that were passing in the baseline.
	type testKey struct{ pkg, name string }
	baselinePassed := map[testKey]bool{}
	for _, r := range results {
		if r.Status == "pass" {
			baselinePassed[testKey{r.Package, r.TestName}] = true
		}
	}

	var regressions []RegressionInfo
	for _, f := range failures {
		key := testKey{f.Package, f.TestName}
		if baselinePassed[key] {
			// Record the flip first so IsFlaky sees the current flip count
			// (fixes off-by-one: 3rd flip should trigger flaky, not 4th).
			bs.RecordFlip(ctx, workDirHash, f.Package, f.TestName)
			isFlaky := bs.IsFlaky(ctx, workDirHash, f.Package, f.TestName)
			regressions = append(regressions, RegressionInfo{
				Package:        f.Package,
				TestName:       f.TestName,
				BaselineStatus: "pass",
				CurrentOutput:  f.Output,
				IsFlaky:        isFlaky,
			})
		}
	}
	return regressions, nil
}

// OutputRegression describes a single field-level difference between
// baseline and current test behavior. Unlike RegressionInfo which only
// detects pass→fail flips, this catches output changes in passing tests.
type OutputRegression struct {
	TestName      string `json:"test_name"`
	Package       string `json:"package,omitempty"`
	Field         string `json:"field"` // "stdout" | "stderr" | "duration" | "exit_code"
	BaselineValue string `json:"baseline_value"`
	CurrentValue  string `json:"current_value"`
	Severity      string `json:"severity"` // "critical" | "warning" | "perf"
}

// DetectBehavioralRegressions compares current test outcomes against baseline
// output hashes (L2.5). Unlike DetectRegressions which only catches pass→fail
// flips, this detects tests that still pass but produce different output —
// the most dangerous kind of hidden regression.
//
// flakyList is optional; when non-nil, flaky tests skip output hash comparison
// but still participate in exit_code checks.
func DetectBehavioralRegressions(ctx context.Context, bs *BaselineStore, workDirHash string, outcomes []TestOutcome, flakyList *FlakyList) ([]OutputRegression, error) {
	latest, err := bs.LatestRun(ctx, workDirHash)
	if err != nil || latest == nil {
		return nil, err
	}

	baselineResults, err := bs.LoadResults(ctx, latest.ID)
	if err != nil {
		return nil, err
	}

	type testKey struct{ pkg, name string }
	baselineMap := make(map[testKey]BaselineResult, len(baselineResults))
	for _, r := range baselineResults {
		baselineMap[testKey{r.Package, r.TestName}] = r
	}

	var regressions []OutputRegression
	for _, o := range outcomes {
		key := testKey{o.Package, o.TestName}
		br, ok := baselineMap[key]
		if !ok {
			continue
		}

		baseSnap := SnapshotFromBaseline(br)
		currSnap := SnapshotFromOutcome(o)
		diffs := CompareSnapshots(baseSnap, currSnap)

		for _, d := range diffs {
			if flakyList != nil && flakyList.IsFlaky(o.TestName) {
				// Flaky tests skip output hash comparison but
				// exit_code changes are always reported.
				if d.Field != "exit_code" {
					continue
				}
			}
			d.Package = o.Package
			regressions = append(regressions, d)
		}
	}

	return regressions, nil
}

// FilterNonFlaky returns only regressions that are not marked as flaky (INV-CSE-09).
func FilterNonFlaky(regressions []RegressionInfo) []RegressionInfo {
	var real []RegressionInfo
	for _, r := range regressions {
		if !r.IsFlaky {
			real = append(real, r)
		}
	}
	return real
}

// FormatRegressionFeedback builds a structured feedback message for the
// AI agent, explaining which tests regressed and what they expected.
func FormatRegressionFeedback(regressions []RegressionInfo, testResult TestResult) message.Message {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("⚠️ Behavioral regression detected: %d test(s) that were previously passing are now failing.\n\n", len(regressions)))

	for i, r := range regressions {
		sb.WriteString(fmt.Sprintf("%d. %s / %s\n", i+1, r.Package, r.TestName))
		sb.WriteString("   Status: was PASS in baseline, now FAIL\n")
		if r.CurrentOutput != "" {
			output := r.CurrentOutput
			if len(output) > 500 {
				output = output[:500] + "\n   ... (truncated)"
			}
			sb.WriteString("   Output:\n")
			for _, line := range strings.Split(output, "\n") {
				if line != "" {
					sb.WriteString("   " + line + "\n")
				}
			}
		}
		sb.WriteByte('\n')
	}

	sb.WriteString("These tests were passing before your changes. Please fix the regressions.\n")
	if testResult.TotalFail > len(regressions) {
		sb.WriteString(fmt.Sprintf("Note: %d other test failure(s) are pre-existing (not regressions).\n",
			testResult.TotalFail-len(regressions)))
	}

	return message.Message{
		Role:    message.RoleUser,
		Content: []message.ContentBlock{message.NewTextBlock(sb.String())},
	}
}
