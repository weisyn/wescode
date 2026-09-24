package verification

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/weisyn/wescode/internal/codeintel/langs"
	"github.com/weisyn/wesgine/tool"
)

// LanguageRunner abstracts L1 compilation and L2 test execution.
// Each supported language/toolchain provides an implementation.
// The QualityGate dispatches to the appropriate runner based on project detection.
type LanguageRunner interface {
	// Name returns the language identifier (e.g. "go", "typescript", "python", "rust").
	Name() string

	// Compile runs type checking / compilation for the project.
	// Returns errors found; empty slice means success.
	Compile(ctx context.Context, workDir string) CompileResult

	// Test runs tests for the affected packages/modules.
	// packages is a list of package identifiers (format varies by language);
	// runSelector is an optional test-name regex (empty = run all tests in
	// the packages). It is the runner's job to pass it through as a single
	// `-run` flag — never to interleave it into the packages list.
	Test(ctx context.Context, workDir string, packages []string, runSelector string, timeout time.Duration) TestResult
}

// ProjectType identifies the language/build system of a workspace.
type ProjectType string

const (
	ProjectGo         ProjectType = "go"
	ProjectTypeScript ProjectType = "typescript"
	ProjectPython     ProjectType = "python"
	ProjectRust       ProjectType = "rust"
	ProjectUnknown    ProjectType = "unknown"
)

// DetectProjectType examines the workspace root for build system markers.
// Returns the dominant project type. For polyglot repos, returns the first match
// in priority order (Go > TS > Python > Rust).
func DetectProjectType(workDir string) ProjectType {
	markers := []struct {
		file    string
		project ProjectType
	}{
		{"go.mod", ProjectGo},
		{"package.json", ProjectTypeScript},
		{"pyproject.toml", ProjectPython},
		{"setup.py", ProjectPython},
		{"Cargo.toml", ProjectRust},
	}

	for _, m := range markers {
		if _, err := os.Stat(filepath.Join(workDir, m.file)); err == nil {
			return m.project
		}
	}

	// Check one level of subdirectories for monorepo patterns
	entries, err := os.ReadDir(workDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			for _, m := range markers {
				if _, err := os.Stat(filepath.Join(workDir, e.Name(), m.file)); err == nil {
					return m.project
				}
			}
		}
	}

	return ProjectUnknown
}

// ── Go Runner (default implementation) ─────────────────────────────────────

// GoRunner implements LanguageRunner for Go projects.
type GoRunner struct {
	SP tool.ShellProvider
}

func (r GoRunner) Name() string { return "go" }

func (r GoRunner) Compile(ctx context.Context, workDir string) CompileResult {
	buildDir := resolveBuildDir(workDir)
	return RunGoBuild(ctx, buildDir, r.sp())
}

func (r GoRunner) Test(ctx context.Context, workDir string, packages []string, runSelector string, timeout time.Duration) TestResult {
	testDir := resolveBuildDir(workDir)
	return RunTests(ctx, testDir, packages, runSelector, timeout, r.sp())
}

func (r GoRunner) sp() tool.ShellProvider {
	if r.SP != nil {
		return r.SP
	}
	return &tool.LocalShellProvider{}
}

// ── Null Runner (for unknown/unsupported project types) ────────────────────

// NullRunner passes all verification — used when the project type cannot be
// determined or has no supported toolchain. Satisfies VF-07 (fail-degraded).
type NullRunner struct{}

func (NullRunner) Name() string { return "null" }

func (NullRunner) Compile(_ context.Context, _ string) CompileResult {
	return CompileResult{Success: true}
}

func (NullRunner) Test(_ context.Context, _ string, _ []string, _ string, _ time.Duration) TestResult {
	return TestResult{AllPass: true}
}

// SelectRunner returns the appropriate LanguageRunner for a workspace.
// sp is the ShellProvider for command execution (typically from Host.Shell()).
func SelectRunner(workDir string, sp tool.ShellProvider) LanguageRunner {
	pt := DetectProjectType(workDir)

	reg := langs.Default()
	if reg != nil {
		var langID string
		switch pt {
		case ProjectGo:
			langID = "go"
		case ProjectTypeScript:
			langID = "typescript"
		case ProjectPython:
			langID = "python"
		case ProjectRust:
			langID = "rust"
		case ProjectUnknown:
			// no language-specific runner
		}
		if langID != "" {
			if cfg := reg.ByID(langID); cfg != nil {
				return &ConfigDrivenRunner{cfg: cfg, workDir: workDir, SP: sp}
			}
		}
	}

	if pt == ProjectGo {
		return GoRunner{SP: sp}
	}
	return NullRunner{}
}

// ConfigDrivenRunner executes compile/test commands from registry config.
type ConfigDrivenRunner struct {
	cfg     *langs.LanguageConfig
	workDir string
	SP      tool.ShellProvider
}

func (r *ConfigDrivenRunner) Name() string { return r.cfg.Language.ID }

func (r *ConfigDrivenRunner) sp() tool.ShellProvider {
	if r.SP != nil {
		return r.SP
	}
	return &tool.LocalShellProvider{}
}

func (r *ConfigDrivenRunner) Compile(ctx context.Context, workDir string) CompileResult {
	rc := r.cfg.CompileRunner
	if rc.Command == "" {
		return CompileResult{Success: true}
	}

	if r.cfg.Language.ID == "go" {
		return GoRunner{SP: r.SP}.Compile(ctx, workDir)
	}

	command := rc.Command + " " + strings.Join(rc.Args, " ")
	start := time.Now()
	session, err := r.sp().Start(ctx, tool.ShellRequest{
		Command: command,
		WorkDir: workDir,
	})
	if err != nil {
		return CompileResult{
			Success:   false,
			RawOutput: "failed to start: " + err.Error(),
			Duration:  time.Since(start),
		}
	}

	outputBytes, _ := io.ReadAll(session.Output())
	exitCode, _ := session.Wait()
	duration := time.Since(start)

	if exitCode == 0 {
		return CompileResult{Success: true, Duration: duration}
	}

	output := string(outputBytes)
	errors := parseCompileOutput(output, rc.ParsePattern, workDir)
	return CompileResult{
		Success:   false,
		Errors:    errors,
		RawOutput: output,
		Duration:  duration,
	}
}

func (r *ConfigDrivenRunner) Test(ctx context.Context, workDir string, packages []string, runSelector string, timeout time.Duration) TestResult {
	rc := r.cfg.TestRunner
	if rc.Command == "" {
		return TestResult{AllPass: true}
	}

	if r.cfg.Language.ID == "go" {
		return GoRunner{SP: r.SP}.Test(ctx, workDir, packages, runSelector, timeout)
	}

	// Non-Go runners have no -run semantics; runSelector is intentionally
	// ignored for their command templates.
	args := append(rc.Args, packages...)
	command := rc.Command + " " + strings.Join(args, " ")

	start := time.Now()
	session, err := r.sp().Start(ctx, tool.ShellRequest{
		Command: command,
		WorkDir: workDir,
	})
	if err != nil {
		return TestResult{AllPass: false, BuildFailed: true, RawOutput: "failed to start: " + err.Error(), Duration: time.Since(start)}
	}

	outputBytes, _ := io.ReadAll(session.Output())
	exitCode, _ := session.Wait()
	duration := time.Since(start)
	output := string(outputBytes)

	if exitCode == 0 {
		outcomes := parseTestOutcomes(output, rc.ParsePattern, packages, duration)
		return TestResult{
			AllPass:   true,
			Outcomes:  outcomes,
			TotalRun:  len(outcomes),
			RawOutput: output,
			Duration:  duration,
		}
	}

	failures := parseTestOutput(output, rc.ParsePattern)
	outcomes := buildOutcomesFromFailures(failures, packages, duration)
	buildFailed := len(failures) == 0
	return TestResult{
		AllPass:     false,
		BuildFailed: buildFailed,
		Failed:      failures,
		Outcomes:    outcomes,
		TotalFail:   len(failures),
		TotalRun:    len(outcomes),
		RawOutput:   output,
		Duration:    duration,
	}
}

// parseTestOutcomes extracts structured TestOutcome entries from test output.
// For passing runs, creates one outcome per detected test (via pass pattern)
// or a synthetic package-level outcome if no individual tests can be parsed.
func parseTestOutcomes(output string, patterns map[string]string, packages []string, total time.Duration) []TestOutcome {
	passPattern, ok := patterns["pass"]
	if ok {
		re, err := regexp.Compile(passPattern)
		if err == nil {
			var outcomes []TestOutcome
			for _, line := range strings.Split(output, "\n") {
				matches := re.FindStringSubmatch(line)
				if len(matches) >= 2 {
					pkg := ""
					if len(packages) > 0 {
						pkg = packages[0]
					}
					outcomes = append(outcomes, TestOutcome{
						Package:  pkg,
						TestName: strings.TrimSpace(matches[1]),
						Status:   "pass",
					})
				}
			}
			if len(outcomes) > 0 {
				return outcomes
			}
		}
	}

	// Fallback: if no individual tests can be parsed, create a synthetic
	// package-level outcome so baseline capture can proceed.
	if len(packages) > 0 {
		var outcomes []TestOutcome
		for _, pkg := range packages {
			outcomes = append(outcomes, TestOutcome{
				Package:  pkg,
				TestName: "_suite_",
				Status:   "pass",
				Duration: total,
			})
		}
		return outcomes
	}
	return []TestOutcome{{
		Package:  ".",
		TestName: "_suite_",
		Status:   "pass",
		Duration: total,
	}}
}

// buildOutcomesFromFailures creates TestOutcome entries from failure list,
// enabling baseline regression detection for non-Go projects.
func buildOutcomesFromFailures(failures []TestFailure, packages []string, total time.Duration) []TestOutcome {
	var outcomes []TestOutcome
	for _, f := range failures {
		pkg := f.Package
		if pkg == "" && len(packages) > 0 {
			pkg = packages[0]
		}
		outcomes = append(outcomes, TestOutcome{
			Package:  pkg,
			TestName: f.TestName,
			Status:   "fail",
			Duration: total / time.Duration(max(len(failures), 1)),
			Output:   f.Output,
		})
	}
	return outcomes
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func parseCompileOutput(output string, patterns map[string]string, baseDir string) []CompileError {
	errorPattern, ok := patterns["error"]
	if !ok {
		return []CompileError{{Path: "unknown", Message: strings.TrimSpace(output)}}
	}
	re, err := regexp.Compile(errorPattern)
	if err != nil {
		return []CompileError{{Path: "unknown", Message: strings.TrimSpace(output)}}
	}

	var errors []CompileError
	for _, line := range strings.Split(output, "\n") {
		matches := re.FindStringSubmatch(strings.TrimSpace(line))
		if len(matches) >= 4 {
			var lineNum, colNum int
			fmt.Sscanf(matches[2], "%d", &lineNum)
			if len(matches) >= 5 {
				fmt.Sscanf(matches[3], "%d", &colNum)
			}
			path := matches[1]
			msg := matches[len(matches)-1]
			if !filepath.IsAbs(path) {
				path = filepath.Join(baseDir, path)
			}
			errors = append(errors, CompileError{
				Path:    path,
				Line:    lineNum,
				Column:  colNum,
				Message: msg,
			})
		}
	}
	return errors
}

func parseTestOutput(output string, patterns map[string]string) []TestFailure {
	failPattern, ok := patterns["failure"]
	if !ok {
		return []TestFailure{{TestName: "unknown", Output: output}}
	}
	re, err := regexp.Compile(failPattern)
	if err != nil {
		return []TestFailure{{TestName: "unknown", Output: output}}
	}

	var failures []TestFailure
	for _, line := range strings.Split(output, "\n") {
		matches := re.FindStringSubmatch(line)
		if len(matches) >= 2 {
			failures = append(failures, TestFailure{
				TestName: strings.TrimSpace(matches[1]),
				Output:   line,
			})
		}
	}
	return failures
}
