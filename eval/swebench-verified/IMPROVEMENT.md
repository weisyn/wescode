# SWE-bench 测评驱动的迭代改进计划

> **基于**：[29-swebench-eval-report.md](29-swebench-eval-report.md)（396/500 = 79.2%）
> **目标**：消除每一类失败的根因，不向后兼容，可破坏性实施
> **原则**：从数据出发，每个改进项都对应具体的失败 instance

---

## 一、失败分布与根因总览

```
500 题
 ├── 370 Resolved（79.2%）
 ├── 53 Unresolved（10.6%）—— patch 能 apply，测试没通过
 ├── 74 Patch Error（14.8%）—— patch 无法 apply
 │    ├── 10 题：model_patch 不是 diff，是文字分析报告（bench 捕获 bug）
 │    └── 64 题：是 diff 但格式有问题（patch 截断/行尾/上下文不匹配）
 └── 3 Empty Patch（0.6%）—— 未生成任何修改
```

**按优先级排序**：
1. **P0**：Patch Error 74 题（修复后直接 +48 题 ≈ +9.6pp）
2. **P1**：Unresolved 53 题（需要引擎+模型能力提升）
3. **P2**：Empty Patch 3 题（Agent 策略问题）

---

## 二、P0：Patch Error 根因与修复方案

### 2.1 Bug A：10 题的"补丁"是文字报告，不是 diff

**证据**：

```python
# pytest-dev__pytest-5787 的 model_patch 内容：
"I'll start by orienting in the workspace and finding the upstream fix..."
"## 修改内容"
"## 需注意"
# 整个 patch 是 markdown 分析报告，没有任何 git diff 内容
```

```python
# django__django-11066 的 model_patch 内容：
"I'll start by checking the current state of the target file..."
"## 修改内容"
"- 该改动与上游修复提交（661e6cc2c9）一致"
```

**根因**：`subcmd_bench.go` 在 Agent Run 结束后捕获 patch 的方式有 bug。

当前流程：
1. Agent 运行，调用 read/write/edit/exec 工具
2. Run 结束后，bench 调用 `git diff` 获取工作区变更
3. 将 diff 写入 predictions.jsonl

**但这 10 题的情况是**：模型做了"分析"但没有实际修改文件（或修改后又撤回了）。`git diff` 输出为空，但 bench 把模型的**最终文本响应**当成了 patch。

**修复（wescode `subcmd_bench.go`）**：

```go
// 当前（有 bug）：
// patch 可能来自 model response 而非 git diff
patch := result.Patch  // 这可能是模型文本

// 修复：patch 必须且只能来自 git diff
patch := execGitDiff(workdir)
if patch == "" {
    // 模型没有修改任何文件 → 空 patch
    patch = ""
}
```

**验收**：所有 predictions 的 `model_patch` 要么是标准 `diff --git` 格式，要么为空。不允许出现 markdown、中文或自然语言。

**影响范围**：仅 `wescode.git/backend/cmd/wescode/subcmd_bench.go`

---

### 2.2 Bug B：64 题的 diff 格式有问题

**证据**：
- 所有 500 题的 patch 都**缺少尾部换行符**（`trailing_nl = False`）
- 已通过的 370 题也缺尾部换行——说明缺尾部换行不是直接原因
- 失败的 64 题报错：`patch unexpectedly ends in middle of line` / `malformed patch at line XX`

**推测根因**：

1. **JSON 序列化时丢失尾部换行**：`subcmd_bench.go` 将 `git diff` 输出存入 JSONL 时，可能 `strings.TrimSpace()` 或类似操作去掉了尾部 `\n`。swebench 的 `patch` 命令对部分 diff 能容忍缺失尾部换行，但对另一些不能（取决于最后一行是否是文件内容行 vs `\ No newline at end of file`）

2. **多文件 diff 的 hunk 边界问题**：模型用 `write` 全量重写文件后，`git diff` 生成的 diff 可能很大，在序列化到 JSONL 时被意外截断（JSON 字符串的转义问题）

3. **`\` 反斜杠转义**：diff 中的 `\ No newline at end of file` 标记在 JSON 编码时可能被双重转义

**修复方案**：

**方案 A（wescode bench 层，快速修复）**：

```go
// subcmd_bench.go 输出 patch 时：
func sanitizePatch(patch string) string {
    // 1. 确保以换行结尾
    if !strings.HasSuffix(patch, "\n") {
        patch += "\n"
    }
    // 2. 验证是有效 diff
    if patch != "" && !strings.Contains(patch, "diff --git") {
        // 不是 diff → 视为无效 patch
        return ""
    }
    // 3. 验证能 apply
    // 在写入 JSONL 前用 `git apply --check` 验证
    if err := gitApplyCheck(workdir, patch); err != nil {
        log.Warn("patch apply check failed", "error", err)
        // 仍然保存（让 swebench 评分），但记录警告
    }
    return patch
}
```

**方案 B（wesgine 引擎层，根本修复）**：

在 `write` / `edit` / `apply_patch` 工具层面保证文件修改后 `git diff` 输出始终有效：
- `write` 写入时确保文件以 `\n` 结尾
- `edit` 工具的 diff 算法保证 hunk 上下文行正确
- 每次文件写入后自动 `git add`，确保 `git diff` 能正确追踪

**验收**：每个 predictions 的 `model_patch` 能通过 `git apply --check` 验证。

---

### 2.3 Bug C：bench 超时/中断后的 patch 残留

**推测**：部分 patch error 可能来自 Agent 超时后的不完整状态——模型正在用 `write` 写文件时被 timeout 打断，文件写了一半，`git diff` 捕获到一个不完整的变更。

**修复（wescode `subcmd_bench.go`）**：

```go
// 超时或中断后，先 git checkout -- . 再 diff
// 确保只捕获完整的文件变更
func capturePatch(workdir string) string {
    // 1. 检查是否有未完成的写操作
    // 2. git stash / git checkout -- . 清理半写状态
    // 3. git diff HEAD
    // 4. 验证 diff 完整性
}
```

---

## 三、P1：Unresolved 53 题——引擎与 Agent 策略改进

### 3.1 按仓库分析失败模式

| 仓库 | 失败数 | 通过率 | 失败模式推测 |
|------|--------|--------|------------|
| **django** | 18 | 91% | 少数复杂 ORM/migration 场景，模型理解不够深 |
| **sphinx** | 10 | 72% | RST 解析 + Jinja 模板，模型对文档生成器理解弱 |
| **matplotlib** | 9 | 69% | 图形渲染 + C 扩展交互，纯 Python 修复不够 |
| **sympy** | 7 | 90% | 数学符号计算的边界条件 |
| **pylint** | 3 | 50% | AST 遍历、作用域分析逻辑 |
| **requests** | 3 | 63% | HTTP 协议细节 |
| **scikit-learn** | 2 | 93% | ML 算法边界条件 |
| **astropy** | 1 | 94% | 天文学计算精度 |

### 3.2 wesgine 引擎层改进

#### 改进 1：增强 Context Assembly（上下文组装）

**问题**：模型在长对话中丢失关键代码上下文，导致修复方案不完整。

**现状**：wesgine 的 Context 子系统（`internal/context/`）在 MidRunCompression 和 CognitiveSettlement 时压缩历史消息，可能丢失关键的代码结构信息。

**改进**：
- 在压缩时保留所有 `read` 工具读取过的**文件路径列表**作为 Pinned evidence
- 工具调用的错误信息（如测试失败输出）在压缩时优先保留
- `edit` 工具的变更记录（哪个文件改了什么）作为 Pinned 不被压缩

#### 改进 2：强化 Write Verification Nudge（ADR-311 升级）

**现状**：ADR-311 的 write verification nudge 在 `end_turn` 时检查是否有未验证的写入，但仅产生一次 nudge。

**问题**：
- SWE-bench 场景下，模型经常"写完代码不跑测试"就 end_turn
- Nudge 只生效一次，模型忽略后就不再提醒

**改进**：
- Nudge 改为**硬阻止 end_turn**：有未验证写入时，直接注入"请先运行测试验证"的系统消息，不允许结束
- 增加 `exec` 工具的验证语义：如果 `exec` 的 cwd 在项目根目录且命令包含 `test`/`pytest`/`unittest`，标记为验证行为
- `INV-WRITE-VERIFY-05` 的 `MarkWritten` 跟踪更精确——不仅是文件路径，还跟踪写入行数

#### 改进 3：测试驱动修复策略（Skill 层）

**问题**：模型经常"先改代码，再想跑什么测试"，而非"先跑失败测试定位问题，再改代码"。

**改进**（`agentic-execution` Skill）：
```markdown
## 修复 Bug 的标准流程
1. 先运行 issue 提到的测试用例（或相关测试），确认失败
2. 阅读失败测试的断言和堆栈
3. 定位出错的源码
4. 修改源码
5. 重新运行测试，确认通过
6. 运行相关模块的全部测试，确认无回归
```

这不是建议，是**硬编码到 Skill 的操作流程**。

#### 改进 4：增强 exec 工具的测试结果解析

**问题**：模型用 `exec` 跑 `pytest` 后，输出经常被截断（大量测试用例的输出超过 tool result 的长度限制），模型看不到关键的失败信息。

**改进**：
- `exec` 工具在检测到 `pytest`/`python -m pytest` 命令时，自动添加 `--tb=short -q` 参数（如果用户没指定），减少输出量
- 测试输出的截断策略改为**保留最后 N 行**（失败摘要在末尾），而非截断末尾
- 新增 `exec` 输出的结构化解析：提取 `PASSED`/`FAILED`/`ERROR` 计数，作为 tool result 的摘要

#### 改进 5：多文件修改的原子性保证

**问题**：模型分多次 `write`/`edit` 修改多个文件时，中间状态可能不一致。如果模型在修改 A 文件后、修改 B 文件前被中断或 end_turn，产生的 patch 只包含 A 的变更，但 A 的变更依赖 B 的变更才正确。

**改进**：
- 在 bench 模式下，Agent Loop 的 `maxIterations` 适当放大（当前 90 → 150），给模型更多迭代空间完成多文件修改
- `write` 工具的 content digest 提示模型"还需要修改的其他文件"

---

### 3.3 wescode 应用层改进

#### 改进 6：CKG 对 Python 项目的支持增强

**问题**：wescode 的 CKG（代码知识图谱）主要针对 Go/TypeScript 开发和测试，对 Python 项目的支持不够。SWE-bench 全部是 Python 项目。

**改进**：
- 确保 `langs/python.json` 的调用节点类型（`call`）正确识别 Python 的 `self.method()`、`super().method()`、装饰器调用等
- Python 项目的模块导入边（`import`）解析增强——Django 的 `from django.db import models` 等相对导入
- bench 模式下自动建立 CKG 索引，让 `find_callers`/`find_references` 可用

#### 改进 7：Python 测试框架 Skill 增强

**问题**：不同 Python 项目用不同的测试运行方式（pytest、tox、Django test runner、setup.py test），模型经常用错命令。

**改进**（`wescode-execution` Skill）：
```markdown
## Python 测试运行规则
- 有 `pytest.ini` / `setup.cfg [tool:pytest]` / `pyproject.toml [tool.pytest]` → 用 `python -m pytest`
- 有 `tox.ini` → 用 `tox -e py`（但 SWE-bench 环境没有 tox，退回 pytest）
- Django 项目（有 `manage.py`）→ 用 `python -m django test` 或 `python -m pytest`
- 始终用 `python -m pytest path/to/test_file.py::TestClass::test_method -xvs` 精确运行单个测试
```

#### 改进 8：bench 模式的 QualityGate 闭环

**现状**：bench 模式 `BenchMode=true` 禁用了 QualityGate L2 自动验证。

**问题**：这意味着模型写完代码后没有自动验证环节，直接结束。

**改进**：
- bench 模式不应完全禁用 QualityGate，而是使用**轻量版**：
  - L0：语法检查（`python -c "import ast; ast.parse(open('file.py').read())"` ）
  - L1：运行 issue 相关的测试（从 issue 描述中提取测试路径）
  - 不做 L2（不跑全量测试，节省时间）

---

## 四、P2：Empty Patch 3 题——Agent 策略边界

### 4.1 根因

3 题全部来自 sympy（`sympy-24539`、`sympy-24562`、`sympy-24661`），模型在 90 次迭代（`maxIterations`）内未能产出任何文件修改。

**可能原因**：
- 这 3 题的数学符号逻辑过于复杂，DeepSeek Chat 理解不足
- 模型陷入"分析-再分析"循环，没有进入"动手修改"阶段
- CycleDetector 的 exploration 阈值（100）太高，没能触发 nudge

### 4.2 改进

- 引擎层增加"进度检查"：如果 Agent 在 30 次迭代后仍未调用任何 `write`/`edit` 工具，注入系统消息提示"你需要开始修改代码了"
- 这不是 CycleDetector 的职责（CycleDetector 检测**重复**行为），而是新的"进度阈值"机制

---

## 五、跨切面改进（同时影响 P0/P1/P2）

### 5.1 bench 模式的全链路改进

| 现状 | 改进 |
|------|------|
| patch 来源不确定（可能是模型文本） | patch **只从** `git diff HEAD` 获取 |
| 无 patch 验证 | 输出前 `git apply --check` 验证 |
| BenchMode 禁用全部 QualityGate | 保留轻量版 L0+L1 |
| 超时后直接取 diff | 超时后先清理半写状态再取 diff |
| 无运行日志收集 | 收集每题的 tool call 序列作为 trajectory |

### 5.2 wesgine 引擎层全局改进

| 现状 | 改进 |
|------|------|
| `write` 不保证文件尾换行 | 写入时自动追加 `\n`（如原文件有） |
| `edit` 的 diff 算法自研 | 验证与 `git diff` 输出一致性 |
| Context 压缩丢失代码路径 | 文件路径列表作为 Pinned 保留 |
| Write Verification Nudge 是软提醒 | 改为硬阻止 end_turn |
| exec 输出截断不智能 | pytest 命令自动加 `--tb=short -q` |

### 5.3 Skill 层改进

| 现状 | 改进 |
|------|------|
| Bug 修复流程是建议性的 | 硬编码为"先跑测试→定位→修改→验证"四步 |
| 未区分 Python 测试框架 | 按 marker 文件自动选择 pytest/django test |
| 无"修复完整性"检查 | 修改多个文件后提示"是否还有遗漏的文件" |

---

## 六、实施优先级（经代码审查修正）

> **重要约束**：所有改进项共享同一批候选题（74 Patch Error + 53 Unresolved + 3 Empty = **77 题可提升空间**），预期提分不可叠加超过此上限。

### 已实施（本轮彻底修复，全部有单测锁定）

| 项目 | 状态 | 改动 | 锁定测试 |
|------|------|------|---------|
| **Bug A：PatchProduced 门控** | ✅ 已修复 | `types.go`：`PatchProduced=false` 时 `model_patch=""`，模型文字不再被写成 patch | `TestPredictionsNeverWriteProseAsPatch` |
| **Bug A+：新增文件丢失** | ✅ 已修复 | `swebench.go` `ExtractDiff` 改用 worktree 之外的临时 index + `read-tree HEAD` + `add -A` + `diff --cached --binary`，捕获 untracked 新增文件 | `TestExtractDiff_CapturesUntrackedNewFile` |
| **Bug A+：捕获无副作用** | ✅ 已修复 | 临时 index 不触碰真实 index 与工作区文件 | `TestExtractDiff_DoesNotTouchTheWorktreeIndex` |
| **Bug A+：删除捕获** | ✅ 已修复 | `add -A` 记录删除，patch 携带 removal | `TestExtractDiff_CapturesDeletion` |
| **双实现漂移** | ✅ 已消除 | `subcmd_bench.go` `extractDiff` 委托 `bench.ExtractDiff`，全仓一条捕获规则 | — |
| **Bug B/P0-2：验收器** | ✅ 已修复 | `verifyPatchApplies` 改用 `git apply --check --cached` 对 HEAD 播种的临时 index，移除 `git stash/pop`，验证无副作用 | `TestVerifyPatchAppliesDoesNotTouchTheWorktree`、`TestVerifyPatchAppliesRejectsMissingNewFile` |
| **P0-2：无效 patch 硬门控** | ✅ 已修复 | 校验失败写 `PatchInvalid=true`，predictions 中该题 `model_patch=""`（不可用 patch 视同无 patch），不再盲投 | `TestPredictionsTreatInvalidPatchAsUnusable` |

### 待实施

| 优先级 | 改进项 | 候选题池 | 证据状态 | 工作量 |
|--------|--------|---------|---------|--------|
| **P1-1** | Write Verification 硬阻止 end_turn | 53 Unresolved | ⚠️ 需 trajectory 证据 | 1 天 |
| **P1-2** | exec pytest 输出截断策略 | 53 Unresolved | ⚠️ 需确认 `budget.go` 截断实现 | 1 天 |
| **P1-3** | 测试驱动修复 Skill 硬编码 | 53 Unresolved | ⚠️ 需 trajectory 证据 | 0.5 天 |
| **P1-5** | bench QualityGate L0+L1 | 53 Unresolved | ⚠️ 需设计 | 2 天 |
| **P2-1** | 30 次无写入进度提醒 | 3 Empty | ✅ 逻辑明确 | 0.5 天 |

**需先取证再改的项目**（计划中原为"推测"口径）：

| 项目 | 所需证据 | 取证方法 |
|------|---------|---------|
| Bug C（超时半写状态） | 失败 patch 尾部是否截断 | 抽查 5 个 patch error 的 diff 尾部 |
| P1-4（Context 压缩丢路径） | trajectory 中模型重复 read 同一文件 | 从 session DB 导出 trajectory 分析 |
| P1-2（exec 截断方向） | 确认 `budget.go` 保留头 or 尾 | 读 wesgine 源码 |

### 实施路线（修正版）

```
Phase 1（已全部完成）：
  ✅ Bug A 修复（PatchProduced 门控 + 单测）
  ✅ 新增文件捕获（临时 index + add -A + diff --cached --binary + 单测）
  ✅ 无副作用验证（git apply --check --cached，移除 git stash + 单测）
  ✅ 无效 patch 硬门控（PatchInvalid + predictions 写空串 + 单测）
  验证：go build ./... && go vet ./... && go test ./... 全绿
  → 重跑 74 题 Patch Error 中受影响的题
  → 预期：79.2% → ~78-80%（10 题假 patch 被修正为空，不再假报 error）

Phase 2（取证 + 3-5 天）：
  → 先从 trajectory 取证，确认 P1-1/P1-2/P1-3 的因果链
  → 实施有证据支撑的改进项
  → 重跑 500 题
  → 预期上限：53 Unresolved 中救回 30-50% → +15-25 题 → ~81-85%

Phase 3（1 周）：
  → CKG Python、bench QualityGate、进度阈值
  → 重跑 500 题
  → 预期上限：再救回 5-10 题 → ~83-87%
```

**天花板说明**：不换模型的情况下，理论极限 = 500 - 3（空 patch）= 497 题全通过 = 99.4%。实际因 DeepSeek Chat 模型能力边界，53 Unresolved 中有一部分是模型理解力不足导致的（而非 Agent/引擎问题），这部分只能通过换更强模型解决。

---

## 七、验证方法

每次改进后的验证标准：

1. **回归测试**：`go build ./... && go test ./cmd/wescode ./internal/...`（wescode）+ `make verify`（wesgine）
2. **Patch 格式验证**：所有 predictions 的 `model_patch` 通过 `git apply --check`
3. **增量评分**：只重跑改动影响的题（不需要全量 500 题）
4. **对比**：新结果 resolved 数 ≥ 旧结果（不允许回归）
5. **代价记录**：每次重跑的模型 API 费用

---

## 八、长期方向（超出 SWE-bench 的通用能力提升）

| 方向 | 说明 | SWE-bench 之外的价值 |
|------|------|---------------------|
| **多语言支持** | SWE-bench 只有 Python，但 wescode 面向全语言 | Go/TypeScript/Java 项目同样受益 |
| **仓库级理解** | CKG 增强，跨文件依赖分析 | 大仓代码导航能力提升 |
| **测试生成** | 不只是运行现有测试，还能为修复生成新测试 | 代码质量保障 |
| **多模型协作** | 用便宜模型做检索/分析，用贵模型做修复决策 | 降低成本、提高质量 |
| **学习机制** | 从已通过的 370 题中学习成功模式 | Memory 子系统的 Correction/Convention |
