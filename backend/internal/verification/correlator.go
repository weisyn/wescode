package verification

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// SymbolLister returns the function/method names defined in a file.
type SymbolLister func(ctx context.Context, path string) ([]string, error)

// NameCorrelator implements FailureCorrelator using function-name heuristics.
// It maps failing test names to modified functions in the same package via
// substring matching (TestCreateUser ↔ Create/CreateUser).
// CKG provides the symbol list; correlation is name-based, not call-graph-based.
type NameCorrelator struct {
	SymbolsInFile SymbolLister
}

// CallGraphCorrelator is a deprecated alias for NameCorrelator.
type CallGraphCorrelator = NameCorrelator

func (c *NameCorrelator) Correlate(
	ctx context.Context,
	failures []TestFailure,
	modifiedFiles []string,
	workDir string,
) []string {
	if c.SymbolsInFile == nil {
		return nil
	}

	// Build map: package dir → modified function names
	modifiedFuncs := make(map[string][]string) // dir → []funcName
	for _, f := range modifiedFiles {
		if isTestFile(f) {
			continue
		}
		syms, err := c.SymbolsInFile(ctx, f)
		if err != nil || len(syms) == 0 {
			continue
		}
		dir, _ := filepath.Rel(workDir, filepath.Dir(f))
		if dir == "" {
			dir = "."
		}
		modifiedFuncs[dir] = append(modifiedFuncs[dir], syms...)
	}

	if len(modifiedFuncs) == 0 {
		return nil
	}

	var hints []string
	for _, failure := range failures {
		failPkg := failure.Package
		failPkg = strings.TrimPrefix(failPkg, "./")
		failPkg = strings.TrimSuffix(failPkg, "/...")

		funcs, ok := modifiedFuncs[failPkg]
		if !ok {
			// Go module paths (e.g. "github.com/foo/bar/internal/pkg") don't
			// match relative dirs ("internal/pkg"). Try suffix matching.
			for dir, f := range modifiedFuncs {
				if strings.HasSuffix(failPkg, "/"+dir) || dir == failPkg {
					funcs = f
					ok = true
					break
				}
			}
		}
		if !ok {
			continue
		}

		// Find which modified functions the test name correlates with
		var matched []string
		testLower := strings.ToLower(failure.TestName)
		for _, fn := range funcs {
			if strings.Contains(testLower, strings.ToLower(fn)) {
				matched = append(matched, fn)
			}
		}

		if len(matched) > 0 {
			hints = append(hints, fmt.Sprintf(
				"  Cause: %s likely failed because you modified %s() in this turn",
				failure.TestName, strings.Join(matched, ", ")))
		}
	}

	return hints
}
