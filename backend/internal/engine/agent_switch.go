package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// agentSwitchNotice retires the previous role's traces when the caller changed
// the answering Agent partway through an existing session, and returns the
// system-prompt addendum that announces the change.
//
// The Agent's persona reaches the model as AppRunRequest.SystemPrompt and is
// rebuilt on every Run, so a switch is already in effect the moment the user
// picks a new role. Two things are not rebuilt, and both outlive the switch:
//
//   - The conversation. LoadHistory prepends every prior turn, including the
//     previous role's own answer to "who are you". Against a system prompt
//     saying "you are a Go engineer", its own recent sentence saying "I am the
//     frontend engineer" wins on proximity — nearer, more specific, its own
//     voice. Announcing the switch is the counterweight; nothing else in the
//     context marks that sentence as stale.
//
//   - The CognitiveSettlement snapshot. This one is worse, and it is why the
//     announcement alone was not enough. The snapshot summarizes the session
//     for the next run and renders into the system prompt, so a stale claim in
//     it does not sit in the history to be argued with — it sits at the same
//     authority as the persona it contradicts. Worse, it is self-reinforcing:
//     each run's summary re-derives from the last. Session ses-635534e8 on
//     2026-09-02 is the record — a Go-engineer run wrote the open question
//     「当前生效的角色是"前端工程师"」into session state, and every later run
//     read it back as settled fact regardless of the resolved persona.
//     Clearing it costs the accumulated task summary; a role change is exactly
//     the case where that framing is void, so the cost is the point.
//
// Returns "" when there is nothing to announce (first turn of a session, or the
// role is unchanged).
func (s *Service) agentSwitchNotice(ctx context.Context, sessionID, agentID string) string {
	if sessionID == "" || agentID == "" {
		return ""
	}
	prevID := s.previousSessionAgent(ctx, sessionID)
	s.sessionAgents.Store(sessionID, agentID)
	if prevID == "" || prevID == agentID {
		return ""
	}
	s.clearSettlement(ctx, sessionID)
	prev := s.agentDisplayName(ctx, prevID)
	cur := s.agentDisplayName(ctx, agentID)
	return fmt.Sprintf(`
[身份切换]
本会话此前由「%s」应答，从本轮起改由「%s」应答。
历史消息中出现过的自我介绍与职责描述属于上一个身份，现已失效。
被问到"你是谁"/"你的职责"时，以当前身份定义作答，不要沿用历史消息里的旧身份。`, prev, cur)
}

// clearSettlement drops the session's settlement snapshot. Best-effort: the
// run must proceed either way, and a surviving snapshot degrades to the
// pre-fix behaviour rather than breaking the turn.
func (s *Service) clearSettlement(ctx context.Context, sessionID string) {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return
	}
	if err := cell.Sessions().ClearSettlement(ctx, "local", sessionID); err != nil {
		slog.Warn("engine: clear settlement on agent switch failed",
			"session_id", sessionID, "error", err)
		return
	}
	slog.Info("engine: settlement cleared on agent switch", "session_id", sessionID)
}

// previousSessionAgent returns the Agent that answered this session last.
//
// The in-process map is authoritative for a live session. On a miss (first turn
// after a restart, or a session opened from history) the answer comes from the
// most recent persisted message's agent_id — wes_sessions.agent_id is written
// INSERT OR IGNORE at first run and therefore reports the session's original
// role forever, which is the wrong question here.
func (s *Service) previousSessionAgent(ctx context.Context, sessionID string) string {
	if v, ok := s.sessionAgents.Load(sessionID); ok {
		if id, _ := v.(string); id != "" {
			return id
		}
	}
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return ""
	}
	// Tail of the transcript only: the newest row carrying an agent_id is all
	// this needs, and a full read costs a whole conversation on the hot path.
	msgs, err := cell.Sessions().Messages(ctx, "local", sessionID, "", 8)
	if err != nil || len(msgs) == 0 {
		return ""
	}
	var newest time.Time
	prev := ""
	for _, m := range msgs {
		if m.AgentID == "" {
			continue
		}
		if prev == "" || m.CreatedAt.After(newest) {
			prev, newest = m.AgentID, m.CreatedAt
		}
	}
	return prev
}

// agentDisplayName resolves the user-facing Agent name, falling back to the id
// so a deleted or unregistered Agent still reads as a distinct identity rather
// than as an empty quote.
func (s *Service) agentDisplayName(ctx context.Context, agentID string) string {
	if agentID == "" || agentID == "default" {
		return "通用助手"
	}
	if view, err := s.GetAgent(ctx, agentID); err == nil && view != nil {
		if name := strings.TrimSpace(view.Name); name != "" {
			return name
		}
	}
	return agentID
}

// ForgetSessionAgent drops the tracked role for a session. Called on delete so
// the map does not outlive the conversations it describes.
func (s *Service) ForgetSessionAgent(sessionID string) {
	if sessionID != "" {
		s.sessionAgents.Delete(sessionID)
	}
}
