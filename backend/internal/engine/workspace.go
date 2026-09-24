package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/weisyn/wescode/internal/codeintel"
	"github.com/weisyn/wescode/internal/verification"
	wesgine "github.com/weisyn/wesgine"
)

func (s *Service) Workspace() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workspace
}

// DebugStatus returns the current state of all subsystems for diagnostics.
func (s *Service) DebugStatus(ctx context.Context) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()

	status := map[string]any{
		"initialized": s.initialized,
		"configMode":  s.configMode,
		"workspace":   s.workspace,
	}

	if s.codeAsm != nil {
		state := s.codeAsm.EditorState()
		status["editorState"] = map[string]any{
			"focusFile":  state.FocusFile,
			"cursorLine": state.CursorLine,
		}
	} else {
		status["editorState"] = nil
	}

	if s.codeIndex != nil {
		count, _ := s.codeIndex.SymbolCount(ctx)
		status["codeIndex"] = map[string]any{
			"symbolCount": count,
		}
	} else {
		status["codeIndex"] = nil
	}

	if s.editEngine != nil {
		status["editEngine"] = map[string]any{
			"modifiedFiles": s.editEngine.ModifiedFiles(),
		}
	} else {
		status["editEngine"] = nil
	}

	if s.qualityGate != nil {
		status["qualityGate"] = map[string]any{
			"configured": true,
			"metrics":    s.qualityGate.MetricsSnapshot(),
		}
	}

	if s.constraintReg != nil {
		active := s.constraintReg.Active()
		conflicts := s.constraintReg.DetectConflicts()
		status["constraints"] = map[string]any{
			"total":     s.constraintReg.Count(),
			"active":    len(active),
			"conflicts": len(conflicts),
		}
	}

	if s.codeAsm != nil && s.codeAsm.Metrics() != nil {
		status["retrievalMetrics"] = s.codeAsm.Metrics().Snapshot()
	}

	if s.codeintelCap != nil {
		status["capabilities"] = s.codeintelCap.Status()
	}

	if s.tcrTracker != nil {
		status["tcr"] = s.tcrTracker.Snapshot()
	}

	return status
}

// ReadinessStatus returns the readiness state of all CSE primitives.
func (s *Service) ReadinessStatus(ctx context.Context) map[string]any {
	s.mu.Lock()
	codeIdx := s.codeIndex
	reg := s.constraintReg
	qg := s.qualityGate
	ws := s.workspace
	s.mu.Unlock()

	result := map[string]any{}

	if codeIdx != nil {
		result["ckg"] = codeIdx.Readiness()
	}
	if reg != nil {
		result["constraint"] = reg.Readiness()
	}
	if qg != nil {
		if bs := qg.BaselineStore(); bs != nil && ws != "" {
			result["baseline"] = bs.Readiness(ctx, verification.WorkDirHash(ws))
		}
	}

	return result
}

// VerificationMetrics returns a point-in-time snapshot of all verification
// counters (L0-L3, L2.5, baseline, revision). ENG-5: diagnostics are part
// of the feature.
func (s *Service) VerificationMetrics() verification.VerificationStatsSnapshot {
	s.mu.Lock()
	qg := s.qualityGate
	s.mu.Unlock()
	if qg == nil {
		return verification.VerificationStatsSnapshot{}
	}
	return qg.MetricsSnapshot()
}

// GitInfo returns basic SCM information for the current workspace.
type GitInfo struct {
	Branch         string   `json:"branch"`
	HasChanges     bool     `json:"hasChanges"`
	ChangedFiles   []string `json:"changedFiles,omitempty"`
	UntrackedFiles []string `json:"untrackedFiles,omitempty"`
	RemoteURL      string   `json:"remoteUrl,omitempty"`
}

func (s *Service) GetGitInfo(ctx context.Context) (*GitInfo, error) {
	s.mu.Lock()
	workDir := s.workspace
	s.mu.Unlock()
	if workDir == "" {
		return nil, fmt.Errorf("workspace not initialized")
	}

	info := &GitInfo{}

	if out, err := execGit(ctx, workDir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		info.Branch = strings.TrimSpace(out)
	}

	if out, err := execGit(ctx, workDir, "diff", "--name-only", "HEAD"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line != "" {
				info.ChangedFiles = append(info.ChangedFiles, line)
			}
		}
	}

	if out, err := execGit(ctx, workDir, "ls-files", "--others", "--exclude-standard"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line != "" {
				info.UntrackedFiles = append(info.UntrackedFiles, line)
			}
		}
	}

	info.HasChanges = len(info.ChangedFiles) > 0 || len(info.UntrackedFiles) > 0

	if out, err := execGit(ctx, workDir, "config", "--get", "remote.origin.url"); err == nil {
		info.RemoteURL = strings.TrimSpace(out)
	}

	return info, nil
}

func execGit(ctx context.Context, workDir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// GitStage stages files in the workspace git repository.
// Paths are validated to be within the workspace to prevent path traversal.
func (s *Service) GitStage(ctx context.Context, paths []string) error {
	s.mu.Lock()
	workDir := s.workspace
	s.mu.Unlock()
	if workDir == "" {
		return fmt.Errorf("workspace not initialized")
	}
	if len(paths) == 0 {
		return fmt.Errorf("no paths specified")
	}
	for _, p := range paths {
		abs := p
		if !filepath.IsAbs(p) {
			abs = filepath.Join(workDir, p)
		}
		abs = filepath.Clean(abs)
		if !strings.HasPrefix(abs, filepath.Clean(workDir)+string(filepath.Separator)) && abs != filepath.Clean(workDir) {
			return fmt.Errorf("path %q is outside workspace", p)
		}
	}
	args := append([]string{"add", "--"}, paths...)
	_, err := execGit(ctx, workDir, args...)
	return err
}

// GitCommit creates a commit in the workspace git repository.
func (s *Service) GitCommit(ctx context.Context, message string) error {
	s.mu.Lock()
	workDir := s.workspace
	s.mu.Unlock()
	if workDir == "" {
		return fmt.Errorf("workspace not initialized")
	}
	_, err := execGit(ctx, workDir, "commit", "-m", message)
	return err
}

// ContextSources returns all registered context sources.
func (s *Service) ContextSources() []ContextSource {
	getWS := func() string { s.mu.Lock(); defer s.mu.Unlock(); return s.workspace }
	getAllRoots := func() []string { return s.WorkspaceRoots() }
	return []ContextSource{
		&fileContextSource{getWorkspace: getWS, getAllRoots: getAllRoots},
		&folderContextSource{getWorkspace: getWS, getAllRoots: getAllRoots},
		&symbolContextSource{getIndex: func() *codeintel.CodeIndex { s.mu.Lock(); defer s.mu.Unlock(); return s.codeIndex }},
		&diffContextSource{getWorkspace: getWS, getAllRoots: getAllRoots},
		&diagnosticContextSource{getDiagCache: func() *codeintel.DiagnosticsCache { return s.diagCache }},
		&ckgContextSource{getIndex: func() *codeintel.CodeIndex { s.mu.Lock(); defer s.mu.Unlock(); return s.codeIndex }},
		s.historySource,
	}
}

// SearchContext searches a specific source or all sources for matching items.
func (s *Service) SearchContext(ctx context.Context, sourceID, query string, limit int) ([]ContextSearchItem, error) {
	if limit <= 0 {
		limit = 20
	}
	sources := s.ContextSources()

	if sourceID == "*" {
		var all []ContextSearchItem
		for _, src := range sources {
			info := src.Info()
			if !info.Available || !info.Searchable {
				continue
			}
			items, err := src.Search(ctx, query, 5)
			if err != nil {
				continue
			}
			all = append(all, items...)
		}
		return all, nil
	}

	for _, src := range sources {
		if src.ID() == sourceID {
			return src.Search(ctx, query, limit)
		}
	}
	return nil, fmt.Errorf("unknown context source: %s", sourceID)
}

// ResolveContextItems resolves multiple context items to their content.
func (s *Service) ResolveContextItems(ctx context.Context, items []ContextResolveReq) ([]ContextResolved, error) {
	sources := s.ContextSources()
	sourceMap := make(map[string]ContextSource, len(sources))
	for _, src := range sources {
		sourceMap[src.ID()] = src
	}

	var resolved []ContextResolved
	for _, item := range items {
		src, ok := sourceMap[item.SourceID]
		if !ok {
			continue
		}
		r, err := src.Resolve(ctx, item.ID)
		if err != nil {
			continue
		}
		resolved = append(resolved, *r)
	}
	return resolved, nil
}

// SaveExplorationPaths persists the tool call exploration pattern from a Run
// to code.db exploration_paths table (run trace, not cognitive memory).
func (s *Service) SaveExplorationPaths(ctx context.Context, agentID, sessionID, runID string, paths []string) {
	s.mu.Lock()
	idx := s.codeIndex
	s.mu.Unlock()
	if idx == nil || len(paths) == 0 {
		return
	}
	db := idx.WriterDB()
	if db == nil {
		return
	}

	db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS exploration_paths (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id     TEXT NOT NULL,
		session_id TEXT NOT NULL,
		paths_json TEXT NOT NULL,
		created_at INTEGER NOT NULL DEFAULT 0
	)`)
	db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_exploration_run ON exploration_paths(run_id)`)

	pathsJSON, _ := json.Marshal(paths)
	_, err := db.ExecContext(ctx,
		`INSERT INTO exploration_paths (run_id, session_id, paths_json, created_at) VALUES (?, ?, ?, ?)`,
		runID, sessionID, string(pathsJSON), time.Now().Unix(),
	)
	if err != nil {
		slog.Warn("[codeintel] failed to save exploration paths", "error", err)
	} else {
		slog.Info("[codeintel] exploration paths persisted",
			"runId", runID, "paths", len(paths))
	}
}

// RetrievalMetrics returns the current retrieval quality metrics snapshot.
func (s *Service) RetrievalMetrics() *codeintel.MetricsSnapshot {
	s.mu.Lock()
	asm := s.codeAsm
	s.mu.Unlock()
	if asm == nil || asm.Metrics() == nil {
		return nil
	}
	snap := asm.Metrics().Snapshot()
	return &snap
}

// UpdateEditorState forwards the editor state to the CodeAssembler
// and feeds the AttentionTracker with focus/edit events.
func (s *Service) UpdateEditorState(state codeintel.EditorState) {
	s.mu.Lock()
	asm := s.codeAsm
	at := s.attentionTracker
	s.mu.Unlock()
	if asm != nil {
		asm.UpdateEditorState(state)
	}
	if at != nil && state.FocusFile != "" {
		at.RecordFocus(state.FocusFile)
		for _, edited := range state.RecentEdits {
			if edited.Path == state.FocusFile {
				at.RecordEdit(state.FocusFile)
				break
			}
		}
	}
}

// TopAttentionFiles implements codeintel.AttentionProvider.
func (s *Service) TopAttentionFiles(n int) []codeintel.AttentionFile {
	if s.attentionTracker == nil {
		return nil
	}
	raw := s.attentionTracker.TopN(n)
	out := make([]codeintel.AttentionFile, len(raw))
	for i, r := range raw {
		out[i] = codeintel.AttentionFile{
			Path:       r.Path,
			OpenCount:  r.OpenCount,
			TotalDwell: r.TotalDwell,
			LastActive: r.LastActive,
			WasEdited:  r.WasEdited,
			CoVisited:  r.CoVisited,
			Score:      r.Score,
			Category:   r.Category,
		}
	}
	return out
}

// CoVisitedFiles implements codeintel.AttentionProvider.
func (s *Service) CoVisitedFiles(path string) []string {
	if s.attentionTracker == nil {
		return nil
	}
	return s.attentionTracker.CoVisitedFiles(path)
}

// IndexFile triggers incremental indexing of a single file.
// Also notifies the file watcher so debounced re-indexing picks up further changes.
func (s *Service) IndexFile(ctx context.Context, path string) error {
	s.mu.Lock()
	idx := s.codeIndex
	fw := s.watcher
	s.mu.Unlock()
	if idx == nil {
		return nil
	}
	if fw != nil {
		fw.NotifyChange(path)
	}
	return idx.IndexFile(ctx, path)
}

// NotifyFileChange notifies the file watcher that a file has been modified
// (e.g. from IDE didSave events). The watcher debounces and re-indexes.
// For document files, also notifies DocSync for deletion detection.
func (s *Service) NotifyFileChange(path string) {
	s.mu.Lock()
	fw := s.watcher
	ds := s.docSync
	s.mu.Unlock()
	if fw != nil {
		fw.NotifyChange(path)
	}
	if ds != nil && IsDocFile(path) {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			ds.OnFileDeleted(path)
		}
	}
}

// ConfirmConstraint promotes a constraint to Active (confidence 1.0).
// Called when a user or Agent explicitly confirms an inferred constraint.
func (s *Service) ConfirmConstraint(id string) bool {
	s.mu.Lock()
	reg := s.constraintReg
	s.mu.Unlock()
	if reg == nil {
		return false
	}
	c := reg.Get(id)
	if c == nil {
		return false
	}
	c.Confirm()
	reg.Add(*c)
	slog.Info("[engine] constraint confirmed", "id", id, "rule", c.Rule)
	return true
}

// cleanupLegacyConstraintMemory removes constraint_data and exploration_path
// entries from wes_memories that were written by the pre-refactor code.
// DEV-1: no backward compatibility — constraints now live in code.db.
func (s *Service) cleanupLegacyConstraintMemory() {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return
	}
	mem := cell.Memory()
	if mem == nil {
		return
	}
	ctx := context.Background()

	// Empty actor = admin view (INV-MEM-40). This is a boot-time migration sweep,
	// not a user request: the rows it deletes predate actor stamping, so they are
	// untagged and fail-closed invisible to any real actor — scoping this to
	// "local" would leave them in the DB forever.
	n, err := mem.DeleteByMeta(ctx, string(wesgine.ScopeAgent), "wescode-system", "inject_mode", "explicit", "")
	if err != nil {
		slog.Warn("[engine] legacy constraint memory cleanup failed", "error", err)
	} else if n > 0 {
		slog.Info("[engine] legacy constraint_data cleaned from memory", "deleted", n)
	}

	n2, err := mem.DeleteByMeta(ctx, string(wesgine.ScopeAgent), "", "source", "exploration_path", "")
	if err != nil {
		slog.Warn("[engine] legacy exploration_path memory cleanup failed", "error", err)
	} else if n2 > 0 {
		slog.Info("[engine] legacy exploration_path cleaned from memory", "deleted", n2)
	}
}

// PersistConstraints saves the current constraint registry to code.db.
// Uses atomic singleflight: concurrent calls coalesce into one persist.
// Safe to call from any goroutine — spawns its own goroutine internally.
func (s *Service) PersistConstraints() {
	if !atomic.CompareAndSwapInt32(&s.persistPending, 0, 1) {
		return
	}
	go func() {
		defer atomic.StoreInt32(&s.persistPending, 0)
		s.mu.Lock()
		reg := s.constraintReg
		idx := s.codeIndex
		s.mu.Unlock()
		if reg == nil || idx == nil {
			return
		}
		db := idx.WriterDB()
		if db == nil {
			return
		}
		if err := reg.SaveToSQLite(db); err != nil {
			slog.Warn("[engine] constraint persistence failed", "error", err)
		}
	}()
}

// LoadPersistedConstraints loads previously saved constraints from code.db.
func (s *Service) LoadPersistedConstraints() {
	s.mu.Lock()
	reg := s.constraintReg
	idx := s.codeIndex
	s.mu.Unlock()
	if reg == nil || idx == nil {
		return
	}
	db := idx.DB()
	if db == nil {
		return
	}
	if err := reg.LoadFromSQLite(db); err != nil {
		slog.Warn("[engine] constraint load from code.db failed", "error", err)
	} else {
		slog.Info("[engine] constraints loaded from code.db",
			"total", reg.Count(), "active", len(reg.Active()))
	}
}

// learnFromRegressions persists regression information as Memory L4 correction
// entries.
//
// It deliberately does not create constraints. A regression detected from test
// output alone carries no edited symbol and no file — there is nothing to bind a
// checker to, so a constraint minted here would be a prose sentence the executor
// can never evaluate (INV-CSE-17). The Memory correction is the right sink for
// that knowledge. The constraint path for regressions is
// verification.LearnFromRegression, which runs with EditInfo and can pin the
// changed symbol's arity.
func (s *Service) learnFromRegressions(regressions []verification.RegressionInfo) {
	atomic.StoreInt32(&s.regressionDetected, 1)

	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return
	}
	mem := cell.Memory()
	if mem == nil {
		return
	}

	for _, r := range regressions {
		content := fmt.Sprintf("Modifying code caused test regression: %s (package: %s) failed after passing in baseline. Be careful when modifying this area.",
			r.TestName, r.Package)
		// Key on the test name: the same test regressing twice is one standing
		// fact, not two. Without it the package name rides in Content and each
		// re-run mints a near-identical row. actor is a column, not a metadata
		// key — L4 refuses an ownerless row outright (INV-MEM-38/48).
		_, err := mem.SaveToLayer(context.Background(), wesgine.MemoryWrite{
			Layer:     wesgine.LayerAgentMemory,
			Namespace: regressionNamespace,
			Actor:     memoryActor,
			Kind:      string(wesgine.MemoryKindCorrection),
			Key:       "regression:" + r.TestName,
			Content:   content,
			Metadata: map[string]string{
				"source":    "regression_detection",
				"test_name": r.TestName,
				"package":   r.Package,
				"category":  classifyRegressionFromOutput(r.CurrentOutput),
			},
		})
		if err != nil {
			slog.Warn("[engine] failed to save regression correction", "test", r.TestName, "error", err)
		} else {
			slog.Info("[engine] regression correction saved to memory", "test", r.TestName)
		}
	}
}

// classifyRegressionFromOutput labels a regression from its test output. The
// label is metadata on the Memory correction only — it never selects a checker.
func classifyRegressionFromOutput(output string) string {
	lower := strings.ToLower(output)
	switch {
	case containsAny(lower, "nil pointer", "nil dereference", "invalid memory", "panic"):
		return "boundary"
	case containsAny(lower, "timeout", "deadline", "context deadline exceeded"):
		return "performance"
	case containsAny(lower, "type assertion", "cannot convert", "type mismatch"):
		return "type"
	case containsAny(lower, "expected", "got", "want", "assert"):
		return "api"
	case containsAny(lower, "permission denied", "unauthorized", "forbidden"):
		return "security"
	default:
		return "behavior"
	}
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// ListConstraints backs the human constraint panel (constraint/list RPC). This
// is the one surface that carries the Rule prose and the inferrer's evidence —
// a reviewer deciding confirm-or-dismiss needs to read the rule and see what it
// was derived from (INV-CSE-13). Model-facing tools get the checker verdict
// instead and never read Rule (INV-CSE-17).
func (s *Service) ListConstraints() []map[string]any {
	s.mu.Lock()
	reg := s.constraintReg
	s.mu.Unlock()
	if reg == nil {
		return nil
	}
	all := reg.All()
	out := make([]map[string]any, len(all))
	for i, c := range all {
		out[i] = map[string]any{
			"id":         c.ID,
			"rule":       c.Rule,
			"kind":       c.Kind,
			"priority":   c.Priority,
			"status":     c.Status,
			"confidence": c.Confidence,
			"source":     c.Source,
			"root":       c.Root,
			"targetKind": string(c.TargetKind),
			"targetPath": c.TargetPath,
			"checker":    string(c.Checker.Kind),
			"evidence":   c.Evidence,
		}
	}
	return out
}

// DismissConstraint retires a constraint (user explicitly rejects it).
func (s *Service) DismissConstraint(id string) bool {
	s.mu.Lock()
	reg := s.constraintReg
	s.mu.Unlock()
	if reg == nil {
		return false
	}
	if reg.DismissByID(id) {
		s.PersistConstraints()
		return true
	}
	return false
}
