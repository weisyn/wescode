package constraints

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InferConcurrencyConstraints (C4) registers a goroutine_capture constraint for
// every file that currently has a `go func(){...}()` capturing enclosing-scope
// variables without visible synchronization.
//
// Detection and enforcement share one predicate (checkGoroutineCapture), so a
// PreWrite re-check evaluates the edited content instead of replaying a stale
// line number (INV-CSE-15).
//
// db is accepted for signature uniformity but not used (pure AST analysis).
// Returns the number of constraints registered.
func InferConcurrencyConstraints(db *sql.DB, reg *Registry, workDir string) (int, error) {
	if reg == nil {
		return 0, nil
	}

	root := absRoot(workDir)
	if _, err := os.Stat(filepath.Join(workDir, "go.mod")); err != nil {
		return 0, nil
	}

	count := 0
	err := filepath.Walk(workDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() && IsSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relPath := RelToRoot(root, path)
		if relPath == "" {
			return nil
		}
		failed, msg := checkGoroutineCapture(path, "")
		if !failed {
			return nil
		}

		reg.AddInferred(Constraint{
			ID:         inferID("pattern-c4", relPath),
			Rule:       fmt.Sprintf("Goroutine closures in %s capture enclosing-scope variables without visible synchronization. Pass the value as a parameter or guard it with a mutex/channel.", relPath),
			Kind:       "quality",
			Priority:   PriorityQuality,
			Status:     StatusCandidate,
			Confidence: 0.65,
			Source:     SourceInferredPattern,
			TTL:        120,
			Evidence:   []string{msg},
		}.AtFile(root, relPath, CheckGoroutineCapture))
		count++
		return nil
	})

	return count, err
}
