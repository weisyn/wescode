package codeintel

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// RetrieveV2 is the enriched retrieval path that extends the existing Retrieve
// with ContextNeed-driven expansion. When ContextNeed has zero-value enrichment
// fields (NeedsCallers=false, NeedsTests=false, etc.), behavior is identical
// to the original Retrieve (CE-INV-02 non-regression guarantee).
func (r *Retriever) RetrieveV2(ctx context.Context, state EditorState, userMessage string, tokenBudget int, need ContextNeed) []CodeFragment {
	// Baseline: call existing retrieval logic (unchanged).
	fragments := r.deterministicBase(ctx, state, need.TaskType, isFocusFileUserTarget(state.FocusFile, userMessage))
	fragments = append(fragments, r.heuristicExpand(ctx, state, userMessage, need.TaskType)...)

	// ContextNeed-driven extensions (additive only).
	if need.NeedsCallers && state.FocusFile != "" {
		fragments = append(fragments, r.expandCallerDepth(ctx, state, fragments)...)
	}
	if need.NeedsCallees && state.FocusFile != "" {
		fragments = append(fragments, r.expandCalleeDepth(ctx, state, fragments)...)
	}
	if need.NeedsTests && state.FocusFile != "" {
		fragments = append(fragments, r.findRelatedTests(ctx, state)...)
	}
	if need.NeedsSiblings && state.FocusFile != "" {
		fragments = append(fragments, r.findSiblingPatterns(ctx, state)...)
	}
	if need.NeedsHistory && state.FocusFile != "" {
		fragments = append(fragments, r.gitBlameHotspots(ctx, state)...)
	}

	// Universal enrichment: terminal + global diagnostics (all TaskTypes).
	fragments = append(fragments, r.terminalContext(state)...)
	fragments = append(fragments, r.globalDiagnosticsContext(state)...)

	applyProximityBoost(fragments, state.FocusFile)
	applyStrategyWeights(fragments, r.strategy)
	applyCommunityBias(fragments, state.FocusFile, r.strategy, r.index)
	packed := budgetPack(fragments, tokenBudget)

	// Auto-expand (unchanged logic).
	usedTokens := 0
	for _, f := range packed {
		usedTokens += f.TokenCost
	}
	if tokenBudget > 0 && usedTokens < tokenBudget/2 {
		remaining := tokenBudget - usedTokens
		extra := r.autoExpand(ctx, state, packed, remaining)
		packed = append(packed, budgetPack(extra, remaining)...)
	}

	return packed
}

// noFocusFallback provides context when no file is focused in the editor.
// CE-INV-04: must not reference state.FocusFile.
func (r *Retriever) noFocusFallback(ctx context.Context, state EditorState, userMessage string, budget int) []CodeFragment {
	var fragments []CodeFragment

	// 1. Project mind map (always inject if available).
	if mm := r.index.GetMindMap(ctx); mm != nil {
		maxTokens := budget / 3
		if maxTokens > 500 {
			maxTokens = 500
		}
		content := mm.ToContextString(maxTokens)
		if content != "" {
			fragments = append(fragments, CodeFragment{
				Content:    content,
				Kind:       FragmentProjectMap,
				Reason:     "project overview (no active file)",
				TokenCost:  estimateTokens(content),
				ValueScore: 0.5,
			})
		}
	}

	// 2. Symbols mentioned in the user message.
	ids := extractIdentifiers(userMessage)
	limit := 5
	if len(ids) < limit {
		limit = len(ids)
	}
	for _, id := range ids[:limit] {
		syms, _ := r.index.FindSymbol(ctx, id)
		symLimit := 2
		if len(syms) < symLimit {
			symLimit = len(syms)
		}
		for _, sym := range syms[:symLimit] {
			content := r.readLines(sym.FilePath, sym.LineStart, sym.LineEnd)
			if content == "" {
				continue
			}
			fragments = append(fragments, CodeFragment{
				Path:       sym.FilePath,
				StartLine:  sym.LineStart,
				EndLine:    sym.LineEnd,
				Content:    content,
				Kind:       FragmentRelevantCode,
				Symbol:     sym.Name,
				Reason:     "symbol mentioned in message",
				TokenCost:  estimateTokens(content),
				ValueScore: 0.7,
			})
		}
	}

	// 3. Terminal errors (if present).
	fragments = append(fragments, r.terminalContext(state)...)

	// 4. Global diagnostics.
	fragments = append(fragments, r.globalDiagnosticsContext(state)...)

	return budgetPack(fragments, budget)
}

// terminalContext injects recent terminal output when it contains build errors.
// CE-INV-06: hard cap at 1500 characters.
func (r *Retriever) terminalContext(state EditorState) []CodeFragment {
	if state.TerminalSnapshot == "" {
		return nil
	}
	if !hasBuildError(state.TerminalSnapshot) && !hasTestFailure(state.TerminalSnapshot) {
		return nil
	}

	snapshot := state.TerminalSnapshot
	if len(snapshot) > 1500 {
		snapshot = snapshot[len(snapshot)-1500:]
	}

	return []CodeFragment{{
		Content:    "[Recent Terminal Output]\n" + snapshot,
		Kind:       FragmentTerminal,
		Reason:     "terminal build/test error detected",
		TokenCost:  estimateTokens(snapshot) + 10,
		ValueScore: 0.75,
	}}
}

// globalDiagnosticsContext injects project-wide error-level diagnostics.
// CE-INV-05: max 10 entries, each < 200 chars.
func (r *Retriever) globalDiagnosticsContext(state EditorState) []CodeFragment {
	if len(state.GlobalErrors) == 0 {
		return nil
	}

	var sb strings.Builder
	sb.WriteString("[Project Build Errors]\n")
	limit := 10
	if len(state.GlobalErrors) < limit {
		limit = len(state.GlobalErrors)
	}
	for i := 0; i < limit; i++ {
		e := state.GlobalErrors[i]
		line := fmt.Sprintf("  %s:%d: %s", filepath.Base(e.File), e.Line, e.Message)
		if len(line) > 200 {
			line = line[:197] + "..."
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}

	content := sb.String()
	return []CodeFragment{{
		Content:    content,
		Kind:       FragmentGlobalDiag,
		Reason:     "project-wide build errors",
		TokenCost:  estimateTokens(content),
		ValueScore: 0.7,
	}}
}

// expandCallerDepth adds 2nd-level callers (callers of callers) for symbols
// already in fragments. Triggered by NeedsCallers=true (diagnostic signal,
// async/interface selection).
func (r *Retriever) expandCallerDepth(ctx context.Context, state EditorState, existing []CodeFragment) []CodeFragment {
	included := make(map[string]bool)
	for _, f := range existing {
		if f.Symbol != "" {
			included[f.Symbol] = true
		}
	}

	var extra []CodeFragment
	for _, f := range existing {
		if f.Kind != FragmentSignature || f.Symbol == "" {
			continue
		}
		callers, _ := r.index.SearchSymbols(ctx, f.Symbol, 10)
		for _, sym := range callers {
			if included[sym.Name] || sym.FilePath == state.FocusFile {
				continue
			}
			extra = append(extra, CodeFragment{
				Path:       sym.FilePath,
				StartLine:  sym.LineStart,
				EndLine:    sym.LineEnd,
				Content:    sym.Signature,
				Kind:       FragmentSignature,
				Symbol:     sym.Name,
				Reason:     "2nd-level caller of " + f.Symbol,
				TokenCost:  estimateTokens(sym.Signature),
				ValueScore: 0.4,
			})
			included[sym.Name] = true
			if len(extra) >= 10 {
				return extra
			}
		}
	}
	return extra
}

// expandCalleeDepth adds 2nd-level callees (functions called by callees) for
// symbols already in fragments. Triggered by NeedsCallees=true (async context,
// struct definition selection).
func (r *Retriever) expandCalleeDepth(ctx context.Context, state EditorState, existing []CodeFragment) []CodeFragment {
	included := make(map[string]bool)
	for _, f := range existing {
		if f.Symbol != "" {
			included[f.Symbol] = true
		}
	}

	var extra []CodeFragment
	for _, f := range existing {
		if !strings.Contains(f.Reason, "call chain") || f.Symbol == "" {
			continue
		}
		callees, _ := r.index.SearchSymbols(ctx, f.Symbol, 10)
		for _, sym := range callees {
			if included[sym.Name] || sym.FilePath == state.FocusFile {
				continue
			}
			extra = append(extra, CodeFragment{
				Path:       sym.FilePath,
				StartLine:  sym.LineStart,
				EndLine:    sym.LineEnd,
				Content:    sym.Signature,
				Kind:       FragmentSignature,
				Symbol:     sym.Name,
				Reason:     "2nd-level callee via " + f.Symbol,
				TokenCost:  estimateTokens(sym.Signature),
				ValueScore: 0.35,
			})
			included[sym.Name] = true
			if len(extra) >= 10 {
				return extra
			}
		}
	}
	return extra
}

// findRelatedTests searches for test files/functions related to the focus file.
func (r *Retriever) findRelatedTests(ctx context.Context, state EditorState) []CodeFragment {
	if state.FocusFile == "" {
		return nil
	}

	dir := filepath.Dir(state.FocusFile)
	base := filepath.Base(state.FocusFile)
	ext := filepath.Ext(base)
	nameNoExt := strings.TrimSuffix(base, ext)

	// Common test file patterns across languages.
	testPatterns := []string{
		nameNoExt + "_test" + ext, // Go: foo_test.go
		nameNoExt + ".test" + ext, // JS/TS: foo.test.ts
		nameNoExt + "_spec" + ext, // Ruby: foo_spec.rb
		"test_" + nameNoExt + ext, // Python: test_foo.py
	}

	var fragments []CodeFragment
	for _, pattern := range testPatterns {
		testPath := filepath.Join(dir, pattern)
		content, err := r.readFile(testPath)
		if err != nil || len(content) == 0 {
			continue
		}
		// Inject first 50 lines (test structure overview).
		lines := strings.Split(string(content), "\n")
		end := 50
		if len(lines) < end {
			end = len(lines)
		}
		snippet := strings.Join(lines[:end], "\n")
		fragments = append(fragments, CodeFragment{
			Path:       testPath,
			StartLine:  0,
			EndLine:    end - 1,
			Content:    snippet,
			Kind:       FragmentRelatedTest,
			Reason:     "test file for " + base,
			TokenCost:  estimateTokens(snippet),
			ValueScore: 0.5,
		})
		break // one test file is enough
	}

	return fragments
}

// findSiblingPatterns finds functions/types in the same directory with similar
// kind to the cursor function, providing "how others do it" context.
func (r *Retriever) findSiblingPatterns(ctx context.Context, state EditorState) []CodeFragment {
	if state.FocusFile == "" {
		return nil
	}

	// List all open tabs in the same directory as "sibling" candidates.
	dir := filepath.Dir(state.FocusFile)
	var siblingFiles []string
	for _, f := range state.OpenFiles {
		if f != state.FocusFile && filepath.Dir(f) == dir {
			siblingFiles = append(siblingFiles, f)
		}
	}

	var fragments []CodeFragment
	for _, sibFile := range siblingFiles {
		if len(fragments) >= 5 {
			break
		}
		syms, _ := r.index.ListFileSymbols(ctx, sibFile)
		for _, sym := range syms {
			if sym.Kind != "function" && sym.Kind != "method" {
				continue
			}
			if sym.Signature == "" {
				continue
			}
			fragments = append(fragments, CodeFragment{
				Path:       sym.FilePath,
				StartLine:  sym.LineStart,
				EndLine:    sym.LineEnd,
				Content:    sym.Signature,
				Kind:       FragmentSiblingImpl,
				Symbol:     sym.Name,
				Reason:     "sibling implementation pattern",
				TokenCost:  estimateTokens(sym.Signature),
				ValueScore: 0.35,
			})
			if len(fragments) >= 5 {
				break
			}
		}
	}

	return fragments
}

// gitBlameHotspots shows which parts of the focus file were recently modified.
func (r *Retriever) gitBlameHotspots(ctx context.Context, state EditorState) []CodeFragment {
	if state.FocusFile == "" {
		return nil
	}

	workDir := filepath.Dir(state.FocusFile)
	if len(state.WorkspaceRoots) > 0 {
		workDir = state.WorkspaceRoots[0]
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "git", "log", "--format=%H", "--since=7 days ago", "--follow", "--", state.FocusFile)
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return nil
	}

	commits := strings.Split(strings.TrimSpace(string(out)), "\n")
	summary := fmt.Sprintf("[Git History: %s]\n%d commits in last 7 days", filepath.Base(state.FocusFile), len(commits))

	return []CodeFragment{{
		Content:    summary,
		Kind:       FragmentGitBlame,
		Reason:     "recent modification history",
		TokenCost:  estimateTokens(summary),
		ValueScore: 0.3,
	}}
}
