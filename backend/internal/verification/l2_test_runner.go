package verification

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// TestOutcome records the result of a single test function.
type TestOutcome struct {
	Package    string
	TestName   string
	Status     string // "pass" | "fail" | "skip"
	Duration   time.Duration
	Output     string // retained only for failures
	OutputHash string // SHA-256 of test stdout for L2.5 behavioral comparison
}

// TestResult is the outcome of running tests on one or more packages.
type TestResult struct {
	AllPass     bool
	BuildFailed bool          // exitCode != 0 but no test function failed (compile error in test binary)
	Outcomes    []TestOutcome // per-test results (pass + fail + skip)
	Failed      []TestFailure // backward-compatible failure list
	TotalRun    int
	TotalFail   int
	TotalSkip   int
	RawOutput   string
	Duration    time.Duration
}

// TestFailure describes a single failing test (backward-compatible).
type TestFailure struct {
	Package  string
	TestName string
	Output   string
}

// RunTests executes `go test -json` and returns structured results.
// runSelector, when non-empty, is appended as a single `-run <regex>` flag
// (shell-quoted). Packages must already be narrowed to the current workspace
// root — cross-repo packages are filtered by FilterSameRepoPackages before
// this call (go test cannot resolve a sibling repo's packages from this
// module root; it fails instantly with "directory outside main module").
func RunTests(ctx context.Context, dir string, packages []string, runSelector string, timeout time.Duration, sp tool.ShellProvider) TestResult {
	if len(packages) == 0 {
		return TestResult{AllPass: true}
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	args := "go test -json -count=1 -timeout " + timeout.String() + " " + strings.Join(packages, " ")
	if runSelector != "" {
		// Single-quote so shell metacharacters in the regex (| ( ) $) are
		// passed through to the go test -run flag untouched.
		args += " -run '" + strings.ReplaceAll(runSelector, "'", `'\''`) + "'"
	}

	start := time.Now()
	session, err := sp.Start(ctx, tool.ShellRequest{
		Command: args,
		WorkDir: dir,
	})
	if err != nil {
		return TestResult{
			AllPass:     false,
			BuildFailed: true,
			RawOutput:   "failed to start tests: " + err.Error(),
			Duration:    time.Since(start),
		}
	}

	outputBytes, _ := io.ReadAll(session.Output())
	exitCode, _ := session.Wait()
	duration := time.Since(start)
	output := string(outputBytes)

	outcomes, failures := parseJSONTestOutput(output)

	totalRun := 0
	totalFail := 0
	totalSkip := 0
	for _, o := range outcomes {
		switch o.Status {
		case "pass":
			totalRun++
		case "fail":
			totalRun++
			totalFail++
		case "skip":
			totalSkip++
		}
	}

	// Fallback: if JSON parsing yielded no outcomes (e.g. non-Go runner
	// or build failure before any test runs), use legacy text parsing.
	if len(outcomes) == 0 && exitCode != 0 {
		failures = parseTestFailures(output)
		totalFail = len(failures)
	}

	allPass := exitCode == 0 && totalFail == 0
	buildFailed := exitCode != 0 && totalFail == 0

	return TestResult{
		AllPass:     allPass,
		BuildFailed: buildFailed,
		Outcomes:    outcomes,
		Failed:      failures,
		TotalRun:    totalRun,
		TotalFail:   totalFail,
		TotalSkip:   totalSkip,
		RawOutput:   output,
		Duration:    duration,
	}
}

// AffectedPackages derives Go packages from modified file paths.
func AffectedPackages(modifiedFiles []string, workDir string) []string {
	pkgs := make(map[string]struct{})
	for _, f := range modifiedFiles {
		rel, err := filepath.Rel(workDir, f)
		if err != nil {
			continue
		}
		pkg := "./" + filepath.Dir(rel) + "/..."
		pkgs[pkg] = struct{}{}
	}
	result := make([]string, 0, len(pkgs))
	for pkg := range pkgs {
		result = append(result, pkg)
	}
	return result
}

// FilterSameRepoPackages drops package arguments that resolve outside workDir
// (cross-repo siblings in a multi-root workspace, or absolute paths). `go test`
// cannot run them from this module root — each one fails instantly with
// "directory outside main module" (measured ~15ms false failure), which then
// flips the L2 verdict through fail-degraded and tells the model tests passed
// when nothing actually ran. Skipping is explicit and logged.
func FilterSameRepoPackages(packages []string, workDir string) []string {
	out := make([]string, 0, len(packages))
	var skipped []string
	for _, pkg := range packages {
		if isCrossRepoPackage(pkg) {
			skipped = append(skipped, pkg)
			continue
		}
		out = append(out, pkg)
	}
	if len(skipped) > 0 {
		slog.Warn("[verification] L2 skipped cross-repo packages (outside workspace root)",
			"work_dir", workDir, "skipped", skipped)
	}
	return out
}

// isCrossRepoPackage reports whether a `./<rel>/...` package argument points
// outside the current workspace root (rel escapes with ../, or an absolute path).
func isCrossRepoPackage(pkg string) bool {
	return strings.HasPrefix(pkg, "./../") || strings.HasPrefix(pkg, "../") || strings.HasPrefix(pkg, "/")
}

// testEvent is a single line from `go test -json` output.
type testEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
	Output  string  `json:"Output"`
}

// parseJSONTestOutput parses `go test -json` output into per-test outcomes.
func parseJSONTestOutput(output string) ([]TestOutcome, []TestFailure) {
	type testKey struct{ pkg, name string }
	outputBuf := map[testKey]*strings.Builder{}
	var outcomes []TestOutcome
	var failures []TestFailure

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		var ev testEvent
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev.Test == "" {
			continue // package-level event
		}
		key := testKey{ev.Package, ev.Test}

		switch ev.Action {
		case "output":
			if outputBuf[key] == nil {
				outputBuf[key] = &strings.Builder{}
			}
			outputBuf[key].WriteString(ev.Output)
		case "pass":
			hash := ""
			if b := outputBuf[key]; b != nil {
				hash = CanonicalHash([]byte(b.String()))
			}
			outcomes = append(outcomes, TestOutcome{
				Package:    ev.Package,
				TestName:   ev.Test,
				Status:     "pass",
				Duration:   time.Duration(ev.Elapsed * float64(time.Second)),
				OutputHash: hash,
			})
		case "fail":
			out := ""
			hash := ""
			if b := outputBuf[key]; b != nil {
				out = b.String()
				hash = CanonicalHash([]byte(out))
			}
			outcomes = append(outcomes, TestOutcome{
				Package:    ev.Package,
				TestName:   ev.Test,
				Status:     "fail",
				Duration:   time.Duration(ev.Elapsed * float64(time.Second)),
				Output:     out,
				OutputHash: hash,
			})
			failures = append(failures, TestFailure{
				Package:  ev.Package,
				TestName: ev.Test,
				Output:   out,
			})
		case "skip":
			outcomes = append(outcomes, TestOutcome{
				Package:  ev.Package,
				TestName: ev.Test,
				Status:   "skip",
			})
		}
	}
	return outcomes, failures
}

// parseTestFailures is the legacy text-based parser for non-JSON output.
func parseTestFailures(output string) []TestFailure {
	var failures []TestFailure
	var currentTest string
	var currentPkg string
	var currentOutput strings.Builder

	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "--- FAIL: ") {
			if currentTest != "" {
				failures = append(failures, TestFailure{
					Package:  currentPkg,
					TestName: currentTest,
					Output:   currentOutput.String(),
				})
			}
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				currentTest = parts[2]
			}
			currentOutput.Reset()
		} else if strings.HasPrefix(line, "FAIL\t") {
			parts := strings.Split(line, "\t")
			if len(parts) >= 2 {
				currentPkg = parts[1]
			}
			if currentTest != "" {
				failures = append(failures, TestFailure{
					Package:  currentPkg,
					TestName: currentTest,
					Output:   currentOutput.String(),
				})
				currentTest = ""
				currentOutput.Reset()
			}
		} else if currentTest != "" {
			currentOutput.WriteString(line)
			currentOutput.WriteByte('\n')
		}
	}
	return failures
}
