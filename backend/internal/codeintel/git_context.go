package codeintel

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

const (
	maxDiffChars   = 8000
	diffCacheTTL   = 30 * time.Second
	maxCommitLines = 5
)

// GitContextProvider extracts git working tree state for context injection.
type GitContextProvider struct {
	sp tool.ShellProvider

	cachedDiff    *GitDiffSummary
	cachedAt      time.Time
	cachedWorkDir string
}

// NewGitContextProvider creates a provider using the given shell.
func NewGitContextProvider(sp tool.ShellProvider) *GitContextProvider {
	return &GitContextProvider{sp: sp}
}

// DiffSummary returns a cached or freshly computed git diff summary.
// CE-12: result is cached for diffCacheTTL to avoid per-turn shell overhead.
func (g *GitContextProvider) DiffSummary(ctx context.Context, workDir string) *GitDiffSummary {
	if g.cachedDiff != nil && g.cachedWorkDir == workDir && time.Since(g.cachedAt) < diffCacheTTL {
		return g.cachedDiff
	}

	diff := g.fetchDiff(ctx, workDir)
	g.cachedDiff = diff
	g.cachedAt = time.Now()
	g.cachedWorkDir = workDir
	return diff
}

// InvalidateCache forces next DiffSummary to re-fetch.
func (g *GitContextProvider) InvalidateCache() {
	g.cachedDiff = nil
}

func (g *GitContextProvider) fetchDiff(ctx context.Context, workDir string) *GitDiffSummary {
	fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	stat := g.runGit(fetchCtx, workDir, "diff --stat HEAD")
	if stat == "" {
		return &GitDiffSummary{HasChanges: false}
	}

	diff := g.runGit(fetchCtx, workDir, "diff HEAD")
	if len(diff) > maxDiffChars {
		diff = diff[:maxDiffChars] + "\n... (diff truncated)"
	}

	recentLog := g.runGit(fetchCtx, workDir, fmt.Sprintf("log --oneline -%d", maxCommitLines))

	var sb strings.Builder
	sb.WriteString("[Git Working Tree Changes]\n")
	sb.WriteString(stat)
	if recentLog != "" {
		sb.WriteString("\n[Recent Commits]\n")
		sb.WriteString(recentLog)
	}
	if diff != "" {
		sb.WriteString("\n[Diff]\n")
		sb.WriteString(diff)
	}

	return &GitDiffSummary{
		HasChanges: true,
		Summary:    sb.String(),
	}
}

func (g *GitContextProvider) runGit(ctx context.Context, workDir, args string) string {
	session, err := g.sp.Start(ctx, tool.ShellRequest{
		Command: "git " + args,
		WorkDir: workDir,
	})
	if err != nil {
		slog.Debug("[git_context] shell start failed", "args", args, "err", err)
		return ""
	}

	out, err := io.ReadAll(session.Output())
	if err != nil {
		return ""
	}
	exitCode, _ := session.Wait()
	if exitCode != 0 {
		return ""
	}
	return strings.TrimSpace(string(out))
}
