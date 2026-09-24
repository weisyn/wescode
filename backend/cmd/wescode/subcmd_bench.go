package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	weisyn "github.com/weisyn/weisyn"
	"github.com/weisyn/wesapp/provider"
	"github.com/weisyn/wesapp/wire"
	"github.com/weisyn/wescode/internal/bench"
	"github.com/weisyn/wescode/internal/engine"
)

type benchOpts struct {
	datasetDir     string
	caseType       string
	caseID         string
	runs           int
	verbose        bool
	outputPath     string
	predictionsOut string
	forcePlan      bool
	thinkingLevel  string
	disableCKG     bool
}

// runBench implements `wescode bench --dataset DIR [--runs N] [--verbose]`.
// It boots a single engine.Service, loops over all cases in the dataset,
// and produces a report. Each case is driven through the same engine.RunChat
// that the VSCode RPC path uses — no separate binary, no bypass.
func runBench(ctx context.Context, args []string) error {
	opts := parseBenchArgs(args)

	provider.SetCatalog(weisyn.ProviderCatalog())

	cfg, err := engine.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cfg.BenchMode = true
	if opts.disableCKG {
		cfg.DisableCKG = true
	}

	cases, err := bench.LoadDataset(opts.datasetDir)
	if err != nil && len(cases) == 0 {
		return fmt.Errorf("load dataset: %w", err)
	}
	if opts.caseID != "" {
		cases = bench.FilterByID(cases, opts.caseID)
		if len(cases) == 0 {
			return fmt.Errorf("case %q not found", opts.caseID)
		}
	} else if opts.caseType != "" {
		cases = bench.FilterByType(cases, bench.CaseType(opts.caseType))
		if len(cases) == 0 {
			return fmt.Errorf("no cases of type %q", opts.caseType)
		}
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Boot one shared Service for all cases.
	svc := engine.NewService(cfg)
	bootDir, _ := os.MkdirTemp("", "wescode-bench-boot-*")
	if _, initErr := svc.Initialize(ctx, bootDir, []string{bootDir}); initErr != nil {
		os.RemoveAll(bootDir)
		return fmt.Errorf("boot: %w", initErr)
	}
	defer func() {
		time.Sleep(500 * time.Millisecond)
		svc.Close()
		os.RemoveAll(bootDir)
	}()

	logger.Info("bench: starting",
		"cases", len(cases), "runs_per_case", opts.runs, "dataset", opts.datasetDir)

	report := &bench.Report{
		Agent:       "wescode",
		Timestamp:   time.Now(),
		RunsPerCase: opts.runs,
	}
	if len(cfg.Providers) > 0 {
		report.Model = cfg.Providers[0].Model
	}
	report.SetCaseTypes(cases)

	total := len(cases) * opts.runs
	done := 0

	judgeFn := buildJudge(cfg)

	for _, c := range cases {
		for run := 0; run < opts.runs; run++ {
			done++
			logger.Info("bench: running",
				"case", c.ID, "run", run+1, "progress", fmt.Sprintf("%d/%d", done, total))

			result := runBenchCase(ctx, c, run, cfg, svc, opts, judgeFn)
			report.Results = append(report.Results, result)

			if opts.verbose {
				status := "FAIL"
				if result.Passed {
					status = "PASS"
				}
				attrs := []any{
					"case", c.ID, "run", run + 1, "status", status,
					"wall_time", result.WallTime.Round(time.Millisecond),
					"tokens", result.TokensIn + result.TokensOut,
				}
				if result.Error != "" {
					attrs = append(attrs, "error", result.Error)
				}
				if result.FailureReason != "" {
					attrs = append(attrs, "reason", result.FailureReason)
				}
				logger.Info("bench: result", attrs...)
			}
		}
	}

	report.ComputeScores()
	report.PrintSummary(os.Stdout)

	if opts.outputPath != "" {
		if err := writeJSON(report, opts.outputPath); err != nil {
			return err
		}
		logger.Info("bench: report written", "path", opts.outputPath)
	}
	if opts.predictionsOut != "" {
		modelName := "wescode"
		if report.Model != "" {
			modelName = "wescode-" + report.Model
		}
		if err := writePredictionsFile(report, opts.predictionsOut, modelName); err != nil {
			return err
		}
		logger.Info("bench: predictions written", "path", opts.predictionsOut)
	}

	return nil
}

func runBenchCase(ctx context.Context, c bench.Case, runIndex int, cfg engine.AppConfig, svc *engine.Service, opts benchOpts, judgeFn bench.LLMJudgeFn) bench.Result {
	startedAt := time.Now()
	result := bench.Result{
		CaseID:   c.ID,
		RunIndex: runIndex,
	}

	// QnA defaults to $WESGINE source dir.
	if c.Type == bench.CaseTypeQnA && c.SourceDir == "" && c.Setup == "" && c.Fixture == "" {
		c.SourceDir = "$WESGINE"
	}

	// SWE-bench cases: clone + worktree.
	if c.Repo != "" && c.BaseCommit != "" {
		wtDir, wtCleanup, wtErr := bench.PrepareSWEBenchWorkdir(ctx, c)
		if wtErr != nil {
			result.Error = fmt.Sprintf("swebench workdir: %v", wtErr)
			result.FailureReason = bench.FailSetup
			result.WallTime = time.Since(startedAt)
			return result
		}
		defer wtCleanup()
		return runBenchCaseSWE(ctx, c, runIndex, svc, wtDir, startedAt)
	}

	// Regular cases: PrepareWorkdir.
	workdir, shouldCleanup, prepErr := bench.PrepareWorkdir(c)
	if prepErr != nil {
		result.Error = fmt.Sprintf("prepare workdir: %v", prepErr)
		result.FailureReason = bench.FailSetup
		result.WallTime = time.Since(startedAt)
		return result
	}
	if shouldCleanup {
		defer os.RemoveAll(workdir)
	}

	if c.Setup != "" {
		if !shouldCleanup {
			result.Error = "setup rejected: workdir is a real source tree"
			result.FailureReason = bench.FailSetup
			result.WallTime = time.Since(startedAt)
			return result
		}
		if err := bench.RunSetup(ctx, workdir, c.Setup); err != nil {
			result.Error = fmt.Sprintf("setup: %v", err)
			result.FailureReason = bench.FailSetup
			result.WallTime = time.Since(startedAt)
			return result
		}
	}

	caseCtx, cancel := context.WithTimeout(ctx, time.Duration(c.DefaultTimeout())*time.Second)
	defer cancel()

	output, tokensIn, tokensOut, runErr := driveSingleChat(caseCtx, c, workdir, svc)
	result.Output = output
	result.TokensIn = tokensIn
	result.TokensOut = tokensOut
	if runErr != nil {
		result.Error = runErr.Error()
		if caseCtx.Err() != nil {
			result.FailureReason = bench.FailTimeout
		} else {
			result.FailureReason = bench.FailRunError
		}
	}

	switch c.Type {
	case bench.CaseTypeSWE:
		result.Passed = bench.JudgeByTest(ctx, workdir, c.VerifyCmd)
	case bench.CaseTypeTerminal:
		result.Passed = bench.JudgeByState(ctx, workdir, c.VerifyCmd)
	case bench.CaseTypeQnA:
		if judgeFn != nil {
			result.Passed = bench.JudgeByLLM(ctx, output, c.GoldAnswer, judgeFn)
		} else {
			result.FailureReason = bench.FailNoJudge
		}
	}
	if result.Passed {
		result.Score = 1.0
		result.FailureReason = ""
	} else if result.FailureReason == "" && runErr == nil {
		result.FailureReason = bench.FailVerify
	}
	result.WallTime = time.Since(startedAt)
	return result
}

func runBenchCaseSWE(ctx context.Context, c bench.Case, runIndex int, svc *engine.Service, workdir string, startedAt time.Time) bench.Result {
	result := bench.Result{
		CaseID:   c.ID,
		RunIndex: runIndex,
	}

	caseCtx, cancel := context.WithTimeout(ctx, time.Duration(c.DefaultTimeout())*time.Second)
	defer cancel()

	output, tokensIn, tokensOut, runErr := driveSingleChat(caseCtx, c, workdir, svc)
	result.Output = output
	result.TokensIn = tokensIn
	result.TokensOut = tokensOut
	if runErr != nil {
		result.Error = runErr.Error()
		if caseCtx.Err() != nil {
			result.FailureReason = bench.FailTimeout
		} else {
			result.FailureReason = bench.FailRunError
		}
	}

	if diff, err := extractDiff(caseCtx, workdir); err == nil && len(strings.TrimSpace(diff)) > 0 {
		result.Passed = true
		result.Score = 1.0
		result.Output = diff
	} else if result.FailureReason == "" {
		result.FailureReason = bench.FailVerify
	}

	result.WallTime = time.Since(startedAt)
	return result
}

// driveSingleChat runs a single Chat through the product's RunChat path.
func driveSingleChat(ctx context.Context, c bench.Case, workdir string, svc *engine.Service) (output string, tokensIn, tokensOut int, err error) {
	sessionID := fmt.Sprintf("bench-%s-%d", c.ID, time.Now().UnixMilli())

	chatOpts := engine.ChatOpts{
		WorkDir: workdir,
	}

	events, _, runErr := svc.RunChat(ctx, sessionID, "builtin-coder", c.Prompt, chatOpts)
	if runErr != nil {
		return "", 0, 0, fmt.Errorf("RunChat: %w", runErr)
	}

	var text strings.Builder
	var firstErr error
	res := wire.Pump(ctx, events, func(ev wire.Event) error {
		switch ev.Type {
		case wire.TextDelta:
			switch d := ev.Data.(type) {
			case string:
				text.WriteString(d)
			case interface{ GetText() string }:
				text.WriteString(d.GetText())
			}
		case wire.Error:
			if ed, ok := ev.Data.(map[string]any); ok {
				if rec, _ := ed["recoverable"].(bool); !rec && firstErr == nil {
					if msg, _ := ed["message"].(string); msg != "" {
						firstErr = fmt.Errorf("agent error: %s", msg)
					}
				}
			}
		}
		return nil
	})
	return text.String(), res.InputTokens, res.OutputTokens, firstErr
}

func extractDiff(ctx context.Context, workdir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff")
	cmd.Dir = workdir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func buildJudge(cfg engine.AppConfig) bench.LLMJudgeFn {
	if len(cfg.Providers) == 0 || cfg.Providers[0].APIKey == "" {
		return nil
	}
	p := cfg.Providers[0]
	return func(ctx context.Context, output, gold string) (bool, error) {
		prompt := fmt.Sprintf(
			"You are a benchmark judge. Compare the ANSWER against the REFERENCE.\n"+
				"Reply with exactly \"PASS\" if the answer captures the core meaning (exact wording not required).\n"+
				"Reply with exactly \"FAIL\" otherwise.\n\n"+
				"REFERENCE:\n%s\n\nANSWER:\n%s\n\nVerdict:", gold, output)

		body := map[string]any{
			"model":       p.Model,
			"messages":    []map[string]string{{"role": "user", "content": prompt}},
			"max_tokens":  16,
			"temperature": 0,
		}
		jsonBody, _ := json.Marshal(body)

		baseURL := p.BaseURL
		if baseURL == "" {
			baseURL = "https://api.deepseek.com"
		}
		url := strings.TrimRight(baseURL, "/") + "/v1/chat/completions"

		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
		if err != nil {
			return false, err
		}
		req.Header.Set("Content-Type", "application/json")
		if p.Type == "anthropic" {
			req.Header.Set("x-api-key", p.APIKey)
			req.Header.Set("anthropic-version", "2023-06-01")
		} else {
			req.Header.Set("Authorization", "Bearer "+p.APIKey)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			return false, fmt.Errorf("judge LLM HTTP %d: %s", resp.StatusCode, string(respBody))
		}

		var chatResp struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		verdict := string(respBody)
		if json.Unmarshal(respBody, &chatResp) == nil && len(chatResp.Choices) > 0 {
			verdict = chatResp.Choices[0].Message.Content
		}
		return strings.Contains(strings.ToUpper(verdict), "PASS"), nil
	}
}

func writeJSON(report *bench.Report, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return report.WriteJSON(f)
}

func writePredictionsFile(report *bench.Report, path, modelName string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return report.WriteSWEBenchPredictions(f, modelName)
}

func parseBenchArgs(args []string) benchOpts {
	opts := benchOpts{
		datasetDir: defaultBenchDataset(),
		runs:       1,
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dataset":
			i++
			if i < len(args) {
				opts.datasetDir = args[i]
			}
		case "--type":
			i++
			if i < len(args) {
				opts.caseType = args[i]
			}
		case "--case":
			i++
			if i < len(args) {
				opts.caseID = args[i]
			}
		case "--runs":
			i++
			if i < len(args) {
				fmt.Sscanf(args[i], "%d", &opts.runs)
			}
		case "--verbose", "-v":
			opts.verbose = true
		case "--output":
			i++
			if i < len(args) {
				opts.outputPath = args[i]
			}
		case "--predictions":
			i++
			if i < len(args) {
				opts.predictionsOut = args[i]
			}
		case "--force-plan":
			opts.forcePlan = true
		case "--disable-ckg":
			opts.disableCKG = true
		case "--thinking":
			i++
			if i < len(args) {
				opts.thinkingLevel = args[i]
			}
		case "--help", "-h":
			printBenchUsage()
			os.Exit(0)
		}
	}
	if opts.runs <= 0 {
		opts.runs = 1
	}
	return opts
}

func printBenchUsage() {
	fmt.Fprintf(os.Stderr, `wescode bench — run benchmark suite through the product engine

Usage:
  wescode bench [flags]

Flags:
  --dataset DIR        Dataset directory (default: auto-detect)
  --type TYPE          Filter: swe | terminal | qna
  --case ID            Run a single case by ID
  --runs N             Runs per case (default 1)
  --verbose, -v        Show per-case results
  --output PATH        Write JSON report
  --predictions PATH   Write SWE-bench predictions JSONL
  --disable-ckg        Disable CKG tools (measure Token Efficiency baseline)

Provider: uses the same provider configured in wescode settings.
         No environment variable fallback — configure in the product first.
`)
}

func defaultBenchDataset() string {
	// Priority: wesgine agent dataset (100 cases) > wescode smoke tests (3 cases)
	cwd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(cwd, "..", "wesgine", "tests", "bench", "agent", "dataset"),
		filepath.Join(cwd, "tests", "bench", "swebench"),
		filepath.Join(cwd, "tests", "bench", "dataset"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	return "tests/bench/dataset"
}
