//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"
)

// Stage 1: Code Intelligence Tool Trigger Verification
// Each test sends a prompt designed to trigger a specific Tier C tool.

func TestS1_SearchSymbols(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	h.WaitForIndex(ctx, 5)

	result := h.Chat(ctx, "列出这个项目中所有导出的函数和类型")
	AssertToolTriggered(t, result, "search_symbols")
}

func TestS1_FindReferences(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	h.WaitForIndex(ctx, 5)

	result := h.Chat(ctx, "查找 AuthService 的所有引用位置")
	AssertToolTriggered(t, result, "find_references")
}

func TestS1_ProjectMap(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result := h.Chat(ctx, "展示这个项目的整体目录结构和文件组织")
	AssertToolTriggered(t, result, "project_map")
}

func TestS1_GetDiagnostics(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result := h.Chat(ctx, "当前项目有什么编译错误或诊断问题？")
	AssertToolTriggered(t, result, "get_diagnostics")
}

func TestS1_TraceVariable(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	h.WaitForIndex(ctx, 5)

	result := h.Chat(ctx, "追踪 internal/service/auth.go 中 username 参数的数据流向")
	AssertToolTriggered(t, result, "trace_variable")
}

func TestS1_ControlFlow(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	h.WaitForIndex(ctx, 5)

	result := h.Chat(ctx, "分析 Login 函数的控制流路径，有哪些分支？")
	AssertToolTriggered(t, result, "control_flow")
}

func TestS1_FindOrphans(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	h.WaitForIndex(ctx, 5)

	result := h.Chat(ctx, "这个项目中有哪些孤立的导出函数，没有被任何代码调用？")
	AssertToolTriggered(t, result, "find_orphans")
}

func TestS1_ImpactAnalysis(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	h.WaitForIndex(ctx, 5)

	result := h.Chat(ctx, "如果我修改了 AuthService 的 Login 方法签名，会影响哪些下游调用者？")
	AssertToolTriggered(t, result, "impact_analysis")
}

func TestS1_FindSimilar(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	h.WaitForIndex(ctx, 5)

	result := h.Chat(ctx, "项目中有没有跟 ValidateEmail 功能类似的函数？")
	AssertToolTriggered(t, result, "find_similar")
}

func TestS1_AnalyzeCrash(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result := h.Chat(ctx, "分析这个 crash 日志：goroutine 1 [running]:\nmain.main()\n\t/app/main.go:15 +0x28\npanic: runtime error: invalid memory address or nil pointer dereference")
	AssertToolTriggered(t, result, "analyze_crash")
}

func TestS1_SuggestBreakpoint(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	h.WaitForIndex(ctx, 5)

	result := h.Chat(ctx, "如果我想调试用户登录失败的问题，应该在哪里设置断点？")
	AssertToolTriggered(t, result, "suggest_breakpoint")
}

// helper to create Go project harness (shared across stage 1 tests)
func newGoHarness(t *testing.T) *Harness {
	t.Helper()
	dir := GenerateGoProject(t)
	return NewHarness(t, dir)
}
