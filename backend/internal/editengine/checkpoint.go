package editengine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// CheckpointManager 管理恢复点。存储后端只有一个：影子 git 仓库（EE-15）。
//
// 此前这里有两条后备路径，强度恰好相反：git 仓库走 `git stash create`（一个
// SHA），非 git 走内存 map（进程一死就没），而 `MemBackupBeforeEdit` 在 git 下
// 直接 return。两套语义不同的实现必然漂移，且漂移的那一半永远是没人盯的那一半。
// 现在两者合成一条——影子仓库不问用户项目有没有 .git。
//
// 账本也在影子仓库里（EE-15/EE-16，2026-09-20 落地）：恢复点的可枚举性由
// `git log` 回答，不由进程内存回答。此前这里有一个 `checkpoints []Checkpoint`
// 切片，于是快照跨进程存活而**记得它的人不跨进程**——一次窗口重载之后恢复点无法
// 枚举，UI 就提供不了"回到这里"。
//
// 为什么不是 SQLite 表（原 Stage 2 计划）：表会制造一份能与 commit 漂移的副本
// （表里有行而 commit 不在，或反之），而两者不一致时没有任何一侧能自证。内容寻址
// 存储的意义正在于此——记住 SHA 的人可以有很多个，快照只有一份。
//
// 唯一的状态是 `expected`（EE-19 的冲突判据），见 MarkWritten 的说明。
type CheckpointManager struct {
	shadow *ShadowRepo

	mu sync.Mutex
	// expected 记的是"AI 写完之后盘上应该是什么"（path → content hash）。
	//
	// 它必须在**原语**这一层，不能留在 EditEngine（EE-19）：冲突门此前只写在
	// `RejectEdits` 里比对 `e.fileHashes`，于是任何别的恢复入口——轮次级回滚、
	// 恢复点面板、将来的 UI "回到这里"——都会直接覆盖用户的手改并报告成功。
	// 在 N 个调用点各记一次的规则撑不到第 N+1 个（同 EE-13 的治法）。
	expected map[string]string
}

// ErrUserModified 表示盘上的内容不是 AI 上次写进去的那份，因此恢复会吃掉别人的劳动。
//
// 它是一个**可判别的**错误而不是静默跳过：调用方（UI）要据此问用户"你改过这个文件，
// 仍要丢弃吗"。静默跳过会让"回滚了但没变"看起来像 bug；静默覆盖更糟，那是数据丢失。
var ErrUserModified = errors.New("file was modified by user after agent edit; restore blocked to avoid overwriting user changes")

// checkpointMessagePrefix 是恢复点 commit message 的前缀。
//
// 写与读必须是同一份字面量：`Create` 用它组装 message，`List` 用它剥回 runID。
// 抄成两份的话，改了一处之后 runID 会带着前缀出现在 UI 上，而那看起来只是"文案有点怪"。
const checkpointMessagePrefix = "wescode checkpoint "

// Checkpoint 是工作树的一个时间点快照。ID 是影子仓库里的 commit SHA，
// 内容寻址——只要还知道这个 SHA，快照就能打开，与谁记着它无关。
type Checkpoint struct {
	ID        string
	CreatedAt time.Time
	RunID     string
}

// NewCheckpointManager 为 workDir 建一个恢复点管理器。
// shadowRoot 通常是 {dataDir}/cells/{cellID}/shadow。
func NewCheckpointManager(shadowRoot, workDir string, sp tool.ShellProvider) *CheckpointManager {
	return &CheckpointManager{
		shadow: NewShadowRepo(shadowRoot, workDir, sp),
	}
}

// Shadow 暴露底层影子仓库，供诊断与测试。
func (cm *CheckpointManager) Shadow() *ShadowRepo { return cm.shadow }

// Create 在 Agent 动手之前抓一个恢复点。
//
// 失败必须被调用方当真：EE-18 要求没有恢复点就不写。此处只负责诚实地失败，
// 拒绝写入的执行点在 Stage 2 的写入 seam。
func (cm *CheckpointManager) Create(runID string) (*Checkpoint, error) {
	sha, err := cm.shadow.Snapshot(context.Background(), checkpointMessagePrefix+runID)
	if err != nil {
		return nil, fmt.Errorf("checkpoint create: %w", err)
	}

	slog.Info("[checkpoint] created", "sha", sha, "runID", runID)
	return &Checkpoint{ID: sha, CreatedAt: time.Now(), RunID: runID}, nil
}

// RestoreAll 把给定文件全部恢复到快照状态。
//
// 逐个恢复而不是 `checkout SHA -- .`：后者会把**这轮没碰过的**文件也拽回快照
// 状态，包括用户在别处手改的。回滚的边界是 AI 改过的那些文件，不是整棵树。
func (cm *CheckpointManager) RestoreAll(cp *Checkpoint, modifiedFiles []string) error {
	if cp == nil {
		return fmt.Errorf("checkpoint: nil checkpoint")
	}
	var firstErr error
	for _, f := range modifiedFiles {
		if err := cm.RestoreFile(cp, f); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return firstErr
	}
	slog.Info("[checkpoint] restored all", "sha", cp.ID, "files", len(modifiedFiles))
	return nil
}

// RestoreFile 把单个文件恢复到快照状态。
//
// "文件是这轮新建的所以该删掉"是一个**判定**，判据是它在不在快照里（EE-19）。
// 原实现把它当猜测——checkout 失败就 os.Remove——于是 SHA 无效、仓库损坏、git
// 不在 PATH、权限不足都会删掉一个带用户内容的文件，并且返回 nil。
func (cm *CheckpointManager) RestoreFile(cp *Checkpoint, absPath string) error {
	return cm.restoreFile(cp, absPath, false)
}

// ForceRestoreFile 明知盘上有用户改动仍然恢复。
//
// 它与 `RestoreFile` 分成两个名字而不是加一个 bool 参数：`RestoreFile(cp, f, true)`
// 在调用点读不出"这会丢掉用户的改动"，而一个叫 Force 的名字读得出。丢数据的操作
// 必须在调用点看得见。
func (cm *CheckpointManager) ForceRestoreFile(cp *Checkpoint, absPath string) error {
	return cm.restoreFile(cp, absPath, true)
}

// MarkWritten 记下"AI 刚把这个文件写成什么样"，供 EE-19 的冲突判定使用。
//
// 写入侧必须调它，否则 `expected` 里没有条目——那时 `RestoreFile` 按"没有基线可比"
// 放行（见 restoreFile 的注释）。这是刻意的：把"忘了标记"读成"有冲突"会让每次回滚
// 都失败，而回滚是可逆性的唯一出口，挡住它比放过一次覆盖更糟。
func (cm *CheckpointManager) MarkWritten(absPath string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.expected == nil {
		cm.expected = make(map[string]string)
	}
	cm.expected[absPath] = QuickFileHash(absPath)
}

func (cm *CheckpointManager) restoreFile(cp *Checkpoint, absPath string, force bool) error {
	if cp == nil {
		return fmt.Errorf("checkpoint: nil checkpoint")
	}

	// EE-19：恢复不得吃掉用户自己的劳动。判据在这一层，任何调用方都绕不过。
	//
	// 没有基线（`expected` 里没有这个路径）时放行：那说明写入侧没调 MarkWritten，
	// 而把"不知道"读成"有冲突"会挡住回滚本身。挡住回滚比放过一次覆盖更糟——
	// 回滚是可逆性的唯一出口。
	if !force {
		cm.mu.Lock()
		want, tracked := cm.expected[absPath]
		cm.mu.Unlock()
		if tracked && want != "" {
			if now := QuickFileHash(absPath); now != "" && now != want {
				slog.Warn("[checkpoint] refusing restore: file changed outside the agent",
					"path", absPath, "expected", want, "actual", now)
				return fmt.Errorf("%w: %s", ErrUserModified, absPath)
			}
		}
	}

	ctx := context.Background()
	rel := cm.shadow.Rel(absPath)

	present, err := cm.shadow.HasPath(ctx, cp.ID, rel)
	if err != nil {
		// 不知道。什么都不做——尤其不能删。
		return fmt.Errorf("checkpoint: cannot determine whether %s exists in snapshot: %w", rel, err)
	}
	if !present {
		// 判定成立：快照可读，里面确实没有这个路径。
		if rmErr := os.Remove(absPath); rmErr != nil && !os.IsNotExist(rmErr) {
			return fmt.Errorf("checkpoint: remove new file %s: %w", absPath, rmErr)
		}
		slog.Info("[checkpoint] removed file absent from snapshot", "sha", cp.ID, "path", absPath)
		return nil
	}

	if err := cm.shadow.RestoreFile(ctx, cp.ID, rel); err != nil {
		return err
	}
	slog.Info("[checkpoint] restored file", "sha", cp.ID, "path", absPath)
	return nil
}

// List 枚举恢复点，最新的在前。跨进程、跨 Run（EE-15/EE-16）——判据是
// `git log`，不是进程内存。
//
// limit <= 0 表示不限。
func (cm *CheckpointManager) List(ctx context.Context, limit int) ([]Checkpoint, error) {
	commits, err := cm.shadow.ListCommits(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Checkpoint, 0, len(commits))
	for _, c := range commits {
		out = append(out, Checkpoint{
			ID:        c.SHA,
			CreatedAt: c.CreatedAt,
			RunID:     strings.TrimPrefix(c.Message, checkpointMessagePrefix),
		})
	}
	return out, nil
}

// Latest 返回最近的恢复点，没有则 nil。
//
// **它现在会返回上一个 Run 的恢复点**（EE-16 正是要这个：此前 Run 边界会清空账本，
// 于是回滚到不了当前 Run 之前）。所以回滚不能问它"该退回哪里"——那个答案在
// `Changeset.CheckpointID` 里，它记的是本轮开始时的那个 SHA。Latest 的用途是
// 枚举与诊断。
func (cm *CheckpointManager) Latest(ctx context.Context) (*Checkpoint, error) {
	list, err := cm.List(ctx, 1)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	cp := list[0]
	return &cp, nil
}
