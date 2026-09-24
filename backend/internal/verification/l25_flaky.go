package verification

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// FlakyList maintains tests that produce non-deterministic output.
// These tests skip L2.5 output hash comparison (but still do pass/fail check).
// Patterns are standard Go filepath.Match globs (e.g. "TestRandom*").
type FlakyList struct {
	patterns []string
}

// NewFlakyList creates a FlakyList from an explicit set of glob patterns.
func NewFlakyList(patterns []string) *FlakyList {
	if len(patterns) == 0 {
		return nil
	}
	return &FlakyList{patterns: patterns}
}

// LoadFlakyList reads glob patterns from a file (one per line).
// Lines starting with '#' are comments; blank lines are ignored.
// Returns nil if the file does not exist or contains no patterns.
// The file path is typically ".wescode/flaky_tests.txt" under workDir.
func LoadFlakyList(workDir string) *FlakyList {
	path := filepath.Join(workDir, ".wescode", "flaky_tests.txt")
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	if len(patterns) == 0 {
		return nil
	}
	return &FlakyList{patterns: patterns}
}

// IsFlaky returns true if testName matches any pattern in the list.
func (fl *FlakyList) IsFlaky(testName string) bool {
	if fl == nil || len(fl.patterns) == 0 {
		return false
	}
	for _, p := range fl.patterns {
		if matched, _ := filepath.Match(p, testName); matched {
			return true
		}
	}
	return false
}

// Patterns returns the list of glob patterns (for diagnostics/logging).
func (fl *FlakyList) Patterns() []string {
	if fl == nil {
		return nil
	}
	return fl.patterns
}
