//go:build !windows

package engine

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
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
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		var stalePID string
		if data, readErr := os.ReadFile(lockPath); readErr == nil {
			stalePID = string(data)
		}
		slog.Warn("[channels] another wescode instance holds IM channel lock; skipping channel boot",
			"lock", lockPath,
			"holder_pid", stalePID,
			"hint", "kill orphan backend processes (ps aux | grep bin/wescode) and restart once",
		)
		return nil, false
	}
	_ = f.Truncate(0)
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	release = func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
	return release, true
}
