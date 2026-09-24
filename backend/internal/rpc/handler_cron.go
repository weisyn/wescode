package rpc

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	"github.com/weisyn/wescode/internal/store"
	"github.com/weisyn/wesgine/cron"
)

type cronJobWithState struct {
	Entry cronEntryResp     `json:"entry"`
	State *cronJobStateResp `json:"state,omitempty"`
}

// cronJobStateResp projects cron.JobState with absent timestamps actually
// absent.
//
// A zero time.Time marshals to "0001-01-01T00:00:00Z", which is a non-empty
// string, so `{nextRun && …}` in the UI renders it — a one-shot job that has
// already fired (next_run_at = 0 means "nothing scheduled") displayed
// "下次 01/01 08:05". Sending a sentinel and expecting the consumer to know it
// is a sentinel is how that happens; a pointer says "none" in the one way
// every consumer already handles.
type cronJobStateResp struct {
	NextRunAt        *time.Time     `json:"next_run_at,omitempty"`
	LastRunAt        *time.Time     `json:"last_run_at,omitempty"`
	LastStatus       cron.RunStatus `json:"last_status,omitempty"`
	LastError        string         `json:"last_error,omitempty"`
	ConsecutiveFails int            `json:"consecutive_fails,omitempty"`
	LastAlertAt      *time.Time     `json:"last_alert_at,omitempty"`
}

func stateToResp(st cron.JobState) cronJobStateResp {
	return cronJobStateResp{
		NextRunAt:        nonZeroTime(st.NextRunAt),
		LastRunAt:        nonZeroTime(st.LastRunAt),
		LastStatus:       st.LastStatus,
		LastError:        st.LastError,
		ConsecutiveFails: st.ConsecutiveFails,
		LastAlertAt:      nonZeroTime(st.LastAlertAt),
	}
}

func nonZeroTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// cronEntryResp is the wire format returned to the frontend.
// Unlike cron.Entry, Duration fields are converted to seconds and field names
// match the frontend @wesui/cron TypeScript types (snake_case).
type cronEntryResp struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	AgentID string `json:"agent_id"`
	Actor   string `json:"actor,omitempty"`
	// Model and ProviderName are what the run will actually use. The picker
	// id the user originally chose is not stored and not returned — the form
	// recovers it by pinning each option and matching ProviderName (wesui
	// pickerIdFor), which is also how the other three products do it.
	Model          string             `json:"model,omitempty"`
	ProviderName   string             `json:"provider_name,omitempty"`
	Enabled        bool               `json:"enabled"`
	Schedule       cronScheduleResp   `json:"schedule"`
	Timezone       string             `json:"timezone,omitempty"`
	Payload        cron.Payload       `json:"payload"`
	MaxTurns       int                `json:"max_turns,omitempty"`
	TimeoutSec     int                `json:"timeout_sec,omitempty"`
	DeleteAfterRun bool               `json:"delete_after_run,omitempty"`
	Delivery       cron.Delivery      `json:"delivery,omitempty"`
	FailureAlert   *cron.FailureAlert `json:"failure_alert,omitempty"`
}

type cronScheduleResp struct {
	Kind     cron.ScheduleKind `json:"kind"`
	CronExpr string            `json:"cron_expr,omitempty"`
	EverySec int               `json:"every_sec,omitempty"`
	At       *time.Time        `json:"at,omitempty"`
}

func entryToResp(e cron.Entry) cronEntryResp {
	resp := cronEntryResp{
		ID:      e.ID,
		Name:    e.Name,
		AgentID: e.AgentID,
		Actor:   e.Actor,
		// The picker id is not carried back. Recovering it here would mean
		// searching the live model list for every job on every render — a
		// network round-trip to the org catalog — and the other three
		// products cannot do it at all (saas assembles its picker in the
		// browser). The form pins each option and matches ProviderName
		// instead; see wesui pickerIdFor.
		Model:        e.Model,
		ProviderName: e.ProviderName,
		Enabled:      e.Enabled,
		Schedule: cronScheduleResp{
			Kind:     e.Schedule.Kind,
			CronExpr: e.Schedule.CronExpr,
			EverySec: int(e.Schedule.Every / time.Second),
		},
		Timezone:       e.Timezone,
		Payload:        e.Payload,
		MaxTurns:       e.MaxTurns,
		TimeoutSec:     int(e.Timeout / time.Second),
		DeleteAfterRun: e.DeleteAfterRun,
		Delivery:       e.Delivery,
		FailureAlert:   e.FailureAlert,
	}
	if !e.Schedule.At.IsZero() {
		resp.Schedule.At = &e.Schedule.At
	}
	return resp
}

func (h *Handler) handleListCronJobs(ctx context.Context, _ Request) (any, *RPCError) {
	entries, states, err := h.engine.ListCronJobs(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	if entries == nil {
		return []any{}, nil
	}
	result := make([]cronJobWithState, len(entries))
	for i, e := range entries {
		item := cronJobWithState{Entry: entryToResp(e)}
		if states != nil {
			if st, ok := states[e.ID]; ok {
				resp := stateToResp(st)
				item.State = &resp
			}
		}
		result[i] = item
	}
	return result, nil
}

type cronJobDTO struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	AgentID string `json:"agent_id"`
	Actor   string `json:"actor,omitempty"`
	// ProviderID is the picker entry the user chose. The engine fields
	// (Model / ProviderName) are derived from it server-side, so the browser
	// never has to know how a picker id maps onto a provider.
	ProviderID     string             `json:"provider_id"`
	Schedule       cronScheduleDTO    `json:"schedule"`
	Timezone       string             `json:"timezone"`
	Payload        cron.Payload       `json:"payload"`
	Enabled        bool               `json:"enabled"`
	MaxTurns       int                `json:"max_turns"`
	TimeoutSec     int                `json:"timeout_sec"`
	DeleteAfterRun bool               `json:"delete_after_run"`
	Delivery       cron.Delivery      `json:"delivery"`
	FailureAlert   *cron.FailureAlert `json:"failure_alert,omitempty"`
}

type cronScheduleDTO struct {
	Kind     cron.ScheduleKind `json:"kind"`
	CronExpr string            `json:"cron_expr"`
	EverySec int               `json:"every_sec"`
	At       time.Time         `json:"at"`
}

func (d cronJobDTO) toEntry() cron.Entry {
	return cron.Entry{
		ID:      d.ID,
		Name:    d.Name,
		AgentID: d.AgentID,
		Actor:   d.Actor,
		Schedule: cron.Schedule{
			Kind:     d.Schedule.Kind,
			CronExpr: d.Schedule.CronExpr,
			Every:    time.Duration(d.Schedule.EverySec) * time.Second,
			At:       d.Schedule.At,
		},
		Timezone:       d.Timezone,
		Payload:        d.Payload,
		Enabled:        d.Enabled,
		MaxTurns:       d.MaxTurns,
		Timeout:        time.Duration(d.TimeoutSec) * time.Second,
		DeleteAfterRun: d.DeleteAfterRun,
		Delivery:       d.Delivery,
		FailureAlert:   d.FailureAlert,
	}
}

func cronGenerateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("cron-%x", b)
}

func (h *Handler) handleAddCronJob(ctx context.Context, req Request) (any, *RPCError) {
	var dto cronJobDTO
	if err := json.Unmarshal(req.Params, &dto); err != nil {
		return nil, invalidParams(err)
	}
	if dto.Name == "" {
		return nil, &RPCError{Code: -32602, Message: "name is required"}
	}
	if dto.ID == "" {
		dto.ID = cronGenerateID()
	}
	entry := dto.toEntry()
	if entry.Actor == "" {
		entry.Actor = "local"
	}
	// The model is part of the job, so a job cannot be created without one.
	// Refusing here is the point: the alternative is a task that looks
	// scheduled and then runs on whatever the engine picks, which is how a
	// reminder ended up billing an account the user never selected.
	model, providerName, ok := h.engine.ResolveModelChoice(ctx, dto.ProviderID)
	if !ok {
		return nil, &RPCError{Code: -32602, Message: "choose a model for this task"}
	}
	entry.Model, entry.ProviderName = model, providerName
	if err := h.engine.AddCronJob(ctx, entry); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[cron] cron.add", "id", entry.ID, "name", entry.Name, "agent_id", entry.AgentID, "schedule_kind", entry.Schedule.Kind)
	return map[string]any{"ok": true, "id": entry.ID}, nil
}

func (h *Handler) handleUpdateCronJob(ctx context.Context, req Request) (any, *RPCError) {
	var raw struct {
		ID    string          `json:"id"`
		Patch json.RawMessage `json:"patch"`
	}
	if err := json.Unmarshal(req.Params, &raw); err != nil {
		return nil, invalidParams(err)
	}
	if raw.ID == "" {
		return nil, &RPCError{Code: -32602, Message: "id is required"}
	}

	var patchMap map[string]json.RawMessage
	if err := json.Unmarshal(raw.Patch, &patchMap); err != nil {
		return nil, invalidParams(err)
	}

	patch := cron.EntryPatch{}
	if v, ok := patchMap["name"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return nil, invalidParams(fmt.Errorf("name: %w", err))
		}
		patch.Name = &s
	}
	if v, ok := patchMap["agent_id"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return nil, invalidParams(fmt.Errorf("agent_id: %w", err))
		}
		patch.AgentID = &s
	}
	if v, ok := patchMap["provider_id"]; ok {
		var pickerID string
		if err := json.Unmarshal(v, &pickerID); err != nil {
			return nil, invalidParams(fmt.Errorf("provider_id: %w", err))
		}
		// Same refusal as create: an edit may change the model but not clear
		// it, and an id the picker no longer offers is a stale form rather
		// than an instruction to fall back to something.
		model, providerName, ok := h.engine.ResolveModelChoice(ctx, pickerID)
		if !ok {
			return nil, &RPCError{Code: -32602, Message: "choose a model for this task"}
		}
		patch.Model = &model
		patch.ProviderName = &providerName
	}
	if v, ok := patchMap["timezone"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return nil, invalidParams(fmt.Errorf("timezone: %w", err))
		}
		patch.Timezone = &s
	}
	if v, ok := patchMap["enabled"]; ok {
		var b bool
		if err := json.Unmarshal(v, &b); err != nil {
			return nil, invalidParams(fmt.Errorf("enabled: %w", err))
		}
		patch.Enabled = &b
	}
	if v, ok := patchMap["delete_after_run"]; ok {
		var b bool
		if err := json.Unmarshal(v, &b); err != nil {
			return nil, invalidParams(fmt.Errorf("delete_after_run: %w", err))
		}
		patch.DeleteAfterRun = &b
	}
	if v, ok := patchMap["max_turns"]; ok {
		var n int
		if err := json.Unmarshal(v, &n); err != nil {
			return nil, invalidParams(fmt.Errorf("max_turns: %w", err))
		}
		patch.MaxTurns = &n
	}
	if v, ok := patchMap["timeout_sec"]; ok {
		var sec int
		if err := json.Unmarshal(v, &sec); err != nil {
			return nil, invalidParams(fmt.Errorf("timeout_sec: %w", err))
		}
		d := time.Duration(sec) * time.Second
		patch.Timeout = &d
	}
	if v, ok := patchMap["schedule"]; ok {
		var dto cronScheduleDTO
		if err := json.Unmarshal(v, &dto); err != nil {
			return nil, invalidParams(fmt.Errorf("schedule: %w", err))
		}
		sch := cron.Schedule{
			Kind:     dto.Kind,
			CronExpr: dto.CronExpr,
			Every:    time.Duration(dto.EverySec) * time.Second,
			At:       dto.At,
		}
		patch.Schedule = &sch
	}
	if v, ok := patchMap["payload"]; ok {
		var payload cron.Payload
		if err := json.Unmarshal(v, &payload); err != nil {
			return nil, invalidParams(fmt.Errorf("payload: %w", err))
		}
		patch.Payload = &payload
	}
	if v, ok := patchMap["delivery"]; ok {
		var delivery cron.Delivery
		if err := json.Unmarshal(v, &delivery); err != nil {
			return nil, invalidParams(fmt.Errorf("delivery: %w", err))
		}
		patch.Delivery = &delivery
	}
	if v, ok := patchMap["failure_alert"]; ok {
		if string(v) == "null" {
			var nilAlert *cron.FailureAlert
			patch.FailureAlert = &nilAlert
		} else {
			var alert cron.FailureAlert
			if err := json.Unmarshal(v, &alert); err != nil {
				return nil, invalidParams(fmt.Errorf("failure_alert: %w", err))
			}
			ptr := &alert
			patch.FailureAlert = &ptr
		}
	}

	if err := h.engine.UpdateCronJob(ctx, raw.ID, patch); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[cron] cron.update", "id", raw.ID)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleDeleteCronJob(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.ID == "" {
		return nil, &RPCError{Code: -32602, Message: "id is required"}
	}
	if err := h.engine.DeleteCronJob(ctx, params.ID); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[cron] cron.delete", "id", params.ID)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleTriggerCronJob(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.ID == "" {
		return nil, &RPCError{Code: -32602, Message: "id is required"}
	}
	if err := h.engine.TriggerCronJob(ctx, params.ID); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[cron] cron.trigger", "id", params.ID)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleEnableCronJob(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if err := h.engine.EnableCronJob(ctx, params.ID); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[cron] cron.enable", "id", params.ID)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleDisableCronJob(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if err := h.engine.DisableCronJob(ctx, params.ID); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[cron] cron.disable", "id", params.ID)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleListCronRuns(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		JobID string `json:"jobId"`
		Limit int    `json:"limit"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
	}
	runs, err := h.engine.ListCronRuns(ctx, params.JobID, params.Limit)
	if err != nil {
		return nil, internalError(err)
	}
	if runs == nil {
		return []any{}, nil
	}
	items := make([]map[string]any, 0, len(runs))
	for _, r := range runs {
		item := map[string]any{
			"run_id":        r.RunID,
			"job_id":        r.JobID,
			"job_name":      r.JobName,
			"triggered_at":  r.TriggeredAt,
			"completed_at":  r.CompletedAt,
			"status":        r.Status,
			"session_key":   r.SessionKey,
			"error_msg":     r.ErrorMsg,
			"input_tokens":  r.InputTokens,
			"output_tokens": r.OutputTokens,
		}
		if r.SessionKey != "" {
			if msgs, err := h.engine.ListUIMessages(ctx, r.SessionKey, "", 0); err == nil && len(msgs) > 0 {
				for i := len(msgs) - 1; i >= 0; i-- {
					if msgs[i].Role != "assistant" {
						continue
					}
					// Not msgs[i].Content: FoldToUI blanks it when the text
					// rides in content_parts, which is the normal shape for an
					// assistant reply. Reading the field directly is why this
					// column was empty for every run.
					if text := store.DisplayText(msgs[i]); text != "" {
						item["last_message"] = text
						break
					}
				}
			}
		}
		items = append(items, item)
	}
	return items, nil
}

func (h *Handler) handleCronStats(ctx context.Context, _ Request) (any, *RPCError) {
	stats, err := h.engine.CronStats(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	return map[string]any{
		"total_jobs":   stats.TotalJobs,
		"enabled_jobs": stats.EnabledJobs,
		"next_run_at":  stats.NextRunAt,
		"last_tick_at": stats.LastTickAt,
	}, nil
}
