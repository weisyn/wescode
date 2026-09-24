package engine

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/weisyn/wescode/internal/deviceagent"
	"github.com/weisyn/wescode/internal/notify"
)

// EngineHealthWatchdog periodically checks engine responsiveness and sends
// health status notifications to the IDE Extension.
//
// Debouncing: to avoid false alarms from transient stalls, `healthy` only
// flips to false after `unhealthyThreshold` consecutive probe failures. On
// state transitions we emit `engine/unhealthy` / `engine/healthy` (distinct
// from the noisy per-tick `engine/health` sample) so the frontend can gate
// ChatInput / StatusBar without flapping.
type EngineHealthWatchdog struct {
	svc                *Service
	notifier           deviceagent.NotifyFn
	interval           time.Duration
	unhealthyThreshold int32
	cancel             context.CancelFunc
	wg                 sync.WaitGroup
	healthy            atomic.Bool
	consecutiveFail    atomic.Int32
	firstCheck         atomic.Bool // true until the first successful check fires engine/healthy
}

type healthStatus struct {
	Healthy      bool   `json:"healthy"`
	Initialized  bool   `json:"initialized"`
	HasRuntime   bool   `json:"hasRuntime"`
	RunActive    bool   `json:"runActive"`
	CheckLatency int64  `json:"checkLatencyMs"`
	Error        string `json:"error,omitempty"`
}

func newEngineHealthWatchdog(svc *Service, notifier deviceagent.NotifyFn) *EngineHealthWatchdog {
	w := &EngineHealthWatchdog{
		svc:                svc,
		notifier:           notifier,
		interval:           30 * time.Second,
		unhealthyThreshold: 3,
	}
	w.healthy.Store(true)
	w.firstCheck.Store(true)
	return w
}

func (w *EngineHealthWatchdog) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	w.cancel = cancel
	w.wg.Add(1)
	go w.loop(ctx)
}

func (w *EngineHealthWatchdog) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
}

func (w *EngineHealthWatchdog) Healthy() bool {
	return w.healthy.Load()
}

func (w *EngineHealthWatchdog) loop(ctx context.Context) {
	defer w.wg.Done()

	// Grace period: skip the first tick after engine init to avoid false
	// positives while wesgine Start() is still running DB migrations, skill
	// seeding, etc.
	select {
	case <-ctx.Done():
		return
	case <-time.After(w.interval):
	}

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.check(ctx)
		}
	}
}

func (w *EngineHealthWatchdog) check(ctx context.Context) {
	start := time.Now()
	status := healthStatus{
		Initialized: w.svc.initialized,
		RunActive:   atomic.LoadInt32(&w.svc.fgRunInFlight) == 1,
	}

	if !status.Initialized {
		status.Healthy = false
		status.Error = "engine not initialized"
		w.recordFailure(status)
		return
	}

	// Probe engine responsiveness via a lightweight call with timeout.
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	probeDone := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				probeDone <- &probeError{msg: "engine panic during health check"}
			}
		}()
		// Use TryLock to avoid blocking on Initialize/Close/Run which may
		// hold mu for extended periods. If the lock is contended, treat it
		// as "engine busy" (healthy) rather than timing out after 5s.
		if !w.svc.mu.TryLock() {
			status.HasRuntime = w.svc.runtime != nil
			probeDone <- nil // busy = healthy (not stuck)
			return
		}
		cell := w.svc.cell
		w.svc.mu.Unlock()
		if cell == nil {
			probeDone <- &probeError{msg: "cell is nil"}
			return
		}
		if err := cell.EnsureActive(probeCtx); err != nil {
			probeDone <- &probeError{msg: err.Error()}
			return
		}
		_ = cell.Tools().Names()
		status.HasRuntime = w.svc.runtime != nil
		probeDone <- nil
	}()

	select {
	case <-probeCtx.Done():
		status.Healthy = false
		status.Error = "engine probe timed out (5s)"
		status.CheckLatency = time.Since(start).Milliseconds()
		slog.Warn("[watchdog] engine health check timed out", "latencyMs", status.CheckLatency)
		w.recordFailure(status)
		return
	case err := <-probeDone:
		status.CheckLatency = time.Since(start).Milliseconds()
		if err != nil {
			status.Healthy = false
			status.Error = err.Error()
			slog.Warn("[watchdog] engine health check failed", "error", err, "latencyMs", status.CheckLatency)
			w.recordFailure(status)
			return
		}
		status.Healthy = true
		w.recordSuccess(status)
	}
}

// recordFailure increments the consecutive-failure counter. When it crosses
// `unhealthyThreshold`, flip `healthy` to false and emit the state-change
// `engine/unhealthy` notification. The sampled `engine/health` still fires
// every tick for observers that want raw latency data.
func (w *EngineHealthWatchdog) recordFailure(status healthStatus) {
	fails := w.consecutiveFail.Add(1)
	w.notify(notify.EngineHealth, status)
	if fails == w.unhealthyThreshold && w.healthy.CompareAndSwap(true, false) {
		slog.Warn("[watchdog] engine marked unhealthy",
			"consecutive_failures", fails, "reason", status.Error)
		w.notify(notify.EngineUnhealthy, map[string]any{
			"reason":               status.Error,
			"consecutive_failures": fails,
			"latency_ms":           status.CheckLatency,
		})
	}
}

// recordSuccess resets the consecutive-failure counter. On transition from
// unhealthy → healthy emit `engine/healthy` so the frontend can re-enable
// ChatInput / clear the StatusBar warning.
//
// Also fires on the very first successful check after watchdog start so
// the frontend gets an explicit healthy signal — covers the case where a
// previous backend process exit left the TS side in Unhealthy state and
// the new watchdog starts with healthy=true (CompareAndSwap would no-op).
func (w *EngineHealthWatchdog) recordSuccess(status healthStatus) {
	w.consecutiveFail.Store(0)
	w.notify(notify.EngineHealth, status)
	firstTime := w.firstCheck.CompareAndSwap(true, false)
	if firstTime || w.healthy.CompareAndSwap(false, true) {
		slog.Info("[watchdog] engine healthy", "latencyMs", status.CheckLatency, "firstCheck", firstTime)
		w.notify(notify.EngineHealthy, map[string]any{
			"latency_ms":  status.CheckLatency,
			"first_check": firstTime,
		})
	}
}

func (w *EngineHealthWatchdog) notify(method notify.Method, params any) {
	if w.notifier == nil {
		return
	}
	if err := w.notifier(method, params); err != nil {
		slog.Debug("[watchdog] failed to send notification", "method", method, "error", err)
	}
}

type probeError struct {
	msg string
}

func (e *probeError) Error() string { return e.msg }
