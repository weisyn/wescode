package engine

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// historyContextSource provides session edit history as context.
// Tracks files modified by the AI Agent (via editEngine) and exposes them
// as a searchable context source with git diff summaries.
//
// This gives the Agent awareness of "what I did before in this session" —
// critical for multi-turn iterative workflows (edit → test → fix cycle).
type historyContextSource struct {
	getWorkspace   func() string
	getModified    func() []string
	getRecentEdits func() []recentEditRecord
	getAllRoots    func() []string

	mu         sync.Mutex
	sessionLog []sessionEditEntry
}

type sessionEditEntry struct {
	Path      string
	Timestamp time.Time
}

type recentEditRecord struct {
	Path      string
	Timestamp int64
	StartLine int
	EndLine   int
}

func (s *historyContextSource) ID() string { return "history" }

func (s *historyContextSource) Info() ContextSourceInfo {
	return ContextSourceInfo{
		ID: "history", Label: "修改历史", Icon: "history",
		Searchable: true, Available: s.getWorkspace() != "",
	}
}

// RecordModification adds a file to the session log. Called from PostCallHook
// when write/edit/apply_patch tools complete.
func (s *historyContextSource) RecordModification(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionLog = append(s.sessionLog, sessionEditEntry{
		Path:      path,
		Timestamp: time.Now(),
	})
	if len(s.sessionLog) > 100 {
		s.sessionLog = s.sessionLog[len(s.sessionLog)-100:]
	}
}

func (s *historyContextSource) Search(_ context.Context, query string, limit int) ([]ContextSearchItem, error) {
	if limit <= 0 {
		limit = 20
	}
	query = strings.ToLower(strings.TrimSpace(query))
	ws := s.getWorkspace()

	// Merge: editEngine modified files + session log (deduplicated, most recent first)
	seen := make(map[string]struct{})
	var items []ContextSearchItem

	// Current Run modifications (editEngine)
	for _, path := range s.getModified() {
		if query != "" && !strings.Contains(strings.ToLower(filepath.Base(path)), query) {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		rel := relativePath(ws, path)
		items = append(items, ContextSearchItem{
			ID:       "hist:" + path,
			SourceID: "history",
			Label:    rel,
			Detail:   "当前 Run 修改",
			Icon:     "edit",
		})
		if len(items) >= limit {
			return items, nil
		}
	}

	// Session log (accumulated across runs in this session)
	s.mu.Lock()
	logCopy := make([]sessionEditEntry, len(s.sessionLog))
	copy(logCopy, s.sessionLog)
	s.mu.Unlock()

	for i := len(logCopy) - 1; i >= 0; i-- {
		entry := logCopy[i]
		if _, ok := seen[entry.Path]; ok {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(filepath.Base(entry.Path)), query) {
			continue
		}
		seen[entry.Path] = struct{}{}
		rel := relativePath(ws, entry.Path)
		ago := time.Since(entry.Timestamp).Round(time.Second)
		items = append(items, ContextSearchItem{
			ID:       "hist:" + entry.Path,
			SourceID: "history",
			Label:    rel,
			Detail:   fmt.Sprintf("修改于 %s 前", ago),
			Icon:     "history",
		})
		if len(items) >= limit {
			break
		}
	}

	return items, nil
}

func (s *historyContextSource) Resolve(ctx context.Context, itemID string) (*ContextResolved, error) {
	path := strings.TrimPrefix(itemID, "hist:")
	if path == "" {
		return &ContextResolved{ID: itemID, Content: ""}, nil
	}

	ws := s.getWorkspace()
	if ws == "" {
		return &ContextResolved{ID: itemID, Content: ""}, nil
	}

	diff := s.gitDiffForFile(ctx, ws, path)
	if diff == "" {
		return &ContextResolved{
			ID:       itemID,
			FilePath: path,
			Content:  fmt.Sprintf("[%s 已修改，但无 diff（可能未 commit 前的新文件）]", relativePath(ws, path)),
		}, nil
	}

	rel := relativePath(ws, path)
	content := fmt.Sprintf("## 修改历史: %s\n\n```diff\n%s\n```", rel, diff)

	return &ContextResolved{
		ID:       itemID,
		FilePath: path,
		Content:  content,
	}, nil
}

func (s *historyContextSource) gitDiffForFile(ctx context.Context, workDir, path string) string {
	// Try unstaged diff first, then staged
	for _, args := range [][]string{
		{"diff", "--no-color", "-U3", "--", path},
		{"diff", "--staged", "--no-color", "-U3", "--", path},
	} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = workDir
		out, err := cmd.Output()
		if err == nil && len(out) > 0 {
			result := string(out)
			if len(result) > 4000 {
				result = result[:4000] + "\n... (truncated)"
			}
			return result
		}
	}
	return ""
}

func relativePath(workspace, path string) string {
	if rel, err := filepath.Rel(workspace, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return filepath.Base(path)
}
