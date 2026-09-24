package engine

import (
	"os"
	"path/filepath"
	"strconv"
)

// staleChannelLockPID returns the PID recorded in the lock file, if any.
func staleChannelLockPID(dataDir string) int {
	data, err := os.ReadFile(filepath.Join(dataDir, ".im-channel.lock"))
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(string(data))
	return pid
}
