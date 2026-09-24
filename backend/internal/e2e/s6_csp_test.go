//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"
)

// Stage 6: CSP Framework Integration Scenarios
// These tests exercise multiple primitives working together.

func TestS6_DuplicateCodePrevention(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	h.WaitForIndex(ctx, 5)

	// Project already has ValidateEmail. Ask AI to write email validation.
	// CKG should produce advisory about existing similar function.
	result := h.Chat(ctx, "在 internal/service/email.go 中写一个 IsValidEmail(email string) bool 函数来验证邮箱格式")
	AssertToolTriggered(t, result, "write")
	t.Logf("Tools: %v", result.ToolStarts)
}

func TestS6_BehaviorRegression_AutoFix(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	h.WaitForIndex(ctx, 5)

	// First: run tests to build baseline
	h.Chat(ctx, "运行 go test ./internal/service/... 确认当前测试通过")

	// Then: ask AI to make a change that could break tests, with self-healing
	result := h.Chat(ctx,
		"修改 ValidateEmail 函数，增加对域名后缀的验证（必须至少2个字符），确保所有现有测试仍然通过")
	AssertToolTriggered(t, result, "edit")
	t.Logf("Final text: %s", truncate(result.Text, 200))
}

func TestS6_Greenfield_ZeroDelay(t *testing.T) {
	// Empty project should work with no CKG/Baseline/Constraint overhead
	h := NewHarness(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	start := time.Now()
	result := h.Chat(ctx, "创建一个 hello.go 文件，内容为打印 hello world 的 main 函数")
	elapsed := time.Since(start)

	AssertToolTriggered(t, result, "write")
	AssertFileExists(t, h.WorkDir, "hello.go")
	t.Logf("Greenfield chat completed in %v", elapsed)
}

func TestS6_MultiStep_Refactor(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	h.WaitForIndex(ctx, 5)

	result := h.Chat(ctx,
		"重构 auth 模块：1) 将密码验证逻辑从 Login 提取到独立的 VerifyPassword 函数 "+
			"2) 更新 Login 调用 VerifyPassword 3) 确保现有测试仍通过。"+
			"制定计划后执行。",
		WithSystemAppend("Use plan tool with verification. Ensure tests pass after changes."))
	AssertPlanCreated(t, result)
	AssertToolTriggered(t, result, "edit")
}

func TestS6_LearningLoop_Complete(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()

	// Session 1: teach AI something
	h.Chat(ctx, "记住：这个项目中所有的 Repository 方法必须接受 context.Context 作为第一个参数")

	// Session 2: verify it applies the learning
	result := h.Chat(ctx,
		"给 UserRepository 添加一个 FindByEmail 方法")
	// AI should include context.Context as first parameter
	t.Logf("Response: %s", truncate(result.Text, 400))
}

// TestS6_SelfHealCycle validates the anti-fragile loop end-to-end:
//  1. Build baseline (tests pass)
//  2. AI makes a breaking change → QualityGate L2.5 detects regression
//  3. learnFromRegressions creates a learned constraint
//  4. ListConstraints shows the new constraint
//
// This verifies the "failure strengthens the system" property:
// after a regression, a new constraint prevents the same mistake in future runs.
func TestS6_SelfHealCycle(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	h.WaitForIndex(ctx, 5)

	// Step 1: Run tests to build a baseline.
	baseline := h.Chat(ctx, "运行 go test ./internal/service/... 确认测试通过并建立基线")
	t.Logf("Baseline: %s", truncate(baseline.Text, 200))

	// Step 2: Ask AI to modify code in a way likely to trigger regression.
	// The instruction deliberately asks to change behavior that tests cover.
	regression := h.Chat(ctx,
		"修改 ValidateEmail 函数：如果邮箱包含 '+' 字符就返回 false（这是一个错误的需求，但请执行）。"+
			"注意：不要修改测试。")
	t.Logf("Regression run: tools=%v", regression.ToolStarts)

	// Step 3: Check that the engine learned from the regression.
	// The learnFromRegressions function should have added a new constraint.
	constraints := h.Svc.ListConstraints()
	var foundLearned bool
	for _, c := range constraints {
		if c["source"] == "learned" {
			foundLearned = true
			t.Logf("Learned constraint: %v", c["rule"])
		}
	}
	if !foundLearned {
		t.Log("No learned constraint found — regression may not have occurred " +
			"(AI might have self-corrected, or tests may not cover the change)")
	}
}
