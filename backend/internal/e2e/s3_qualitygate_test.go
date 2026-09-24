//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"
)

// Stage 3: QualityGate Complete Pipeline (L1-L3 + L2.5)

func TestS3_L1_CompileError_AutoFix(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Ask AI to write code with a deliberate syntax issue context
	result := h.Chat(ctx, "在 main.go 中添加一个 greet 函数，接受 name string 参数，打印问候语。注意确保代码能编译通过。")
	AssertToolTriggered(t, result, "write")
	AssertNoErrors(t, result)
}

func TestS3_L1_CompilePass(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result := h.Chat(ctx, "在 main.go 中添加一个 Add(a, b int) int 函数返回 a+b")
	AssertToolTriggered(t, result, "edit")
	AssertCompiles(t, h.WorkDir)
}

func TestS3_L2_TestFailure_Revision(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	h.WaitForIndex(ctx, 5)

	// Ask to modify a function that has tests, in a way that might break them
	result := h.Chat(ctx, "修改 ValidateEmail 函数，让它要求邮箱必须以 .com 结尾才算有效")
	AssertToolTriggered(t, result, "edit")
	// The QualityGate should detect test regression and AI should attempt fix
	t.Logf("Tools triggered: %v, Errors: %v", result.ToolStarts, result.Errors)
}

func TestS3_L2_TestPass(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Simple change that shouldn't break any test
	result := h.Chat(ctx, "给 ValidateEmail 函数添加一行注释说明它的用途")
	AssertToolTriggered(t, result, "edit")
	AssertNoErrors(t, result)
}

func TestS3_L25_BaselineCapture(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Run a chat that triggers test execution to establish baseline
	result := h.Chat(ctx, "运行 internal/service 包的单元测试，告诉我结果")
	AssertToolTriggered(t, result, "exec")
}

func TestS3_L25_RegressionDetection(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	h.WaitForIndex(ctx, 5)

	// First: establish baseline by running tests
	h.Chat(ctx, "运行 go test ./internal/service/... 确认所有测试通过")

	// Then: make a breaking change
	result := h.Chat(ctx, "修改 ValidateEmail，让空字符串也返回 true（故意引入 bug）")
	// L2.5 should detect this as a regression vs baseline
	t.Logf("Second chat tools: %v, errors: %v", result.ToolStarts, result.Errors)
}
