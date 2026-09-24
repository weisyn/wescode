package rpc

import (
	"context"
	"encoding/json"

	"github.com/weisyn/wescode/internal/codeintel"
	appengine "github.com/weisyn/wescode/internal/engine"
)

func (h *Handler) handleTerminalOutput(_ context.Context, req Request) (any, *RPCError) {
	var params struct {
		SessionID string `json:"sessionId"`
		Data      string `json:"data"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, &RPCError{Code: -32602, Message: "invalid params"}
	}
	mgr := h.engine.TerminalManager()
	if mgr != nil {
		mgr.OnOutput(params.SessionID, []byte(params.Data))
	}
	h.engine.FeedTerminalOutput(params.SessionID, params.Data)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleTerminalExited(_ context.Context, req Request) (any, *RPCError) {
	var params struct {
		SessionID string `json:"sessionId"`
		ExitCode  int    `json:"exitCode"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, &RPCError{Code: -32602, Message: "invalid params"}
	}
	mgr := h.engine.TerminalManager()
	if mgr != nil {
		mgr.OnExited(params.SessionID, params.ExitCode)
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleLSPResponse(_ context.Context, req Request) (any, *RPCError) {
	var params struct {
		RequestID string          `json:"requestId"`
		Result    json.RawMessage `json:"result"`
		Error     string          `json:"error"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.RequestID == "" {
		return nil, &RPCError{Code: -32602, Message: "requestId is required"}
	}
	h.engine.HandleLSPResponse(params.RequestID, params.Result, params.Error)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleDiagnosticsReport(_ context.Context, req Request) (any, *RPCError) {
	var report DiagnosticsReport
	if err := json.Unmarshal(req.Params, &report); err != nil {
		return nil, invalidParams(err)
	}
	diags := make([]codeintel.IDEDiagnostic, len(report.Diagnostics))
	for i, d := range report.Diagnostics {
		diags[i] = codeintel.IDEDiagnostic{
			Path:     d.Path,
			Line:     d.Line,
			Column:   d.Column,
			EndLine:  d.EndLine,
			EndCol:   d.EndColumn,
			Severity: d.Severity,
			Message:  d.Message,
			Source:   d.Source,
			Code:     d.Code,
		}
	}
	h.engine.HandleDiagnosticsReport(diags)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleDebugHit(_ context.Context, req Request) (any, *RPCError) {
	var params struct {
		SessionID  string            `json:"sessionId"`
		Breakpoint string            `json:"breakpoint"`
		File       string            `json:"file"`
		Line       int               `json:"line"`
		Variables  map[string]string `json:"variables"`
		StackTrace string            `json:"stackTrace"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	var vars []appengine.DebugVariable
	for k, v := range params.Variables {
		vars = append(vars, appengine.DebugVariable{Scope: "local", Name: k, Value: v})
	}
	var frames []appengine.DebugFrame
	if params.File != "" {
		frames = append(frames, appengine.DebugFrame{Source: params.File, Line: params.Line})
	}
	h.engine.HandleDebugEvent(appengine.DebugEvent{
		Kind:      "breakpoint_hit",
		SessionID: params.SessionID,
		Frames:    frames,
		Variables: vars,
	})
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleDebugEvent(_ context.Context, req Request) (any, *RPCError) {
	var event appengine.DebugEvent
	if err := json.Unmarshal(req.Params, &event); err != nil {
		return nil, invalidParams(err)
	}
	h.engine.HandleDebugEvent(event)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleDebugResponse(_ context.Context, req Request) (any, *RPCError) {
	var params struct {
		RequestID string          `json:"requestId"`
		Result    json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	h.engine.HandleDebugResponse(params.RequestID, params.Result)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleCrashReport(_ context.Context, req Request) (any, *RPCError) {
	var params struct {
		Source   string `json:"source"`
		Text     string `json:"text"`
		Language string `json:"language"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	h.engine.HandleCrashReport(params.Source, params.Text, params.Language)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleTestResult(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Passed   int `json:"passed"`
		Failed   int `json:"failed"`
		Skipped  int `json:"skipped"`
		Failures []struct {
			Name    string `json:"name"`
			File    string `json:"file"`
			Line    int    `json:"line"`
			Message string `json:"message"`
		} `json:"failures"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	L(ctx).Info("[test/result]",
		"passed", params.Passed,
		"failed", params.Failed,
		"skipped", params.Skipped,
		"failure_count", len(params.Failures),
	)
	if params.Failed > 0 && len(params.Failures) > 0 {
		infos := make([]appengine.TestFailureInfo, len(params.Failures))
		for i, f := range params.Failures {
			infos[i] = appengine.TestFailureInfo{
				Name:    f.Name,
				File:    f.File,
				Line:    f.Line,
				Message: f.Message,
			}
		}
		h.engine.HandleTestFailures(infos)
	}
	return map[string]bool{"ok": true}, nil
}
