package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// --- ci_status tool ---

// NewCIStatusTool creates the ci_status tool for querying CI pipeline status.
func NewCIStatusTool() tool.Tool {
	return &ciStatusTool{
		BaseTool: tool.BaseTool{Meta: tool.ToolMeta{
			Dimension:    tool.DimensionPerceive,
			Availability: tool.AvailabilityConfigurable,
			ReadOnly:     false,
			Concurrent:   true,
			Timeout:      30 * time.Second,
			Risk:         tool.RiskSafe,
			PolicyFamily: tool.PolicyFamilySubprocess,
			Enabled:      true,
			Domain:       "ci",
		}},
	}
}

type ciStatusTool struct{ tool.BaseTool }

func (t *ciStatusTool) Name() string { return "ci_status" }

func (t *ciStatusTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "ci_status",
		Description: "Query CI/CD pipeline status, get failed job logs, or retry failed jobs. Supports GitHub Actions (gh CLI) and GitLab CI (glab CLI). Auto-detects provider from git remote URL.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"action": {
					"type": "string",
					"enum": ["status", "logs", "retry"],
					"description": "status: get CI status for current branch/PR. logs: get failed job logs. retry: re-trigger failed jobs."
				},
				"provider": {
					"type": "string",
					"enum": ["github", "gitlab"],
					"description": "CI provider. Auto-detected from .git/config remote URL if omitted."
				},
				"run_id": {
					"type": "string",
					"description": "Specific CI run/pipeline ID (for logs/retry). If omitted with action=logs, uses the latest run."
				},
				"job_name": {
					"type": "string",
					"description": "Specific job name to get logs for (action=logs). If omitted, returns all failed job logs."
				}
			},
			"required": ["action"]
		}`),
	}
}

func (t *ciStatusTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var args struct {
		Action   string `json:"action"`
		Provider string `json:"provider"`
		RunID    string `json:"run_id"`
		JobName  string `json:"job_name"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return &tool.ToolResult{Content: "Invalid arguments: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}

	provider, err := resolveProvider(ctx, tc, args.Provider)
	if err != nil {
		return &tool.ToolResult{Content: err.Error(), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}

	switch args.Action {
	case "status":
		res, list, err = ciStatus(ctx, tc, provider)
		return res, err
	case "logs":
		return ciLogs(ctx, tc, provider, args.RunID, args.JobName)
	case "retry":
		return ciRetry(ctx, tc, provider, args.RunID)
	default:
		return &tool.ToolResult{Content: "Unknown action: " + args.Action + ". Use status, logs, or retry.", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
}

func resolveProvider(ctx context.Context, tc *tool.ToolContext, explicit string) (string, error) {
	if explicit != "" {
		cli := cliForProvider(explicit)
		if err := checkCLI(ctx, tc, cli); err != nil {
			return "", fmt.Errorf("%s CLI (%s) not available: %w. Install it first.", explicit, cli, err)
		}
		return explicit, nil
	}

	// argv mode: no "2>/dev/null" — stderr is captured and only surfaced
	// on failure. "git" resolves to git.exe via the engine's argv shim.
	remoteURL, err := runCommandArgv(ctx, tc, []string{"git", "remote", "get-url", "origin"}, 5*time.Second)
	if err != nil {
		return "", fmt.Errorf("cannot detect CI provider: git remote get-url origin failed: %w", err)
	}
	remoteURL = strings.TrimSpace(remoteURL)

	detected := detectProviderFromURL(remoteURL)
	if detected == "" {
		return "", fmt.Errorf("cannot detect CI provider from remote URL %q. Specify provider explicitly.", remoteURL)
	}

	cli := cliForProvider(detected)
	if err := checkCLI(ctx, tc, cli); err != nil {
		return "", fmt.Errorf("detected %s from remote URL, but %s CLI not available: %w", detected, cli, err)
	}
	return detected, nil
}

func detectProviderFromURL(url string) string {
	lower := strings.ToLower(url)
	switch {
	case strings.Contains(lower, "github.com"):
		return "github"
	case strings.Contains(lower, "gitlab"):
		return "gitlab"
	default:
		return ""
	}
}

func cliForProvider(provider string) string {
	switch provider {
	case "github":
		return "gh"
	case "gitlab":
		return "glab"
	default:
		return ""
	}
}

func checkCLI(ctx context.Context, tc *tool.ToolContext, cli string) error {
	_, err := runCommandArgv(ctx, tc, []string{cli, "--version"}, 5*time.Second)
	return err
}

func currentBranch(ctx context.Context, tc *tool.ToolContext) (string, error) {
	out, err := runCommandArgv(ctx, tc, []string{"git", "branch", "--show-current"}, 5*time.Second)
	if err != nil {
		return "", fmt.Errorf("cannot determine current branch: %w", err)
	}
	branch := strings.TrimSpace(out)
	if branch == "" {
		return "", fmt.Errorf("not on a branch (detached HEAD?)")
	}
	return branch, nil
}

// --- action: status ---

// 返回列表是为了让 Call 的唯一附加点拿到它：ci run 列表由 formatGHStatus 构造，
// 而那是包级函数，拿不到 Call 里的 list 变量。
func ciStatus(ctx context.Context, tc *tool.ToolContext, provider string) (*tool.ToolResult, *ListData, error) {
	branch, err := currentBranch(ctx, tc)
	if err != nil {
		return &tool.ToolResult{Content: err.Error(), IsError: true, ErrorKind: tool.ErrorExecution}, nil, nil
	}

	var argv []string
	switch provider {
	case "github":
		argv = []string{"gh", "run", "list", "--branch", branch, "--limit", "5",
			"--json", "databaseId,status,conclusion,name,createdAt,url"}
	case "gitlab":
		argv = []string{"glab", "ci", "list", "--branch", branch, "-F", "json"}
	}

	output, err := runCommandArgv(ctx, tc, argv, 15*time.Second)
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("CI status query failed: %s\n\n%s", err, output), IsError: true, ErrorKind: tool.ErrorExecution}, nil, nil
	}

	if provider == "github" {
		return formatGHStatus(output, branch)
	}
	r, e := formatGLabStatus(output, branch)
	return r, nil, e
}

func formatGHStatus(raw, branch string) (*tool.ToolResult, *ListData, error) {
	var runs []struct {
		DatabaseId int    `json:"databaseId"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		Name       string `json:"name"`
		CreatedAt  string `json:"createdAt"`
		URL        string `json:"url"`
	}
	if err := json.Unmarshal([]byte(raw), &runs); err != nil {
		return &tool.ToolResult{Content: "## CI Status (branch: " + branch + ")\n\n```\n" + truncateOutput(raw, 3000) + "\n```"}, nil, nil
	}
	if len(runs) == 0 {
		return &tool.ToolResult{Content: fmt.Sprintf("No CI runs found for branch %q.", branch)}, nil, nil
	}

	var sb strings.Builder
	// 一项 = 一次 CI run。结论进 kind 徽标（failure / success 决定用户看不看），
	// 时间进 detail。CI run 没有文件位置——它是远端事实。
	ciItems := make([]ListItem, 0, len(runs))
	for _, r := range runs {
		st := r.Status
		if r.Conclusion != "" {
			st = r.Conclusion
		}
		when := r.CreatedAt
		if ts, perr := time.Parse(time.RFC3339, r.CreatedAt); perr == nil {
			when = ts.Format("Jan 2 15:04")
		}
		ciItems = append(ciItems, ListItem{
			Label:  r.Name,
			Detail: when,
			Kind:   st,
		})
	}
	ghList := ListOf(ciItems, 0)

	sb.WriteString(fmt.Sprintf("## CI Status — branch: %s\n\n", branch))
	for _, r := range runs {
		status := r.Status
		if r.Conclusion != "" {
			status = r.Conclusion
		}
		icon := ciIcon(status)
		sb.WriteString(fmt.Sprintf("%s **%s** (ID: %d) — %s", icon, r.Name, r.DatabaseId, status))
		if r.CreatedAt != "" {
			if t, err := time.Parse(time.RFC3339, r.CreatedAt); err == nil {
				sb.WriteString(fmt.Sprintf(" — %s", t.Format("Jan 2 15:04")))
			}
		}
		sb.WriteByte('\n')
	}

	hasFailure := false
	for _, r := range runs {
		if r.Conclusion == "failure" {
			hasFailure = true
			break
		}
	}
	if hasFailure {
		sb.WriteString(fmt.Sprintf("\nUse `ci_status(action=\"logs\", run_id=\"%d\")` to see failed job logs.", runs[0].DatabaseId))
	}

	return &tool.ToolResult{Content: sb.String()}, ghList, nil
}

func formatGLabStatus(raw, branch string) (*tool.ToolResult, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## CI Status — branch: %s\n\n", branch))
	sb.WriteString("```\n")
	sb.WriteString(truncateOutput(raw, 4000))
	sb.WriteString("\n```")
	return &tool.ToolResult{Content: sb.String()}, nil
}

func ciIcon(status string) string {
	switch strings.ToLower(status) {
	case "success":
		return "[PASS]"
	case "failure":
		return "[FAIL]"
	case "in_progress", "queued", "running", "pending":
		return "[....]"
	case "cancelled", "skipped":
		return "[SKIP]"
	default:
		return "[????]"
	}
}

// --- action: logs ---

func ciLogs(ctx context.Context, tc *tool.ToolContext, provider, runID, jobName string) (*tool.ToolResult, error) {
	if runID == "" {
		id, err := latestRunID(ctx, tc, provider)
		if err != nil {
			return &tool.ToolResult{Content: err.Error(), IsError: true, ErrorKind: tool.ErrorExecution}, nil
		}
		runID = id
	}

	var argv []string
	switch provider {
	case "github":
		if jobName != "" {
			argv = []string{"gh", "run", "view", runID, "--log", "--job", jobName}
		} else {
			argv = []string{"gh", "run", "view", runID, "--log-failed"}
		}
	case "gitlab":
		if jobName != "" {
			argv = []string{"glab", "ci", "trace", runID, "--job", jobName}
		} else {
			argv = []string{"glab", "ci", "trace", runID}
		}
	}

	output, err := runCommandArgv(ctx, tc, argv, 25*time.Second)
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("CI log retrieval failed: %s\n\n%s", err, truncateOutput(output, 2000)), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}

	content := fmt.Sprintf("## CI Logs — Run %s", runID)
	if jobName != "" {
		content += fmt.Sprintf(" / Job: %s", jobName)
	}
	content += "\n\n```\n" + truncateOutput(output, 8000) + "\n```"

	if strings.Contains(strings.ToLower(output), "error") || strings.Contains(strings.ToLower(output), "fail") {
		content += "\n\nUse `ci_status(action=\"retry\", run_id=\"" + runID + "\")` to re-trigger failed jobs after fixing."
	}

	return &tool.ToolResult{Content: content}, nil
}

func latestRunID(ctx context.Context, tc *tool.ToolContext, provider string) (string, error) {
	branch, err := currentBranch(ctx, tc)
	if err != nil {
		return "", err
	}

	switch provider {
	case "github":
		// --jq expression as a bare argv element (no shell quoting needed).
		out, err := runCommandArgv(ctx, tc, []string{
			"gh", "run", "list", "--branch", branch, "--limit", "1",
			"--json", "databaseId", "--jq", ".[0].databaseId",
		}, 10*time.Second)
		if err != nil {
			return "", fmt.Errorf("failed to get latest run ID: %w", err)
		}
		id := strings.TrimSpace(out)
		if id == "" || id == "null" {
			return "", fmt.Errorf("no CI runs found for branch %q", branch)
		}
		return id, nil
	case "gitlab":
		out, err := runCommandArgv(ctx, tc, []string{
			"glab", "ci", "list", "--branch", branch, "-F", "json", "--per-page", "1",
		}, 10*time.Second)
		if err != nil {
			return "", fmt.Errorf("failed to get latest pipeline ID: %w", err)
		}
		var pipelines []struct {
			ID int `json:"id"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &pipelines); err != nil || len(pipelines) == 0 {
			return "", fmt.Errorf("no CI pipelines found for branch %q", branch)
		}
		return fmt.Sprintf("%d", pipelines[0].ID), nil
	}
	return "", fmt.Errorf("unsupported provider: %s", provider)
}

// --- action: retry ---

func ciRetry(ctx context.Context, tc *tool.ToolContext, provider, runID string) (*tool.ToolResult, error) {
	if runID == "" {
		id, err := latestRunID(ctx, tc, provider)
		if err != nil {
			return &tool.ToolResult{Content: err.Error(), IsError: true, ErrorKind: tool.ErrorExecution}, nil
		}
		runID = id
	}

	var argv []string
	switch provider {
	case "github":
		argv = []string{"gh", "run", "rerun", runID, "--failed"}
	case "gitlab":
		argv = []string{"glab", "ci", "retry", runID}
	}

	output, err := runCommandArgv(ctx, tc, argv, 15*time.Second)
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("CI retry failed: %s\n\n%s", err, output), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}

	content := fmt.Sprintf("## CI Retry — Run %s\n\nRetry triggered successfully.\n\n%s", runID, truncateOutput(output, 2000))
	content += "\n\nUse `ci_status(action=\"status\")` to check the new run status."
	return &tool.ToolResult{Content: content}, nil
}
