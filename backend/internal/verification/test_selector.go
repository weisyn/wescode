package verification

import (
	"context"
	"path/filepath"
	"strings"
)

// FunctionTestSelector narrows the test scope from package-level to
// individual test functions that likely exercise the modified code.
//
// Strategy: for each modified file, find test files in the same package,
// then filter test functions whose names contain the modified function names.
// This is a heuristic — Go convention: TestFoo tests function Foo.
type FunctionTestSelector struct {
	// SymbolsInFile returns function/method names defined in a file.
	SymbolsInFile func(ctx context.Context, path string) ([]string, error)
	// TestFunctionsInFile returns test function names (Test*) in a test file.
	TestFunctionsInFile func(ctx context.Context, path string) ([]string, error)
}

// TestRunFilter represents a narrowed test execution scope.
type TestRunFilter struct {
	Package   string
	TestNames []string // -run regex pattern (e.g. "TestCreate|TestUpdate")
}

// SelectTests maps modified files to specific test functions.
// Returns nil if selection cannot be narrowed (caller should run all package tests).
func (s *FunctionTestSelector) SelectTests(
	ctx context.Context,
	modifiedFiles []string,
	workDir string,
) []TestRunFilter {
	if s.SymbolsInFile == nil || s.TestFunctionsInFile == nil {
		return nil
	}

	// Collect modified function names per package directory.
	modifiedSymbols := make(map[string][]string) // dir → []funcName
	for _, f := range modifiedFiles {
		if isTestFile(f) {
			continue
		}
		syms, err := s.SymbolsInFile(ctx, f)
		if err != nil || len(syms) == 0 {
			continue
		}
		dir := filepath.Dir(f)
		modifiedSymbols[dir] = append(modifiedSymbols[dir], syms...)
	}

	if len(modifiedSymbols) == 0 {
		return nil
	}

	var filters []TestRunFilter
	for dir, funcNames := range modifiedSymbols {
		testFiles := findTestFilesInDir(dir)
		if len(testFiles) == 0 {
			continue
		}

		var matched []string
		for _, tf := range testFiles {
			testFuncs, err := s.TestFunctionsInFile(ctx, tf)
			if err != nil {
				continue
			}
			for _, testFn := range testFuncs {
				if testNameMatchesAnySymbol(testFn, funcNames) {
					matched = append(matched, testFn)
				}
			}
		}

		if len(matched) == 0 {
			continue
		}

		rel, _ := filepath.Rel(workDir, dir)
		pkg := "./" + rel + "/..."
		filters = append(filters, TestRunFilter{
			Package:   pkg,
			TestNames: matched,
		})
	}

	if len(filters) == 0 {
		return nil
	}
	return filters
}

// BuildRunRegex builds a -run regex from test function names.
func BuildRunRegex(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return "^(" + strings.Join(names, "|") + ")$"
}

func findTestFilesInDir(dir string) []string {
	pattern := filepath.Join(dir, "*_test.go")
	matches, _ := filepath.Glob(pattern)
	if len(matches) > 0 {
		return matches
	}
	// Try TS/JS/Python conventions
	for _, pat := range []string{
		filepath.Join(dir, "*.test.ts"),
		filepath.Join(dir, "*.spec.ts"),
		filepath.Join(dir, "*.test.js"),
		filepath.Join(dir, "test_*.py"),
	} {
		if m, _ := filepath.Glob(pat); len(m) > 0 {
			matches = append(matches, m...)
		}
	}
	return matches
}

// testNameMatchesAnySymbol checks if a test function name (e.g. TestCreateUser)
// contains any of the modified function names (e.g. "Create", "CreateUser").
func testNameMatchesAnySymbol(testName string, symbols []string) bool {
	upper := strings.ToLower(testName)
	for _, sym := range symbols {
		if strings.Contains(upper, strings.ToLower(sym)) {
			return true
		}
	}
	return false
}
