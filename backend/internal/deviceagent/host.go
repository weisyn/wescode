// Package deviceagent implements wescode's IDE device-agent capabilities:
// buffer overlay, integrated terminal, editor state. These are the concrete
// implementations backing CellSpec.HostEnvironment.
//
// Dependency direction: deviceagent → codeintel (types only), deviceagent → wesgine/tool.
// engine → deviceagent (construction + injection). No reverse dependency.
package deviceagent

import (
	"context"
	"io"

	"github.com/weisyn/wescode/internal/codeintel"
	"github.com/weisyn/wescode/internal/notify"
	"github.com/weisyn/wesgine/tool"
)

// NotifyFn sends a JSON-RPC notification to the VSCode extension.
//
// The method is a notify.Method, not a string: the extension dispatches on an
// exhaustive switch generated from that package, so a name it cannot construct
// is a name nothing is listening for.
type NotifyFn func(method notify.Method, params any) error

// BufferReader provides read access to dirty editor buffers.
type BufferReader interface {
	Get(path string) (content string, ok bool)
}

// Host wraps LocalHost with editor buffer overlay and optional VSCode terminal.
type Host struct {
	local       tool.LocalHost
	Buffers     BufferReader
	Notifier    NotifyFn
	TermMgr     *TerminalManager
	EditorState func() codeintel.EditorState
}

// NewHost creates a device-agent Host. notifier may be nil (CLI mode fallback).
func NewHost(buffers BufferReader, notifier NotifyFn, editorState func() codeintel.EditorState) *Host {
	return &Host{
		Buffers:     buffers,
		Notifier:    notifier,
		TermMgr:     NewTerminalManager(),
		EditorState: editorState,
	}
}

func (h *Host) Shell() tool.ShellProvider {
	if h.Notifier != nil {
		return &VSCodeShellProvider{
			Notifier: h.Notifier,
			Local:    h.local.Shell(),
			Mgr:      h.TermMgr,
		}
	}
	return h.local.Shell()
}

func (h *Host) Files() tool.FileProvider {
	return &OverlayFileProvider{Buffers: h.Buffers}
}

func (h *Host) Editor() tool.EditorProvider {
	if h.EditorState == nil {
		return nil
	}
	return &EditorAdapter{Fn: h.EditorState}
}

// EditorAdapter bridges codeintel.EditorState to tool.EditorProvider.
type EditorAdapter struct {
	Fn func() codeintel.EditorState
}

func (a *EditorAdapter) FocusFile() string          { return a.Fn().FocusFile }
func (a *EditorAdapter) OpenFiles() []string        { return a.Fn().OpenFiles }
func (a *EditorAdapter) CursorPosition() (int, int) { s := a.Fn(); return s.CursorLine, s.CursorCol }

// OverlayFileProvider wraps LocalFileProvider, intercepting reads with buffer overlay.
type OverlayFileProvider struct {
	tool.LocalFileProvider
	Buffers BufferReader
}

func (f *OverlayFileProvider) ReadFile(_ context.Context, path string) ([]byte, error) {
	if f.Buffers != nil {
		if content, ok := f.Buffers.Get(path); ok {
			return []byte(content), nil
		}
	}
	return f.LocalFileProvider.ReadFile(context.Background(), path)
}

func (f *OverlayFileProvider) Open(_ context.Context, path string) (io.ReadCloser, error) {
	if f.Buffers != nil {
		if content, ok := f.Buffers.Get(path); ok {
			return io.NopCloser(io.NewSectionReader(
				readerAtString(content), 0, int64(len(content)),
			)), nil
		}
	}
	return f.LocalFileProvider.Open(context.Background(), path)
}

type readerAtString string

func (s readerAtString) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(s)) {
		return 0, io.EOF
	}
	n := copy(p, s[off:])
	if off+int64(n) >= int64(len(s)) {
		return n, io.EOF
	}
	return n, nil
}
