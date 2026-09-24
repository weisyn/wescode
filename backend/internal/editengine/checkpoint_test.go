package editengine

// CheckpointManager 的行为测试。存储后端是影子 git 仓库（EE-15），所以这些测试
// 需要 git 可执行——这不是可以 mock 掉的实现细节，git 就是存储层。
//
// 随双路径删除而删掉的测试（断言的是即将不存在的内部机制，不是用户可见行为）：
//
//   - TestCheckpointManager_NonGitCreate      断言 cp.ID == "mem:run-1"
//   - TestCheckpointManager_DoubleBackupIgnored  断言内存单槽"只记首次"语义
//
// 其余保留断言、改 setup：树级快照下没有"逐文件先备份"这一步，Create 一次覆盖
// 整棵工作树。

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// newTestCheckpointManager 建一个项目目录 + 独立的影子仓库根。
//
// 项目目录里**没有** .git：影子仓库不问用户项目有没有版本控制，这正是 EE-15 要
// 消灭的那个分岔。
func newTestCheckpointManager(t *testing.T) (*CheckpointManager, string) {
	t.Helper()
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	return NewCheckpointManager(filepath.Join(root, "shadow"), proj, nil), proj
}

func TestCheckpointManager_Latest(t *testing.T) {
	ctx := context.Background()
	cm, proj := newTestCheckpointManager(t)
	mustWrite(t, filepath.Join(proj, "a.txt"), "x")

	cp, err := cm.Latest(ctx)
	if err != nil {
		t.Fatalf("Latest on empty repo: %v", err)
	}
	if cp != nil {
		t.Fatal("Latest should be nil before any Create")
	}

	if _, err := cm.Create("run-1"); err != nil {
		t.Fatalf("Create run-1: %v", err)
	}
	if cp, err := cm.Latest(ctx); err != nil || cp == nil || cp.RunID != "run-1" {
		t.Fatalf("Latest = %v (err %v), want run-1", cp, err)
	}

	if _, err := cm.Create("run-2"); err != nil {
		t.Fatalf("Create run-2: %v", err)
	}
	if cp, err := cm.Latest(ctx); err != nil || cp == nil || cp.RunID != "run-2" {
		t.Fatalf("Latest = %v (err %v), want run-2", cp, err)
	}

	// 账本跨 Run（EE-16）：两个 Run 的恢复点都还在，不是只剩最后一个。
	// 此前 Run 边界会清空列表，于是回滚到不了当前 Run 之前。
	all, err := cm.List(ctx, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List = %d 个恢复点，要 2（账本必须跨 Run）", len(all))
	}
	if all[0].RunID != "run-2" || all[1].RunID != "run-1" {
		t.Errorf("List 顺序错：%v（最新的要在前）", all)
	}
}

// Reset 已删除（2026-09-20）：账本在影子仓库里，Run 边界不清空它（EE-16）。
// 此前那次清空正是"回滚到不了当前 Run 之前"的成因，所以这里断言的是它的反面——
// 新 manager（模拟进程重启）看得见旧 Run 的恢复点。
func TestCheckpointManager_LedgerSurvivesNewManager(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	shadowRoot := filepath.Join(root, "shadow")
	mustWrite(t, filepath.Join(proj, "a.txt"), "x")

	cm1 := NewCheckpointManager(shadowRoot, proj, nil)
	if _, err := cm1.Create("run-1"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	cm2 := NewCheckpointManager(shadowRoot, proj, nil)
	cp, err := cm2.Latest(ctx)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if cp == nil || cp.RunID != "run-1" {
		t.Fatalf("新 manager 看不见旧恢复点：%v（账本应在 git 里，不在内存里）", cp)
	}
}

func TestCheckpointManager_RestoreFile(t *testing.T) {
	cm, proj := newTestCheckpointManager(t)
	f := filepath.Join(proj, "hello.txt")
	mustWrite(t, f, "original content")

	cp, err := cm.Create("run-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	mustWrite(t, f, "modified content")
	if got := mustRead(t, f); got != "modified content" {
		t.Fatalf("setup: file should be modified, got %q", got)
	}

	if err := cm.RestoreFile(cp, f); err != nil {
		t.Fatalf("RestoreFile: %v", err)
	}
	if got := mustRead(t, f); got != "original content" {
		t.Errorf("restored = %q, want %q", got, "original content")
	}
}

// 快照里没有的文件是这一轮新建的，回滚应该删掉它。判据是"它在不在快照里"，
// 不是"checkout 失败了"（EE-19）。
func TestCheckpointManager_RestoreRemovesFileAbsentFromSnapshot(t *testing.T) {
	cm, proj := newTestCheckpointManager(t)
	mustWrite(t, filepath.Join(proj, "seed.txt"), "seed")

	cp, err := cm.Create("run-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newFile := filepath.Join(proj, "newfile.txt")
	mustWrite(t, newFile, "new content")

	if err := cm.RestoreFile(cp, newFile); err != nil {
		t.Fatalf("RestoreFile: %v", err)
	}
	if _, err := os.Stat(newFile); !os.IsNotExist(err) {
		t.Error("file absent from snapshot should be removed after restore")
	}
}

func TestCheckpointManager_RestoreAll(t *testing.T) {
	cm, proj := newTestCheckpointManager(t)
	f1 := filepath.Join(proj, "a.txt")
	f2 := filepath.Join(proj, "b.txt")
	mustWrite(t, f1, "aaa")
	mustWrite(t, f2, "bbb")

	cp, err := cm.Create("run-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	mustWrite(t, f1, "AAA")
	mustWrite(t, f2, "BBB")

	if err := cm.RestoreAll(cp, []string{f1, f2}); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}
	if got := mustRead(t, f1); got != "aaa" {
		t.Errorf("f1 = %q, want %q", got, "aaa")
	}
	if got := mustRead(t, f2); got != "bbb" {
		t.Errorf("f2 = %q, want %q", got, "bbb")
	}
}

// RestoreAll 只动传进来的那些文件。回滚的边界是 AI 改过的文件，不是整棵树——
// `checkout SHA -- .` 会把用户在别处的手改一起拽回快照状态。
func TestCheckpointManager_RestoreAllLeavesUntouchedFilesAlone(t *testing.T) {
	cm, proj := newTestCheckpointManager(t)
	agentFile := filepath.Join(proj, "agent.txt")
	userFile := filepath.Join(proj, "user.txt")
	mustWrite(t, agentFile, "orig")
	mustWrite(t, userFile, "orig")

	cp, err := cm.Create("run-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	mustWrite(t, agentFile, "agent-edit")
	mustWrite(t, userFile, "user-edit-elsewhere")

	if err := cm.RestoreAll(cp, []string{agentFile}); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}
	if got := mustRead(t, agentFile); got != "orig" {
		t.Errorf("agent file = %q, want %q", got, "orig")
	}
	if got := mustRead(t, userFile); got != "user-edit-elsewhere" {
		t.Errorf("回滚动了没被点名的文件：user file = %q，期望 %q", got, "user-edit-elsewhere")
	}
}

// Drop 已删除（2026-09-20）：EE-14「Accept 无删除权」从一条纪律变成物理事实——
// 没有任何 API 能摘掉恢复点，因为账本就是 git 历史。
//
// 这里断言的是那条不变量本身：快照创建之后，无论上层做了什么，它既**可枚举**
// 又**可打开**。此前 Drop 会摘掉指针，而在内存账本时代"摘掉指针"等于销毁。
func TestCheckpointManager_SnapshotIsNeverDroppable(t *testing.T) {
	ctx := context.Background()
	cm, proj := newTestCheckpointManager(t)
	f := filepath.Join(proj, "a.txt")
	mustWrite(t, f, "original")

	cp, err := cm.Create("run-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	mustWrite(t, f, "modified")

	// 可枚举：没有"忘记"这个操作了。
	all, err := cm.List(ctx, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 || all[0].ID != cp.ID {
		t.Fatalf("恢复点不可枚举：%v", all)
	}

	// 可打开：拿着 SHA 仍然恢复得回来。
	if err := cm.RestoreFile(cp, f); err != nil {
		t.Fatalf("RestoreFile: %v", err)
	}
	if got := mustRead(t, f); got != "original" {
		t.Errorf("快照打不开了：got %q, want %q", got, "original")
	}
}

func TestCheckpointManager_NilCheckpoint(t *testing.T) {
	cm, proj := newTestCheckpointManager(t)

	if err := cm.RestoreFile(nil, filepath.Join(proj, "x.txt")); err == nil {
		t.Error("RestoreFile(nil) should return error")
	}
	if err := cm.RestoreAll(nil, []string{filepath.Join(proj, "x.txt")}); err == nil {
		t.Error("RestoreAll(nil) should return error")
	}
}
