package editengine

// 影子 git 仓库：恢复点的唯一存储后端（EE-15）。
//
// git 目录在 wescode 自己的数据目录，work-tree 指向用户项目。于是用户项目里不会
// 出现 .git，`git status` / `git log` / `git stash list` 一概干净，而 wescode 拿到
// git 的全部能力——增量存储、diff、GC。
//
// 这么做的理由不是省事，是**让 git 项目与非 git 项目走同一条代码路径**。此前两条
// 路径的后备强度恰好相反：非 git 有内容备份（内存，进程一死就没）、git 只有一个
// stash SHA 且 `MemBackupBeforeEdit` 在 git 下直接 return。保护最弱的那条，正好是
// 最需要保护的用户所在的那条——中低端用户的项目大概率没 git init，或有 git 但从
// 不 commit。两套语义不同的实现必然漂移，而漂移的那一半永远是没人盯的那一半。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/weisyn/wesgine/tool"
)

// shadowGitTimeout 单条 git 命令的上限。`add -A` 在大仓上是最慢的一条，所以这个
// 值按它定，不按 `cat-file` 定。基线快照应该在项目打开时做（见 26-reversibility
// §四），到了这里还超时就是真出事了，不该无限等。
const shadowGitTimeout = 60 * time.Second

// shadowExcludes 是 .gitignore 缺失时的兜底。
//
// work-tree 指向用户项目，所以项目自己的 .gitignore 会被 `add -A` 尊重；没有
// .gitignore 的项目会把 node_modules 整个提交进影子仓库。这份清单只列"生成物，
// 且体量足以让快照失去意义"的目录，不表达任何关于用户该不该版本化什么的意见——
// 它是安全网，不是策略。
var shadowExcludes = []string{
	"node_modules/",
	".venv/",
	"venv/",
	"__pycache__/",
	"target/",
	"vendor/",
	"dist/",
	"build/",
	"out/",
	".next/",
	".turbo/",
	".gradle/",
	".cache/",
}

// ShadowRepo 是一个 git 目录在别处、work-tree 指向用户项目的 git 仓库。
type ShadowRepo struct {
	gitDir   string
	workTree string
	sp       tool.ShellProvider

	// snapMu 串行化 Snapshot 的整个 add→write-tree→读父→commit-tree→update-ref 序列。
	//
	// 这把锁不是为了保护某个字段，是因为那五步共享两份仓库级状态：git 索引和账本 ref。
	//
	// **它守的是成功率，不是完整性**——git 的 `index.lock` 是排他的且**立即失败不等待**，
	// 所以少了这把锁，并发快照里只有一个能过 `git add`，其余全报
	// `Unable to create index.lock`（实测 8 并发 → 1 成功）。而 `BeginRun` 对创建失败的
	// 处置是"本轮无恢复点 + 一行 Warn + 继续跑"，于是症状不是报错：**大部分并发 Run
	// 悄悄没有退路**，用户照常看到 AI 改完文件，只是撤销背后什么都没有。
	//
	// 完整性由 update-ref 的 old-value CAS 守（见那里）。两者不可互替：CAS 挡的是
	// 跨进程静默覆盖，锁挡的是本进程无谓失败。
	snapMu sync.Mutex
}

// NewShadowRepo 返回 workTree 对应的影子仓库句柄。shadowRoot 通常是
// {cellDir}/shadow——按 cell 分区，随 cell 一起被清理。
//
// 不做任何 I/O：Ensure 才建仓库。
func NewShadowRepo(shadowRoot, workTree string, sp tool.ShellProvider) *ShadowRepo {
	if sp == nil {
		sp = &tool.LocalShellProvider{}
	}
	return &ShadowRepo{
		gitDir:   filepath.Join(shadowRoot, shadowKey(workTree)),
		workTree: workTree,
		sp:       sp,
	}
}

// shadowKey 由 work-tree 路径派生一个稳定目录名。同一台机器上打开同一个项目必须
// 命中同一个影子仓库，否则每次重开都从零快照。
func shadowKey(workTree string) string {
	canonical := workTree
	if resolved, err := filepath.EvalSymlinks(workTree); err == nil {
		canonical = resolved
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])[:16]
}

// GitDir 暴露 git 目录，供诊断与测试。
func (r *ShadowRepo) GitDir() string { return r.gitDir }

// Ensure 幂等地建好仓库。
func (r *ShadowRepo) Ensure(ctx context.Context) error {
	if r.workTree == "" {
		return fmt.Errorf("shadow: empty work tree")
	}
	if _, err := os.Stat(filepath.Join(r.gitDir, "HEAD")); err == nil {
		return nil
	}
	if err := os.MkdirAll(r.gitDir, 0o700); err != nil {
		return fmt.Errorf("shadow: mkdir git dir: %w", err)
	}
	if _, err := r.runGit(ctx, "init", "--quiet"); err != nil {
		return fmt.Errorf("shadow: init: %w", err)
	}

	// commit-tree 需要一个身份。没有全局 git config 的机器上（CI 容器、刚装完
	// 系统的开发机）不设这两项，每次快照都会失败——而 EE-18 会把那个失败变成
	// "拒绝写入"，也就是产品整体不可用。
	for _, kv := range [][2]string{
		{"user.name", "wescode"},
		{"user.email", "wescode@localhost"},
		{"commit.gpgsign", "false"},
		{"core.autocrlf", "false"},
	} {
		if _, err := r.runGit(ctx, "config", kv[0], kv[1]); err != nil {
			return fmt.Errorf("shadow: config %s: %w", kv[0], err)
		}
	}

	if err := r.writeExcludes(); err != nil {
		return err
	}
	return nil
}

func (r *ShadowRepo) writeExcludes() error {
	infoDir := filepath.Join(r.gitDir, "info")
	if err := os.MkdirAll(infoDir, 0o700); err != nil {
		return fmt.Errorf("shadow: mkdir info: %w", err)
	}
	body := "# wescode 兜底忽略表（项目自带 .gitignore 优先生效）\n" +
		strings.Join(shadowExcludes, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(infoDir, "exclude"), []byte(body), 0o600); err != nil {
		return fmt.Errorf("shadow: write exclude: %w", err)
	}
	return nil
}

// Snapshot 把当前工作树状态存成一个 commit，返回它的 SHA。
//
// 三步 add-A / write-tree / commit-tree，而不是 `commit`：`commit` 会移动 HEAD，
// 而影子仓库没有"当前分支"的概念——每个恢复点都是一个游离 commit，由账本持有它
// 的 SHA。这也是恢复点只追加（EE-14）的物理形式：没有任何 git 操作会删掉一个被
// 记着 SHA 的 commit，直到 GC 明确要求。
func (r *ShadowRepo) Snapshot(ctx context.Context, message string) (string, error) {
	// 整个序列一个临界区，理由见 snapMu。
	r.snapMu.Lock()
	defer r.snapMu.Unlock()

	if err := r.Ensure(ctx); err != nil {
		return "", err
	}
	if _, err := r.runGit(ctx, "add", "-A", "--", "."); err != nil {
		return "", fmt.Errorf("shadow: add: %w", err)
	}
	tree, err := r.runGit(ctx, "write-tree")
	if err != nil {
		return "", fmt.Errorf("shadow: write-tree: %w", err)
	}
	if tree == "" {
		return "", fmt.Errorf("shadow: write-tree returned empty tree id")
	}
	// 挂上父 commit 并推进 ref——两件事都是承重的，而原实现两件都没做：
	//
	// **父**让快照成为一条历史而不是一堆孤立的树，于是 `git log` 能枚举它们
	// （EE-15/EE-16 的账本就是这条历史，不需要第二份存储）。
	//
	// **ref** 让它们**可达**。原实现的 `commit-tree` 产出的是 dangling commit：
	// 内容寻址所以拿着 SHA 打得开，但没有任何 ref 指向它，于是一次 `git gc`
	// （git 自己在 `add` 时也会按启发式触发 auto-gc）就能把用户的全部退路清掉，
	// 而症状是"恢复点昨天还在今天没了"且没有任何日志。EE-15 说的"跨进程存活"
	// 此前只对重启成立，对 gc 不成立。
	parent, _ := r.headSHA(ctx) // 首个快照没有父，取不到是正常的
	args := []string{"commit-tree", tree, "-m", message}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	sha, err := r.runGit(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("shadow: commit-tree: %w", err)
	}
	if sha == "" {
		return "", fmt.Errorf("shadow: commit-tree returned empty sha")
	}
	// 带 old-value 的 compare-and-swap，不是裸 update-ref：snapMu 只管得住本进程，
	// 而 doctor / CLI / 第二个实例都可能碰同一个影子仓库。裸写在那种情况下**成功**并
	// 静默挤掉别人刚记下的恢复点；CAS 让它响亮失败，而 BeginRun 对失败的处置是
	// "本轮无恢复点 + 留日志"——用一次可见的降级换掉一个查不出来的数据丢失。
	//
	// 空 old-value 是 git 的"这个 ref 必须还不存在"，正是首个快照要断言的东西。
	if _, err := r.runGit(ctx, "update-ref", shadowRef, sha, parent); err != nil {
		// 这里不能只警告：ref 没推进意味着这个快照是可被 gc 的，而调用方会拿着
		// 它当恢复点用。诚实地失败，让 EE-18（没有恢复点就不写）有机会生效。
		return "", fmt.Errorf("shadow: update-ref (ref moved concurrently?): %w", err)
	}
	return sha, nil
}

// shadowRef 是恢复点历史所在的 ref。
//
// 用显式 ref 而非 HEAD：影子仓库的 HEAD 指向什么取决于 `git init` 的默认分支名
// （master / main，随 git 版本与用户配置变），而账本的位置不该由那个配置决定。
const shadowRef = "refs/heads/wescode-checkpoints"

// headSHA 返回账本 ref 当前指向的 commit，ref 还不存在时返回空串。
func (r *ShadowRepo) headSHA(ctx context.Context) (string, error) {
	out, err := r.runGit(ctx, "rev-parse", "--verify", shadowRef)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// HasPath 回答"这个快照里有没有这个路径"，并且把"不知道"与"确实没有"分开。
//
// 它的存在是为了让"文件是这轮新建的"成为**判定**而不是**猜测**。原实现在 checkout
// 失败时假设文件是新建的于是 os.Remove，但 SHA 无效、仓库损坏、git 不在 PATH、
// 权限不足都会走到那里——删掉一个带用户内容的文件并返回 nil（EE-19）。
//
// 返回值三态：
//
//	(true,  nil) 快照里有这个路径 → 恢复它
//	(false, nil) 快照可读但路径不在里面 → 判定文件是新建的 → 可以删
//	(false, err) 不知道 → 调用方必须什么都别做
//
// 两次 cat-file 而不是一次是承重的：单次 `cat-file -e sha:path` 对"路径不在树里"
// 与"快照根本读不了"都是非零退出，而这两者在这里的处置完全相反。
func (r *ShadowRepo) HasPath(ctx context.Context, sha, relPath string) (bool, error) {
	if _, err := r.runGit(ctx, "cat-file", "-e", sha+"^{commit}"); err != nil {
		return false, fmt.Errorf("shadow: snapshot %s unreadable: %w", sha, err)
	}
	if _, err := r.runGit(ctx, "cat-file", "-e", sha+":"+relPath); err != nil {
		return false, nil
	}
	return true, nil
}

// RestoreFile 把单个文件恢复到快照状态。relPath 相对 work-tree。
func (r *ShadowRepo) RestoreFile(ctx context.Context, sha, relPath string) error {
	if _, err := r.runGit(ctx, "checkout", sha, "--", relPath); err != nil {
		return fmt.Errorf("shadow: checkout %s: %w", relPath, err)
	}
	return nil
}

// Rel 把绝对路径转成相对 work-tree 的路径。
// ListCommits 枚举影子仓库里的快照，最新的在前。
//
// **影子仓库就是账本**（EE-15/EE-16）：SHA 是恢复点 id，commit 时间是创建时刻，
// message 里带 runID。所以恢复点的可枚举性不需要第二份存储——加一张 SQLite 表会
// 制造一份能与 commit 漂移的副本（表里有行而 commit 不在，或反之），而两者不一致时
// 没有任何一侧能自证。内容寻址存储的意义正在于此：记住 SHA 的人可以有很多个，
// 快照只有一份。
//
// `%x00` 分隔字段而非 `|`：commit message 里带 runID，而 runID 由调用方给，
// 里面出现 `|` 不是不可能——用一个 message 里不可能出现的字节做分隔，就不必相信
// 调用方的命名习惯。
//
// limit <= 0 表示不限。仓库是空的（还没有任何 commit）时 `git log` 以非零退出，
// 那不是错误而是"还没有恢复点"，所以返回空切片。
func (r *ShadowRepo) ListCommits(ctx context.Context, limit int) ([]ShadowCommit, error) {
	if err := r.Ensure(ctx); err != nil {
		return nil, err
	}
	// 读显式 ref 而非 HEAD：见 shadowRef 的说明。ref 不存在（还没有任何快照）时
	// `git log` 非零退出，那不是错误而是"还没有恢复点"。
	args := []string{"log", shadowRef, "--format=%H%x00%cI%x00%s"}
	if limit > 0 {
		args = append(args, fmt.Sprintf("--max-count=%d", limit))
	}
	out, err := r.runGit(ctx, args...)
	if err != nil {
		// 空仓库：`git log` 报 "does not have any commits yet"。
		// ref 不存在有两种措辞（git 版本不同）：unknown revision / ambiguous argument，
		// 以及空仓库的 does not have any commits。三者都是"还没有恢复点"。
		msg := out + " " + err.Error()
		for _, benign := range []string{
			"does not have any commits",
			"unknown revision",
			"ambiguous argument",
			"bad revision",
		} {
			if strings.Contains(msg, benign) {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("shadow list commits: %w", err)
	}

	var commits []ShadowCommit
	// bytes/strings.Lines 而非 bufio.Scanner：后者超限时与 EOF 同值，于是"某条
	// message 太长"会被读成"日志到此为止"，而恢复点列表就少了后半截且不报错
	// （wesgine INV-LINE-01）。
	for line := range strings.Lines(out) {
		fields := strings.Split(strings.TrimRight(line, "\r\n"), "\x00")
		if len(fields) != 3 || fields[0] == "" {
			continue
		}
		at, perr := time.Parse(time.RFC3339, fields[1])
		if perr != nil {
			// 时间解析不了不丢掉这条：SHA 才是恢复所需的东西，时间只影响显示。
			at = time.Time{}
		}
		commits = append(commits, ShadowCommit{SHA: fields[0], CreatedAt: at, Message: fields[2]})
	}
	return commits, nil
}

// ShadowCommit 是影子仓库里的一个快照。
type ShadowCommit struct {
	SHA       string
	CreatedAt time.Time
	Message   string
}

func (r *ShadowRepo) Rel(absPath string) string {
	rel, err := filepath.Rel(r.workTree, absPath)
	if err != nil {
		return absPath
	}
	return rel
}

func (r *ShadowRepo) runGit(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, shadowGitTimeout)
	defer cancel()

	full := append([]string{
		"git",
		"--git-dir=" + r.gitDir,
		"--work-tree=" + r.workTree,
	}, args...)

	session, err := r.sp.Start(ctx, tool.ShellRequest{
		Command: shellJoin(full),
		WorkDir: r.workTree,
	})
	if err != nil {
		return "", err
	}
	out, _ := io.ReadAll(session.Output())
	exitCode, waitErr := session.Wait()
	if waitErr != nil {
		return "", waitErr
	}
	if exitCode != 0 {
		return "", fmt.Errorf("git %s: exit %d: %s", args[0], exitCode, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// shellJoin 把 argv 拼成一条可以交给 shell 的命令，逐个引号化。
//
// ShellProvider 收的是一条命令字符串而不是 argv，所以引号化是这一层的义务。
// 不做的话第一个带空格的路径就会把命令拆散——而 macOS 上的数据目录**就是**
// `~/Library/Application Support/wescode/`，也就是说影子仓库在 macOS 上默认
// 必然踩这一条。
func shellJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n'\"\\$`&|;<>()*?[]#~=%{}!") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
