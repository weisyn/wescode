//go:build windows

package codeintel

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// CREATE_NO_WINDOW prevents child processes from flashing a console window.
const createNoWindow = 0x08000000

func setProcGroup(cmd *exec.Cmd) {
	// Windows has no POSIX process groups via SysProcAttr.Setpgid; instead we
	// hide the child console and kill the whole tree via taskkill /T.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	setCancellation(cmd)
}

func killGroup(pid int) error {
	// taskkill /T /F terminates the process and its entire child tree.
	// A plain Kill() would only stop the parent and leak CreateProcess
	// children (LSP servers, index workers).
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	if err := kill.Run(); err == nil {
		return nil
	}
	// Fall back to killing the parent process directly (e.g. the tree was
	// already gone or taskkill is unavailable).
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
