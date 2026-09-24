package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// LogWatcher monitors background tasks (BGTask) for error patterns.
// Each monitored task has a goroutine that periodically polls the task
// output and scans for anomalies, pushing results to DebugEventStore.
type LogWatcher struct {
	mu       sync.Mutex
	monitors map[string]*logMonitorEntry
	store    *DebugEventStore
	stopCh   chan struct{}
	stopped  bool
}

type logMonitorEntry struct {
	taskID         string
	task           *tool.BGTask
	cancelFn       context.CancelFunc
	customPatterns []string
	startedAt      time.Time
	lastScanLen    int
	anomalyCount   int
	anomalies      []LogErrorMatch
}

// NewLogWatcher creates a LogWatcher backed by the given DebugEventStore.
func NewLogWatcher(store *DebugEventStore) *LogWatcher {
	return &LogWatcher{
		monitors: make(map[string]*logMonitorEntry),
		store:    store,
		stopCh:   make(chan struct{}),
	}
}

// StartMonitor begins monitoring a BGTask for error patterns.
// pollInterval defaults to 5s. customPatterns are additional regex strings
// to match (on top of the built-in logErrorPatterns).
func (w *LogWatcher) StartMonitor(taskID string, task *tool.BGTask, customPatterns []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.stopped {
		return fmt.Errorf("log watcher is stopped")
	}
	if _, exists := w.monitors[taskID]; exists {
		return fmt.Errorf("task %q is already being monitored", taskID)
	}
	if task == nil {
		return fmt.Errorf("task is nil")
	}

	ctx, cancel := context.WithCancel(context.Background())
	entry := &logMonitorEntry{
		taskID:         taskID,
		task:           task,
		cancelFn:       cancel,
		customPatterns: customPatterns,
		startedAt:      time.Now(),
	}
	w.monitors[taskID] = entry

	go w.pollLoop(ctx, entry)

	slog.Info("[log_watcher] started monitoring",
		"task_id", taskID,
		"custom_patterns", len(customPatterns),
	)
	return nil
}

// StopMonitor stops monitoring a specific task.
func (w *LogWatcher) StopMonitor(taskID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	entry, exists := w.monitors[taskID]
	if !exists {
		return fmt.Errorf("task %q is not being monitored", taskID)
	}
	entry.cancelFn()
	delete(w.monitors, taskID)
	slog.Info("[log_watcher] stopped monitoring", "task_id", taskID, "anomalies", entry.anomalyCount)
	return nil
}

// Status returns the current monitoring status for a task, or all tasks if taskID is empty.
func (w *LogWatcher) Status(taskID string) []LogMonitorStatus {
	w.mu.Lock()
	defer w.mu.Unlock()

	if taskID != "" {
		entry, exists := w.monitors[taskID]
		if !exists {
			return nil
		}
		return []LogMonitorStatus{entryToStatus(entry)}
	}

	out := make([]LogMonitorStatus, 0, len(w.monitors))
	for _, entry := range w.monitors {
		out = append(out, entryToStatus(entry))
	}
	return out
}

// LogMonitorStatus is a snapshot of a single monitored task.
type LogMonitorStatus struct {
	TaskID          string          `json:"taskId"`
	Running         bool            `json:"running"`
	MonitoringSince time.Time       `json:"monitoringSince"`
	AnomalyCount    int             `json:"anomalyCount"`
	Anomalies       []LogErrorMatch `json:"anomalies,omitempty"`
}

func entryToStatus(e *logMonitorEntry) LogMonitorStatus {
	taskRunning := !e.task.IsDone()
	return LogMonitorStatus{
		TaskID:          e.taskID,
		Running:         taskRunning,
		MonitoringSince: e.startedAt,
		AnomalyCount:    e.anomalyCount,
		Anomalies:       e.anomalies,
	}
}

// Stop terminates all monitoring goroutines.
func (w *LogWatcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.stopped {
		return
	}
	w.stopped = true
	close(w.stopCh)

	for id, entry := range w.monitors {
		entry.cancelFn()
		delete(w.monitors, id)
	}
	slog.Info("[log_watcher] stopped all monitors")
}

const logWatchPollInterval = 5 * time.Second

func (w *LogWatcher) pollLoop(ctx context.Context, entry *logMonitorEntry) {
	ticker := time.NewTicker(logWatchPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.scanTask(entry)
			if entry.task.IsDone() {
				w.scanTask(entry)
				w.mu.Lock()
				if _, exists := w.monitors[entry.taskID]; exists {
					delete(w.monitors, entry.taskID)
					slog.Info("[log_watcher] task finished, auto-stopped monitoring",
						"task_id", entry.taskID,
						"anomalies", entry.anomalyCount,
					)
				}
				w.mu.Unlock()
				return
			}
		}
	}
}

func (w *LogWatcher) scanTask(entry *logMonitorEntry) {
	output, _, _, _ := entry.task.Poll()
	if len(output) <= entry.lastScanLen {
		return
	}

	newContent := output[entry.lastScanLen:]
	entry.lastScanLen = len(output)

	matches := ScanLogErrors(newContent)
	language, isCrash := DetectCrash(newContent)
	if isCrash {
		section := ExtractCrashSection(newContent, language)
		if w.store != nil {
			event := DebugEvent{
				Kind:      "crash",
				Source:    "log_monitor:" + entry.taskID,
				CrashText: truncateCrash(section, 5000),
				Language:  language,
			}
			w.store.Push(event)
		}
		entry.anomalyCount++
		entry.anomalies = appendBounded(entry.anomalies, LogErrorMatch{
			Level:   "crash",
			Pattern: language + "_crash",
			Line:    truncateLineForMatch(section, 200),
		}, 50)
		return
	}

	if len(matches) == 0 {
		return
	}

	entry.anomalyCount += len(matches)
	for _, m := range matches {
		entry.anomalies = appendBounded(entry.anomalies, m, 50)
	}

	if w.store != nil {
		var sb strings.Builder
		for _, m := range matches {
			sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", m.Level, m.Pattern, m.Line))
		}
		hasCrash := false
		for _, m := range matches {
			if m.Level == "crash" {
				hasCrash = true
				break
			}
		}
		kind := "log_anomaly"
		if hasCrash {
			kind = "crash"
		}
		w.store.Push(DebugEvent{
			Kind:      kind,
			Source:    "log_monitor:" + entry.taskID,
			CrashText: truncateCrash(sb.String(), 3000),
		})
		slog.Debug("[log_watcher] anomalies detected",
			"task_id", entry.taskID,
			"count", len(matches),
			"kind", kind,
		)
	}
}

func appendBounded(s []LogErrorMatch, m LogErrorMatch, max int) []LogErrorMatch {
	if len(s) >= max {
		s = s[1:]
	}
	return append(s, m)
}
