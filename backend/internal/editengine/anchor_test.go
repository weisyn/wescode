package editengine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestQuickFileHash_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	os.WriteFile(path, []byte("package main\n"), 0644)

	hash := QuickFileHash(path)
	if hash == "" {
		t.Fatal("expected non-empty hash for valid file")
	}
	if len(hash) != 16 {
		t.Errorf("expected 16 hex chars (8 bytes), got %d: %s", len(hash), hash)
	}
}

func TestQuickFileHash_Deterministic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	os.WriteFile(path, []byte("package main\n"), 0644)

	h1 := QuickFileHash(path)
	h2 := QuickFileHash(path)
	if h1 != h2 {
		t.Errorf("hash not deterministic: %s != %s", h1, h2)
	}
}

func TestQuickFileHash_DifferentContent(t *testing.T) {
	dir := t.TempDir()
	p1 := filepath.Join(dir, "a.go")
	p2 := filepath.Join(dir, "b.go")
	os.WriteFile(p1, []byte("package a\n"), 0644)
	os.WriteFile(p2, []byte("package b\n"), 0644)

	h1 := QuickFileHash(p1)
	h2 := QuickFileHash(p2)
	if h1 == h2 {
		t.Error("different content should produce different hashes")
	}
}

func TestQuickFileHash_NonexistentFile(t *testing.T) {
	hash := QuickFileHash("/nonexistent/path/file.go")
	if hash != "" {
		t.Errorf("expected empty hash for nonexistent file, got %s", hash)
	}
}

func TestValidateAnchor_NoAnchor(t *testing.T) {
	if !ValidateAnchor(nil) {
		t.Error("nil metadata should be valid (KA-01)")
	}
	if !ValidateAnchor(map[string]string{}) {
		t.Error("empty metadata should be valid (KA-01)")
	}
	if !ValidateAnchor(map[string]string{"other": "key"}) {
		t.Error("metadata without anchor keys should be valid")
	}
}

func TestValidateAnchor_FileUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	os.WriteFile(path, []byte("package main\n"), 0644)

	hash := QuickFileHash(path)
	meta := map[string]string{
		AnchorFileKey: path,
		AnchorHashKey: hash,
	}
	if !ValidateAnchor(meta) {
		t.Error("anchor should be valid when file unchanged")
	}
}

func TestValidateAnchor_FileChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	os.WriteFile(path, []byte("package main\n"), 0644)
	hash := QuickFileHash(path)

	os.WriteFile(path, []byte("package main\n\nfunc hello() {}\n"), 0644)

	meta := map[string]string{
		AnchorFileKey: path,
		AnchorHashKey: hash,
	}
	if ValidateAnchor(meta) {
		t.Error("anchor should be invalid when file changed")
	}
}

func TestValidateAnchor_FileUnreadable(t *testing.T) {
	meta := map[string]string{
		AnchorFileKey: "/nonexistent/file.go",
		AnchorHashKey: "abc123",
	}
	if !ValidateAnchor(meta) {
		t.Error("should return true (fail-open) when file unreadable (KA-05)")
	}
}

func TestValidateAnchor_MarkedStale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	os.WriteFile(path, []byte("package main\n"), 0644)
	hash := QuickFileHash(path)

	meta := map[string]string{
		AnchorFileKey:  path,
		AnchorHashKey:  hash,
		AnchorStaleKey: "true",
	}
	if ValidateAnchor(meta) {
		t.Error("stale-marked entry should be invalid")
	}
}

func TestBuildAnchorMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	os.WriteFile(path, []byte("package main\n"), 0644)

	meta := BuildAnchorMetadata(path, "RunChat", "Initialize")
	if meta == nil {
		t.Fatal("expected non-nil metadata")
	}
	if meta[AnchorFileKey] != path {
		t.Errorf("file: %s", meta[AnchorFileKey])
	}
	if meta[AnchorHashKey] == "" {
		t.Error("expected non-empty hash")
	}
	if meta[AnchorSymbolsKey] != "RunChat,Initialize" {
		t.Errorf("symbols: %s", meta[AnchorSymbolsKey])
	}
}

func TestBuildAnchorMetadata_NonexistentFile(t *testing.T) {
	meta := BuildAnchorMetadata("/nonexistent/file.go")
	if meta != nil {
		t.Error("expected nil for nonexistent file")
	}
}

// ── MarkStaleByFile ──────────────────────────────────────────────────────────

type mockAnchorStore struct {
	entries []MemoryRef
	updated map[string]string // id → value set for AnchorStaleKey
}

func (m *mockAnchorStore) ListByAnchorFile(_ context.Context, filePath string, limit int) ([]MemoryRef, error) {
	var result []MemoryRef
	for _, e := range m.entries {
		if e.Metadata[AnchorFileKey] == filePath {
			result = append(result, e)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *mockAnchorStore) ListUnanchored(_ context.Context, limit int) ([]MemoryRef, error) {
	var result []MemoryRef
	for _, e := range m.entries {
		if e.Metadata[AnchorFileKey] == "" {
			result = append(result, e)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *mockAnchorStore) SetMetadata(_ context.Context, entryID, key, value string) error {
	if m.updated == nil {
		m.updated = make(map[string]string)
	}
	m.updated[entryID] = value
	return nil
}

func TestMarkStaleByFile_MarksChangedEntries(t *testing.T) {
	store := &mockAnchorStore{
		entries: []MemoryRef{
			{ID: "e1", Metadata: map[string]string{AnchorFileKey: "a.go", AnchorHashKey: "old_hash"}},
			{ID: "e2", Metadata: map[string]string{AnchorFileKey: "a.go", AnchorHashKey: "current_hash"}},
			{ID: "e3", Metadata: map[string]string{AnchorFileKey: "b.go", AnchorHashKey: "other"}},
		},
	}

	n, err := MarkStaleByFile(context.Background(), store, "a.go", "current_hash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 marked stale, got %d", n)
	}
	if store.updated["e1"] != "true" {
		t.Error("e1 should be marked stale (hash mismatch)")
	}
	if _, ok := store.updated["e2"]; ok {
		t.Error("e2 should NOT be marked stale (hash matches)")
	}
}

func TestMarkStaleByFile_SkipsAlreadyStale(t *testing.T) {
	store := &mockAnchorStore{
		entries: []MemoryRef{
			{ID: "e1", Metadata: map[string]string{AnchorFileKey: "a.go", AnchorHashKey: "old", AnchorStaleKey: "true"}},
		},
	}

	n, _ := MarkStaleByFile(context.Background(), store, "a.go", "new_hash")
	if n != 0 {
		t.Error("should skip already-stale entries")
	}
}

func TestMarkStaleByFile_NilStore(t *testing.T) {
	n, err := MarkStaleByFile(context.Background(), nil, "a.go", "hash")
	if err != nil || n != 0 {
		t.Error("nil store should return 0, nil")
	}
}

func TestMarkStaleByFile_EmptyPath(t *testing.T) {
	store := &mockAnchorStore{}
	n, err := MarkStaleByFile(context.Background(), store, "", "hash")
	if err != nil || n != 0 {
		t.Error("empty path should return 0, nil")
	}
}
