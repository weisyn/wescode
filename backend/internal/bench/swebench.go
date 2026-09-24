package bench

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SWEBenchCacheDir returns the directory where SWE-bench repos are cached.
// Each repo is cloned once and reused across cases via git checkout.
func SWEBenchCacheDir() string {
	if v := os.Getenv("SWEBENCH_CACHE_DIR"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".wescode", "swebench-repos")
}

// PrepareSWEBenchWorkdir prepares a workdir for a SWE-bench case.
// It clones the repo (or reuses a cached clone), checks out the base commit,
// and returns the workdir path. The workdir is a git worktree so the base
// clone can be shared across cases from the same repo.
func PrepareSWEBenchWorkdir(ctx context.Context, c Case) (workdir string, cleanup func(), err error) {
	if c.Repo == "" || c.BaseCommit == "" {
		return "", nil, fmt.Errorf("swebench: case %s missing repo or base_commit", c.ID)
	}

	cacheDir := SWEBenchCacheDir()
	repoDir := filepath.Join(cacheDir, strings.ReplaceAll(c.Repo, "/", "__"))

	if err := ensureRepoCloned(ctx, c.Repo, repoDir); err != nil {
		return "", nil, fmt.Errorf("swebench: clone %s: %w", c.Repo, err)
	}

	// Create a disposable worktree for this case so we don't pollute the
	// base clone (multiple cases from the same repo can run in parallel).
	wtDir, mkErr := os.MkdirTemp("", "swebench-"+sanitize(c.ID)+"-*")
	if mkErr != nil {
		return "", nil, fmt.Errorf("swebench: mktemp: %w", mkErr)
	}

	// Fetch the specific commit if not already present.
	fetchCmd := exec.CommandContext(ctx, "git", "fetch", "origin", c.BaseCommit)
	fetchCmd.Dir = repoDir
	fetchCmd.CombinedOutput() // best-effort; commit may already be local

	// Create worktree at the base commit.
	wtCmd := exec.CommandContext(ctx, "git", "worktree", "add", "--detach", wtDir, c.BaseCommit)
	wtCmd.Dir = repoDir
	if out, err := wtCmd.CombinedOutput(); err != nil {
		os.RemoveAll(wtDir)
		return "", nil, fmt.Errorf("swebench: worktree add: %w: %s", err, string(out))
	}

	cleanupFn := func() {
		// Remove worktree registration then delete the directory.
		rmCmd := exec.Command("git", "worktree", "remove", "--force", wtDir)
		rmCmd.Dir = repoDir
		rmCmd.Run()
		os.RemoveAll(wtDir)
	}

	return wtDir, cleanupFn, nil
}

// ExtractDiff extracts the git diff from the workdir after the agent has
// made changes. This becomes the prediction patch for SWE-bench evaluation.
func ExtractDiff(ctx context.Context, workdir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff")
	cmd.Dir = workdir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	return string(out), nil
}

func ensureRepoCloned(ctx context.Context, repo, dir string) error {
	// Bare repos have HEAD at the root (not .git/HEAD).
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err == nil {
		return nil
	}
	// Non-bare repos have .git directory.
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}

	url := "https://github.com/" + repo + ".git"
	cmd := exec.CommandContext(ctx, "git", "clone", "--bare", "--filter=blob:none", url, dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone: %w: %s", err, string(out))
	}
	return nil
}

// WritePrediction writes a single SWE-bench prediction entry.
type SWEBenchPrediction struct {
	InstanceID string `json:"instance_id"`
	ModelPatch string `json:"model_patch"`
	ModelName  string `json:"model_name_or_path"`
}
