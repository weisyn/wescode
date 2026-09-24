package verification

import (
	"context"
	"log/slog"
	"path/filepath"
	"strconv"
	"time"

	wesgine "github.com/weisyn/wesgine"
	"github.com/weisyn/wesgine/message"
	"github.com/weisyn/wesgine/tool"
)

// CompilePatrol implements wesgine.EditPatrol by running a per-package compile
// check after every EXECUTE phase that includes write-class tools.
//
// Unlike CodeQualityGate (terminal turn, full L1+L2+L3), CompilePatrol is
// lightweight (compile only, 10s timeout) and runs on every edit batch.
// Errors are injected as synthetic messages so the model sees compile failures
// immediately — before wasting turns editing files that depend on broken code.
type CompilePatrol struct {
	workDir       string
	runner        LanguageRunner
	shellProvider tool.ShellProvider
	invPatrol     wesgine.EditPatrol // optional invariant discipline check
	bugRecorder   BugRecorder        // optional compile-failure receiver
}

// BugEvent describes a compile failure attributed to an edited file — the
// engine's most direct evidence of an engineering bug in that file.
type BugEvent struct {
	Path    string // edited file path (same form as modifiedFiles)
	Message string // first compiler error message for the file
	Line    int
}

// BugRecorder receives compile-failure events; engine callers persist them as
// KindBug memories so later sessions recall the file's bug history. Nil = no
// recording.
type BugRecorder func(ctx context.Context, b BugEvent)

var _ wesgine.EditPatrol = (*CompilePatrol)(nil)

func NewCompilePatrol(workDir string, sp tool.ShellProvider) *CompilePatrol {
	return NewCompilePatrolWithInvariants(workDir, sp, nil)
}

// NewCompilePatrolWithInvariants additionally enforces the workspace's
// inline INV comment directives (@inv-forbid / @inv-require in code comments)
// after every edit batch. The recorder receives each violation (e.g. memory
// L4 persistence); may be nil.
func NewCompilePatrolWithInvariants(workDir string, sp tool.ShellProvider, recorder wesgine.InvariantRecorder) *CompilePatrol {
	if sp == nil {
		sp = &tool.LocalShellProvider{}
	}
	return &CompilePatrol{
		workDir:       workDir,
		runner:        SelectRunner(workDir, sp),
		shellProvider: sp,
		invPatrol:     wesgine.NewInvariantPatrol(workDir, recorder),
	}
}

func (p *CompilePatrol) SetWorkDir(dir string) {
	p.workDir = dir
	p.runner = SelectRunner(dir, p.shellProvider)
}

// WithBugRecorder wires a receiver for compile-failure events on edited
// files. Engine callers persist these as KindBug memories (cross-session
// bug-history recall). May be nil.
func (p *CompilePatrol) WithBugRecorder(r BugRecorder) *CompilePatrol {
	p.bugRecorder = r
	return p
}

func (p *CompilePatrol) Patrol(ctx context.Context, modifiedFiles []string) (wesgine.PatrolResult, error) {
	if len(modifiedFiles) == 0 {
		return wesgine.PatrolResult{}, nil
	}

	// 1) Invariant discipline check (in-process, no subprocess). Violations
	// take priority so the model fixes the discipline breach first — a
	// deleted ok-check or a resurrected openPage is more valuable feedback
	// than a compile error downstream of it.
	if p.invPatrol != nil {
		invRes, invErr := p.invPatrol.Patrol(ctx, modifiedFiles)
		if invErr != nil {
			slog.Warn("[patrol] invariant check error (continuing to compile)", "err", invErr)
		} else if invRes.HasErrors {
			return invRes, nil
		}
	}

	effectiveDir := inferProjectRoot(modifiedFiles, p.workDir)
	runner := p.runner
	if runner == nil || effectiveDir != p.workDir {
		runner = SelectRunner(effectiveDir, p.shellProvider)
	}
	if runner == nil {
		return wesgine.PatrolResult{}, nil
	}

	patrolCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	result := runner.Compile(patrolCtx, effectiveDir)
	if result.Success || len(result.Errors) == 0 {
		return wesgine.PatrolResult{}, nil
	}

	slog.Info("[patrol] compile errors detected",
		"workDir", effectiveDir,
		"errors", len(result.Errors),
		"duration", result.Duration,
	)

	feedback := formatPatrolFeedback(result.Errors, modifiedFiles)

	// Report compile failures on edited files as bug events (KindBug memory
	// upstream). Only files the agent actually touched count — unrelated
	// pre-existing breakage is noise, not the agent's bug. One event per
	// file (first error message) to avoid flooding memory.
	if p.bugRecorder != nil {
		seen := make(map[string]bool)
		for _, e := range result.Errors {
			clean := filepath.Clean(e.Path)
			if seen[clean] {
				continue
			}
			inModified := false
			for _, f := range modifiedFiles {
				if filepath.Clean(f) == clean {
					inModified = true
					break
				}
			}
			if !inModified {
				continue
			}
			seen[clean] = true
			p.bugRecorder(ctx, BugEvent{Path: clean, Message: e.Message, Line: e.Line})
		}
	}

	return wesgine.PatrolResult{
		HasErrors: true,
		Feedback:  feedback,
	}, nil
}

func formatPatrolFeedback(errors []CompileError, modifiedFiles []string) message.Message {
	modSet := make(map[string]bool, len(modifiedFiles))
	for _, f := range modifiedFiles {
		modSet[filepath.Clean(f)] = true
	}

	var relevant, other []CompileError
	for _, e := range errors {
		if modSet[filepath.Clean(e.Path)] {
			relevant = append(relevant, e)
		} else {
			other = append(other, e)
		}
	}

	var text string
	text = "⚠️ **Compile errors detected** after your edits. Fix these before continuing:\n\n"

	show := relevant
	if len(show) == 0 {
		show = other
	}
	const maxShow = 10
	if len(show) > maxShow {
		show = show[:maxShow]
	}
	for _, e := range show {
		text += e.Path + ":" + strconv.Itoa(e.Line) + ": " + e.Message + "\n"
	}
	remaining := len(errors) - len(show)
	if remaining > 0 {
		text += "\n... and " + strconv.Itoa(remaining) + " more errors\n"
	}

	return message.Message{
		Role:    message.RoleUser,
		Content: []message.ContentBlock{message.NewTextBlock(text)},
	}
}
