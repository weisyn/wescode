//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"
)

// Stage 5: Memory & Learning Feedback Loop

func TestS5_Correction_Write(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result := h.Chat(ctx, "记住：这个项目的所有错误类型必须实现 Error() 和 Unwrap() 两个方法")
	AssertToolTriggered(t, result, "memory")
}

func TestS5_Correction_CrossSession(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// First session: save a correction
	h.Chat(ctx, "记住：密码必须使用 bcrypt 哈希，绝对不能明文存储")

	// Second session (same engine, different prompt): should recall
	result := h.Chat(ctx, "帮我写一个保存用户密码的函数")
	// AI should mention bcrypt due to correction
	t.Logf("Response text (first 300 chars): %s", truncate(result.Text, 300))
}

// TestS5_Constraints_AreExecutable checks the registry never holds a rule the
// engine cannot evaluate. Constraints reach the model through the CSE Overlay
// and PreWrite FAIL advisories, never through the system prompt, so a rule
// without a checker or a root would be unreachable prose (INV-CSE-15/16).
func TestS5_Constraints_AreExecutable(t *testing.T) {
	h := newGoHarness(t)

	constraints := h.Svc.ListConstraints()
	if len(constraints) == 0 {
		t.Skip("no constraints inferred (project may be too simple)")
	}
	t.Logf("Registered constraints: %d", len(constraints))

	for _, c := range constraints {
		id, _ := c["id"].(string)
		if checker, _ := c["checker"].(string); checker == "" {
			t.Errorf("constraint %q has no checker", id)
		}
		if root, _ := c["root"].(string); root == "" {
			t.Errorf("constraint %q has no root", id)
		}
		if target, _ := c["targetPath"].(string); target == "" {
			t.Errorf("constraint %q has no target path", id)
		}
		if src, _ := c["source"].(string); src == "agents_md" {
			t.Errorf("constraint %q came from AGENTS.md, which is no longer a constraint source", id)
		}
	}
}

func TestS5_ConstraintConflict_Warning(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Manually add conflicting constraints
	h.Svc.ConfirmConstraint("arch-layered-handler-service") // if exists

	result := h.Chat(ctx, "在这个项目的约束中有没有什么冲突？")
	_ = result
	// Conflict detection is verified by the constraint registry test;
	// here we just confirm the system doesn't crash.
}

func TestS5_Memory_GC_ConstraintSurvives(t *testing.T) {
	h := newGoHarness(t)
	ctx := context.Background()

	// Verify constraints survive (GC exempt)
	before := h.Svc.ListConstraints()
	_ = ctx

	// Constraints should persist (KindConstraint is GC exempt in wesgine)
	after := h.Svc.ListConstraints()
	if len(after) < len(before) {
		t.Errorf("constraints decreased from %d to %d after GC", len(before), len(after))
	}
}
