package editengine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Metadata keys for Memory entry code anchoring.
// These keys are stored in MemoryEntry.Metadata (map[string]string)
// and use the wesgine Metadata mechanism — zero engine changes required.
const (
	AnchorFileKey    = "anchor:file"
	AnchorHashKey    = "anchor:hash"
	AnchorSymbolsKey = "anchor:symbols"
	AnchorStaleKey   = "anchor:stale"
)

// MemoryRef is a minimal reference to a Memory entry for stale marking.
type MemoryRef struct {
	ID       string
	Content  string
	Metadata map[string]string
}

// MemoryAnchorStore is the minimal interface needed for stale marking and auto-anchoring.
// Implemented by an adapter over wesgine's public Memory API.
type MemoryAnchorStore interface {
	ListByAnchorFile(ctx context.Context, filePath string, limit int) ([]MemoryRef, error)
	ListUnanchored(ctx context.Context, limit int) ([]MemoryRef, error)
	SetMetadata(ctx context.Context, entryID, key, value string) error
}

// QuickFileHash returns the SHA-256 hex of a file's content (first 8 bytes = 16 hex chars).
// Returns "" if the file cannot be read. Uses os.ReadFile directly — for
// overlay-aware hashing, use EditEngine.quickFileHash instead.
func QuickFileHash(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:8])
}

// quickFileHashFn computes a file hash using the given read function.
func quickFileHashFn(path string, readFn func(string) ([]byte, error)) string {
	data, err := readFn(path)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:8])
}

// ValidateAnchor checks if a Memory entry's code anchor is still valid.
// Returns true (valid) if:
//   - No anchor metadata present (KA-01: unanchored entries are always valid)
//   - File content hash matches the stored hash
//   - File cannot be read (KA-05: fail-open)
func ValidateAnchor(metadata map[string]string) bool {
	if metadata == nil {
		return true
	}
	file := metadata[AnchorFileKey]
	hash := metadata[AnchorHashKey]
	if file == "" || hash == "" {
		return true
	}
	if metadata[AnchorStaleKey] == "true" {
		return false
	}
	currentHash := QuickFileHash(file)
	if currentHash == "" {
		return true // fail-open: file unreadable
	}
	return currentHash == hash
}

// MarkStaleByFile queries Memory entries anchored to the given file path
// and marks them stale if the stored hash no longer matches currentHash.
// Returns the number of entries marked stale.
func MarkStaleByFile(ctx context.Context, store MemoryAnchorStore, filePath, currentHash string) (int, error) {
	if store == nil || filePath == "" || currentHash == "" {
		return 0, nil
	}

	entries, err := store.ListByAnchorFile(ctx, filePath, 100)
	if err != nil {
		return 0, err
	}

	marked := 0
	for _, ref := range entries {
		storedHash := ref.Metadata[AnchorHashKey]
		if storedHash == "" || storedHash == currentHash {
			continue
		}
		if ref.Metadata[AnchorStaleKey] == "true" {
			continue // already marked
		}
		if err := store.SetMetadata(ctx, ref.ID, AnchorStaleKey, "true"); err != nil {
			slog.Warn("[anchor] failed to mark stale", "id", ref.ID, "file", filePath, "error", err)
			continue
		}
		slog.Info("[anchor] marked stale", "id", ref.ID, "file", filePath,
			"storedHash", storedHash, "currentHash", currentHash)
		marked++
	}
	return marked, nil
}

// AnchorRecentEntries scans Memory entries that lack anchor metadata and attempts
// to associate them with code files by matching content keywords against the workspace.
// This is a best-effort background operation (Phase 2 auto-anchoring).
func AnchorRecentEntries(ctx context.Context, store MemoryAnchorStore, workDir string, limit int) (int, error) {
	entries, err := store.ListUnanchored(ctx, limit)
	if err != nil {
		return 0, err
	}

	anchored := 0
	for _, ref := range entries {
		filePath := extractFilePathFromContent(ref.Content, workDir)
		if filePath == "" {
			continue
		}
		hash := QuickFileHash(filePath)
		if hash == "" {
			continue
		}
		if err := store.SetMetadata(ctx, ref.ID, AnchorFileKey, filePath); err != nil {
			continue
		}
		if err := store.SetMetadata(ctx, ref.ID, AnchorHashKey, hash); err != nil {
			continue
		}
		anchored++
	}
	return anchored, nil
}

// extractFilePathFromContent looks for file path patterns in memory entry content.
func extractFilePathFromContent(content, workDir string) string {
	words := strings.Fields(content)
	for _, w := range words {
		w = strings.Trim(w, "\"'`(),;:")
		if hasCodeExtension(w) {
			if filepath.IsAbs(w) {
				if _, err := os.Stat(w); err == nil {
					return w
				}
			}
			full := filepath.Join(workDir, w)
			if _, err := os.Stat(full); err == nil {
				return full
			}
		}
	}
	return ""
}

func hasCodeExtension(path string) bool {
	for _, ext := range []string{".go", ".ts", ".tsx", ".js", ".py", ".rs", ".java"} {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

// BuildAnchorMetadata creates the anchor metadata entries for a given file.
// Returns nil if the file cannot be read.
func BuildAnchorMetadata(filePath string, symbols ...string) map[string]string {
	hash := QuickFileHash(filePath)
	if hash == "" {
		return nil
	}
	m := map[string]string{
		AnchorFileKey: filePath,
		AnchorHashKey: hash,
	}
	if len(symbols) > 0 {
		combined := ""
		for i, s := range symbols {
			if i > 0 {
				combined += ","
			}
			combined += s
		}
		m[AnchorSymbolsKey] = combined
	}
	return m
}
