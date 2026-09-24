//go:build e2e

// Package e2e provides end-to-end test infrastructure for wescode AI
// programming scenarios. It drives the full engine stack (Initialize →
// Runtime.Run) and validates tool invocations, QualityGate behavior,
// Plan execution, and CSP primitive integration.
//
// Run with: go test -tags e2e ./internal/e2e/... -timeout 600s
package e2e

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weisyn/wescode/internal/engine"
	wesengine "github.com/weisyn/wesgine/engine"
)

// Harness wraps a fully initialized wescode engine.Service for E2E testing.
type Harness struct {
	T       *testing.T
	Svc     *engine.Service
	WorkDir string
	dataDir string
}

// NewHarness creates a test harness with an isolated data directory.
// fixtureDir is either a path to testdata/* or empty string for greenfield.
func NewHarness(t *testing.T, fixtureDir string) *Harness {
	t.Helper()

	dataDir, err := os.MkdirTemp("", "wescode-e2e-data-*")
	if err != nil {
		t.Fatalf("create data dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dataDir) })
	os.Setenv("WESCODE_DATA_DIR", dataDir)
	t.Cleanup(func() { os.Unsetenv("WESCODE_DATA_DIR") })

	var workDir string
	if fixtureDir != "" {
		workDir, err = copyFixture(t, fixtureDir)
		if err != nil {
			t.Fatalf("copy fixture: %v", err)
		}
	} else {
		workDir, err = os.MkdirTemp("", "wescode-e2e-ws-*")
		if err != nil {
			t.Fatalf("create workspace: %v", err)
		}
		t.Cleanup(func() { os.RemoveAll(workDir) })
	}

	providers := e2eProviders()
	if providers == nil {
		t.Skip("E2E tests require E2E_PROVIDER_API_KEY or WESCODE_PROVIDER_API_KEY env var")
	}

	cfg := engine.AppConfig{
		BenchMode: true,
		Providers: providers,
	}
	svc := engine.NewService(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var folders []string
	if workDir != "" {
		folders = []string{workDir}
	}
	_, initErr := svc.Initialize(ctx, workDir, folders)
	if initErr != nil {
		t.Fatalf("Initialize(%s): %v", workDir, initErr)
	}
	t.Cleanup(func() { svc.Close() })

	return &Harness{T: t, Svc: svc, WorkDir: workDir, dataDir: dataDir}
}

// Chat sends a prompt and collects all stream events until done.
// Uses engine.RunChat (the product path) to ensure full tool registration.
func (h *Harness) Chat(ctx context.Context, prompt string, opts ...ChatOption) *ChatResult {
	h.T.Helper()
	o := defaultChatOpts()
	for _, fn := range opts {
		fn(&o)
	}

	chatOpts := engine.ChatOpts{
		WorkDir: h.WorkDir,
	}

	events, _, err := h.Svc.RunChat(ctx, "", o.AgentID, prompt, chatOpts)
	if err != nil {
		h.T.Fatalf("RunChat: %v", err)
	}

	result := &ChatResult{}
	for ev := range events {
		result.AllEvents = append(result.AllEvents, ev)
		switch ev.Type {
		// Every arm below asserts the type the engine payload registry
		// declares for that EventType. The previous version guessed instead
		// — duck-typed `GetText()`/`GetName()` interfaces no engine type
		// implements, a `message.TokenUsage` case that a Go type switch can
		// never reach through TokenUsageData's embedding, and `%v` fallbacks
		// underneath. Guessing compiles and vets clean because Data is `any`,
		// so the wrong arms were silently dead and the fallbacks were what ran.
		case wesengine.EventStreamDelta:
			if d, ok := ev.Data.(string); ok {
				result.Text += d
			}
		case wesengine.EventToolStart:
			if ts, ok := ev.Data.(wesengine.ToolStartData); ok {
				// Tool name only. `%v` on ToolStartData spliced the raw
				// params JSON into the same string HasTool substring-matches,
				// so a param value could satisfy an assertion about a tool.
				result.ToolStarts = append(result.ToolStarts, ts.Tool)
			}
		case wesengine.EventToolResult:
			result.ToolResults = append(result.ToolResults, ev)
		case wesengine.EventTokenUsage:
			// TokenUsageData embeds message.TokenUsage; the token fields are
			// promoted, so no separate case for the embedded type is possible.
			if u, ok := ev.Data.(wesengine.TokenUsageData); ok {
				result.TokensIn += u.InputTokens
				result.TokensOut += u.OutputTokens
			}
		case wesengine.EventError:
			// Display() is the only sanctioned rendering; `%v` on ErrorData
			// leaks a Go struct literal into text tests print as the failure.
			result.Errors = append(result.Errors, wesengine.ParseErrorData(ev.Data).Display())
		}
		// Plan events
		switch ev.Type {
		case wesengine.EventPlanStart:
			result.PlanCreated = true
		case wesengine.EventPlanComplete:
			result.PlanCompleted = true
		}
	}
	return result
}

// WaitForIndex polls DebugStatus until CKG indexing stabilizes.
func (h *Harness) WaitForIndex(ctx context.Context, minSymbols int) int {
	h.T.Helper()
	var last int
	for {
		select {
		case <-ctx.Done():
			h.T.Fatalf("WaitForIndex timed out, last count: %d (want >= %d)", last, minSymbols)
			return last
		default:
		}
		status := h.Svc.DebugStatus(ctx)
		if ci, ok := status["codeIndex"].(map[string]any); ok {
			if count, ok := ci["symbolCount"].(int); ok {
				last = count
				if count >= minSymbols {
					return count
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// ChatResult holds the collected events from a Chat call.
type ChatResult struct {
	Text          string
	AllEvents     []wesengine.Event
	ToolStarts    []string
	ToolResults   []wesengine.Event
	Errors        []string
	TokensIn      int
	TokensOut     int
	PlanCreated   bool
	PlanCompleted bool
}

// HasTool checks if a specific tool was started during the chat.
// ToolStarts holds exact tool names, and every AssertToolTriggered call site
// names a whole tool ("read", "memory", "search_symbols"), never a prefix — so
// equality is the honest comparison. Substring matching would still pass while
// hiding a rename to a longer name that contains the old one.
func (r *ChatResult) HasTool(name string) bool {
	for _, t := range r.ToolStarts {
		if t == name {
			return true
		}
	}
	return false
}

// ToolResultContent returns combined content from all tool results.
func (r *ChatResult) ToolResultContent() string {
	var sb strings.Builder
	for _, ev := range r.ToolResults {
		if tr, ok := ev.Data.(interface{ GetContent() string }); ok {
			sb.WriteString(tr.GetContent())
		}
	}
	return sb.String()
}

// HasEvent checks if any event of the given type exists.
func (r *ChatResult) HasEvent(eventType wesengine.EventType) bool {
	for _, ev := range r.AllEvents {
		if ev.Type == eventType {
			return true
		}
	}
	return false
}

// ChatOption configures a Chat call.
type ChatOption func(*chatOpts)

type chatOpts struct {
	AgentID      string
	AppendSystem string
}

func defaultChatOpts() chatOpts {
	return chatOpts{AgentID: "builtin-coder"}
}

func WithAgent(id string) ChatOption {
	return func(o *chatOpts) { o.AgentID = id }
}

func WithSystemAppend(s string) ChatOption {
	return func(o *chatOpts) { o.AppendSystem = s }
}

// e2eProviders returns the LLM provider configuration for E2E tests.
// Requires E2E_PROVIDER_API_KEY or WESCODE_PROVIDER_API_KEY env var.
// Returns nil when no key is set — callers must t.Skip().
func e2eProviders() []engine.ProviderConfig {
	apiKey := os.Getenv("E2E_PROVIDER_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("WESCODE_PROVIDER_API_KEY")
	}
	if apiKey == "" {
		return nil
	}
	baseURL := os.Getenv("E2E_PROVIDER_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	model := os.Getenv("E2E_PROVIDER_MODEL")
	if model == "" {
		model = "deepseek-v4-flash"
	}
	return []engine.ProviderConfig{{
		Name:      "deepseek",
		Type:      "openai-compatible",
		BaseURL:   baseURL,
		APIKey:    apiKey,
		Model:     model,
		IsDefault: true,
	}}
}

// copyFixture copies a testdata directory to a temp location.
func copyFixture(t *testing.T, src string) (string, error) {
	t.Helper()
	dst, err := os.MkdirTemp("", "wescode-e2e-ws-*")
	if err != nil {
		return "", err
	}
	t.Cleanup(func() { os.RemoveAll(dst) })

	return dst, filepath.Walk(src, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// ── Parallel test runner ────────────────────────────────────────────────────

// ParallelCases runs multiple independent test scenarios concurrently,
// sharing a single Harness (thread-safe engine).
type ParallelCases struct {
	h     *Harness
	cases []testCase
	mu    sync.Mutex
}

type testCase struct {
	name string
	fn   func(t *testing.T, h *Harness)
}

func NewParallelCases(h *Harness) *ParallelCases {
	return &ParallelCases{h: h}
}

func (pc *ParallelCases) Add(name string, fn func(t *testing.T, h *Harness)) {
	pc.cases = append(pc.cases, testCase{name: name, fn: fn})
}

func (pc *ParallelCases) Run(t *testing.T) {
	for _, tc := range pc.cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tc.fn(t, pc.h)
		})
	}
}

// ── Logging ─────────────────────────────────────────────────────────────────

func init() {
	if os.Getenv("E2E_VERBOSE") != "" {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}
}
