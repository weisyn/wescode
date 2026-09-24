package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/weisyn/wescode/internal/codeintel/constraints"
	"github.com/weisyn/wescode/internal/editengine"
	"github.com/weisyn/wescode/internal/treesitter"
	"github.com/weisyn/wesgine/tool"
)

// ConstraintHitRecord tracks which constraints were referenced during a Run.
// Collected by PreWriteCheck, consumed by the engine to drive Promote/Demote
// learning after the Run completes (anti-fragile feedback loop).
type ConstraintHitRecord struct {
	ConstraintID string
	FilePath     string
	Warned       bool // warning was emitted to the Agent
}

// ConstraintHitTracker accumulates constraint hits across a Run.
type ConstraintHitTracker struct {
	mu   sync.Mutex
	hits []ConstraintHitRecord
}

// Record adds a constraint hit.
func (t *ConstraintHitTracker) Record(id, path string, warned bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.hits = append(t.hits, ConstraintHitRecord{
		ConstraintID: id,
		FilePath:     path,
		Warned:       warned,
	})
}

// Drain returns all accumulated hits and resets the tracker.
func (t *ConstraintHitTracker) Drain() []ConstraintHitRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.hits
	t.hits = nil
	return out
}

// PreWriteCheckOpts configures optional components for PreWriteCheck.
type PreWriteCheckOpts struct {
	Constraints *constraints.Registry
	Buffers     BufferProvider        // dirty editor buffers for freshness (CI-26)
	HitTracker  *ConstraintHitTracker // tracks which constraints were referenced (learning loop)
}

// NewPreWriteCheck returns a paired PreCallHook + PostCallHook that checks
// for duplicate symbols, impact analysis, and constraint violations around
// write/edit/apply_patch execution.
//
// The PreCallHook collects CKG + constraint warnings (CI-23: never blocks).
// The PostCallHook appends collected warnings to the ToolResult so the
// Agent sees them in the next reasoning turn.
func NewPreWriteCheck(ci *CodeIndex, ts *treesitter.ParserPool, opts ...PreWriteCheckOpts) (tool.PreCallHook, tool.PostCallHook) {
	var opt PreWriteCheckOpts
	if len(opts) > 0 {
		opt = opts[0]
	}
	var mu sync.Mutex
	pending := map[string][]string{} // call.ID -> warnings

	pre := func(ctx context.Context, call tool.ToolCall, tc *tool.ToolContext) (*tool.ToolResult, error) {
		if call.Name != "write" && call.Name != "edit" && call.Name != "apply_patch" {
			return nil, nil
		}
		if ci == nil || ts == nil {
			return nil, nil
		}

		// INV-HOOK-01: global timeout prevents blocking tool execution.
		checkCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()

		var warnings []string

		// CKG checks (FindSimilar / ImpactAnalysis) require index readiness.
		// CI-24: greenfield Day 1 — skip CKG when index barely started.
		r := ci.Readiness()
		if r.Completeness >= ReadinessLow {
			switch call.Name {
			case "write":
				warnings = collectWriteWarnings(checkCtx, ci, ts, call, tc, opt.Buffers)
			case "edit":
				warnings = collectEditWarnings(checkCtx, ci, ts, call, tc, opt.Buffers)
			case "apply_patch":
				warnings = collectPatchWarnings(checkCtx, ci, ts, call, tc)
			}
		}

		// Constraint checks always run — they are filesystem-based (inferred
		// from directory structure), not CKG-dependent. Even greenfield projects
		// benefit from security/naming constraints inferred at boot.
		if opt.Constraints != nil {
			if cw := checkConstraints(call, tc, opt.Constraints, opt.HitTracker); len(cw) > 0 {
				warnings = append(warnings, cw...)
			}
		}

		if len(warnings) > 0 {
			mu.Lock()
			pending[call.ID] = warnings
			mu.Unlock()
		}

		return nil, nil // CI-23: never block
	}

	post := func(_ context.Context, call tool.ToolCall, result *tool.ToolResult, _ *tool.ToolContext) {
		mu.Lock()
		warnings, ok := pending[call.ID]
		delete(pending, call.ID)
		mu.Unlock()

		if !ok || len(warnings) == 0 || result == nil || result.IsError {
			return
		}

		// CI-23: split warnings by confidence level.
		// High-confidence warnings (prefixed with "[H]") get injected into
		// the tool result content (AI sees them in next reasoning turn).
		// Low-confidence warnings (prefixed with "[L]") go to metadata only
		// (logged, not consuming AI tokens on noise).
		var highConf, lowConf []string
		for _, w := range warnings {
			if strings.HasPrefix(w, "[L] ") {
				lowConf = append(lowConf, strings.TrimPrefix(w, "[L] "))
			} else {
				highConf = append(highConf, strings.TrimPrefix(w, "[H] "))
			}
		}

		if result.Metadata == nil {
			result.Metadata = map[string]any{}
		}

		if len(highConf) > 0 {
			result.Content += "\n\n[Advisory]\n" + strings.Join(highConf, "\n")
			result.Metadata["ckg_warnings"] = highConf
		}
		if len(lowConf) > 0 {
			result.Metadata["ckg_low_confidence"] = lowConf
		}
	}

	return pre, post
}

func collectWriteWarnings(ctx context.Context, ci *CodeIndex, ts *treesitter.ParserPool, call tool.ToolCall, tc *tool.ToolContext, buffers BufferProvider) []string {
	var params struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if json.Unmarshal(call.Input, &params) != nil || params.Path == "" {
		return nil
	}

	absPath := resolvePath(params.Path, tc)
	lang, ok := treesitter.DetectLang(absPath)
	if !ok {
		return nil
	}

	// CI-26: if target file has a dirty buffer, do on-demand symbol
	// extraction from the buffer content (not persisted to DB) so that
	// FindSimilar comparisons account for the editor's current state.
	if buffers != nil {
		if dirty := buffers.Get(absPath); dirty != nil {
			refreshTemporarySymbols(ctx, ci, absPath, dirty)
		}
	}

	tree, err := ts.Parse(lang, []byte(params.Content), nil)
	if err != nil {
		return nil
	}
	defer tree.Close()

	var warnings []string
	symbols := treesitter.ExtractSymbols(lang, tree, []byte(params.Content))
	for _, sym := range symbols {
		if !sym.Exported || (sym.Kind != treesitter.KindFunction && sym.Kind != treesitter.KindMethod && sym.Kind != treesitter.KindType) {
			continue
		}
		warnings = append(warnings, findSimilarWarnings(ctx, ci, sym.Name, absPath)...)
	}
	return warnings
}

func collectEditWarnings(ctx context.Context, ci *CodeIndex, ts *treesitter.ParserPool, call tool.ToolCall, tc *tool.ToolContext, buffers BufferProvider) []string {
	var params struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if json.Unmarshal(call.Input, &params) != nil || params.Path == "" {
		return nil
	}

	absPath := resolvePath(params.Path, tc)
	lang, ok := treesitter.DetectLang(absPath)
	if !ok {
		return nil
	}

	// CI-26: refresh from dirty buffer before CKG queries.
	if buffers != nil {
		if dirty := buffers.Get(absPath); dirty != nil {
			refreshTemporarySymbols(ctx, ci, absPath, dirty)
		}
	}

	var warnings []string

	if len(params.OldString) > 20 {
		oldTree, err := ts.Parse(lang, []byte(params.OldString), nil)
		if err == nil {
			oldSyms := treesitter.ExtractSymbols(lang, oldTree, []byte(params.OldString))
			oldTree.Close()
			for _, sym := range oldSyms {
				if sym.Exported && (sym.Kind == treesitter.KindFunction || sym.Kind == treesitter.KindMethod) {
					warnings = append(warnings, findImpactWarnings(ctx, ci, sym.Name, absPath)...)
				}
			}
		}
	}

	if len(params.NewString) > 20 {
		newTree, err := ts.Parse(lang, []byte(params.NewString), nil)
		if err == nil {
			newSyms := treesitter.ExtractSymbols(lang, newTree, []byte(params.NewString))
			newTree.Close()
			for _, sym := range newSyms {
				if sym.Exported && (sym.Kind == treesitter.KindFunction || sym.Kind == treesitter.KindMethod || sym.Kind == treesitter.KindType) {
					warnings = append(warnings, findSimilarWarnings(ctx, ci, sym.Name, absPath)...)
				}
			}
		}
	}
	return warnings
}

func collectPatchWarnings(ctx context.Context, ci *CodeIndex, _ *treesitter.ParserPool, call tool.ToolCall, tc *tool.ToolContext) []string {
	var params struct {
		Patch   string `json:"patch"`
		BaseDir string `json:"base_dir"`
	}
	if json.Unmarshal(call.Input, &params) != nil || params.Patch == "" {
		return nil
	}

	paths := resolvePatchPaths(params.Patch, params.BaseDir, tc)
	var warnings []string
	for _, p := range paths {
		absPath := p
		if _, ok := treesitter.DetectLang(absPath); !ok {
			continue
		}
		syms, _ := ci.ListFileSymbols(ctx, absPath)
		for _, sym := range syms {
			if sym.Exported && (sym.Kind == "function" || sym.Kind == "method") {
				warnings = append(warnings, findImpactWarnings(ctx, ci, sym.Name, absPath)...)
			}
		}
	}
	return warnings
}

// prewriteHighConfidenceThreshold is the minimum similarity score for a
// warning to be injected into the AI's visible output (CI-23).
// Below this threshold, warnings go to metadata only (CI-23).
const prewriteHighConfidenceThreshold = 0.8

func findSimilarWarnings(ctx context.Context, ci *CodeIndex, name, filePath string) []string {
	results, err := ci.FindSimilar(ctx, name, FindSimilarOpts{Limit: 3, MinScore: 0.4})
	if err != nil || len(results) == 0 {
		return nil
	}
	var relevant []SimilarResult
	for _, r := range results {
		if r.Symbol.FilePath != filePath {
			relevant = append(relevant, r)
		}
	}
	if len(relevant) == 0 {
		return nil
	}

	r := relevant[0]
	pkg := packagePathFromFile(r.Symbol.FilePath)
	w := fmt.Sprintf("- Similar symbol exists: %s (%.0f%% match, pkg: %s) in %s:%d — consider reusing instead of duplicating",
		r.Symbol.Name, r.Score*100, filepath.Base(pkg), r.Symbol.FilePath, r.Symbol.LineStart+1)

	slog.Info("[prewrite] similar symbol exists",
		"symbol", name,
		"similar_to", r.Symbol.Name,
		"file", r.Symbol.FilePath,
		"score", r.Score,
	)

	// CI-23: tag with confidence level.
	if r.Score >= prewriteHighConfidenceThreshold {
		return []string{"[H] " + w}
	}
	return []string{"[L] " + w}
}

func findImpactWarnings(ctx context.Context, ci *CodeIndex, name, filePath string) []string {
	nodes, _, err := ci.ImpactAnalysis(ctx, name, 2)
	if err != nil || len(nodes) == 0 {
		return nil
	}
	var downstream []ImpactNode
	allBound := true
	for _, n := range nodes {
		if n.Symbol.FilePath != filePath || n.Symbol.Name != name {
			downstream = append(downstream, n)
			if !n.Resolution.Bound() {
				allBound = false
			}
		}
	}
	if len(downstream) == 0 {
		return nil
	}

	var parts []string
	for _, d := range downstream {
		if len(parts) >= 3 {
			break
		}
		parts = append(parts, fmt.Sprintf("%s (%s:%d)", d.Symbol.Name, d.Symbol.FilePath, d.Symbol.LineStart+1))
	}
	suffix := ""
	if len(downstream) > 3 {
		suffix = fmt.Sprintf(" and %d more", len(downstream)-3)
	}
	w := fmt.Sprintf("- Modifying %s affects %d downstream symbol(s): %s%s",
		name, len(downstream), strings.Join(parts, ", "), suffix)
	slog.Info("[prewrite] modifying symbol with downstream dependents",
		"symbol", name,
		"dependents", len(downstream),
		"first", downstream[0].Symbol.Name,
		"first_file", downstream[0].Symbol.FilePath,
	)

	// CI-23: [H] only when every downstream node was reached through a bound
	// edge. ImpactAnalysis has a second BFS branch that walks `target_name`
	// where `target_id IS NULL`, so a node can enter this list on a bare name
	// match — same name, different package, no proven call. The predecessor
	// compared a float and its comment claimed impact warnings are "always
	// high-confidence"; both were wrong in the same direction, because
	// unresolved edges carried certainty 1.0 and the [L] branch almost never
	// fired. With ~60% of call edges unbound in a real index, the mixed case is
	// the common case, not the corner.
	if allBound {
		return []string{"[H] " + w}
	}
	return []string{"[L] " + w}
}

func resolvePath(path string, tc *tool.ToolContext) string {
	if tc == nil || filepath.IsAbs(path) {
		return path
	}
	if tc.PrimaryRoot != "" {
		return filepath.Join(tc.PrimaryRoot, path)
	}
	if tc.WorkDir != "" {
		return filepath.Join(tc.WorkDir, path)
	}
	return path
}

// resolvePatchPaths resolves apply_patch header paths exactly like the engine's
// apply_patch tool: against the call's base_dir when present, else the tool
// context root. Consumers must never join patch paths against the workspace
// root alone — base_dir-carrying patches would otherwise resolve to phantom
// paths (e.g. wesclaw.git/wescodeServerChannel.ts when base_dir pointed into
// wescode.git), breaking constraint matching for the file actually written.
func resolvePatchPaths(patch, baseDir string, tc *tool.ToolContext) []string {
	root := ""
	if tc != nil {
		root = tc.PrimaryRoot
		if root == "" {
			root = tc.WorkDir
		}
	}
	raw := editengine.PatchFilePaths(patch)
	paths := make([]string, len(raw))
	for i, p := range raw {
		paths[i] = editengine.ResolvePatchPath(p, baseDir, root)
	}
	return paths
}

// refreshTemporarySymbols does a synchronous on-demand re-index of a single
// file from dirty buffer content (CI-26). This ensures FindSimilar and
// ImpactAnalysis queries see the editor's current state, not stale disk data.
// The indexed data IS persisted (same as PostWriteIndex) because the buffer
// content is more recent than disk — this is a freshness correction, not a
// temporary overlay.
func refreshTemporarySymbols(ctx context.Context, ci *CodeIndex, path string, content []byte) {
	_ = ci.IndexFileFromBuffer(ctx, path, content)
}

// checkConstraints runs every matching constraint's checker against the content
// the tool is about to write and returns one advisory line per FAIL.
//
// Fail-only is the whole point (INV-CSE-16). The pre-refactor version listed
// every constraint whose scope matched — so a clean edit to a well-behaved file
// still came back carrying rules the code already satisfied, teaching the model
// that constraint text is background noise. A PASS now produces no text at all,
// which makes a FAIL mean something.
//
// Content per tool: write ships its own payload; edit reconstructs the post-edit
// text by applying old_string→new_string to the file on disk; apply_patch has no
// cheap way to render the result, so it is silently skipped rather than checked
// against pre-patch content (a stale check is worse than no check).
func checkConstraints(call tool.ToolCall, tc *tool.ToolContext, reg *constraints.Registry, tracker *ConstraintHitTracker) []string {
	absPath, content, ok := prewriteTargetContent(call, tc)
	if !ok {
		return nil
	}

	var warnings []string
	for _, c := range reg.MatchingFile(absPath) {
		failed, msg := constraints.CheckFile(c, absPath, content)
		if !failed {
			continue
		}
		warnings = append(warnings, fmt.Sprintf("[H] - [FAIL %s] %s", c.ID, msg))
		// Only a FAIL is a hit. Recording matches-but-passes would make the
		// RunEnd feedback loop promote constraints on the strength of edits
		// that never engaged them.
		if tracker != nil {
			tracker.Record(c.ID, absPath, true)
		}
		slog.Info("[prewrite] constraint FAIL",
			"id", c.ID,
			"checker", string(c.Checker.Kind),
			"file", absPath,
			"msg", msg,
		)
	}
	return warnings
}

// prewriteTargetContent renders the post-write content of the single file a
// write/edit call targets. apply_patch returns false: multi-file patch
// application is the edit engine's job, not something to re-derive here.
func prewriteTargetContent(call tool.ToolCall, tc *tool.ToolContext) (absPath, content string, ok bool) {
	switch call.Name {
	case "write":
		var params struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if json.Unmarshal(call.Input, &params) != nil || params.Path == "" {
			return "", "", false
		}
		return resolvePath(params.Path, tc), params.Content, true

	case "edit":
		var params struct {
			Path      string `json:"path"`
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		}
		if json.Unmarshal(call.Input, &params) != nil || params.Path == "" {
			return "", "", false
		}
		abs := resolvePath(params.Path, tc)
		disk, err := os.ReadFile(abs)
		if err != nil {
			return "", "", false
		}
		if params.OldString == "" {
			return abs, string(disk), true
		}
		before := string(disk)
		after := strings.Replace(before, params.OldString, params.NewString, 1)
		if after == before {
			// old_string not found: the edit will fail anyway, and checking the
			// unmodified file would blame the user for pre-existing violations.
			return "", "", false
		}
		return abs, after, true
	}
	return "", "", false
}

// RunOutcome describes what happened after a Run completed.
type RunOutcome struct {
	EditAccepted bool // user accepted the AI's edits without changes
	EditRejected bool // user rejected the AI's edits
	EditModified bool // user accepted but modified the AI's edits
}

// ApplyRunEndFeedback adjusts constraint confidence based on the Run outcome
// and the constraint hits recorded during the Run.
//
// Learning loop:
//   - AI obeyed a warned constraint + user accepted → Promote (correct behavior reinforced)
//   - AI was warned but user rejected → Demote slightly (constraint may be wrong)
//   - User modified → Promote slightly (constraint directionally correct)
func ApplyRunEndFeedback(reg *constraints.Registry, tracker *ConstraintHitTracker, outcome RunOutcome) {
	if reg == nil || tracker == nil {
		return
	}

	hits := tracker.Drain()
	if len(hits) == 0 {
		return
	}

	seen := map[string]bool{}
	for _, h := range hits {
		if seen[h.ConstraintID] {
			continue
		}
		seen[h.ConstraintID] = true

		if !h.Warned {
			continue
		}

		switch {
		case outcome.EditAccepted:
			reg.PromoteByID(h.ConstraintID, 0.05)
		case outcome.EditRejected:
			reg.DemoteByID(h.ConstraintID, 0.03)
		case outcome.EditModified:
			reg.PromoteByID(h.ConstraintID, 0.02)
		}

		slog.Info("[prewrite] RunEnd constraint feedback",
			"constraintID", h.ConstraintID,
			"file", h.FilePath,
			"accepted", outcome.EditAccepted,
			"rejected", outcome.EditRejected,
			"modified", outcome.EditModified,
		)
	}
}
