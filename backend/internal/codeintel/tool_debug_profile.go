package codeintel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/weisyn/wescode/internal/platform"
	"github.com/weisyn/wesgine/tool"
)

// --- run_profile (Extended) ---

func NewRunProfileTool() tool.Tool {
	return &runProfileTool{
		BaseTool: tool.BaseTool{Meta: tool.ToolMeta{
			Dimension: tool.DimensionAct, ReadOnly: false, Concurrent: false,
			Timeout: 120 * time.Second, Risk: tool.RiskModerate, Enabled: true,
			Domain: "debug", DomainToolTier: tool.DomainTierExtended,
		}},
	}
}

type runProfileTool struct{ tool.BaseTool }

func (t *runProfileTool) Name() string { return "run_profile" }
func (t *runProfileTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "run_profile",
		Description: "Run a profiling command to collect CPU or memory performance data. For Go: uses go test -cpuprofile/-memprofile. For Python: uses cProfile. Returns the profile output file path for analysis with analyze_profile.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"language":{"type":"string","enum":["go","python","node"],"description":"Programming language/runtime"},"kind":{"type":"string","enum":["cpu","memory","trace"],"description":"Type of profiling (default: cpu)"},"target":{"type":"string","description":"Package path, script, or command to profile"},"duration":{"type":"integer","description":"Profile duration in seconds (default: 10)"}},"required":["language","target"]}`),
	}
}

func (t *runProfileTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。本工具不产出列表（输出形态不是可跳转条目），
	// 传 nil 得到 category=text + 分级——分级本身仍是信息（PC-02）。
	var notReady bool
	defer func() { res = PresentList(res, notReady, nil) }()

	var args struct {
		Language string `json:"language"`
		Kind     string `json:"kind"`
		Target   string `json:"target"`
		Duration int    `json:"duration"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return &tool.ToolResult{Content: "Invalid arguments: " + err.Error(), IsError: true}, nil
	}
	if args.Kind == "" {
		args.Kind = "cpu"
	}
	if args.Duration <= 0 {
		args.Duration = 10
	}

	var argv []string
	var profilePath string

	switch args.Language {
	case "go":
		switch args.Kind {
		case "cpu":
			profilePath = filepath.Join(os.TempDir(), "wescode-cpu.prof")
			argv = []string{"go", "test", args.Target, "-cpuprofile=" + profilePath, "-count=1", fmt.Sprintf("-timeout=%ds", args.Duration+30)}
		case "memory":
			profilePath = filepath.Join(os.TempDir(), "wescode-mem.prof")
			argv = []string{"go", "test", args.Target, "-memprofile=" + profilePath, "-count=1", fmt.Sprintf("-timeout=%ds", args.Duration+30)}
		case "trace":
			profilePath = filepath.Join(os.TempDir(), "wescode-trace.out")
			argv = []string{"go", "test", args.Target, "-trace=" + profilePath, "-count=1", fmt.Sprintf("-timeout=%ds", args.Duration+30)}
		}
	case "python":
		profilePath = filepath.Join(os.TempDir(), "wescode-profile.prof")
		py, err := platform.ResolveTool("python3")
		if err != nil {
			return &tool.ToolResult{Content: fmt.Sprintf("python interpreter not found: %v", err), IsError: true}, nil
		}
		// argv (no shell) keeps "C:\Program Files\..." and the Windows tool
		// name intact — no cmd.exe quoting involved (review P0-1).
		argv = []string{py.Path, "-m", "cProfile", "-o", profilePath, args.Target}
	case "node":
		profilePath = filepath.Join(os.TempDir(), "wescode-profile.cpuprofile")
		argv = []string{"node", "--cpu-prof", "--cpu-prof-dir=" + os.TempDir(), args.Target}
	default:
		return &tool.ToolResult{Content: "Unsupported language: " + args.Language, IsError: true}, nil
	}

	output, err := runCommandArgv(ctx, tc, argv, time.Duration(args.Duration+60)*time.Second)
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("Profile failed: %s", err), IsError: true}, nil
	}

	content := fmt.Sprintf("Profile collected: %s\n\nProfile file: %s\n\nUse `analyze_profile` to interpret the results.\n\n---\n%s",
		args.Kind, profilePath, truncateOutput(output, 3000))
	return &tool.ToolResult{Content: content}, nil
}

// --- analyze_profile (Extended) ---

func NewAnalyzeProfileTool() tool.Tool {
	return &analyzeProfileTool{
		BaseTool: tool.BaseTool{Meta: tool.ToolMeta{
			Dimension: tool.DimensionPerceive, ReadOnly: true, Concurrent: true,
			Timeout: 30 * time.Second, Risk: tool.RiskSafe, Enabled: true,
			Domain: "debug", DomainToolTier: tool.DomainTierExtended,
		}},
	}
}

type analyzeProfileTool struct{ tool.BaseTool }

func (t *analyzeProfileTool) Name() string { return "analyze_profile" }
func (t *analyzeProfileTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "analyze_profile",
		Description: "Analyze a collected profile file (CPU, memory, or trace). For Go profiles, uses go tool pprof to identify top hot functions.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"profile_path":{"type":"string","description":"Path to the profile file"},"language":{"type":"string","enum":["go","python"],"description":"Language/runtime (default: go)"},"top_n":{"type":"integer","description":"Number of top functions to show (default: 20)"}},"required":["profile_path"]}`),
	}
}

func (t *analyzeProfileTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。本工具不产出列表（profile 报告是叙述而非条目），
	// 传 nil 得到 category=text + 分级——分级本身仍是信息（PC-02）。
	var notReady bool
	defer func() { res = PresentList(res, notReady, nil) }()

	var args struct {
		ProfilePath string `json:"profile_path"`
		Language    string `json:"language"`
		TopN        int    `json:"top_n"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return &tool.ToolResult{Content: "Invalid arguments: " + err.Error(), IsError: true}, nil
	}
	if args.Language == "" {
		args.Language = "go"
	}
	if args.TopN <= 0 {
		args.TopN = 20
	}

	var argv []string
	switch args.Language {
	case "go":
		argv = []string{"go", "tool", "pprof", "-text", "-top", fmt.Sprintf("-nodecount=%d", args.TopN), args.ProfilePath}
	case "python":
		py, err := platform.ResolveTool("python3")
		if err != nil {
			return &tool.ToolResult{Content: fmt.Sprintf("python interpreter not found: %v", err), IsError: true}, nil
		}
		// -c code as a single argv element: no shell quoting involved, so
		// the embedded single quotes and the profile path survive intact.
		code := fmt.Sprintf("import pstats; p=pstats.Stats('%s'); p.sort_stats('cumulative'); p.print_stats(%d)",
			args.ProfilePath, args.TopN)
		argv = []string{py.Path, "-c", code}
	default:
		return &tool.ToolResult{Content: "Unsupported language: " + args.Language, IsError: true}, nil
	}

	output, err := runCommandArgv(ctx, tc, argv, 15*time.Second)
	if err != nil {
		return &tool.ToolResult{Content: "Profile analysis failed: " + err.Error(), IsError: true}, nil
	}
	return &tool.ToolResult{Content: "## Profile Analysis\n\n```\n" + truncateOutput(output, 5000) + "\n```"}, nil
}

// --- watch_logs (Extended) ---

func NewWatchLogsTool() tool.Tool {
	return &watchLogsTool{
		BaseTool: tool.BaseTool{Meta: tool.ToolMeta{
			Dimension: tool.DimensionPerceive, ReadOnly: true, Concurrent: true,
			Timeout: 30 * time.Second, Risk: tool.RiskSafe, Enabled: true,
			Domain: "debug", DomainToolTier: tool.DomainTierExtended,
		}},
	}
}

type watchLogsTool struct{ tool.BaseTool }

func (t *watchLogsTool) Name() string { return "watch_logs" }
func (t *watchLogsTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "watch_logs",
		Description: "Watch a log file for error patterns. Reads the last N lines and highlights anomalies (errors, panics, exceptions, timeouts).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Log file path"},"lines":{"type":"integer","description":"Number of tail lines (default: 100)"},"pattern":{"type":"string","description":"Optional grep pattern (default: error/panic/exception)"}},"required":["path"]}`),
	}
}

func (t *watchLogsTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。本工具不产出列表（输出形态不是可跳转条目），
	// 传 nil 得到 category=text + 分级——分级本身仍是信息（PC-02）。
	var notReady bool
	defer func() { res = PresentList(res, notReady, nil) }()

	var args struct {
		Path    string `json:"path"`
		Lines   int    `json:"lines"`
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return &tool.ToolResult{Content: "Invalid arguments: " + err.Error(), IsError: true}, nil
	}
	if args.Lines <= 0 {
		args.Lines = 100
	}

	lines, err := readTailLines(args.Path, args.Lines)
	if err != nil {
		return &tool.ToolResult{Content: "Log watch failed: " + err.Error(), IsError: true}, nil
	}
	var matched []string
	if args.Pattern != "" {
		re, err := regexp.Compile("(?i)" + regexp.QuoteMeta(args.Pattern))
		if err != nil {
			return &tool.ToolResult{Content: "Invalid pattern: " + err.Error(), IsError: true}, nil
		}
		for _, l := range lines {
			if re.MatchString(l) {
				matched = append(matched, l)
			}
		}
	} else {
		errRe := regexp.MustCompile(`(?i)(error|panic|exception|fatal|timeout|refused|denied|fail)`)
		for _, l := range lines {
			if errRe.MatchString(l) {
				matched = append(matched, l)
			}
		}
	}
	if len(matched) == 0 {
		if args.Pattern != "" {
			return &tool.ToolResult{Content: "## Log Analysis\n\n(no matches)"}, nil
		}
		return &tool.ToolResult{Content: "## Log Analysis\n\n(no error patterns found)"}, nil
	}
	return &tool.ToolResult{Content: "## Log Analysis\n\n" + highlightErrors(strings.Join(matched, "\n"))}, nil
}

// runCommandArgv executes a command as an argv array (no shell), so paths
// with spaces and Windows tool names survive untouched (review P0-1:
// cmd.exe /C quoting must not eat the first/last quote). Output is collected
// from the merged stdout+stderr pipe of the shell session. Shared by
// run_profile / analyze_profile / ci_status.
func runCommandArgv(ctx context.Context, tc *tool.ToolContext, argv []string, timeout time.Duration) (string, error) {
	if tc == nil || tc.Host == nil {
		return "", fmt.Errorf("host environment not available")
	}
	shell := tc.Host.Shell()
	if shell == nil {
		return "", fmt.Errorf("shell provider not available")
	}

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	session, err := shell.Start(cmdCtx, tool.ShellRequest{
		Command:        strings.Join(argv, " "), // display/audit form
		Argv:           argv,
		WorkDir:        projectRoot(tc),
		Timeout:        timeout,
		ReadOnlyMounts: tc.EnginePaths,
	})
	if err != nil {
		return "", fmt.Errorf("start: %w", err)
	}

	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, session.Output())
	}()

	exitCode, waitErr := session.Wait()
	output := buf.String()

	if waitErr != nil && cmdCtx.Err() != nil {
		return output, fmt.Errorf("command timed out after %s", timeout)
	}
	if exitCode != 0 && output == "" {
		return "", fmt.Errorf("exit code %d", exitCode)
	}
	return output, nil
}

// readTailLines reads the last n lines of a file (bounded to the last 256 KiB),
// mirroring "tail -n" without the POSIX-only tail binary (review P1).
func readTailLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	const maxTail = 256 * 1024
	start := int64(0)
	if st.Size() > maxTail {
		start = st.Size() - maxTail
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

func truncateOutput(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n... (truncated)"
}

func highlightErrors(text string) string {
	lines := strings.Split(text, "\n")
	var sb strings.Builder
	errorCount := 0
	for _, line := range lines {
		lower := strings.ToLower(line)
		isError := strings.Contains(lower, "error") || strings.Contains(lower, "panic") ||
			strings.Contains(lower, "exception") || strings.Contains(lower, "fatal")
		if isError {
			errorCount++
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	if errorCount > 0 {
		return fmt.Sprintf("Found %d error lines:\n\n%s", errorCount, sb.String())
	}
	return sb.String()
}
