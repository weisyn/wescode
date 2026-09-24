package codeintel

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
)

// AnalyzeGitCoChanges mines git history for file co-change patterns.
// Produces CO_CHANGES_WITH edges when two files appear in the same commit
// ≥ minCoChanges times and their Jaccard coefficient ≥ minJaccard.
//
// Reference: codebase-memory-mcp src/git/co_change.c
func AnalyzeGitCoChanges(workDir string, buf *GraphBuffer, since string) error {
	if since == "" {
		since = "6months"
	}

	cmd := exec.Command("git", "log", "--format=%H", "--name-only", "--since="+since)
	cmd.Dir = workDir

	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 69 && runtime.GOOS == "darwin" {
			slog.Warn("git co-change: git blocked by macOS — run 'sudo xcodebuild -license accept' or 'xcode-select --install' to fix",
				"exit_code", 69, "workdir", workDir)
		} else {
			slog.Warn("git co-change: git log failed", "err", err, "workdir", workDir)
		}
		return nil // non-fatal: no git repo or empty history
	}

	// Parse git log output: commit hash lines alternate with file name groups.
	type fileSet = map[string]bool

	commitFiles := make([]fileSet, 0, 256)
	fileCommitCount := make(map[string]int) // file → total commits
	pairCount := make(map[[2]string]int)    // (fileA, fileB) → co-change count

	var currentFiles fileSet
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if currentFiles != nil && len(currentFiles) > 1 && len(currentFiles) <= 50 {
				commitFiles = append(commitFiles, currentFiles)
				for f := range currentFiles {
					fileCommitCount[f]++
				}
			}
			currentFiles = nil
			continue
		}
		if len(line) == 40 && !strings.Contains(line, "/") {
			currentFiles = make(fileSet)
			continue
		}
		if currentFiles != nil {
			currentFiles[line] = true
		}
	}
	if currentFiles != nil && len(currentFiles) > 1 && len(currentFiles) <= 50 {
		commitFiles = append(commitFiles, currentFiles)
		for f := range currentFiles {
			fileCommitCount[f]++
		}
	}

	// Count co-occurrences for each file pair.
	for _, files := range commitFiles {
		sorted := make([]string, 0, len(files))
		for f := range files {
			sorted = append(sorted, f)
		}
		for i := 0; i < len(sorted); i++ {
			for j := i + 1; j < len(sorted); j++ {
				a, b := sorted[i], sorted[j]
				if a > b {
					a, b = b, a
				}
				pairCount[[2]string{a, b}]++
			}
		}
	}

	// Generate edges for pairs exceeding thresholds.
	const minCoChanges = 3
	const minJaccard = 0.3
	edgeCount := 0

	for pair, count := range pairCount {
		if count < minCoChanges {
			continue
		}
		a, b := pair[0], pair[1]
		countA := fileCommitCount[a]
		countB := fileCommitCount[b]
		jaccard := float64(count) / float64(countA+countB-count)
		if jaccard < minJaccard {
			continue
		}

		// One edge per unordered pair; readers query both directions.
		fileQNameA := "file:" + a
		fileQNameB := "file:" + b

		buf.AddEdge(Edge{
			SourceQName: fileQNameA,
			TargetQName: fileQNameB,
			TargetName:  b,
			Kind:        EdgeCoChangesWith,
			// `exact` describes the binding, and it only holds when both paths
			// were also parsed into file nodes — git history contains docs,
			// configs, and deleted files the parser never saw, so those rows
			// degrade to unresolved at flush and keep target_name.
			// The Jaccard coefficient is the measurement, and it is the one
			// number here comparable against another row of this kind.
			Resolution: ResolutionExact,
			Score:      &jaccard,
			Source:     fmt.Sprintf("git-cochange:%d", count),
		})
		edgeCount++
	}

	slog.Info("git co-change analysis complete",
		"commits_analyzed", len(commitFiles),
		"pairs_found", edgeCount,
	)
	return nil
}
