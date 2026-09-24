package verification

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/weisyn/wescode/internal/platform"
	wesgine "github.com/weisyn/wesgine"
	"github.com/weisyn/wesgine/engine"
	"github.com/weisyn/wesgine/message"
	"github.com/weisyn/wesgine/tool"
)

// CodeQualityGate implements wesgine.QualityGate for programming scenarios.
// It runs compilation and test checks against the current disk state.
// Files are already written to disk by the edit/write tools — this gate
// performs pure verification with no side effects.
// SymbolFinder resolves symbol names to their definition locations.
// CodeIndex implements this; nil means index-driven hints are disabled.
type SymbolFinder interface {
	FindSymbol(ctx context.Context, name string) ([]SymbolResult, error)
}

// SymbolResult is a single symbol definition location (language-agnostic).
type SymbolResult struct {
	FilePath  string
	Name      string
	LineStart int
}

// ImportGraphQuerier provides reverse import lookups for dependency expansion.
type ImportGraphQuerier interface {
	ReverseImports(ctx context.Context, importPath string) ([]string, error)
	PackageOfFile(ctx context.Context, filePath string) (string, error)
}

// ProgressNotifier pushes verification status to the Extension UI (status bar).
type ProgressNotifier interface {
	Notify(phase string, detail string) error
}

// ReviewFunc performs L3 code review via wesgine Chat (single-turn, read-only).
// Returns review findings as text; empty string means no issues found.
type ReviewFunc func(ctx context.Context, diff string) (string, error)

// DiagnosticsFunc pulls diagnostics for a file from the language server.
// Returns compile-level errors (filtered to severity=Error by caller).
// Used when L1Method="lsp" to avoid full `go build`.
type DiagnosticsFunc func(ctx context.Context, path string) ([]CompileError, error)

// BaselineDiffResult represents the difference between pre-Run diagnostics and current state.
type BaselineDiffResult struct {
	NewErrors   []CompileError // AI-introduced (not in baseline)
	Preexisting []CompileError // Existed before this Run
	Resolved    int            // Count fixed by AI
}

// BaselineDiffFn queries the diagnostics baseline to separate AI-introduced
// errors from pre-existing project debt. Returns nil if no baseline is available.
type BaselineDiffFn func(files []string) *BaselineDiffResult

// LSPAutoFixResult reports what the LSP auto-fix pass accomplished.
type LSPAutoFixResult struct {
	Fixed     int // number of errors fixed via preferred code actions
	Remaining int // number of errors that still remain after auto-fix
}

// LSPAutoFixFn attempts to fix compile errors using LSP preferred code actions.
// Returns the number of errors fixed (zero-token repair path).
// This is called before failing L1, giving LSP a chance to fix trivial issues
// (missing imports, unused variables, etc.) without consuming model tokens.
type LSPAutoFixFn func(ctx context.Context, errors []CompileError) *LSPAutoFixResult

// FailureCorrelator maps test failures to specific modified functions.
// Returns enriched hints like "TestCreateUser failed because you changed Create() in service.go".
type FailureCorrelator interface {
	// CorrelateFailures takes failing tests and modified files, returns correlation hints.
	Correlate(ctx context.Context, failures []TestFailure, modifiedFiles []string, workDir string) []string
}

// TestFailureHookFn is called when L2 tests fail, allowing the caller
// to push failure details into DebugEventStore for overlay injection.
type TestFailureHookFn func(failures []TestFailure)

type CodeQualityGate struct {
	cfg               Config
	mu                sync.RWMutex
	workDir           string
	modifiedFilesFn   func() []string
	taskType          string
	forceL2           bool
	symbolFinder      SymbolFinder
	importGraph       ImportGraphQuerier
	progress          ProgressNotifier
	reviewFn          ReviewFunc
	metrics           *VerificationMetrics
	diagnosticsFn     DiagnosticsFunc
	diagnosticsPush   func(errors []CompileError)
	correlator        FailureCorrelator
	runner            LanguageRunner
	shellProvider     tool.ShellProvider
	testFailureHook   TestFailureHookFn
	regressionLearnFn func(regressions []RegressionInfo)
	baselineStore     *BaselineStore
	gitCommitFn       func() string
	testSelector      *FunctionTestSelector
	baselineDiffFn    BaselineDiffFn
	lspAutoFixFn      LSPAutoFixFn
}

var _ wesgine.QualityGate = (*CodeQualityGate)(nil)

// NewCodeQualityGate creates a quality gate for the given work directory.
// sp is the ShellProvider for running compile/test commands (typically from Host.Shell()).
func NewCodeQualityGate(cfg Config, workDir string, sp tool.ShellProvider) *CodeQualityGate {
	if sp == nil {
		sp = &tool.LocalShellProvider{}
	}
	return &CodeQualityGate{
		cfg:           cfg,
		workDir:       workDir,
		metrics:       NewVerificationMetrics(),
		runner:        SelectRunner(workDir, sp),
		shellProvider: sp,
	}
}

func (g *CodeQualityGate) sp() tool.ShellProvider {
	if g.shellProvider != nil {
		return g.shellProvider
	}
	return &tool.LocalShellProvider{}
}

// MetricsSnapshot returns a point-in-time copy of all verification counters.
func (g *CodeQualityGate) MetricsSnapshot() VerificationStatsSnapshot {
	return g.metrics.Snapshot()
}

// SetWorkDir updates the working directory used for compilation and testing.
func (g *CodeQualityGate) SetWorkDir(dir string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.workDir = dir
}

// SetTestTimeout updates the L2 test timeout.
func (g *CodeQualityGate) SetTestTimeout(seconds int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cfg.L2Timeout = seconds
}

// SetModifiedFilesFn injects a function that returns the list of files
// modified during the current Run. This replaces extractModifiedFromTurn
// which only sees the terminal turn's tool_use blocks (typically empty).
func (g *CodeQualityGate) SetModifiedFilesFn(fn func() []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.modifiedFilesFn = fn
}

// SetTaskType injects the current task classification (e.g. "fix_bug").
// Called by the engine layer after intent classification.
func (g *CodeQualityGate) SetTaskType(tt string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.taskType = tt
}

// ResetTaskType clears the task type at the start of each Run.
func (g *CodeQualityGate) ResetTaskType() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.taskType = ""
}

// SetForceL2 forces L2 test execution on the next Evaluate call.
// Used by /test command handling in the engine layer.
func (g *CodeQualityGate) SetForceL2(force bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.forceL2 = force
}

// SetSymbolFinder injects a symbol index for enhanced hint generation.
// nil disables index-driven hints (falls back to pattern matching).
func (g *CodeQualityGate) SetSymbolFinder(sf SymbolFinder) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.symbolFinder = sf
}

// SetImportGraph injects an import graph querier for dependency expansion.
// nil disables expansion (only direct packages are tested).
func (g *CodeQualityGate) SetImportGraph(ig ImportGraphQuerier) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.importGraph = ig
}

// SetProgressNotifier injects a notifier for verification status updates.
// The Extension status bar displays "Building...", "Testing...", etc.
func (g *CodeQualityGate) SetProgressNotifier(p ProgressNotifier) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.progress = p
}

// SetReviewFn injects the L3 review function (wesgine Chat single-turn).
// nil disables L3 review.
func (g *CodeQualityGate) SetReviewFn(fn ReviewFunc) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reviewFn = fn
}

// SetDiagnosticsFn injects an LSP diagnostics function for L1 verification.
// When set and L1Method="lsp", file-level diagnostics replace `go build`.
func (g *CodeQualityGate) SetDiagnosticsFn(fn DiagnosticsFunc) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.diagnosticsFn = fn
}

// SetDiagnosticsPush injects a callback to push compile errors to the IDE
// Problems panel via RPC notification. Called after L1 compile completes.
func (g *CodeQualityGate) SetDiagnosticsPush(fn func(errors []CompileError)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.diagnosticsPush = fn
}

// SetFailureCorrelator injects function-level test failure correlation.
func (g *CodeQualityGate) SetFailureCorrelator(c FailureCorrelator) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.correlator = c
}

// SetTestSelector injects function-level test narrowing. When set, L2
// attempts to run only tests correlated to modified functions rather than
// all tests in the affected packages.
func (g *CodeQualityGate) SetTestSelector(s *FunctionTestSelector) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.testSelector = s
}

// SetTestFailureHook injects a callback invoked when L2 tests fail.
// Used to push test failures into DebugEventStore for overlay injection.
func (g *CodeQualityGate) SetTestFailureHook(fn TestFailureHookFn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.testFailureHook = fn
}

// SetRegressionLearnFn injects a callback invoked when L2.5 detects real
// regressions. Used to persist regression information as Memory L4 correction
// entries for the learning feedback loop (CSP framework section 5.2).
func (g *CodeQualityGate) SetRegressionLearnFn(fn func(regressions []RegressionInfo)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.regressionLearnFn = fn
}

// SetBaselineStore injects the behavioral baseline store for L2.5 regression detection.
// nil disables regression detection (L2 behavior unchanged).
func (g *CodeQualityGate) SetBaselineStore(bs *BaselineStore) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.baselineStore = bs
}

// SetGitCommitFn injects a function that returns the current git HEAD commit hash.
// Used to tag baseline runs for traceability. nil = no git commit recorded.
func (g *CodeQualityGate) SetGitCommitFn(fn func() string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gitCommitFn = fn
}

// SetBaselineDiffFn injects a function that computes the diagnostics baseline diff.
// When set, L1 only reports AI-introduced errors to the model for repair.
func (g *CodeQualityGate) SetBaselineDiffFn(fn BaselineDiffFn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.baselineDiffFn = fn
}

// SetLSPAutoFixFn injects a function that attempts to fix errors using LSP
// preferred code actions (auto-import, remove unused, etc.).
// When set, L1 tries LSP auto-fix before reporting errors to the model.
func (g *CodeQualityGate) SetLSPAutoFixFn(fn LSPAutoFixFn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lspAutoFixFn = fn
}

// BaselineReadiness returns the behavioral baseline readiness for prompt injection.
// Returns nil if no baseline store is configured.
func (g *CodeQualityGate) BaselineReadiness(ctx context.Context, workDir string) *BaselineReadiness {
	g.mu.RLock()
	bs := g.baselineStore
	g.mu.RUnlock()
	if bs == nil {
		return nil
	}
	r := bs.Readiness(ctx, WorkDirHash(workDir))
	return &r
}

// Metrics returns the verification statistics collector.
func (g *CodeQualityGate) Metrics() *VerificationMetrics {
	return g.metrics
}

// BaselineStore returns the behavioral baseline store (may be nil).
func (g *CodeQualityGate) BaselineStore() *BaselineStore {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.baselineStore
}

// Evaluate implements wesgine.QualityGate.
// Pure verification: reads disk state, runs go build / go test, no side effects.
func (g *CodeQualityGate) Evaluate(
	ctx context.Context,
	state engine.SessionState,
	result engine.TurnResult,
	ec wesgine.QualityEvaluateContext,
) (wesgine.QualityVerdict, error) {
	g.mu.RLock()
	workDir := g.workDir
	fn := g.modifiedFilesFn
	symbolFinder := g.symbolFinder
	importGraph := g.importGraph
	progress := g.progress
	reviewFn := g.reviewFn
	diagnosticsFn := g.diagnosticsFn
	correlator := g.correlator
	testFailureHook := g.testFailureHook
	regressionLearnFn := g.regressionLearnFn
	baselineStore := g.baselineStore
	gitCommitFn := g.gitCommitFn
	testSelector := g.testSelector
	baselineDiffFn := g.baselineDiffFn
	lspAutoFixFn := g.lspAutoFixFn
	totalRevCap := g.cfg.MaxTotalRevisions
	g.mu.RUnlock()

	failVerdict := func(criteria string, maxRev int, feedback message.Message) wesgine.QualityVerdict {
		return wesgine.QualityVerdict{
			Pass:              false,
			Criteria:          criteria,
			MaxRevisions:      maxRev,
			TotalRevisionsCap: totalRevCap,
			Feedback:          feedback,
		}
	}

	var modifiedFiles []string
	if fn != nil {
		modifiedFiles = fn()
	}
	if len(modifiedFiles) == 0 {
		return wesgine.QualityVerdict{Pass: true}, nil
	}

	g.metrics.RevisionAttempts.Add(1)

	effectiveWorkDir := inferProjectRoot(modifiedFiles, workDir)
	slog.Info("[verification] Evaluate", "modifiedFiles", modifiedFiles, "workDir", workDir, "effectiveWorkDir", effectiveWorkDir)
	workDir = effectiveWorkDir

	// L1: Compilation / type checking
	if g.cfg.L1Compile {
		g.notifyProgress(progress, "l1", "Building...")
		g.metrics.L1Triggered.Add(1)

		var compileErrors []CompileError

		if g.cfg.L1Method == "lsp" && diagnosticsFn != nil {
			slog.Info("[verification] L1 via LSP", "files", len(modifiedFiles))
			compileErrors = g.runLSPDiagnostics(ctx, modifiedFiles, diagnosticsFn)
		} else {
			runner := g.runner
			if runner == nil || workDir != g.workDir {
				runner = SelectRunner(workDir, g.sp())
			}
			if runner == nil {
				runner = NullRunner{}
			}
			slog.Info("[verification] L1 compile", "runner", runner.Name(), "workDir", workDir)
			compileCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			compileResult := runner.Compile(compileCtx, workDir)
			cancel()
			slog.Info("[verification] L1 result", "success", compileResult.Success, "errors", len(compileResult.Errors), "duration", compileResult.Duration)
			if !compileResult.Success {
				compileErrors = compileResult.Errors
			}
		}

		// Push diagnostics to IDE Problems panel (best-effort)
		g.mu.RLock()
		pushFn := g.diagnosticsPush
		g.mu.RUnlock()
		if pushFn != nil {
			pushFn(compileErrors) // empty slice clears previous diagnostics
		}

		if len(compileErrors) > 0 {
			// Zero-token repair: try LSP preferred code actions before failing.
			if lspAutoFixFn != nil {
				fixResult := lspAutoFixFn(ctx, compileErrors)
				if fixResult != nil && fixResult.Fixed > 0 {
					slog.Info("[verification] L1 LSP auto-fix applied",
						"fixed", fixResult.Fixed, "remaining", fixResult.Remaining)
					if fixResult.Remaining == 0 {
						g.metrics.L1Passed.Add(1)
						goto l2
					}
					// Re-check after auto-fix (errors may have shifted)
					if g.cfg.L1Method == "lsp" && diagnosticsFn != nil {
						compileErrors = g.runLSPDiagnostics(ctx, modifiedFiles, diagnosticsFn)
					} else {
						runner := g.runner
						if runner == nil || workDir != g.workDir {
							runner = SelectRunner(workDir, g.sp())
						}
						if runner == nil {
							runner = NullRunner{}
						}
						reCtx, reCancel := context.WithTimeout(ctx, 30*time.Second)
						reResult := runner.Compile(reCtx, workDir)
						reCancel()
						if reResult.Success {
							g.metrics.L1Passed.Add(1)
							goto l2
						}
						compileErrors = reResult.Errors
					}
					if len(compileErrors) == 0 {
						g.metrics.L1Passed.Add(1)
						goto l2
					}
				}
			}

			// Use baseline diff to separate AI-introduced errors from pre-existing debt.
			errorsToReport := compileErrors
			if baselineDiffFn != nil {
				diffResult := baselineDiffFn(modifiedFiles)
				if diffResult != nil && len(diffResult.NewErrors) > 0 {
					errorsToReport = diffResult.NewErrors
					slog.Info("[verification] L1 baseline diff",
						"total_errors", len(compileErrors),
						"new_errors", len(diffResult.NewErrors),
						"preexisting", len(diffResult.Preexisting),
						"resolved", diffResult.Resolved)
				} else if diffResult != nil && len(diffResult.NewErrors) == 0 {
					// All errors are pre-existing — AI didn't introduce any new ones.
					slog.Info("[verification] L1 all errors pre-existing, passing",
						"preexisting", len(diffResult.Preexisting), "resolved", diffResult.Resolved)
					g.metrics.L1Passed.Add(1)
					goto l2
				}
			}

			g.metrics.L1Failed.Add(1)
			g.notifyProgress(progress, "l1", "Build failed")

			feedback := FormatCompileErrors(errorsToReport)
			hint := FormatHintWithIndex(ctx, errorsToReport, symbolFinder)
			if hint != "" {
				feedback.Content = append(feedback.Content,
					message.ContentBlock{Type: "text", Text: "\n" + hint})
			}
			return failVerdict("compilation", g.cfg.MaxRevisions.Compile, feedback), nil
		}
		g.metrics.L1Passed.Add(1)
	}

l2:
	if g.cfg.BenchMode {
		g.metrics.RevisionSuccess.Add(1)
		return wesgine.QualityVerdict{Pass: true}, nil
	}

	// L2: Test execution
	if g.shouldRunTests(modifiedFiles) {
		g.notifyProgress(progress, "l2", "Testing...")
		g.metrics.L2Triggered.Add(1)

		timeout := time.Duration(g.cfg.L2Timeout) * time.Second
		if timeout <= 0 {
			timeout = 60 * time.Second
		}

		runner := g.runner
		if runner == nil || workDir != g.workDir {
			runner = SelectRunner(workDir, g.sp())
		}
		if runner == nil {
			runner = NullRunner{}
		}

		var packages []string
		if g.cfg.L2ExpandDeps && importGraph != nil {
			packages = ExpandByImportGraph(ctx, modifiedFiles, workDir, importGraph)
		} else {
			packages = AffectedPackages(modifiedFiles, workDir)
		}

		// INV: cross-repo packages must not run from this workspace root —
		// `go test ./../sibling/...` fails instantly ("directory outside main
		// module", ~15ms) and the failure was previously swallowed by
		// fail-degraded, telling the model tests passed when nothing ran.
		packages = FilterSameRepoPackages(packages, workDir)

		// Function-level test narrowing: when a selector is available and
		// produces filters, narrow the packages and produce a single -run
		// regex. The regex is passed as a SEPARATE argument — interleaving
		// "-run <regex>" into the packages list produced an invalid go test
		// argv (`go test pkgA -run rA pkgB -run rB`) that failed the whole
		// invocation.
		runSelector := ""
		if testSelector != nil {
			if filters := testSelector.SelectTests(ctx, modifiedFiles, workDir); len(filters) > 0 {
				pkgSet := make(map[string]struct{}, len(filters))
				var regexParts []string
				for _, f := range filters {
					if isCrossRepoPackage(f.Package) {
						continue
					}
					pkgSet[f.Package] = struct{}{}
					if regex := BuildRunRegex(f.TestNames); regex != "" {
						regexParts = append(regexParts, regex)
					}
				}
				if len(pkgSet) > 0 {
					narrowed := make([]string, 0, len(pkgSet))
					for p := range pkgSet {
						narrowed = append(narrowed, p)
					}
					sort.Strings(narrowed)
					if len(regexParts) > 0 {
						runSelector = strings.Join(regexParts, "|")
					}
					slog.Info("[verification] L2 narrowed by test selector",
						"original_packages", len(packages), "packages", narrowed,
						"run_selector", runSelector)
					packages = narrowed
				}
			}
		}

		testCtx, cancel := context.WithTimeout(ctx, timeout)
		slog.Info("[verification] L2 test", "runner", runner.Name(), "packages", packages, "run_selector", runSelector, "expanded", g.cfg.L2ExpandDeps)
		testResult := runner.Test(testCtx, workDir, packages, runSelector, timeout)
		cancel()
		slog.Info("[verification] L2 result", "allPass", testResult.AllPass, "buildFailed", testResult.BuildFailed, "failures", len(testResult.Failed), "duration", testResult.Duration)

		if testResult.BuildFailed {
			// exitCode != 0 but no test function failed: the test binary
			// failed to compile. This is a BUILD error, not a test error.
			// Route to "compilation" criteria so it doesn't burn a separate
			// "tests" revision counter for an issue the agent already sees
			// as a compile problem (P0-3 fix).
			compileErrors := extractCompileErrorsFromRawOutput(testResult.RawOutput, workDir)
			if len(compileErrors) == 0 {
				// Can't extract actionable errors → fail-degraded: pass.
				// Blocking the agent with empty feedback causes infinite
				// revision loops (swe-001 root cause).
				slog.Warn("[verification] L2 build failed but cannot extract errors — passing (fail-degraded)",
					"rawOutput_len", len(testResult.RawOutput))
				g.metrics.L2Passed.Add(1)
				goto afterL2
			}
			g.metrics.L1Failed.Add(1)
			g.notifyProgress(progress, "l1", "Test build failed")
			feedback := FormatCompileErrors(compileErrors)
			return failVerdict("compilation", g.cfg.MaxRevisions.Compile, feedback), nil
		}

		if !testResult.AllPass {
			// L2.5: Regression baseline detection (INV-CSE-04: no LLM calls).
			// Distinguishes "new regression" from "pre-existing failure".
			if baselineStore != nil && g.cfg.L25Regression != "off" {
				g.metrics.L25Triggered.Add(1)
				workDirHash := WorkDirHash(workDir)
				regressions, regErr := DetectRegressions(ctx, baselineStore, workDirHash, testResult.Failed)
				if regErr != nil {
					slog.Error("[verification] L2.5 regression detection failed", "error", regErr)
				}
				realRegressions := FilterNonFlaky(regressions)

				flakyCount := len(regressions) - len(realRegressions)
				if flakyCount > 0 {
					g.metrics.FlakyDetected.Add(int64(flakyCount))
				}

				if len(realRegressions) > 0 {
					g.metrics.L25Failed.Add(1)
					g.notifyProgress(progress, "l25", "Regression detected")
					slog.Info("[verification] L2.5 regression", "regressions", len(realRegressions), "flaky", flakyCount)

					if testFailureHook != nil {
						testFailureHook(testResult.Failed)
					}
					if regressionLearnFn != nil {
						regressionLearnFn(realRegressions)
					}

					feedback := FormatRegressionFeedback(realRegressions, testResult)
					if correlator != nil {
						if hints := correlator.Correlate(ctx, testResult.Failed, modifiedFiles, workDir); len(hints) > 0 {
							feedback.Content = append(feedback.Content,
								message.ContentBlock{Type: "text", Text: "\n" + strings.Join(hints, "\n")})
						}
					}

					return failVerdict("regression", g.cfg.MaxRevisions.Regression, feedback), nil
				}
				g.metrics.L25Passed.Add(1)
			}

			g.metrics.L2Failed.Add(1)
			g.notifyProgress(progress, "l2", "Tests failed")

			feedback := FormatTestFailures(testResult)

			if correlator != nil {
				if hints := correlator.Correlate(ctx, testResult.Failed, modifiedFiles, workDir); len(hints) > 0 {
					feedback.Content = append(feedback.Content,
						message.ContentBlock{Type: "text", Text: "\n" + strings.Join(hints, "\n")})
				}
			} else if related := RelatedChangeHint(testResult.Failed, modifiedFiles, workDir); related != "" {
				feedback.Content = append(feedback.Content,
					message.ContentBlock{Type: "text", Text: related})
			}

			if testFailureHook != nil {
				testFailureHook(testResult.Failed)
			}

			return failVerdict("tests", g.cfg.MaxRevisions.Test, feedback), nil
		}

		// L2.5 behavioral regression: tests pass but output may have changed.
		// Compare output hashes before capturing the new baseline.
		if baselineStore != nil && g.cfg.L25Regression != "off" && len(testResult.Outcomes) > 0 {
			workDirHash := WorkDirHash(workDir)
			flakyList := LoadFlakyList(workDir)
			outputRegs, brErr := DetectBehavioralRegressions(ctx, baselineStore, workDirHash, testResult.Outcomes, flakyList)
			if brErr != nil {
				slog.Error("[verification] L2.5 behavioral regression detection failed", "error", brErr)
			}
			if len(outputRegs) > 0 {
				hasCritical := false
				for _, r := range outputRegs {
					if r.Severity == "critical" {
						hasCritical = true
						break
					}
				}
				slog.Info("[verification] L2.5 behavioral regression",
					"total", len(outputRegs), "has_critical", hasCritical)

				if hasCritical {
					g.metrics.L25Failed.Add(1)
					g.notifyProgress(progress, "l25", "Behavioral regression detected")
					feedback := FormatBehavioralRegressionFeedback(outputRegs)
					return failVerdict("regression", g.cfg.MaxRevisions.Regression, feedback), nil
				}
				// Non-critical (warning/perf): log but don't block.
				g.metrics.L25Passed.Add(1)
			}
		}

		// L2 全部通过 — capture baseline (INV-BL-01).
		// Only save when we have structured outcomes (go test -json);
		// non-Go runners that don't populate Outcomes skip baseline capture.
		if baselineStore != nil && len(testResult.Outcomes) > 0 {
			workDirHash := WorkDirHash(workDir)
			gitCommit := ""
			if gitCommitFn != nil {
				gitCommit = gitCommitFn()
			}
			scope := "full"
			if testSelector != nil {
				if filters := testSelector.SelectTests(ctx, modifiedFiles, workDir); len(filters) > 0 {
					scope = "selective"
				}
			}
			if _, err := baselineStore.SaveRun(ctx, workDirHash, workDir, packages, gitCommit, scope, testResult.Outcomes); err != nil {
				slog.Warn("[verification] baseline capture failed", "error", err)
			} else {
				g.metrics.BaselineCaptured.Add(1)
				slog.Info("[verification] baseline captured", "workDir", workDir, "tests", testResult.TotalRun)
			}
		}
		g.metrics.L2Passed.Add(1)
	}

afterL2:
	// L3: Code review (optional, async via wesgine Chat)
	if reviewFn != nil && g.cfg.L3Review == "auto" {
		g.notifyProgress(progress, "l3", "Reviewing...")
		g.metrics.L3Triggered.Add(1)

		reviewCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		findings, err := reviewFn(reviewCtx, formatDiffSummary(modifiedFiles))
		cancel()

		if err != nil {
			slog.Warn("[verification] L3 review error (non-fatal)", "error", err)
		} else if findings != "" {
			g.metrics.L3Failed.Add(1)
			g.notifyProgress(progress, "l3", "Review issues found")
			return failVerdict("code_review", g.cfg.MaxRevisions.Review, message.Message{
				Role:    message.RoleUser,
				Content: []message.ContentBlock{message.NewTextBlock("❌ Code review findings:\n" + findings)},
			}), nil
		} else {
			g.metrics.L3Passed.Add(1)
		}
	}

	g.metrics.RevisionSuccess.Add(1)
	g.notifyProgress(progress, "done", "Verification passed")
	return wesgine.QualityVerdict{Pass: true}, nil
}

func (g *CodeQualityGate) runLSPDiagnostics(ctx context.Context, files []string, fn DiagnosticsFunc) []CompileError {
	var allErrors []CompileError
	for _, f := range files {
		diagCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		errs, err := fn(diagCtx, f)
		cancel()
		if err != nil {
			slog.Debug("[verification] LSP diagnostics failed for file", "path", f, "error", err)
			continue
		}
		allErrors = append(allErrors, errs...)
	}
	return allErrors
}

func (g *CodeQualityGate) notifyProgress(p ProgressNotifier, phase, detail string) {
	if p == nil {
		return
	}
	_ = p.Notify(phase, detail)
}

// inferProjectRoot finds the best project root for verification by walking up
// from the first modified file looking for go.mod, package.json, or Cargo.toml.
// Falls back to defaultWorkDir if no project marker is found.
func inferProjectRoot(modifiedFiles []string, defaultWorkDir string) string {
	if len(modifiedFiles) == 0 {
		return defaultWorkDir
	}
	candidate := filepath.Dir(modifiedFiles[0])
	markers := []string{"go.mod", "package.json", "Cargo.toml", "pom.xml", "Makefile"}
	for {
		for _, m := range markers {
			if _, err := os.Stat(filepath.Join(candidate, m)); err == nil {
				return candidate
			}
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			break
		}
		candidate = parent
	}
	return defaultWorkDir
}

func formatDiffSummary(files []string) string {
	if len(files) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Modified files:\n")
	for _, f := range files {
		sb.WriteString("  - " + f + "\n")
	}
	return sb.String()
}

// resolveBuildDir walks up from workDir looking for go.mod.
func resolveBuildDir(dir string) string {
	candidates := []string{dir}
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				sub := filepath.Join(dir, e.Name())
				if _, err := os.Stat(filepath.Join(sub, "go.mod")); err == nil {
					candidates = append(candidates, sub)
				}
			}
		}
	}
	for _, d := range candidates {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	return dir
}

func (g *CodeQualityGate) shouldRunTests(files []string) bool {
	switch g.cfg.L2Test {
	case "always":
		return true
	case "never":
		return false
	default:
		if g.forceL2 {
			return true
		}
		if g.taskType == "fix_bug" {
			return true
		}
		return HasTestFile(files) || HasCorrespondingTestFile(files)
	}
}

// extractCompileErrorsFromRawOutput parses Go compiler errors from `go test`
// raw output when the test binary failed to build (BuildFailed=true).
// Returns nil if no structured errors can be extracted.
func extractCompileErrorsFromRawOutput(rawOutput string, workDir string) []CompileError {
	var errors []CompileError
	for _, line := range strings.Split(rawOutput, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		path, lineNum, colNum, msg, ok := platform.ParseFileRef(line)
		if !ok || lineNum <= 0 {
			continue
		}
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		msg = strings.TrimSpace(msg)
		if msg == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(workDir, path)
		}
		errors = append(errors, CompileError{
			Path:    path,
			Line:    lineNum,
			Column:  colNum,
			Message: msg,
		})
	}
	return errors
}
