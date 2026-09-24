package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/weisyn/wesapp/file"
	"github.com/weisyn/wesgine/tool"
)

// ── Buffer overlay (editor dirty buffers) ────────────────────────────────────

func (s *Service) SetBuffer(path, content string) { s.bufferStore.Set(path, content) }
func (s *Service) RemoveBuffer(path string) {
	s.bufferStore.Thaw(path)
	s.bufferStore.Remove(path)
}

// OnFileSaved is called when the user saves a file. It detects slow
// modifications to agent-written files (the user edited and saved
// something the agent produced earlier).
func (s *Service) OnFileSaved(path string) {
	s.mu.Lock()
	cell := s.cell
	ds := s.docSync
	s.mu.Unlock()

	if ds != nil {
		ds.OnFileSaved(path)
	}

	if s.feedbackHandler == nil || cell == nil {
		return
	}
	ctx := context.Background()
	s.feedbackHandler.HandleFileSaved(ctx, cell, "local", path, &s.agentFileHashes)
}

// bufferInvalidateHook returns a PostCallHook that freezes the BufferStore
// overlay for files modified by edit/write/apply_patch. Freeze removes the
// stale overlay AND prevents the editor from re-pushing its old dirty buffer
// before it reloads from disk. The freeze is released on the next editor/didSave.
func (s *Service) bufferInvalidateHook() tool.PostCallHook {
	return func(_ context.Context, call tool.ToolCall, result *tool.ToolResult, _ *tool.ToolContext) {
		if call.Name != "edit" && call.Name != "write" && call.Name != "apply_patch" {
			return
		}
		if result == nil || result.IsError {
			return
		}
		if result.Metadata == nil {
			return
		}
		files, ok := result.Metadata["output_files"].([]map[string]any)
		if !ok {
			return
		}
		for _, f := range files {
			if p, ok := f["path"].(string); ok && p != "" {
				s.bufferStore.Freeze(p)
			}
		}
	}
}

// bufferProviderAdapter wraps BufferStore to implement codeintel.BufferProvider.
type bufferProviderAdapter struct {
	store *BufferStore
}

func (a *bufferProviderAdapter) Get(path string) []byte {
	content, ok := a.store.Get(path)
	if !ok {
		return nil
	}
	return []byte(content)
}

// ── File operations ─────────────────────────────────────────────────────────

// ErrFileStoreNotReady means the attachment store does not exist yet. It is a
// boot-window state, not a fault: the store is created in the async phase
// after `initialize` returns, so a paperclip click in the first second is
// genuinely early. A sentinel rather than a fresh errors.New at each site,
// because the caller has to recognise it — reported as a bare internal error
// it reaches the user as the English string "file store not initialized",
// which is what happens when a condition that has a name is left unnamed.
var ErrFileStoreNotReady = errors.New("attachment store not ready")

func (s *Service) UploadFile(fileName, dataB64 string) (*file.FileManifest, error) {
	if s.fileStore == nil {
		return nil, ErrFileStoreNotReady
	}
	return s.fileStore.Upload(fileName, dataB64)
}

func (s *Service) ImportLocalFile(path string) (*file.FileManifest, error) {
	if s.fileStore == nil {
		return nil, ErrFileStoreNotReady
	}
	return s.fileStore.ImportLocal(path)
}

func (s *Service) GetFileContent(fileID string) (string, string, string, error) {
	if s.fileStore == nil {
		return "", "", "", ErrFileStoreNotReady
	}
	m := s.fileStore.Get(fileID)
	if m == nil {
		return "", "", "", fmt.Errorf("file %q not found", fileID)
	}
	data, err := s.fileStore.GetContent(fileID)
	if err != nil {
		return "", "", "", err
	}
	return data, m.FileName, m.MIMEType, nil
}

func (s *Service) GetFileManifest(fileID string) *file.FileManifest {
	if s.fileStore == nil {
		return nil
	}
	return s.fileStore.Get(fileID)
}
