package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// LogMonitorToolConfig holds dependencies for the log_monitor tool.
type LogMonitorToolConfig struct {
	Watcher *LogWatcher
}

// NewLogMonitorTool creates the log_monitor tool (Extended, debug domain).
func NewLogMonitorTool(cfg LogMonitorToolConfig) tool.Tool {
	return &logMonitorTool{
		BaseTool: tool.BaseTool{Meta: tool.ToolMeta{
			Dimension:      tool.DimensionPerceive,
			Availability:   tool.AvailabilityConfigurable,
			ReadOnly:       true,
			Concurrent:     true,
			Timeout:        10 * time.Second,
			Risk:           tool.RiskSafe,
			Enabled:        true,
			Domain:         "debug",
			DomainToolTier: tool.DomainTierExtended,
		}},
		cfg: cfg,
	}
}

type logMonitorTool struct {
	tool.BaseTool
	cfg LogMonitorToolConfig
}

func (t *logMonitorTool) Name() string { return "log_monitor" }

func (t *logMonitorTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Name: "log_monitor",
		Description: "Monitor a background task's output for error patterns in real time. " +
			"Starts a background watcher that polls the task every 5 seconds and detects crashes, " +
			"errors, and anomalies. Detected issues are automatically injected into the next chat turn. " +
			"Use after exec(action=start_bg) to continuously watch a dev server or long-running process.",
		Contract: &tool.PromptContract{
			Summary: "Real-time background task log monitoring with automatic error detection.",
			WhenToUse: []tool.PromptScenario{
				{Condition: "After starting a dev server with exec(action=start_bg) and wanting to detect runtime errors automatically"},
				{Condition: "When running a background build/test process and needing to be notified of failures"},
				{Condition: "To check what errors a monitored background task has produced so far"},
			},
		},
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"action": {
					"type": "string",
					"enum": ["start", "stop", "status"],
					"description": "start: begin monitoring a background task; stop: end monitoring; status: show current monitoring state and detected anomalies"
				},
				"task_id": {
					"type": "string",
					"description": "BGTask ID from exec(action=start_bg) result. Required for start and stop."
				},
				"patterns": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Additional regex patterns to watch for (optional, for start action only)"
				}
			},
			"required": ["action"]
		}`),
	}
}

func (t *logMonitorTool) Call(_ context.Context, input json.RawMessage, tc *tool.ToolContext) (*tool.ToolResult, error) {
	var args struct {
		Action   string   `json:"action"`
		TaskID   string   `json:"task_id"`
		Patterns []string `json:"patterns"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return &tool.ToolResult{Content: "invalid arguments: " + err.Error(), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}

	watcher := t.cfg.Watcher
	if watcher == nil {
		return &tool.ToolResult{Content: "log monitoring is not available (watcher not initialized)", IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}

	switch args.Action {
	case "start":
		return t.handleStart(args.TaskID, args.Patterns, tc, watcher)
	case "stop":
		return t.handleStop(args.TaskID, watcher)
	case "status":
		return t.handleStatus(args.TaskID, watcher)
	default:
		return &tool.ToolResult{
			Content:   fmt.Sprintf("unknown action %q; use start, stop, or status", args.Action),
			IsError:   true,
			ErrorKind: tool.ErrorInvocation,
		}, nil
	}
}

func (t *logMonitorTool) handleStart(taskID string, patterns []string, tc *tool.ToolContext, watcher *LogWatcher) (*tool.ToolResult, error) {
	if taskID == "" {
		return &tool.ToolResult{Content: "task_id is required for action=start", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}

	if tc == nil || tc.BGTasks == nil {
		return &tool.ToolResult{Content: "background task registry not available", IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}

	task := tc.BGTasks.Get(taskID)
	if task == nil {
		return &tool.ToolResult{Content: fmt.Sprintf("task %q not found in current session", taskID), IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}

	if task.IsDone() {
		output, _, exitCode, _ := task.Poll()
		matches := ScanLogErrors(output)
		language, isCrash := DetectCrash(output)
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Task %q has already exited (code %d). ", taskID, exitCode))
		if isCrash {
			sb.WriteString(fmt.Sprintf("Crash detected (%s). ", language))
		}
		if len(matches) > 0 {
			sb.WriteString(fmt.Sprintf("Found %d error pattern(s):\n", len(matches)))
			for _, m := range matches {
				sb.WriteString(fmt.Sprintf("  [%s] %s: %s\n", m.Level, m.Pattern, m.Line))
			}
		} else {
			sb.WriteString("No error patterns found in output.")
		}
		return &tool.ToolResult{Content: sb.String()}, nil
	}

	if err := watcher.StartMonitor(taskID, task, patterns); err != nil {
		return &tool.ToolResult{Content: "start monitor: " + err.Error(), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}

	content := fmt.Sprintf("Started monitoring task %q. Polling every 5s for error patterns. "+
		"Detected anomalies will be automatically injected into your next chat turn. "+
		"Use log_monitor(action=status, task_id=%q) to check findings, "+
		"or log_monitor(action=stop, task_id=%q) to stop.", taskID, taskID, taskID)
	return &tool.ToolResult{Content: content}, nil
}

func (t *logMonitorTool) handleStop(taskID string, watcher *LogWatcher) (*tool.ToolResult, error) {
	if taskID == "" {
		return &tool.ToolResult{Content: "task_id is required for action=stop", IsError: true, ErrorKind: tool.ErrorInvocation}, nil
	}

	statuses := watcher.Status(taskID)
	var summary string
	if len(statuses) > 0 {
		s := statuses[0]
		summary = fmt.Sprintf(" Detected %d anomaly(ies) during monitoring.", s.AnomalyCount)
	}

	if err := watcher.StopMonitor(taskID); err != nil {
		return &tool.ToolResult{Content: "stop monitor: " + err.Error(), IsError: true, ErrorKind: tool.ErrorExecution}, nil
	}

	return &tool.ToolResult{Content: fmt.Sprintf("Stopped monitoring task %q.%s", taskID, summary)}, nil
}

func (t *logMonitorTool) handleStatus(taskID string, watcher *LogWatcher) (*tool.ToolResult, error) {
	statuses := watcher.Status(taskID)
	if len(statuses) == 0 {
		if taskID != "" {
			return &tool.ToolResult{Content: fmt.Sprintf("Task %q is not being monitored.", taskID)}, nil
		}
		return &tool.ToolResult{Content: "No tasks are currently being monitored."}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Log Monitor Status (%d task(s))\n\n", len(statuses)))
	for _, s := range statuses {
		taskState := "running"
		if !s.Running {
			taskState = "exited"
		}
		sb.WriteString(fmt.Sprintf("**%s** — task %s, monitoring since %s, %d anomaly(ies)\n",
			s.TaskID, taskState, s.MonitoringSince.Format("15:04:05"), s.AnomalyCount))

		if len(s.Anomalies) > 0 {
			sb.WriteString("\nDetected anomalies:\n")
			for i, a := range s.Anomalies {
				if i >= 20 {
					sb.WriteString(fmt.Sprintf("  ... and %d more\n", len(s.Anomalies)-20))
					break
				}
				sb.WriteString(fmt.Sprintf("  [%s] %s: %s\n", a.Level, a.Pattern, a.Line))
			}
		}
		sb.WriteByte('\n')
	}
	return &tool.ToolResult{Content: sb.String()}, nil
}
