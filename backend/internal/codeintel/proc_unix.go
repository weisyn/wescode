//go:build !windows

package codeintel

import (
	"os/exec"
	"syscall"
)

func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	setCancellation(cmd)
}

func killGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}
