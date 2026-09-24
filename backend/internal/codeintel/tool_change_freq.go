package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

func NewChangeFreqTool(workDir string) tool.Tool {
	return &changeFreqTool{
		BaseTool: tool.BaseTool{
			Meta: tool.ToolMeta{
				Dimension:      tool.DimensionPerceive,
				Availability:   tool.AvailabilityConfigurable,
				ReadOnly:       true,
				Concurrent:     true,
				Timeout:        10 * time.Second,
				Risk:           tool.RiskSafe,
				PolicyFamily:   tool.PolicyFamilyOther,
				Enabled:        true,
				Domain:         "temporal",
				DomainToolTier: tool.DomainTierCore,
			},
		},
		workDir: workDir,
	}
}

type changeFreqTool struct {
	tool.BaseTool
	workDir string
}

func (t *changeFreqTool) Name() string { return "file_change_frequency" }

func (t *changeFreqTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name:        "file_change_frequency",
		Description: "Show how frequently files have been modified in git history. Provide a file path to get its commit count, or omit to get the most frequently changed files.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"file": {"type": "string", "description": "File path to check (relative to workspace root)"},
				"since": {"type": "string", "description": "Git history window (default '6months')"},
				"limit": {"type": "integer", "description": "Max files to return when no specific file given (default 20)"}
			},
			"required": []
		}`),
	}
}

type changeFreqEntry struct {
	File    string
	Commits int
}

func (t *changeFreqTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (res *tool.ToolResult, err error) {
	// PC-01 唯一附加点。
	var list *ListData
	var notReady bool
	defer func() { res = PresentList(res, notReady, list) }()

	var params struct {
		File  string `json:"file"`
		Since string `json:"since"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return &tool.ToolResult{Content: "invalid input: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}
	if t.workDir == "" {
		return &tool.ToolResult{Content: "workspace directory is not configured"}, nil
	}
	if params.Since == "" {
		params.Since = "6months"
	}
	if params.Limit <= 0 {
		params.Limit = 20
	}

	// 两条路径形状不同：单文件问的是"这个文件改了几次"（一个数字，text），
	// top-N 问的是"哪些文件最常改"（列表）。helper 把列表递回来，附加点仍只有一处。
	if params.File != "" {
		return t.singleFileFreq(ctx, params.File, params.Since)
	}
	res, list, err = t.topChangedFiles(ctx, params.Since, params.Limit)
	return res, err
}

func (t *changeFreqTool) singleFileFreq(ctx context.Context, file, since string) (*tool.ToolResult, error) {
	cmd := exec.CommandContext(ctx, "git", "log", "--oneline", "--follow", "--since="+since, "--", file)
	cmd.Dir = t.workDir
	out, err := cmd.Output()
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("git log failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	count := 0
	for _, line := range lines {
		if line != "" {
			count++
		}
	}

	return &tool.ToolResult{Content: fmt.Sprintf("File: %s\nCommits in last %s: %d", file, since, count)}, nil
}

func (t *changeFreqTool) topChangedFiles(ctx context.Context, since string, limit int) (*tool.ToolResult, *ListData, error) {
	cmd := exec.CommandContext(ctx, "git", "log", "--format=", "--name-only", "--since="+since)
	cmd.Dir = t.workDir
	out, err := cmd.Output()
	if err != nil {
		return &tool.ToolResult{Content: fmt.Sprintf("git log failed: %v", err), IsError: true, ErrorKind: tool.ErrorExecution}, nil, nil
	}

	freq := map[string]int{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		freq[line]++
	}

	if len(freq) == 0 {
		return &tool.ToolResult{Content: fmt.Sprintf("No file changes found in the last %s", since)},
			&ListData{Items: []ListItem{}}, nil
	}

	entries := make([]changeFreqEntry, 0, len(freq))
	for file, count := range freq {
		entries = append(entries, changeFreqEntry{File: file, Commits: count})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Commits > entries[j].Commits
	})

	// "top N" 本身不是谎，但它说不出池子多大——"top 10 of 240" 才告诉用户这段时间
	// 有多少文件被动过，而那正是"频繁变更"这个问题的规模。
	pool := len(entries)
	if len(entries) > limit {
		entries = entries[:limit]
	}

	// 提交次数进 detail：它是这张表的排序依据，也是"这个文件值不值得关注"的判据。
	// 文件粒度没有行号——变更频率是整文件的事实。
	items := make([]ListItem, 0, len(entries))
	for _, e := range entries {
		items = append(items, ListItem{
			Label:  filepath.Base(e.File),
			Detail: fmt.Sprintf("%d commits", e.Commits),
			File:   e.File,
			Kind:   "hot file",
		})
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Most frequently changed files (last %s, top %s):\n\n", since, CountPhrase(len(entries), pool)))
	for i, e := range entries {
		sb.WriteString(fmt.Sprintf("%d. %s — %d commits\n", i+1, e.File, e.Commits))
	}
	return &tool.ToolResult{Content: sb.String()}, ListOf(items, pool), nil
}
