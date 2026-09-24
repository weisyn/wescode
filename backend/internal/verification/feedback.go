package verification

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/weisyn/wesgine/message"
)

func FormatCompileErrors(errors []CompileError) message.Message {
	var sb strings.Builder
	sb.WriteString("⚠️ QUALITY GATE — your code changes introduced compilation errors.\n")
	sb.WriteString("You MUST fix them using the edit tool before proceeding.\n")
	sb.WriteString("Do NOT explain or apologize — directly edit the file to resolve every error.\n\n")
	sb.WriteString(fmt.Sprintf("❌ Build failed (%d errors):\n", len(errors)))

	for _, e := range errors {
		sb.WriteString(fmt.Sprintf("  %s:%d:%d: %s\n", e.Path, e.Line, e.Column, e.Message))
	}

	return message.Message{
		Role:    message.RoleUser,
		Content: []message.ContentBlock{message.NewTextBlock(sb.String())},
	}
}

func FormatTestFailures(result TestResult) message.Message {
	var sb strings.Builder
	sb.WriteString("⚠️ QUALITY GATE — your code changes caused test failures.\n")
	sb.WriteString("You MUST fix them using the edit tool before proceeding.\n")
	sb.WriteString("Do NOT explain or apologize — directly edit the file to resolve every failure.\n\n")
	sb.WriteString(fmt.Sprintf("❌ Tests failed (%d failures):\n", result.TotalFail))

	for _, f := range result.Failed {
		sb.WriteString(fmt.Sprintf("\n  FAIL %s (%s)\n", f.TestName, f.Package))
		if f.Output != "" {
			for _, line := range strings.Split(strings.TrimSpace(f.Output), "\n") {
				sb.WriteString("    " + line + "\n")
			}
		}
	}

	return message.Message{
		Role:    message.RoleUser,
		Content: []message.ContentBlock{message.NewTextBlock(sb.String())},
	}
}

// FormatHint generates basic pattern-matching hints (Phase 1 fallback).
func FormatHint(errors []CompileError) string {
	var hints []string
	for _, e := range errors {
		if strings.Contains(e.Message, "undefined:") {
			symbol := strings.TrimPrefix(e.Message, "undefined: ")
			symbol = strings.TrimSpace(symbol)
			hints = append(hints, fmt.Sprintf("Hint: %q is undefined — check imports or spelling", symbol))
		}
		if strings.Contains(e.Message, "cannot use") && strings.Contains(e.Message, "as") {
			hints = append(hints, "Hint: type mismatch — check interface implementation or type assertion")
		}
		if strings.Contains(e.Message, "imported and not used") {
			hints = append(hints, "Hint: remove unused import")
		}
	}
	if len(hints) == 0 {
		return ""
	}
	return "\n" + strings.Join(hints, "\n")
}

// FormatHintWithIndex generates enhanced hints using the code index.
// Falls back to FormatHint when finder is nil or index lookup fails.
func FormatHintWithIndex(ctx context.Context, errors []CompileError, finder SymbolFinder) string {
	if finder == nil {
		return FormatHint(errors)
	}
	var hints []string
	for _, e := range errors {
		if sym := extractUndefinedSymbol(e.Message); sym != "" {
			if defs, err := finder.FindSymbol(ctx, sym); err == nil && len(defs) > 0 {
				def := defs[0]
				hints = append(hints, fmt.Sprintf(
					"Hint: %q is defined in %s (line %d) — did you forget to import its package?",
					sym, def.FilePath, def.LineStart))
				continue
			}
			hints = append(hints, fmt.Sprintf("Hint: %q is undefined — check imports or spelling", sym))
			continue
		}
		if strings.Contains(e.Message, "cannot use") && strings.Contains(e.Message, "as") {
			hints = append(hints, "Hint: type mismatch — check interface implementation or type assertion")
		}
		if strings.Contains(e.Message, "imported and not used") {
			hints = append(hints, "Hint: remove unused import")
		}
	}
	if len(hints) == 0 {
		return ""
	}
	return "\n" + strings.Join(hints, "\n")
}

func extractUndefinedSymbol(msg string) string {
	idx := strings.Index(msg, "undefined:")
	if idx < 0 {
		return ""
	}
	sym := strings.TrimSpace(msg[idx+len("undefined:"):])
	if dotIdx := strings.LastIndex(sym, "."); dotIdx >= 0 {
		sym = sym[dotIdx+1:]
	}
	return sym
}

// FormatBehavioralRegressionFeedback builds a structured feedback message for
// the AI agent when L2.5 detects output-level behavioral regressions.
// These are tests that still pass but produce different output — the most
// dangerous kind of hidden regression.
func FormatBehavioralRegressionFeedback(regressions []OutputRegression) message.Message {
	var sb strings.Builder

	criticalCount := 0
	warningCount := 0
	perfCount := 0
	for _, r := range regressions {
		switch r.Severity {
		case "critical":
			criticalCount++
		case "warning":
			warningCount++
		case "perf":
			perfCount++
		}
	}

	sb.WriteString("[L2.5 Behavioral Regression Detected]\n")
	sb.WriteString(fmt.Sprintf("%d behavioral change(s) detected after your edit", len(regressions)))
	if warningCount > 0 {
		sb.WriteString(fmt.Sprintf(" (%d output change(s) in passing tests)", warningCount))
	}
	sb.WriteString(".\n\n")

	for i, r := range regressions {
		sb.WriteString(fmt.Sprintf("%d. %s", i+1, r.TestName))
		if r.Package != "" {
			sb.WriteString(fmt.Sprintf(" (%s)", r.Package))
		}
		sb.WriteByte('\n')

		switch r.Field {
		case "exit_code":
			sb.WriteString(fmt.Sprintf("   exit code: %s → %s\n", r.BaselineValue, r.CurrentValue))
		case "stdout":
			sb.WriteString(fmt.Sprintf("   stdout hash: %s → %s (test still passes, but behavior changed)\n",
				r.BaselineValue, r.CurrentValue))
		case "stderr":
			sb.WriteString(fmt.Sprintf("   stderr hash: %s → %s\n", r.BaselineValue, r.CurrentValue))
		case "duration":
			sb.WriteString(fmt.Sprintf("   duration: %s → %s (>3x slowdown)\n", r.BaselineValue, r.CurrentValue))
		}

		switch r.Severity {
		case "critical":
			sb.WriteString("   Severity: CRITICAL\n")
		case "warning":
			sb.WriteString("   Severity: WARNING — output changed while test still passes\n")
		case "perf":
			sb.WriteString("   Severity: PERFORMANCE\n")
		}
		sb.WriteByte('\n')
	}

	if warningCount > 0 {
		sb.WriteString("Tests with changed output may indicate unintended side effects. Consider reverting or verifying the behavioral change is expected.\n")
	}
	if perfCount > 0 {
		sb.WriteString("Performance regressions (>3x baseline duration) detected. Check for algorithmic complexity changes.\n")
	}

	return message.Message{
		Role:    message.RoleUser,
		Content: []message.ContentBlock{message.NewTextBlock(sb.String())},
	}
}

// RelatedChangeHint generates a hint linking test failures to files modified
// in the current turn (same directory = likely related).
func RelatedChangeHint(failures []TestFailure, modifiedFiles []string, workDir string) string {
	var hints []string
	for _, f := range failures {
		for _, mf := range modifiedFiles {
			rel, err := filepath.Rel(workDir, mf)
			if err != nil {
				continue
			}
			// f.Package is a Go package path and therefore always uses forward
			// slashes, while filepath.Dir yields the native separator. Comparing
			// them raw makes this never match on Windows, so the hint linking a
			// failing test to the file just modified simply stops appearing —
			// no error, just a worse prompt.
			mfDir := filepath.ToSlash(filepath.Dir(rel))
			if mfDir == f.Package || "./"+mfDir+"/..." == f.Package {
				hints = append(hints, fmt.Sprintf(
					"  Related: You modified %s in this turn (same package as failing %s)",
					filepath.Base(mf), f.TestName))
				break
			}
		}
	}
	if len(hints) == 0 {
		return ""
	}
	return "\n" + strings.Join(hints, "\n")
}
