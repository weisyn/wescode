package engine

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"

	"github.com/weisyn/wescode/internal/deviceagent"
	"github.com/weisyn/wescode/internal/notify"
	"github.com/weisyn/wesgine/tool"
)

// qualityMirrorProvider wraps a base ShellProvider, mirroring all command
// output to a read-only VSCode terminal tab with "[AI QualityGate] ..." label.
// This gives users visibility into QualityGate L1/L2 verification output
// (compile errors, test failures) without leaving the IDE.
//
// Best-effort: mirror creation failure does not block command execution.
// Reuses deviceagent.MirrorSession from host.go (same package).
type qualityMirrorProvider struct {
	base     tool.ShellProvider
	notifier deviceagent.NotifyFn
	counter  atomic.Int64
}

// newQualityMirrorProvider creates a ShellProvider that mirrors output to
// a read-only terminal tab. If notifier is nil, returns base unchanged.
func newQualityMirrorProvider(base tool.ShellProvider, notifier deviceagent.NotifyFn) tool.ShellProvider {
	if notifier == nil {
		return base
	}
	return &qualityMirrorProvider{base: base, notifier: notifier}
}

func (p *qualityMirrorProvider) Start(ctx context.Context, req tool.ShellRequest) (tool.ShellSession, error) {
	session, err := p.base.Start(ctx, req)
	if err != nil {
		return nil, err
	}

	mirrorID := fmt.Sprintf("qg-mirror-%d", p.counter.Add(1))
	cmd := req.Command
	if len(cmd) > 60 {
		cmd = cmd[:60]
	}
	label := "[AI QualityGate] " + cmd

	if err := p.notifier(notify.TerminalCreate, map[string]any{
		"sessionId": mirrorID,
		"command":   req.Command,
		"workDir":   req.WorkDir,
		"label":     label,
		"readOnly":  true,
	}); err != nil {
		return session, nil
	}

	pipeR, pipeW := io.Pipe()
	teeOut := io.TeeReader(session.Output(), pipeW)

	notifier := p.notifier
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := pipeR.Read(buf)
			if n > 0 {
				if err := notifier(notify.TerminalOutput, map[string]any{
					"sessionId": mirrorID,
					"data":      string(buf[:n]),
				}); err != nil {
					slog.Warn("[quality_mirror] notify terminal output failed", "mirror_id", mirrorID, "error", err)
				}
			}
			if readErr != nil {
				break
			}
		}
		if err := notifier(notify.TerminalExited, map[string]any{
			"sessionId": mirrorID,
			"exitCode":  0,
		}); err != nil {
			slog.Warn("[quality_mirror] notify terminal exited failed", "mirror_id", mirrorID, "error", err)
		}
	}()

	return &deviceagent.MirrorSession{Inner: session, TeeOut: teeOut, PipeW: pipeW}, nil
}

// engineShellProvider wraps a base ShellProvider and injects EnginePaths as
// ReadOnlyMounts into every ShellRequest. This ensures bypass Shell calls
// (QualityGate, CheckpointManager, codeintel) have EnginePaths visible in
// namespace sandbox mode (INV-PATH-05).
type engineShellProvider struct {
	base        tool.ShellProvider
	enginePaths []string
}

func (p *engineShellProvider) Start(ctx context.Context, req tool.ShellRequest) (tool.ShellSession, error) {
	if len(req.ReadOnlyMounts) == 0 && len(p.enginePaths) > 0 {
		req.ReadOnlyMounts = p.enginePaths
	}
	return p.base.Start(ctx, req)
}
