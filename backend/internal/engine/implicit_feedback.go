package engine

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	wesgine "github.com/weisyn/wesgine"
)

// ImplicitFeedbackEvent is pushed by the Extension when it detects
// user behavior that implies dissatisfaction with an AI edit.
type ImplicitFeedbackEvent struct {
	Type        string `json:"type"` // "undo_immediate" | "post_accept_tweak" | "reject_then_write"
	File        string `json:"file"`
	Lang        string `json:"lang"`
	AIVersion   string `json:"aiVersion"`   // what AI wrote
	UserVersion string `json:"userVersion"` // what user wrote instead (for tweak/reject)
	Line        int    `json:"line"`
	Timestamp   int64  `json:"timestamp"`
}

// ImplicitFeedbackHandler processes implicit feedback events and writes
// correction entries to wesgine Memory L4 (ScopeAgent).
type ImplicitFeedbackHandler struct {
	mu         sync.Mutex
	recentKeys map[string]time.Time // dedup: content hash → last write time
	maxRecent  int
}

func NewImplicitFeedbackHandler() *ImplicitFeedbackHandler {
	return &ImplicitFeedbackHandler{
		recentKeys: make(map[string]time.Time),
		maxRecent:  200,
	}
}

// Handle processes an implicit feedback event and writes a correction to memory.
func (h *ImplicitFeedbackHandler) Handle(ctx context.Context, cell *wesgine.Cell, actor string, event ImplicitFeedbackEvent) {
	if cell == nil {
		return
	}
	mem := cell.Memory()
	if mem == nil {
		return
	}

	content := h.formatCorrection(event)
	if content == "" {
		return
	}

	// Dedup: skip if same content was written recently
	if h.isDuplicate(content) {
		slog.Debug("[implicit_feedback] skipped duplicate", "type", event.Type, "file", event.File)
		return
	}

	// actor rides the Actor field, not Metadata: L4 ownership is a column, and
	// a metadata key of the same name is read by nobody (INV-MEM-48). No Key —
	// two corrections of the same type on the same file are genuinely different
	// facts (different snippets), so the content hash is the right identity and
	// matches what isDuplicate already collapses.
	_, err := mem.SaveToLayer(ctx, wesgine.MemoryWrite{
		Layer:     wesgine.LayerAgentMemory,
		Namespace: correctionNamespace,
		Actor:     actor,
		Kind:      "correction",
		Content:   content,
		Metadata: map[string]string{
			"source":    "implicit_feedback",
			"type":      event.Type,
			"file_lang": event.Lang,
		},
	})
	if err != nil {
		slog.Warn("[implicit_feedback] failed to save correction", "error", err, "type", event.Type)
		return
	}

	slog.Info("[implicit_feedback] correction saved",
		"type", event.Type, "file", event.File, "lang", event.Lang)
}

func (h *ImplicitFeedbackHandler) formatCorrection(event ImplicitFeedbackEvent) string {
	switch event.Type {
	case "undo_immediate":
		if event.AIVersion == "" {
			return ""
		}
		aiSnippet := truncateSnippet(event.AIVersion, 200)
		return fmt.Sprintf("User immediately undid AI edit in %s file. The AI wrote:\n```\n%s\n```\nThis was wrong — avoid this pattern in similar contexts.",
			event.Lang, aiSnippet)

	case "post_accept_tweak":
		if event.AIVersion == "" || event.UserVersion == "" {
			return ""
		}
		aiSnippet := truncateSnippet(event.AIVersion, 150)
		userSnippet := truncateSnippet(event.UserVersion, 150)
		return fmt.Sprintf("User accepted AI edit but immediately tweaked it in %s file.\nAI wrote: `%s`\nUser changed to: `%s`\nPrefer the user's version in similar contexts.",
			event.Lang, aiSnippet, userSnippet)

	case "reject_then_write":
		if event.UserVersion == "" {
			return ""
		}
		userSnippet := truncateSnippet(event.UserVersion, 200)
		if event.AIVersion != "" {
			aiSnippet := truncateSnippet(event.AIVersion, 150)
			return fmt.Sprintf("User rejected AI edit and wrote their own code in %s file.\nAI suggested: `%s`\nUser wrote instead: `%s`\nFollow the user's approach in similar contexts.",
				event.Lang, aiSnippet, userSnippet)
		}
		return fmt.Sprintf("User rejected AI edit and wrote their own code in %s file: `%s`. Follow this style.",
			event.Lang, userSnippet)

	default:
		return ""
	}
}

func (h *ImplicitFeedbackHandler) isDuplicate(content string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	key := simpleHash(content)
	if t, exists := h.recentKeys[key]; exists && time.Since(t) < 10*time.Minute {
		return true
	}

	h.recentKeys[key] = time.Now()

	// Prune old entries
	if len(h.recentKeys) > h.maxRecent {
		cutoff := time.Now().Add(-10 * time.Minute)
		for k, t := range h.recentKeys {
			if t.Before(cutoff) {
				delete(h.recentKeys, k)
			}
		}
	}
	return false
}

func simpleHash(s string) string {
	// Fast dedup hash: first 100 chars normalized
	norm := strings.TrimSpace(s)
	if len(norm) > 100 {
		runes := []rune(norm)
		if len(runes) > 100 {
			norm = string(runes[:100])
		}
	}
	return norm
}

func truncateSnippet(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// HandleFileSaved detects slow modifications: the user saved a file that
// was previously written by the agent and the content differs. This covers
// the "modify 5 minutes later" feedback path that the S2 (immediate undo)
// path misses.
func (h *ImplicitFeedbackHandler) HandleFileSaved(ctx context.Context, cell *wesgine.Cell, actor string, path string, agentHashes *sync.Map) {
	if cell == nil || agentHashes == nil {
		return
	}

	agentHashVal, ok := agentHashes.Load(path)
	if !ok {
		return
	}
	agentHash, _ := agentHashVal.(string)
	if agentHash == "" {
		return
	}

	currentHash := quickHashFromPath(path)
	if currentHash == "" || currentHash == agentHash {
		return
	}

	// File was modified by user after agent wrote it — slow feedback.
	learn := cell.Learn()
	if learn == nil {
		return
	}

	currentContent, err := readFileCapped(path, 50_000)
	if err != nil {
		return
	}

	agentDiff := wesgine.DiffInfo{Path: path}
	userDiff := wesgine.DiffInfo{Path: path, UserText: string(currentContent)}
	if rerr := learn.ReportEditModified(ctx, path, agentDiff, userDiff, actor); rerr != nil {
		slog.Debug("[implicit_feedback] slow modification report failed", "path", path, "err", rerr)
		return
	}

	// Clear the hash entry so we don't re-report on every subsequent save.
	agentHashes.Delete(path)
	slog.Info("[implicit_feedback] slow modification detected",
		"path", path, "actor", actor)
}

func quickHashFromPath(path string) string {
	return quickHash(path)
}

func readFileCapped(path string, maxBytes int) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > maxBytes {
		return nil, fmt.Errorf("file too large: %d bytes", len(data))
	}
	return data, nil
}
