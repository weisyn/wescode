//go:build windows

package engine

import (
	"fmt"
	"golang.org/x/sys/windows"
	"log/slog"
	"os"
	"path/filepath"
)

func acquireChannelBootLock(dataDir string) (release func(), ok bool) {
	lockPath := filepath.Join(dataDir, ".im-channel.lock")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		slog.Warn("[channels] channel lock: mkdir", "error", err)
		return nil, true
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		slog.Warn("[channels] channel lock: open", "error", err)
		return nil, true
	}
	ol := new(windows.Overlapped)
	err = windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, ol,
	)
	if err != nil {
		_ = f.Close()
		var stalePID string
		if data, readErr := os.ReadFile(lockPath); readErr == nil {
			stalePID = string(data)
		}
		slog.Warn("[channels] another wescode instance holds IM channel lock; skipping channel boot",
			"lock", lockPath,
			"holder_pid", stalePID,
			"hint", "kill orphan wescode.exe processes and restart once",
		)
		return nil, false
	}
	_ = f.Truncate(0)
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	release = func() {
		ol := new(windows.Overlapped)
		_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
		_ = f.Close()
	}
	return release, true
}
