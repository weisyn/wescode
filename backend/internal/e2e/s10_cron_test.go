package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weisyn/wesapp/providerid"
	"github.com/weisyn/wescode/internal/engine"
	"github.com/weisyn/wescode/internal/store"
	wcron "github.com/weisyn/wesgine/cron"
)

// Scheduled tasks, end to end, against a real Cell and the real scheduler.
//
// Unlike the rest of this package these tests need no API key: the provider is
// a local OpenAI-compatible server, so the whole chain — job storage, model
// pinning, tick/trigger, the agent run, the run record, the conversation the
// user reads afterwards — runs hermetically and stays a regression test.
//
// It exists because every cron defect this session produced looked fine from
// the outside. The job appeared in the list, the UI said "executed once", and
// nothing ran: first because the model could not reach cron_manage, then
// because the payload kind had no host, then because the run went to a model
// nobody had chosen. None of those were visible without following a job all
// the way to its output, which is exactly what these tests do.

// fakeLLM is an OpenAI-compatible /chat/completions endpoint that streams one
// fixed reply. Records every request so a test can assert which model the
// caller asked for — the question behind the last failure.
type llmCall struct {
	model string
	body  string
}

type fakeLLM struct {
	srv   *httptest.Server
	mu    chan struct{} // 1-buffered, used as a mutex
	calls []llmCall
	reply string
}

func newFakeLLM(t *testing.T, reply string) *fakeLLM {
	t.Helper()
	f := &fakeLLM{mu: make(chan struct{}, 1), reply: reply}
	f.mu <- struct{}{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(raw, &body)
		<-f.mu
		f.calls = append(f.calls, llmCall{model: body.Model, body: string(raw)})
		f.mu <- struct{}{}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		chunk := func(payload string) {
			fmt.Fprintf(w, "data: %s\n\n", payload)
			if flusher != nil {
				flusher.Flush()
			}
		}
		content, _ := json.Marshal(f.reply)
		chunk(fmt.Sprintf(`{"id":"cmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":%s}}]}`, content))
		chunk(`{"id":"cmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":5}}`)
		chunk("[DONE]")
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// modelsAskedFor returns the models used by calls whose prompt contains marker.
//
// Filtering by prompt matters: provider health probes hit this same endpoint
// with the same model, so an unfiltered count cannot tell "the job ran" from
// "the picker checked whether the provider is alive".
func (f *fakeLLM) modelsAskedFor(marker string) []string {
	<-f.mu
	defer func() { f.mu <- struct{}{} }()
	var out []string
	for _, c := range f.calls {
		if strings.Contains(c.body, marker) {
			out = append(out, c.model)
		}
	}
	return out
}

// newCronService boots a real engine.Service against the fake provider in an
// isolated data dir, so it never touches a running app's Cell (INV-RESIL-04
// gives that one an exclusive lock).
func newCronService(t *testing.T, f *fakeLLM) *engine.Service {
	t.Helper()

	dataDir, err := os.MkdirTemp("", "wescode-cron-e2e-*")
	if err != nil {
		t.Fatalf("data dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dataDir) })
	t.Setenv("WESCODE_DATA_DIR", dataDir)

	workDir, err := os.MkdirTemp("", "wescode-cron-ws-*")
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(workDir) })

	// The Cell seeds its providers from config.yaml on disk, not from the
	// in-memory AppConfig (engine.go says so in as many words: s.cfg.Providers
	// "must NOT be used here"). Pointing WESCODE_CONFIG at a temp file is what
	// makes this test hermetic — otherwise it would quietly run against
	// whatever providers the developer has configured.
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	cfgYAML := fmt.Sprintf(`providers:
  - name: fake
    type: openai-compatible
    base_url: %q
    api_key: test-key
    model: fake-model
    is_default: true
`, f.srv.URL)
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("WESCODE_CONFIG", cfgPath)

	cfg, err := engine.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	cfg.BenchMode = true
	svc := engine.NewService(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if _, err := svc.Initialize(ctx, workDir, []string{workDir}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	// Initialize returns before the Cell is fully wired — the cron scheduler,
	// like the group service, is attached by the background postInitialize.
	// Without this wait AddCronJob answers "cron scheduler not initialized",
	// which is the same race the cron page papers over with a retry timer.
	svc.WaitPostInit(ctx)
	t.Cleanup(func() { svc.Close() })
	return svc
}

// waitForTerminalRun polls the job's run history until a record leaves
// "running", or the deadline passes. Returns the record so the caller asserts
// on the outcome rather than on the fact that something happened.
func waitForTerminalRun(t *testing.T, svc *engine.Service, jobID string, within time.Duration) wcron.RunRecord {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		runs, err := svc.ListCronRuns(context.Background(), jobID, 10)
		if err == nil {
			for _, r := range runs {
				if r.Status != wcron.RunStatusRunning {
					return r
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("job %s produced no terminal run within %s", jobID, within)
	return wcron.RunRecord{}
}

func TestCronE2E_TriggeredJobRunsOnItsOwnModelAndAnswers(t *testing.T) {
	f := newFakeLLM(t, "早报已生成。")
	svc := newCronService(t, f)
	ctx := context.Background()

	// The model comes from the picker, exactly as the form supplies it.
	available := svc.ListAvailableModels(ctx)
	if len(available) == 0 {
		t.Fatal("no models available; the fake provider did not reach the picker")
	}
	model, providerName, ok := svc.ResolveModelChoice(ctx, available[0].ID)
	if !ok {
		t.Fatalf("ResolveModelChoice(%q) failed", available[0].ID)
	}

	entry := wcron.Entry{
		ID:           "cron-e2e-1",
		Name:         "每日早报",
		AgentID:      "default",
		Actor:        "local",
		Enabled:      true,
		Model:        model,
		ProviderName: providerName,
		Schedule:     wcron.Schedule{Kind: wcron.ScheduleKindEvery, Every: time.Hour},
		Payload:      wcron.Payload{Kind: wcron.PayloadKindAgentTurn, Text: "生成今日早报"},
	}
	if err := svc.AddCronJob(ctx, entry); err != nil {
		t.Fatalf("AddCronJob: %v", err)
	}

	// Stored with the model, and the stored pair still points back at the row
	// the user picked — that is what lets the edit form preselect instead of
	// asking again. The form does the matching itself (wesui pickerIdFor,
	// which pins each option), so what this end has to guarantee is that
	// pinning the chosen option reproduces what was written.
	jobs, _, err := svc.ListCronJobs(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("ListCronJobs = %v, %v; want one job", jobs, err)
	}
	if jobs[0].Model != model {
		t.Errorf("stored model = %q, want %q", jobs[0].Model, model)
	}
	if got := providerid.Pin(available[0].ProviderID); got != jobs[0].ProviderName {
		t.Errorf("pin(chosen option) = %q, stored provider_name = %q; the form would not preselect", got, jobs[0].ProviderName)
	}

	if err := svc.TriggerCronJob(ctx, entry.ID); err != nil {
		t.Fatalf("TriggerCronJob: %v", err)
	}

	run := waitForTerminalRun(t, svc, entry.ID, 60*time.Second)
	if run.Status != wcron.RunStatusOk {
		t.Fatalf("run status = %q (err=%q), want ok", run.Status, run.ErrorMsg)
	}

	// The run went to the model the job names, not to whatever the engine
	// would have picked. This is the assertion the billing failure needed.
	asked := f.modelsAskedFor("生成今日早报")
	if len(asked) == 0 {
		t.Fatal("the scheduled run never reached the provider")
	}
	for _, m := range asked {
		if m != model {
			t.Errorf("run called model %q, want the job's model %q", m, model)
		}
	}

	// And the user can read the answer: the reply lands in the job's own
	// conversation, which is what the task list links to. Polled because the
	// transcript is written by the tracer after the run's terminal event.
	if run.SessionKey == "" {
		t.Fatal("run recorded no session; the task list has nothing to open")
	}
	var reply string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(reply, "早报已生成") {
		msgs, err := svc.ListUIMessages(ctx, run.SessionKey, "", 0)
		if err != nil {
			t.Fatalf("ListUIMessages(%s): %v", run.SessionKey, err)
		}
		for _, m := range msgs {
			if m.Role == "assistant" {
				if text := store.DisplayText(m); text != "" {
					reply = text
				}
			}
		}
		if strings.Contains(reply, "早报已生成") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !strings.Contains(reply, "早报已生成") {
		convs, cerr := svc.ListConversations(ctx)
		t.Errorf("conversation %q has no assistant reply from the run; last=%q\nconversations in cell: %+v (err=%v)",
			run.SessionKey, reply, convs, cerr)
	}
}

// TestCronE2E_JobWithoutModelFailsLoudly is the other half: a job nobody chose
// a model for must not quietly run on something the engine picked. It fails,
// and the failure names the job so the run history is actionable.
func TestCronE2E_JobWithoutModelFailsLoudly(t *testing.T) {
	f := newFakeLLM(t, "should never be produced")
	svc := newCronService(t, f)
	ctx := context.Background()

	entry := wcron.Entry{
		ID:       "cron-e2e-2",
		Name:     "无模型任务",
		AgentID:  "default",
		Actor:    "local",
		Enabled:  true,
		Schedule: wcron.Schedule{Kind: wcron.ScheduleKindEvery, Every: time.Hour},
		Payload:  wcron.Payload{Kind: wcron.PayloadKindAgentTurn, Text: "生成今日早报"},
	}
	if err := svc.AddCronJob(ctx, entry); err != nil {
		t.Fatalf("AddCronJob: %v", err)
	}
	if err := svc.TriggerCronJob(ctx, entry.ID); err != nil {
		t.Fatalf("TriggerCronJob: %v", err)
	}

	run := waitForTerminalRun(t, svc, entry.ID, 30*time.Second)
	if run.Status != wcron.RunStatusFailed {
		t.Fatalf("run status = %q, want failed", run.Status)
	}
	if !strings.Contains(run.ErrorMsg, "无模型任务") {
		t.Errorf("run error %q does not name the job", run.ErrorMsg)
	}
	if got := f.modelsAskedFor("生成今日早报"); len(got) != 0 {
		t.Errorf("job with no model still called the provider with %v", got)
	}
}
