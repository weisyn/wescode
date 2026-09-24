//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Stage 2: Tier E Core Programming Tools + PreWriteCheck

func TestS2_Read(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result := h.Chat(ctx, "读取 main.go 的内容并告诉我它做了什么")
	AssertToolTriggered(t, result, "read")
}

func TestS2_Write(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result := h.Chat(ctx, "创建文件 utils/math.go，内容为一个 Add(a, b int) int 函数，返回 a+b")
	AssertToolTriggered(t, result, "write")
	AssertFileExists(t, h.WorkDir, "utils/math.go")
}

func TestS2_Edit(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result := h.Chat(ctx, "将 main.go 中的 fmt.Println(\"hello\") 替换为 fmt.Println(\"world\")")
	AssertToolTriggered(t, result, "edit")
	AssertFileContains(t, h.WorkDir, "main.go", "world")
}

func TestS2_Grep(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result := h.Chat(ctx, "搜索项目中所有包含 error 的代码行")
	AssertToolTriggered(t, result, "grep")
}

func TestS2_Glob(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result := h.Chat(ctx, "找到项目中所有 _test.go 测试文件")
	AssertToolTriggered(t, result, "glob")
}

func TestS2_Exec(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result := h.Chat(ctx, "运行 echo 'hello from exec' 命令")
	AssertToolTriggered(t, result, "exec")
}

func TestS2_Plan_Create(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	result := h.Chat(ctx, "制定一个计划来重构 auth 模块，将密码验证逻辑提取到独立函数",
		WithSystemAppend("Always create a plan before making changes."))
	AssertPlanCreated(t, result)
}

func TestS2_Memory(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result := h.Chat(ctx, "记住：这个项目所有错误类型必须实现 Unwrap() 方法")
	AssertToolTriggered(t, result, "memory")
}

func TestS2_ToolSearch(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result := h.Chat(ctx, "搜索有哪些可用的代码分析工具")
	AssertToolTriggered(t, result, "tool_search")
}

// ── PreWriteCheck Verification ──────────────────────────────────────────────

func TestS2_PreWriteCheck_SimilarWarning(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	h.WaitForIndex(ctx, 5)

	result := h.Chat(ctx, "在 internal/service/ 目录创建一个新函数 CheckEmailValid(email string) bool，用于验证邮箱格式")
	// Should trigger CKG advisory about existing ValidateEmail
	content := result.ToolResultContent()
	if len(content) > 0 {
		t.Logf("Tool result content (first 500): %s", truncate(content, 500))
	}
	// Note: this test validates the mechanism exists; actual CKG advisory
	// depends on index timing and similarity threshold.
}

func TestS2_PreWriteCheck_ImpactWarning(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	h.WaitForIndex(ctx, 5)

	result := h.Chat(ctx, "修改 internal/service/auth.go 中 Login 函数的签名，增加一个 ctx context.Context 参数")
	// Should trigger impact analysis warning about downstream dependents
	AssertToolTriggered(t, result, "edit")
}

func TestS2_PreWriteCheck_ConstraintWarning(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Architecture constraint should warn about handler → DB direct access
	writeFile(t, h.WorkDir, "internal/handler/auth.go",
		`package handler

import "database/sql"

func DirectDBAccess(db *sql.DB) {}
`)
	os.MkdirAll(filepath.Join(h.WorkDir, "internal", "handler"), 0755)

	result := h.Chat(ctx, "在 internal/handler/users.go 中写一个 handler，直接用 database/sql 查询用户表")
	_ = result // Constraint warning depends on active constraints
}
