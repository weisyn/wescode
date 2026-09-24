//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"
)

// Stage 9: Skill & Agent Management

func TestS9_SkillActivation(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Trigger a programming skill by asking a coding task
	result := h.Chat(ctx, "帮我做一次代码审查，检查 internal/service/auth.go 的代码质量")
	// Skills are activated silently; we verify the system works end-to-end
	if result.Text == "" {
		t.Error("expected non-empty response")
	}
	t.Logf("Tools triggered: %v", result.ToolStarts)
}

func TestS9_CustomAgent_Behavior(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Use the builtin-coder agent (different from orchestrator)
	result := h.Chat(ctx, "读取 main.go 内容",
		WithAgent("builtin-coder"))
	if result.Text == "" {
		t.Error("expected response from builtin-coder agent")
	}
}

func TestS9_Delegation_SubAgent(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()

	// Complex task that might trigger delegation
	result := h.Chat(ctx,
		"分析整个项目的代码质量，分别检查每个包的错误处理是否正确",
		WithSystemAppend("For complex multi-file analysis, consider delegating sub-tasks to focused sub-agents."))
	if result.Text == "" {
		t.Error("expected response")
	}
	// Check if subagent events appeared
	for _, ev := range result.AllEvents {
		if ev.Type == "subagent_start" {
			t.Log("delegation occurred: subagent_start event detected")
			return
		}
	}
	t.Log("no delegation in this run (acceptable for simple projects)")
}
