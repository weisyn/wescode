package editengine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// 并发快照必须**每一次都拿到恢复点**，且全部留在账本里。
//
// 两条断言守着两件不同的事，缺任一条另一条都不完整：
//
// **成功率**是真实缺陷所在。git 的 `index.lock` 是排他的且**立即失败不等待**，所以
// 无锁时 8 次并发快照只有 1 次成功——而 `BeginRun` 对失败的处置是"本轮无恢复点 +
// 留一行 Warn 继续跑"。于是症状不是报错，是**大部分并发 Run 悄悄没有退路**：用户
// 照常看到 AI 改完文件，只是撤销按钮背后什么都没有。实测 1/8 vs 8/8。
//
// **在账本里**守的是另一个形状（后写的 update-ref 把先写的从 ref 上挤掉，使先写那个
// commit 变成 dangling：SHA 在调用方手里、git show 此刻还打得开、git log 列不到、
// 下一次 gc 之后恢复静默失败）。带 old-value 的 CAS 让那种情况响亮失败而非静默丢失。
// 这一半在当前实现下难以自然复现（index.lock 先把并发挡住了），但它守的是**判据**
// 而不是时序：把 CAS 换回裸 update-ref，跨进程（doctor / 第二个实例）就重新可丢。
func TestSnapshot_ConcurrentSnapshotsAllStayInLedger(t *testing.T) {

	work := t.TempDir()
	repo := NewShadowRepo(filepath.Join(t.TempDir(), "shadow"), work, nil)
	ctx := context.Background()

	if err := repo.Ensure(ctx); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	const n = 8
	var wg sync.WaitGroup
	shas := make([]string, n)
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// 每个 goroutine 先改一个自己的文件，让快照之间确有内容差异——
			// 否则 write-tree 产出同一个 tree，commit 虽仍不同但差异来源不真实。
			f := filepath.Join(work, fmt.Sprintf("f%d.txt", i))
			if err := os.WriteFile(f, []byte(fmt.Sprintf("v%d", i)), 0o644); err != nil {
				errs[i] = err
				return
			}
			shas[i], errs[i] = repo.Snapshot(ctx, fmt.Sprintf("%srun-%d", checkpointMessagePrefix, i))
		}(i)
	}
	wg.Wait()

	ledger, err := repo.ListCommits(ctx, 0)
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	inLedger := map[string]bool{}
	for _, c := range ledger {
		inLedger[c.SHA] = true
	}

	var lost []string
	for i, sha := range shas {
		if errs[i] != nil {
			// 响亮失败是可接受的结局（调用方降级为"本轮无恢复点"并留日志）。
			// 不可接受的是"返回成功但不在账本里"。
			continue
		}
		if sha == "" {
			t.Errorf("第 %d 次快照既没报错也没给 SHA", i)
			continue
		}
		if !inLedger[sha] {
			lost = append(lost, sha)
		}
	}
	if len(lost) > 0 {
		t.Errorf("%d 个快照返回成功却不在账本里：%v\n"+
			"这些 commit 现在是 dangling 的：SHA 在调用方手里、git show 还打得开、"+
			"git log 列不到、下一次 gc 之后恢复静默失败。", len(lost), lost)
	}

	// 全部必须成功。这是本测试的主断言，也是唯一能证伪 snapMu 的那条：
	// 上面的"在账本里"循环对失败的快照走 continue，所以少了这条，一个只有 1/8
	// 成功的实现同样全绿——而那 7 次失败正是用户丢掉撤销能力的地方。
	var ok int
	var failed []error
	for i := range shas {
		if errs[i] == nil && shas[i] != "" {
			ok++
		} else if errs[i] != nil {
			failed = append(failed, errs[i])
		}
	}
	if ok != n {
		t.Errorf("%d/%d 次并发快照成功。每一次失败都是一个 Run 静默失去恢复点"+
			"（BeginRun 只打 Warn 继续跑）。首个错误：%v", ok, n, failed[0])
	}
}

// 账本历史必须是一条链，不是一片孤岛。
//
// 上一个测试问"每个 SHA 在不在账本里"，这个问"账本自己是不是完整的"——两者不同：
// 如果实现改成每次快照都覆盖 ref 而不挂父，每个 SHA 都"在" git log 里（因为它就是
// ref 本身），但历史长度恒为 1，早先的恢复点一个都列不出来。
func TestSnapshot_LedgerIsAChainNotAnIsland(t *testing.T) {

	work := t.TempDir()
	repo := NewShadowRepo(filepath.Join(t.TempDir(), "shadow"), work, nil)
	ctx := context.Background()

	const n = 4
	for i := 0; i < n; i++ {
		if err := os.WriteFile(filepath.Join(work, fmt.Sprintf("s%d.txt", i)),
			[]byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Snapshot(ctx, fmt.Sprintf("%srun-%d", checkpointMessagePrefix, i)); err != nil {
			t.Fatalf("第 %d 次快照: %v", i, err)
		}
	}

	ledger, err := repo.ListCommits(ctx, 0)
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	if len(ledger) != n {
		t.Errorf("账本里 %d 条，做了 %d 次快照——历史断了", len(ledger), n)
	}
}
