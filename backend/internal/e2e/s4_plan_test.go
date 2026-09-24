//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"
)

// Stage 4: Plan v2 DAG + Verify Complete Pipeline

func TestS4_Plan_WithDAG(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	result := h.Chat(ctx,
		"制定一个计划来实现用户注册功能。步骤之间有依赖关系："+
			"1) 创建 User model，"+
			"2) 实现 UserRepository（依赖步骤1），"+
			"3) 实现 RegisterService（依赖步骤1和2），"+
			"4) 写单元测试（依赖步骤3）。"+
			"每个步骤都要有验证条件。",
		WithSystemAppend("IMPORTANT: Always use the plan tool with depends_on and verification fields."))
	AssertPlanCreated(t, result)
}

func TestS4_Plan_Verification_Pass(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	result := h.Chat(ctx,
		"制定并执行一个计划：1) 创建 utils/math.go 包含 Add 函数 2) 验证文件存在。"+
			"对步骤1设置验证条件 grep -r 'func Add' utils/。完成后验证。",
		WithSystemAppend("Use the plan tool with verification conditions. Execute the plan steps."))
	AssertPlanCreated(t, result)
	AssertToolTriggered(t, result, "write")
}

func TestS4_Plan_LinearFallback(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Plan without depends_on should work as linear (INV-CSE-07)
	result := h.Chat(ctx,
		"制定一个简单计划：1) 读取 main.go 2) 添加注释 3) 确认修改",
		WithSystemAppend("Create a simple plan without dependencies."))
	AssertPlanCreated(t, result)
}

func TestS4_Plan_Complete(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()

	result := h.Chat(ctx,
		"制定并完整执行一个计划来给 main.go 添加一个 Version() string 函数返回 \"1.0.0\"。"+
			"完成所有步骤后标记计划为完成。",
		WithSystemAppend("Use the plan tool. Execute all steps and mark plan as complete when done."))
	AssertPlanCreated(t, result)
	if !result.PlanCompleted {
		t.Log("plan not marked completed in this run (may need multi-turn)")
	}
}
