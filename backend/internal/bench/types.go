// Package bench: self-contained coding-agent benchmark
// harness.  Pre-v1.0 wescode consumed types from
// {@code github.com/weisyn/wesgine/tests/bench} but that package has been
// slimmed to a MockCognitive-only microbench harness.  Case-based bench is
// wescode's concern, so the dataset / case / result / report / judge
// infrastructure now lives here.
package bench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/weisyn/wescode/internal/platform"

	"gopkg.in/yaml.v3"
)

// CaseType classifies a benchmark case's evaluation strategy.
type CaseType string

const (
	// CaseTypeSWE is a Software-Engineering case judged by running a
	// verify shell command (usually `go test ./...` or `pytest`).
	CaseTypeSWE CaseType = "swe"
	// CaseTypeTerminal is a shell-oriented case judged by inspecting
	// post-run workdir state via a verify command (e.g. `test -f foo`).
	CaseTypeTerminal CaseType = "terminal"
	// CaseTypeQnA is a knowledge / understanding case judged by an
	// external LLM judge (LLMJudgeFn).
	CaseTypeQnA CaseType = "qna"
)

// Case is a single benchmark scenario loaded from a YAML file in the
// dataset directory.
type Case struct {
	ID         string   `yaml:"id"          json:"id"`
	Type       CaseType `yaml:"type"        json:"type"`
	Title      string   `yaml:"title"       json:"title,omitempty"`
	Difficulty string   `yaml:"difficulty"  json:"difficulty,omitempty"`
	Prompt     string   `yaml:"prompt"      json:"prompt"`
	Setup      string   `yaml:"setup"       json:"setup,omitempty"`
	Fixture    string   `yaml:"fixture"     json:"fixture,omitempty"`
	VerifyCmd  string   `yaml:"verify_cmd"  json:"verify_cmd,omitempty"`
	GoldAnswer string   `yaml:"gold_answer" json:"gold_answer,omitempty"`
	TimeoutSec int      `yaml:"timeout"     json:"timeout,omitempty"`
	// TimeoutSecAlt handles wesgine dataset's "timeout_sec" key.
	TimeoutSecAlt int `yaml:"timeout_sec"  json:"-"`
	MaxTurns      int `yaml:"max_turns"    json:"max_turns,omitempty"`
	// SourceDir specifies a directory whose contents should be available
	// in the workdir. For QnA cases this is typically a source code repo
	// that the agent needs to read/search. Relative paths are resolved
	// against the YAML file's directory. Special value "$WESGINE" is
	// auto-resolved to the wesgine.git repo root.
	SourceDir string `yaml:"source_dir"   json:"source_dir,omitempty"`

	// SWE-bench fields (populated from SWE-bench Verified dataset)
	Repo           string `yaml:"repo"                     json:"repo,omitempty"`
	BaseCommit     string `yaml:"base_commit"              json:"base_commit,omitempty"`
	Version        string `yaml:"version"                  json:"version,omitempty"`
	GoldPatch      string `yaml:"gold_patch"               json:"gold_patch,omitempty"`
	TestPatch      string `yaml:"test_patch"               json:"test_patch,omitempty"`
	FailToPass     string `yaml:"fail_to_pass"             json:"fail_to_pass,omitempty"`
	PassToPass     string `yaml:"pass_to_pass"             json:"pass_to_pass,omitempty"`
	EnvSetupCommit string `yaml:"environment_setup_commit" json:"environment_setup_commit,omitempty"`

	sourcePath string
}

// DefaultTimeout returns the case's timeout in seconds, falling back to
// 300s (5 min) when unset.
func (c Case) DefaultTimeout() int {
	if c.TimeoutSec > 0 {
		return c.TimeoutSec
	}
	if c.TimeoutSecAlt > 0 {
		return c.TimeoutSecAlt
	}
	return 300
}

// FailureReason classifies why a case failed (empty = not failed or unclassified).
type FailureReason string

const (
	FailTimeout  FailureReason = "timeout"
	FailVerify   FailureReason = "verify_fail"
	FailRunError FailureReason = "run_error"
	FailSetup    FailureReason = "setup_error"
	FailNoJudge  FailureReason = "no_judge"
)

// Result is a single (case, run_index) tuple's outcome.
type Result struct {
	CaseID        string        `json:"case_id"`
	RunIndex      int           `json:"run_index"`
	Passed        bool          `json:"passed"`
	Score         float64       `json:"score"`
	WallTime      time.Duration `json:"wall_time"`
	Output        string        `json:"output,omitempty"`
	TokensIn      int           `json:"tokens_in"`
	TokensOut     int           `json:"tokens_out"`
	Error         string        `json:"error,omitempty"`
	FailureReason FailureReason `json:"failure_reason,omitempty"`
}

// Report aggregates the run's results and computed scores.
type Report struct {
	Agent          string    `json:"agent"`
	Model          string    `json:"model,omitempty"`
	Timestamp      time.Time `json:"timestamp"`
	RunsPerCase    int       `json:"runs_per_case"`
	Results        []Result  `json:"results"`
	SWEScore       float64   `json:"swe_score"`       // -1 = N/A (no SWE cases in run)
	TerminalScore  float64   `json:"terminal_score"`  // -1 = N/A
	QnAScore       float64   `json:"qna_score"`       // -1 = N/A
	CompositeScore float64   `json:"composite_score"` // arithmetic mean of the non-N/A category scores × 100

	AvgTokensPerCase int     `json:"avg_tokens_per_case"`
	AvgWallTimeSec   float64 `json:"avg_wall_time_sec"`
	TotalTokens      int     `json:"total_tokens"`

	// caseTypes maps CaseID → CaseType so ComputeScores can classify
	// Results into SWE / Terminal / QnA buckets.  Populated via
	// SetCaseTypes before ComputeScores; unexported so it does not
	// serialise through WriteJSON.
	caseTypes map[string]CaseType `json:"-"`
}

// ComputeScores populates SWEScore / TerminalScore / QnAScore /
// CompositeScore from Results.  Callers MUST invoke SetCaseTypes first
// so per-case type classification is available.  Categories with zero
// cases produce -1 (N/A); Composite is the mean of non-N/A categories
// scaled to 0-100.
func (r *Report) ComputeScores() {
	// Bucket per-run scores by CaseID, average within each case, then
	// average per-case scores within each type.
	perCase := map[string][]float64{}
	for _, res := range r.Results {
		perCase[res.CaseID] = append(perCase[res.CaseID], res.Score)
	}
	swe := []float64{}
	term := []float64{}
	qna := []float64{}
	for id, scores := range perCase {
		avg := mean(scores)
		switch r.caseTypeOf(id) {
		case CaseTypeSWE:
			swe = append(swe, avg)
		case CaseTypeTerminal:
			term = append(term, avg)
		case CaseTypeQnA:
			qna = append(qna, avg)
		}
	}
	r.SWEScore = categoryScore(swe)
	r.TerminalScore = categoryScore(term)
	r.QnAScore = categoryScore(qna)

	present := []float64{}
	for _, s := range []float64{r.SWEScore, r.TerminalScore, r.QnAScore} {
		if s >= 0 {
			present = append(present, s)
		}
	}
	if len(present) == 0 {
		r.CompositeScore = 0
	} else {
		r.CompositeScore = mean(present) * 100
	}

	// Efficiency metrics.
	totalTok := 0
	var totalWall time.Duration
	for _, res := range r.Results {
		totalTok += res.TokensIn + res.TokensOut
		totalWall += res.WallTime
	}
	r.TotalTokens = totalTok
	if n := len(r.Results); n > 0 {
		r.AvgTokensPerCase = totalTok / n
		r.AvgWallTimeSec = totalWall.Seconds() / float64(n)
	}
}

func categoryScore(vals []float64) float64 {
	if len(vals) == 0 {
		return -1
	}
	return mean(vals)
}

func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := 0.0
	for _, v := range vals {
		s += v
	}
	return s / float64(len(vals))
}

// SetCaseTypes registers case types before ComputeScores.  Callers pass
// the []Case they ran to preserve classification through the Report.
func (r *Report) SetCaseTypes(cases []Case) {
	if r == nil {
		return
	}
	r.caseTypes = make(map[string]CaseType, len(cases))
	for _, c := range cases {
		r.caseTypes[c.ID] = c.Type
	}
}

func (r *Report) caseTypeOf(id string) CaseType {
	if r == nil || r.caseTypes == nil {
		return ""
	}
	return r.caseTypes[id]
}

// PrintSummary writes a human-readable summary to w.
func (r *Report) PrintSummary(w io.Writer) {
	fmt.Fprintf(w, "\n=== Benchmark Report: %s ===\n", r.Agent)
	if r.Model != "" {
		fmt.Fprintf(w, "  Model:    %s\n", r.Model)
	}
	fmt.Fprintf(w, "  Runs:     %d per case\n", r.RunsPerCase)
	fmt.Fprintf(w, "  Cases:    %d results across %d unique cases\n",
		len(r.Results), r.uniqueCaseCount())
	fmt.Fprintf(w, "  Wallclk:  %v total\n", r.totalWallTime().Round(time.Second))
	fmt.Fprintf(w, "\n  Category scores:\n")
	printScore(w, "SWE (code fix)     ", r.SWEScore)
	printScore(w, "Terminal (CLI)     ", r.TerminalScore)
	printScore(w, "QnA (understanding)", r.QnAScore)
	fmt.Fprintf(w, "  Composite:           %.1f\n", r.CompositeScore)
	fmt.Fprintf(w, "\n  Efficiency:\n")
	fmt.Fprintf(w, "    Avg tokens/case:   %d\n", r.AvgTokensPerCase)
	fmt.Fprintf(w, "    Avg wall time:     %.1fs\n", r.AvgWallTimeSec)
	fmt.Fprintf(w, "    Total tokens:      %d\n", r.TotalTokens)

	// Failure breakdown.
	failCounts := map[FailureReason]int{}
	for _, res := range r.Results {
		if res.FailureReason != "" {
			failCounts[res.FailureReason]++
		}
	}
	if len(failCounts) > 0 {
		fmt.Fprintf(w, "\n  Failure breakdown:\n")
		for reason, count := range failCounts {
			fmt.Fprintf(w, "    %-18s %d\n", reason, count)
		}
	}
	fmt.Fprintf(w, "======================================\n\n")
}

func printScore(w io.Writer, label string, s float64) {
	if s < 0 {
		fmt.Fprintf(w, "    %s  N/A\n", label)
		return
	}
	fmt.Fprintf(w, "    %s  %.1f%%\n", label, s*100)
}

func (r *Report) uniqueCaseCount() int {
	seen := map[string]struct{}{}
	for _, res := range r.Results {
		seen[res.CaseID] = struct{}{}
	}
	return len(seen)
}

func (r *Report) totalWallTime() time.Duration {
	var t time.Duration
	for _, res := range r.Results {
		t += res.WallTime
	}
	return t
}

// WriteJSON serialises the report to w.  Internal indices (caseTypes)
// are dropped by design.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteSWEBenchPredictions writes a JSONL file compatible with
// swebench.harness.run_evaluation --predictions_path. Each line is a
// JSON object with instance_id, model_patch, and model_name_or_path.
// Only results with non-empty Output (the git diff) are written.
func (r *Report) WriteSWEBenchPredictions(w io.Writer, modelName string) error {
	enc := json.NewEncoder(w)
	for _, res := range r.Results {
		patch := strings.TrimSpace(res.Output)
		if patch == "" {
			patch = "# no changes"
		}
		if err := enc.Encode(map[string]string{
			"instance_id":        res.CaseID,
			"model_patch":        patch,
			"model_name_or_path": modelName,
		}); err != nil {
			return err
		}
	}
	return nil
}

// LLMJudgeFn evaluates a QnA answer against the reference.  Return
// (true, nil) for pass.
type LLMJudgeFn func(ctx context.Context, output, gold string) (bool, error)

// LoadDataset walks {@code dir} and returns every {@code *.yaml} /
// {@code *.yml} case it can decode.  Files that fail to decode are
// skipped with a warning; the caller sees an aggregate error listing
// them.  Cases are returned sorted by ID for deterministic ordering.
func LoadDataset(dir string) ([]Case, error) {
	var cases []Case
	var skipErrs []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := strings.ToLower(d.Name())
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			skipErrs = append(skipErrs, fmt.Sprintf("%s: %v", path, readErr))
			return nil
		}

		// Try YAML array first (wesgine agent/dataset format: top-level
		// list of cases in a single file). Fall back to single Case
		// (wescode example format: one case per file).
		var multi []Case
		if err := yaml.Unmarshal(data, &multi); err == nil && len(multi) > 0 && multi[0].ID != "" {
			for i := range multi {
				if multi[i].ID == "" || multi[i].Type == "" {
					continue
				}
				multi[i].sourcePath = path
				cases = append(cases, multi[i])
			}
			return nil
		}

		var c Case
		if err := yaml.Unmarshal(data, &c); err != nil {
			skipErrs = append(skipErrs, fmt.Sprintf("%s: %v", path, err))
			return nil
		}
		if c.ID == "" || c.Type == "" {
			skipErrs = append(skipErrs, fmt.Sprintf("%s: missing id / type", path))
			return nil
		}
		c.sourcePath = path
		cases = append(cases, c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	if len(skipErrs) > 0 {
		return cases, fmt.Errorf("bench: %d case file(s) skipped: %s",
			len(skipErrs), strings.Join(skipErrs, "; "))
	}
	return cases, nil
}

// FilterByID returns the first case matching {@code id}, wrapped in a
// slice.  Empty when no match.
func FilterByID(cases []Case, id string) []Case {
	for _, c := range cases {
		if c.ID == id {
			return []Case{c}
		}
	}
	return nil
}

// FilterByType returns every case whose Type equals {@code t}.
func FilterByType(cases []Case, t CaseType) []Case {
	out := make([]Case, 0, len(cases))
	for _, c := range cases {
		if c.Type == t {
			out = append(out, c)
		}
	}
	return out
}

// PrepareWorkdir creates or resolves the workdir for a bench case.
//
// For cases with SourceDir: uses the source directory directly as workdir
// (read-only QnA cases) — no temp dir created, caller must NOT RemoveAll.
//
// For cases with Fixture: creates a temp dir and seeds it from the fixture
// path (directory copy or .tar.gz extraction).
//
// For cases with neither: creates an empty temp dir + runs Setup script.
//
// Returns (workdir, shouldCleanup, error). When shouldCleanup is false,
// the caller must NOT os.RemoveAll the workdir (it's a real source tree).
func PrepareWorkdir(c Case) (workdir string, shouldCleanup bool, err error) {
	// SourceDir takes precedence: point workdir at the real source tree.
	if c.SourceDir != "" {
		resolved := resolveSourceDir(c)
		if resolved == "" {
			return "", false, fmt.Errorf("source_dir %q: could not resolve (for case %s)", c.SourceDir, c.ID)
		}
		info, statErr := os.Stat(resolved)
		if statErr != nil || !info.IsDir() {
			return "", false, fmt.Errorf("source_dir %q: %w", resolved, statErr)
		}
		return resolved, false, nil
	}

	tmpdir, mkErr := os.MkdirTemp("", "wescode-bench-"+sanitize(c.ID)+"-*")
	if mkErr != nil {
		return "", false, fmt.Errorf("mkdir temp: %w", mkErr)
	}
	if c.Fixture == "" {
		return tmpdir, true, nil
	}
	fixture := c.Fixture
	if !filepath.IsAbs(fixture) && c.sourcePath != "" {
		fixture = filepath.Join(filepath.Dir(c.sourcePath), fixture)
	}
	info, statErr := os.Stat(fixture)
	if statErr != nil {
		_ = os.RemoveAll(tmpdir)
		return "", false, fmt.Errorf("fixture %s: %w", fixture, statErr)
	}
	if info.IsDir() {
		if err := copyTree(fixture, tmpdir); err != nil {
			_ = os.RemoveAll(tmpdir)
			return "", false, err
		}
		return tmpdir, true, nil
	}
	if strings.HasSuffix(strings.ToLower(fixture), ".tar.gz") ||
		strings.HasSuffix(strings.ToLower(fixture), ".tgz") {
		cmd := exec.Command("tar", "xzf", fixture, "-C", tmpdir)
		if out, err := cmd.CombinedOutput(); err != nil {
			_ = os.RemoveAll(tmpdir)
			return "", false, fmt.Errorf("untar %s: %v: %s", fixture, err, string(out))
		}
		return tmpdir, true, nil
	}
	_ = os.RemoveAll(tmpdir)
	return "", false, fmt.Errorf("fixture %s: unsupported format (want dir / .tar.gz)", fixture)
}

// resolveSourceDir resolves the SourceDir field to an absolute path.
// Special value "$WESGINE" auto-resolves to the wesgine.git repo root
// relative to the dataset YAML file's location.
func resolveSourceDir(c Case) string {
	sd := c.SourceDir
	if sd == "$WESGINE" {
		if c.sourcePath != "" {
			// Walk up from dataset YAML to find wesgine.git root.
			// Dataset lives at wesgine/tests/bench/agent/dataset/qna/cases.yaml
			// So wesgine root is 5 levels up.
			dir := filepath.Dir(c.sourcePath)
			for i := 0; i < 10; i++ {
				candidate := filepath.Join(dir, "wesgine.go")
				if _, err := os.Stat(candidate); err == nil {
					return dir
				}
				parent := filepath.Dir(dir)
				if parent == dir {
					break
				}
				dir = parent
			}
		}
		// Fallback: try common relative paths from CWD.
		cwd, _ := os.Getwd()
		for _, rel := range []string{
			"../../wesgine.git",
			"../wesgine.git",
			"../../../wesgine.git",
		} {
			p := filepath.Join(cwd, rel)
			if _, err := os.Stat(filepath.Join(p, "wesgine.go")); err == nil {
				abs, _ := filepath.Abs(p)
				return abs
			}
		}
		return ""
	}
	if filepath.IsAbs(sd) {
		return sd
	}
	if c.sourcePath != "" {
		return filepath.Join(filepath.Dir(c.sourcePath), sd)
	}
	return sd
}

// RunSetup executes the case's {@code setup} script inside {@code
// workdir} through the host shell. Non-zero exit is returned as an error
// carrying combined output.
func RunSetup(ctx context.Context, workdir, script string) error {
	if strings.TrimSpace(script) == "" {
		return nil
	}
	cmd := platform.ShellCommand(ctx, script)
	cmd.Dir = workdir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("setup: %v: %s", err, string(out))
	}
	return nil
}

// JudgeByTest runs {@code verifyCmd} inside {@code workdir} through the host
// shell and returns {@code exit 0 == pass}.
func JudgeByTest(ctx context.Context, workdir, verifyCmd string) bool {
	return shellExitZero(ctx, workdir, verifyCmd)
}

// JudgeByState is functionally identical to JudgeByTest but expresses
// intent (checking post-run workdir shape instead of running a test
// suite).  Kept as a separate name so future divergence is cheap.
func JudgeByState(ctx context.Context, workdir, verifyCmd string) bool {
	return shellExitZero(ctx, workdir, verifyCmd)
}

// JudgeByLLM defers to the caller-supplied judge fn.  Judge errors are
// treated as fail (fail-closed).
func JudgeByLLM(ctx context.Context, output, gold string, fn LLMJudgeFn) bool {
	if fn == nil {
		return false
	}
	pass, err := fn(ctx, output, gold)
	if err != nil {
		return false
	}
	return pass
}

func shellExitZero(ctx context.Context, workdir, cmdStr string) bool {
	if strings.TrimSpace(cmdStr) == "" {
		return false
	}
	cmd := platform.ShellCommand(ctx, cmdStr)
	cmd.Dir = workdir
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, p)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(p, target)
	})
}

func copyFile(src, dst string) error {
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()
	info, err := sf.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	df, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer df.Close()
	if _, err := io.Copy(df, sf); err != nil {
		return err
	}
	return nil
}

func sanitize(id string) string {
	// keep ascii alphanumerics and hyphens; replace everything else with '_'.
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "case"
	}
	return b.String()
}

// ErrDatasetEmpty is returned by callers that expect at least one case.
var ErrDatasetEmpty = errors.New("bench: dataset directory contains no cases")
