package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	weisyn "github.com/weisyn/weisyn"
	"github.com/weisyn/wesapp/provider"
	"github.com/weisyn/wesapp/wire"
	"github.com/weisyn/wescode/internal/engine"
)

// runHeadlessOpts holds parsed CLI options for the run subcommand.
type runHeadlessOpts struct {
	prompt  string
	workDir string
	model   string
	agentID string
	timeout time.Duration
	quiet   bool
}

// runHeadless implements `wescode run "prompt" [--workdir DIR] [--model M]`.
// It boots the full product engine.Service, runs a single Chat, streams
// output to stdout, then exits. This is the headless CLI equivalent of
// what VSCode does via JSON-RPC — same engine, same RunChat, same tools.
func runHeadless(ctx context.Context, args []string) error {
	opts := parseRunArgs(args)
	if opts.prompt == "" {
		fmt.Fprintln(os.Stderr, "Usage: wescode run \"<prompt>\" [--workdir DIR] [--model MODEL] [--agent AGENT] [--timeout 5m] [--quiet]")
		os.Exit(1)
	}

	provider.SetCatalog(weisyn.ProviderCatalog())

	cfg, err := engine.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	// CLI headless mode: auto-allow all tool tiers (no HITL popup).
	cfg.BenchMode = true

	svc := engine.NewService(cfg)

	workDir := opts.workDir
	if workDir == "" {
		workDir, _ = os.Getwd()
	}

	if _, initErr := svc.Initialize(ctx, workDir, []string{workDir}); initErr != nil {
		return fmt.Errorf("initialize: %w", initErr)
	}
	defer func() {
		time.Sleep(500 * time.Millisecond)
		svc.Close()
	}()

	sessionID := fmt.Sprintf("cli-%d", time.Now().UnixMilli())
	agentID := opts.agentID
	if agentID == "" {
		agentID = "builtin-coder"
	}

	chatOpts := engine.ChatOpts{
		WorkDir: workDir,
	}
	if opts.model != "" {
		chatOpts.ProviderID = opts.model
	}

	var cancel context.CancelFunc
	if opts.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, opts.timeout)
		defer cancel()
	}

	events, _, runErr := svc.RunChat(ctx, sessionID, agentID, opts.prompt, chatOpts)
	if runErr != nil {
		return fmt.Errorf("run: %w", runErr)
	}

	res := wire.Pump(ctx, events, func(ev wire.Event) error {
		switch ev.Type {
		case wire.TextDelta:
			if !opts.quiet {
				switch d := ev.Data.(type) {
				case string:
					fmt.Print(d)
				case interface{ GetText() string }:
					fmt.Print(d.GetText())
				}
			}
		case wire.Error:
			if ed, ok := ev.Data.(map[string]any); ok {
				if rec, _ := ed["recoverable"].(bool); !rec {
					slog.Error("agent error", "message", ed["message"])
				}
			}
		}
		return nil
	})

	fmt.Fprintln(os.Stderr)
	slog.Info("run completed",
		"tokens_in", res.InputTokens,
		"tokens_out", res.OutputTokens,
		"total_tokens", res.InputTokens+res.OutputTokens)

	return nil
}

func parseRunArgs(args []string) runHeadlessOpts {
	var opts runHeadlessOpts
	var prompts []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--workdir":
			i++
			if i < len(args) {
				opts.workDir = args[i]
			}
		case "--model":
			i++
			if i < len(args) {
				opts.model = args[i]
			}
		case "--agent":
			i++
			if i < len(args) {
				opts.agentID = args[i]
			}
		case "--timeout":
			i++
			if i < len(args) {
				d, err := time.ParseDuration(args[i])
				if err != nil {
					log.Fatalf("invalid --timeout: %v", err)
				}
				opts.timeout = d
			}
		case "--quiet", "-q":
			opts.quiet = true
		default:
			if !strings.HasPrefix(args[i], "-") {
				prompts = append(prompts, args[i])
			}
		}
	}
	opts.prompt = strings.Join(prompts, " ")
	return opts
}
