package platform

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// ToolCandidates returns the platform-aware executable names to try for a
// logical tool name. On POSIX the canonical name is used; on Windows the
// .exe/.cmd suffixes and well-known renames (python3 → python.exe) are
// handled here so business code never hardcodes a tool name.
func ToolCandidates(name string) []string {
	if runtime.GOOS != "windows" {
		return []string{name}
	}
	switch name {
	case "rg":
		return []string{"rg.exe", "rg"}
	case "python3":
		return []string{"python.exe", "python3.exe", "python3"}
	case "python":
		return []string{"python.exe", "python"}
	case "npx":
		return []string{"npx.cmd", "npx.exe", "npx"}
	case "npm":
		return []string{"npm.cmd", "npm.exe", "npm"}
	case "go":
		return []string{"go.exe", "go"}
	case "git":
		return []string{"git.exe", "git"}
	default:
		return []string{name + ".exe", name}
	}
}

// Tool is a resolved executable handle.
type Tool struct {
	Name string // logical name, e.g. "rg"
	Path string // resolved executable path
	// Batch is true when the resolution landed on a .cmd/.bat shim
	// (Windows only). Such files cannot be launched directly by
	// CreateProcess and must go through cmd.exe.
	Batch bool
}

// ResolveTool locates a logical tool on the host PATH, trying the platform
// candidate names in order. It returns an explicit error when the tool is
// missing — callers must not silently degrade.
func ResolveTool(name string) (*Tool, error) {
	for _, cand := range ToolCandidates(name) {
		if p, err := exec.LookPath(cand); err == nil {
			lower := strings.ToLower(p)
			return &Tool{
				Name:  name,
				Path:  p,
				Batch: strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".bat"),
			}, nil
		}
	}
	return nil, fmt.Errorf("tool %q not found in PATH (tried: %v)", name, ToolCandidates(name))
}

// Command builds an *exec.Cmd for the resolved tool. On Windows, .cmd/.bat
// shims are wrapped with cmd.exe /c because CreateProcess cannot execute
// batch files directly.
func (t *Tool) Command(args ...string) *exec.Cmd {
	return t.CommandContext(context.Background(), args...)
}

// CommandContext is Command with a context.
func (t *Tool) CommandContext(ctx context.Context, args ...string) *exec.Cmd {
	if runtime.GOOS == "windows" && t.Batch {
		full := append([]string{"/d", "/s", "/c", t.Path}, args...)
		return exec.CommandContext(ctx, "cmd.exe", full...)
	}
	return exec.CommandContext(ctx, t.Path, args...)
}
