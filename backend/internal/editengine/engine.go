package editengine

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/weisyn/wesgine/tool"
)

// SyntaxChecker checks for syntax errors after an edit (L0 verification).
type SyntaxChecker func(path string, content []byte) []SyntaxWarning

// CKGNotifier is called after successful edits to trigger CKG incremental refresh.
// Implemented by codeintel.FileWatcher.NotifyChange.
type CKGNotifier func(path string)

// SyntaxWarning is an L0 syntax error detected after writing an edit.
type SyntaxWarning struct {
	Line    int
	Column  int
	Message string
}

// EditEngine is an observer that tracks file modifications and provides
// L0 syntax checking via PostCallHook. It does NOT stage or buffer edits —
// all writes go directly to disk via wesgine's built-in tools, preserving
// the tool contract (edit writes immediately, read sees new content).
//
// Rollback uses CheckpointManager (git stash in git repos, memory backup
// otherwise) for atomic multi-file restore. Changeset groups all file
// modifications from a Run into a cohesive unit for accept/reject.
type EditEngine struct {
	checker       SyntaxChecker
	ckgNotifier   CKGNotifier
	fp            tool.FileProvider
	mu            sync.Mutex
	modifiedFiles map[string]struct{}
	fileHashes    map[string]string // path → content hash at session start

	checkpoint *CheckpointManager
	changeset  *Changeset
	conflict   *ConflictDetector

	Metrics *EditMetrics
}

// New creates an EditEngine with the given syntax checker.
// Pass nil for checker to disable L0 syntax checking.
func New(checker SyntaxChecker) *EditEngine {
	return &EditEngine{
		checker:       checker,
		modifiedFiles: make(map[string]struct{}),
		fileHashes:    make(map[string]string),
		conflict:      NewConflictDetector(),
		Metrics:       NewEditMetrics(),
	}
}

// SetCKGNotifier injects the CKG incremental refresh callback.
// After each successful edit, NotifyChange is called to update the code knowledge graph.
func (e *EditEngine) SetCKGNotifier(n CKGNotifier) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ckgNotifier = n
}

// SetFileProvider injects the Host FileProvider so that backup captures,
// syntax checks, and file hashing see the same content as wesgine tools.
func (e *EditEngine) SetFileProvider(fp tool.FileProvider) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fp = fp
}

// readFile reads a file through the injected FileProvider.
func (e *EditEngine) readFile(path string) ([]byte, error) {
	if e.fp != nil {
		return e.fp.ReadFile(context.Background(), path)
	}
	return os.ReadFile(path)
}

// quickFileHash hashes file content through the FileProvider.
func (e *EditEngine) quickFileHash(path string) string {
	return quickFileHashFn(path, func(p string) ([]byte, error) {
		return e.readFile(p)
	})
}

// QuickFileHashFn returns a hash function suitable for CheckpointManager.
func (e *EditEngine) QuickFileHashFn() func(string) string {
	return e.quickFileHash
}

// TrackModified records that a file was modified by an edit/write tool call.
func (e *EditEngine) TrackModified(path string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.modifiedFiles[path] = struct{}{}
	slog.Debug("[editengine] tracked modified file", "path", path)
}

// ModifiedFiles returns the list of files modified since the last reset.
func (e *EditEngine) ModifiedFiles() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	files := make([]string, 0, len(e.modifiedFiles))
	for f := range e.modifiedFiles {
		files = append(files, f)
	}
	return files
}

// ResetTracking clears the modified files list and file hash snapshot.
// Called at the start of each Run. Checkpoint and Changeset are managed
// separately via BeginRun/EndRun.
func (e *EditEngine) ResetTracking() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.modifiedFiles = make(map[string]struct{})
	e.fileHashes = make(map[string]string)
	e.changeset = nil
	// 不再调 checkpoint.Reset()：账本在影子仓库里（EE-16），Run 边界不清空它。
	// 此前那次清空正是"回滚到不了当前 Run 之前"的成因。
	slog.Debug("[editengine] tracking reset")
}

// ConflictDetector returns the multi-agent conflict detector for external use
// (e.g. recording edit byte ranges from delegated agents).
func (e *EditEngine) ConflictDetector() *ConflictDetector {
	return e.conflict
}

// SnapshotFiles records content hashes for the given files at session start.
// These hashes are compared before each edit to detect external modifications.
func (e *EditEngine) SnapshotFiles(paths []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, p := range paths {
		if h := e.quickFileHash(p); h != "" {
			e.fileHashes[p] = h
		}
	}
	if len(paths) > 0 {
		slog.Debug("[editengine] file snapshot taken", "files", len(paths))
	}
}

// CheckConflict returns true if the file at path was modified externally since
// the snapshot was taken (hash mismatch). Returns false if the file was not
// in the snapshot or cannot be read (fail-open).
func (e *EditEngine) CheckConflict(path string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	savedHash, ok := e.fileHashes[path]
	if !ok {
		return false
	}
	currentHash := e.quickFileHash(path)
	if currentHash == "" {
		return false
	}
	return currentHash != savedHash
}

// UpdateSnapshot refreshes the hash for a file after a successful edit,
// so subsequent edits to the same file don't trigger false conflicts.
//
// 它同时喂 EE-19 的冲突基线（`checkpoint.MarkWritten`）。**这里是唯一正确的喂点**：
// 判据要回答"盘上现在的内容是不是 AI 上次写进去的"，所以基线必须在写**之后**取。
// `TrackModified` 不行——它可能在写之前被调，那时算出的 hash 是写前的内容，于是
// 每次回滚都会被误判成冲突。
func (e *EditEngine) UpdateSnapshot(path string) {
	e.mu.Lock()
	cm := e.checkpoint
	e.mu.Unlock()
	if cm != nil {
		// 锁外调：MarkWritten 要读盘算 hash，持锁做 I/O 会让并发工具调用排队。
		cm.MarkWritten(path)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.fileHashes[path]; ok {
		if h := e.quickFileHash(path); h != "" {
			e.fileHashes[path] = h
		}
	}
}

// CheckSyntax runs the L0 syntax checker on file content.
// Returns nil if no checker is configured or no errors found.
func (e *EditEngine) CheckSyntax(path string, content []byte) []SyntaxWarning {
	if e.checker == nil {
		return nil
	}
	return e.checker(path, content)
}

// --- Checkpoint + Changeset based accept/reject ---

// SetCheckpointManager injects the checkpoint manager (created after workspace init).
func (e *EditEngine) SetCheckpointManager(cm *CheckpointManager) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.checkpoint = cm
}

// BeginRun creates a checkpoint and changeset for a new Run.
// Must be called after ResetTracking and before any tool execution.
func (e *EditEngine) BeginRun(runID string) error {
	e.mu.Lock()
	cm := e.checkpoint
	e.mu.Unlock()

	if cm == nil {
		return nil
	}

	cp, err := cm.Create(runID)
	if err != nil {
		slog.Warn("[editengine] checkpoint creation failed, continuing without checkpoint", "err", err)
		e.mu.Lock()
		e.changeset = NewChangeset(runID, "")
		e.mu.Unlock()
		return nil
	}

	e.mu.Lock()
	e.changeset = NewChangeset(runID, cp.ID)
	e.mu.Unlock()
	return nil
}

// ActiveChangeset returns the current changeset snapshot, or nil if none.
func (e *EditEngine) ActiveChangeset() *Changeset {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.changeset
}

// RecordFileChange records a file modification in the active changeset.
func (e *EditEngine) RecordFileChange(path, action, txID string) {
	e.mu.Lock()
	cs := e.changeset
	e.mu.Unlock()
	if cs != nil {
		cs.AddFile(path, action, txID)
	}
}

// ErrFileModifiedByUser indicates a reject was blocked because the user
// manually edited the file after the agent's edit (EE-12).
// 它现在**就是** ErrUserModified：判据下沉到 CheckpointManager 之后（EE-19），
// 两个名字指同一件事，而保留这个名字是因为 `rpc/handler_edit.go` 用 errors.Is 认它
// 来给用户提示"你改过这个文件"。让它们成为两个独立错误会让那条 UI 分支静默失效——
// 判据修好了而提示没了，用户看到的是一次无声的失败。
var ErrFileModifiedByUser = ErrUserModified

// AcceptEdits confirms all changes.
// Pass nil to accept all; pass specific paths for partial accept (clears those from changeset).
//
// 它**不动恢复点**（EE-14：Accept 没有删除权）——快照留在影子仓库里，所以
// "接受了这次改动"与"还能回到改动之前"可以同时为真。
func (e *EditEngine) AcceptEdits(paths []string) {
	e.mu.Lock()
	cs := e.changeset
	e.mu.Unlock()

	if cs != nil {
		cs.Accept()
	}
	// 不再 Drop 恢复点：EE-14 —— Accept 没有删除权。快照留在影子仓库里，
	// 所以"接受了这次改动"与"还能回到改动之前"可以同时为真，而此前 Accept
	// 会摘掉指针（内存账本时代那等于销毁）。
	slog.Debug("[editengine] accepted edits", "paths", paths)
}

// RejectEdits restores files from the checkpoint, reverting agent edits.
// Pass nil to reject all pending edits.
// In git repos, uses `git checkout {SHA} -- {paths}` for atomic restore.
// In non-git repos, falls back to memory backup restore (INV-EDIT-21).
func (e *EditEngine) RejectEdits(paths []string) error {
	e.mu.Lock()
	cm := e.checkpoint
	cs := e.changeset
	e.mu.Unlock()

	if cm == nil {
		return nil
	}

	// 退回哪个快照由 **changeset** 回答，不由 `Latest()` 回答。
	//
	// 账本进影子仓库之后（EE-16）`Latest()` 会返回上一个 Run 的恢复点——那正是
	// EE-16 要的（此前 Run 边界清空账本，于是回滚到不了当前 Run 之前）。但回滚的
	// 目标文件是**本轮**改过的那些，把它们退回三轮之前的状态会吃掉中间两轮的成果，
	// 而那两轮的改动可能是用户自己接受过的。`Changeset.CheckpointID` 记的是本轮
	// 开始时的 SHA，是唯一正确的答案。
	var cp *Checkpoint
	if cs != nil && cs.CheckpointID != "" {
		cp = &Checkpoint{ID: cs.CheckpointID, RunID: cs.ID}
	}

	// Determine target files for restore
	targets := paths
	if targets == nil && cs != nil {
		targets = cs.FilePaths()
	}
	if len(targets) == 0 {
		targets = e.ModifiedFiles()
	}

	if len(targets) == 0 {
		return nil
	}

	// 冲突判定已下沉到 `CheckpointManager.restoreFile`（EE-19，2026-09-20）。
	//
	// 此前它在这里：`RejectEdits` 比对 `e.fileHashes`。今天只有一个调用方所以它
	// "能用"，但轮次级回滚、恢复点面板、UI 的"回到这里"都是将来的调用方，而在 N 个
	// 调用点各记一次的规则撑不到第 N+1 个（同 EE-13 的治法）。原语现在自己守，
	// 所以绕不过去。
	//
	// 错误名字也统一了：`ErrFileModifiedByUser` 现在就是 `ErrUserModified` 的别名，
	// 所以 `rpc/handler_edit.go` 的 errors.Is 分支照旧生效。

	// 没有恢复点就不能假装回滚成功。此前这里落到内存备份路径，而那条路径在
	// 备份被清空时静默返回 nil——调用方拿到"已回滚"，磁盘上是 AI 的版本。
	if cp == nil {
		return fmt.Errorf("editengine: no checkpoint for this run; cannot reject")
	}

	var err error
	if paths == nil {
		err = cm.RestoreAll(cp, targets)
	} else {
		for _, p := range paths {
			if restoreErr := cm.RestoreFile(cp, p); restoreErr != nil {
				err = restoreErr
				break
			}
		}
	}
	if err != nil {
		return err
	}

	if cs != nil {
		cs.Reject()
	}

	// Update tracking state
	e.mu.Lock()
	for _, p := range targets {
		delete(e.modifiedFiles, p)
		if _, ok := e.fileHashes[p]; ok {
			if h := e.quickFileHash(p); h != "" {
				e.fileHashes[p] = h
			}
		}
	}
	e.mu.Unlock()

	slog.Info("[editengine] rejected edits via checkpoint", "files", len(targets))
	return nil
}

// PendingBackups returns the list of files with pending (unaccepted) changes.
//
// 恢复点是树级快照，所以"哪些文件有未接受的改动"的唯一答案是本轮改过哪些文件。
// 此前这里先问内存备份表、空了才问 ModifiedFiles——两个来源，而非 git 项目走前者、
// git 项目走后者，同一个问题按项目形态给不同答案。
func (e *EditEngine) PendingBackups() []string {
	return e.ModifiedFiles()
}
