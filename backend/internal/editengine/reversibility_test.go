package editengine

// 可逆性契约（EE-13 ~ EE-19）的行为测试。
//
// 设计与不变量定义见 design/26-reversibility.md 与
// design/appendix/csp-invariants.md 第五节。
//
// Stage 1（影子仓库统一存储）之后的状态：
//
//	EE-14 / EE-16 / EE-19(删文件)  转绿 —— 内容寻址存储让"忘记"与"销毁"分开
//	EE-15(跨进程) / EE-19(吃手改)  仍红 —— 分别等 Stage 2 的账本、Stage 3 的冲突门
//
// Stage 1 相对 Stage 0 改了这些测试的 **setup**：树级快照下不存在"逐文件先备份"
// 这一步（`MemBackupBeforeEdit` 整条路径已删除），所以那几行没了。**断言与失败
// 文案逐字未变**——否则"测试转绿"证明的是我改了测试，不是我改了实现。
//
// EE-13（唯一写入 seam）与 EE-18（无恢复点不写）仍不在本文件：它们的执行点是
// Stage 2 才建立的 seam，在它存在之前，任何针对它的断言都只是对测试自己写的那行
// os.WriteFile 作证。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newReversibilityFixture 返回 (影子仓库根, 项目根)。两者分开返回是因为测试 1 要
// 用同一个影子根构造第二个 manager 来模拟进程重启。
func newReversibilityFixture(t *testing.T) (shadowRoot, proj string) {
	t.Helper()
	root := t.TempDir()
	proj = filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	return filepath.Join(root, "shadow"), proj
}

// EE-15：恢复点必须跨进程存活。
//
// 快照本身已经跨进程了——它是影子仓库里的一个 commit。缺的是**谁记得它的 SHA**：
// `checkpoints` 切片仍在 Go 内存里，所以一次窗口重载或后端重启之后，恢复点无法被
// 枚举，UI 就没法提供"回到这里"。
//
// 已转绿（2026-09-20）：账本不是 wescode-app.db，是**影子仓库自己**——`git log`
// 回答"有哪些恢复点"。表会制造一份能与 commit 漂移的副本，而 commit 就是快照本身。
func TestEE15_CheckpointMustSurviveProcessRestart(t *testing.T) {
	shadowRoot, proj := newReversibilityFixture(t)
	f := filepath.Join(proj, "a.txt")
	mustWrite(t, f, "original")

	cm1 := NewCheckpointManager(shadowRoot, proj, nil)
	if _, err := cm1.Create("run-1"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	mustWrite(t, f, "ai-edit")

	// 模拟进程重启：同一个影子仓库上的一个全新 manager。
	cm2 := NewCheckpointManager(shadowRoot, proj, nil)

	cp, lerr := cm2.Latest(context.Background())
	if lerr != nil {
		t.Fatalf("Latest: %v", lerr)
	}
	if cp == nil {
		t.Fatal("EE-15: 上一个进程创建的恢复点消失了——账本只在内存里")
	}
	if err := cm2.RestoreFile(cp, f); err != nil {
		t.Fatalf("RestoreFile: %v", err)
	}
	if got := mustRead(t, f); got != "original" {
		t.Errorf("EE-15: 重启后恢复得到 %q，期望 %q", got, "original")
	}
}

// EE-14：恢复点只追加，Accept 无删除权。
//
// 此前 Drop 会清空整张内存备份表，于是用户接受第一个文件的那一瞬间，其余还没被
// 审阅的文件的退路一起消失——而中低端用户的典型行为恰好是"看不懂就先点接受"。
//
// 影子仓库让这条自然成立：快照是一个 commit，Drop 摘掉的只是本地指针。
func TestEE14_AcceptMustNotDestroyOtherRecoveryPoints(t *testing.T) {
	shadowRoot, proj := newReversibilityFixture(t)
	accepted := filepath.Join(proj, "accepted.txt")
	pending := filepath.Join(proj, "still-pending.txt")
	mustWrite(t, accepted, "orig-1")
	mustWrite(t, pending, "orig-2")

	cm := NewCheckpointManager(shadowRoot, proj, nil)
	cp, err := cm.Create("run-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	mustWrite(t, accepted, "ai-1")
	mustWrite(t, pending, "ai-2")

	// 用户接受了第一个文件。此前这里调 cm.Drop(cp)——那个方法已删除（2026-09-20）：
	// Accept 没有删除权（EE-14）从一条纪律变成物理事实，因为账本就是 git 历史，没有
	// 任何 API 能摘掉恢复点。这一步现在是**空操作**，而测试断言的东西没变：
	// 接受一个文件之后，另一个文件的退路仍然在。
	if err := cm.RestoreFile(cp, pending); err != nil {
		t.Fatalf("RestoreFile: %v", err)
	}
	if got := mustRead(t, pending); got != "orig-2" {
		t.Errorf("EE-14: 接受一个文件摧毁了另一个文件的退路；pending = %q，期望 %q", got, "orig-2")
	}
}

// EE-16：账本跨 Run。
//
// 此前 mem 路径的 memRestoreFile **完全忽略传入的 checkpoint**，只从单槽取值，
// 所以 Reset 之后拿着旧 checkpoint 恢复会得到新一轮的内容。内容寻址存储下，持有
// SHA 就足以打开快照，与谁记着它无关。
//
// 两半都已证明（2026-09-20）：恢复原语跨 Run 可用（下面的 RestoreFile），
// 恢复点可枚举跨 Run（末尾的 List 断言）。
func TestEE16_LedgerMustSpanRuns(t *testing.T) {
	shadowRoot, proj := newReversibilityFixture(t)
	f := filepath.Join(proj, "a.txt")
	mustWrite(t, f, "turn-0")

	cm := NewCheckpointManager(shadowRoot, proj, nil)

	// 轮 1
	cp1, err := cm.Create("run-1")
	if err != nil {
		t.Fatalf("Create run-1: %v", err)
	}
	mustWrite(t, f, "turn-1")

	// 轮 2 开始。此前 run.go 在这里调 Reset() 清空账本——那个方法已删除
	// （2026-09-20，EE-16）：Run 边界不再清账本，所以轮 1 的恢复点仍可枚举。
	if _, err := cm.Create("run-2"); err != nil {
		t.Fatalf("Create run-2: %v", err)
	}
	mustWrite(t, f, "turn-2")

	if err := cm.RestoreFile(cp1, f); err != nil {
		t.Fatalf("RestoreFile(轮 1 的恢复点): %v", err)
	}
	if got := mustRead(t, f); got != "turn-0" {
		t.Errorf("EE-16: 无法回滚到当前 Run 之前；f = %q，期望 %q", got, "turn-0")
	}

	// EE-16 的另一半：两个 Run 的恢复点都必须可枚举。此前 Run 边界清空账本，
	// 于是 UI 列不出"回到上一轮之前"这个选项——恢复原语可用而入口不存在，
	// 对用户等于不可逆。
	all, err := cm.List(context.Background(), 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("EE-16: 账本只剩 %d 个恢复点，期望 2（Run 边界不得清账本）", len(all))
	}
}

// EE-15：git 与非 git 项目必须同语义。
//
// Stage 0 时这条测试的写法是"制造 Create 失败，然后断言仍有恢复点"——那依赖
// `detectGitRepo` 的分岔。影子仓库把那个分岔删掉了，于是原写法会在 `Create` 成功
// 时 `t.Skip`，**静默变成一个不测任何东西的测试**。所以改成直接断言等价性：同一
// 个场景，项目有没有 .git，恢复结果必须一致。
func TestEE15_GitAndNonGitMustShareSemantics(t *testing.T) {
	for _, tc := range []struct {
		name       string
		projectGit bool
	}{
		{"项目无 git", false},
		{"项目有 git", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shadowRoot, proj := newReversibilityFixture(t)
			if tc.projectGit {
				if err := os.Mkdir(filepath.Join(proj, ".git"), 0o755); err != nil {
					t.Fatalf("setup .git: %v", err)
				}
			}
			f := filepath.Join(proj, "a.txt")
			mustWrite(t, f, "original")

			cm := NewCheckpointManager(shadowRoot, proj, nil)
			cp, err := cm.Create("run-1")
			if err != nil {
				t.Fatalf("EE-15: 恢复点创建失败（项目 .git=%v）：%v", tc.projectGit, err)
			}
			mustWrite(t, f, "ai-edit")

			if err := cm.RestoreFile(cp, f); err != nil {
				t.Fatalf("RestoreFile: %v", err)
			}
			if got := mustRead(t, f); got != "original" {
				t.Errorf("EE-15: 恢复结果因项目有无 .git 而不同；got %q，期望 %q", got, "original")
			}
		})
	}
}

// EE-19：恢复不得吃掉用户自己的劳动。
//
// 冲突门今天在 `EditEngine.RejectEdits` 里（比对 e.fileHashes），而不是在恢复原语
// 里。今天只有一个调用方，所以它"能用"；Stage 2 会加上轮次级恢复这第二个调用方，
// 而在 N 个调用点各记一次的规则，是撑不到第 N+1 个的规则（同 EE-13 的治法）。
// 所以本测试故意打在原语上：判据应该下沉。
//
// 已转绿（2026-09-20）。落地时修正了这条测试原来的一个前提：它假设原语**零输入**
// 就能判定，而那不可能——AI 写进去的字节与用户写进去的字节在盘上完全相同，只看
// 文件系统没有任何信号能区分作者。所以"判据下沉"落到的是**决策**位置，不是信息来源：
//
//	决策在原语（`CheckpointManager.restoreFile` 比对基线并拒绝）——任何恢复入口
//	都绕不过，这正是原来的缺陷（门只写在 `RejectEdits` 里）要修的。
//	观察由写入方提供（`MarkWritten`，生产代码里由 `EditEngine.UpdateSnapshot` 调，
//	那是"AI 刚写完"的权威时刻）。
//
// 因此下面多了一行 MarkWritten——它扮演写入方。断言与失败文案逐字未变。
func TestEE19_RestoreMustNotSilentlyOverwriteUserEdits(t *testing.T) {
	shadowRoot, proj := newReversibilityFixture(t)
	f := filepath.Join(proj, "a.txt")
	mustWrite(t, f, "original")

	cm := NewCheckpointManager(shadowRoot, proj, nil)
	cp, err := cm.Create("run-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	mustWrite(t, f, "ai-edit")
	cm.MarkWritten(f) // 写入方的职责：告诉原语"AI 写完之后盘上是这样"

	// 用户随后手改了同一个文件。
	mustWrite(t, f, "user-edit")

	restoreErr := cm.RestoreFile(cp, f)
	got := mustRead(t, f)

	if restoreErr == nil && got == "original" {
		t.Error("EE-19: 回滚覆盖了用户自己的改动并报告成功——要么保留它，要么报冲突")
	}

	// 拒绝必须是**可判别的**，不是静默跳过：UI 要据此问用户"你改过这个文件，仍要
	// 丢弃吗"。静默跳过会让"回滚了但没变"看起来像 bug。
	if !errors.Is(restoreErr, ErrUserModified) {
		t.Errorf("EE-19: 拒绝原因不可判别：%v（期望 ErrUserModified）", restoreErr)
	}

	// 而用户明确表示"仍要丢弃"时必须能真的回滚——挡住回滚比放过一次覆盖更糟，
	// 回滚是可逆性的唯一出口。
	if err := cm.ForceRestoreFile(cp, f); err != nil {
		t.Fatalf("ForceRestoreFile: %v", err)
	}
	if got := mustRead(t, f); got != "original" {
		t.Errorf("EE-19: 强制恢复没生效；f = %q，期望 %q", got, "original")
	}
}

// EE-19：恢复失败不得删除文件。
//
// 原实现假设"checkout 失败说明这个文件是新建的"于是 os.Remove，但 SHA 无效、仓库
// 损坏、git 不在 PATH、权限不足都会走到那里——删掉一个带用户内容的文件**并返回
// nil**。现在"文件是新建的"是判定而非猜测：先确认快照可读（`sha^{commit}`），再问
// 路径在不在里面；探测失败返回"不知道"，调用方什么都不做。
func TestEE19_RestoreFailureMustNotDeleteFile(t *testing.T) {
	shadowRoot, proj := newReversibilityFixture(t)
	f := filepath.Join(proj, "a.txt")
	mustWrite(t, f, "user content")

	cm := NewCheckpointManager(shadowRoot, proj, nil)
	if _, err := cm.Create("run-1"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// 一个不存在的快照 id。
	cp := &Checkpoint{
		ID:        "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		CreatedAt: time.Now(),
		RunID:     "run-1",
	}
	restoreErr := cm.RestoreFile(cp, f)

	if _, statErr := os.Stat(f); os.IsNotExist(statErr) {
		t.Fatalf("EE-19: checkout 失败后 RestoreFile 删掉了一个已存在的文件（err=%v）；"+
			"「文件是新建的」不是 checkout 失败的唯一原因", restoreErr)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
