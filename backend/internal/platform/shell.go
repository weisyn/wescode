package platform

import (
	"context"
	"os/exec"
	"runtime"
)

// ShellArgv returns the interpreter and arguments that run script as a shell
// command line on the host platform.
//
// `sh -c` is not available on a stock Windows box, and the failure is not a
// clean "unsupported": exec reports "executable file not found in %PATH%",
// which reads like the script's own first command is missing. Callers that only
// check the exit status then report the work as failed rather than unrunnable.
//
// Mirrors the choice wesgine makes for its exec tool (tool/host_shell.go), which
// cannot be reused here because that helper is unexported. The flags match it
// too: /d skips AutoRun, /s keeps the quoting rules predictable for a single
// trailing command string.
func ShellArgv(script string) (name string, args []string) {
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/d", "/s", "/c", script}
	}
	return "sh", []string{"-c", script}
}

// ShellCommand builds an *exec.Cmd running script through the host shell.
func ShellCommand(ctx context.Context, script string) *exec.Cmd {
	name, args := ShellArgv(script)
	return exec.CommandContext(ctx, name, args...)
}
