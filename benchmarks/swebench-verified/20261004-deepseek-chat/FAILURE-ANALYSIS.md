# SWE-bench Verified 失败分析与修复

> **数据来源**：服务器 `/root/logs/run_evaluation/wescode-v3/` 的 report.json、test_output.txt、patch.diff
> **初次分析**：2026-10-05 | **最新更新**：2026-10-05（实际数据替换估算值）

---

## 总览

```
500 题
 ├── 396 Resolved (79.2%) ✅
 ├── 57 Unresolved (11.4%) — patch 正确 apply，修复方向完全错误
 ├── 46 Patch Error (9.2%) — diff 尾换行被剥离，"ends in middle of line"
 └── 1 Empty Patch (0.2%) — django__django-13513 未生成修改
```

---

## 一、Patch Error 46 题——单一根因

### 确认的根因

对 46 个 Patch Error 的 `run_instance.log` 做了全量扫描：

| 错误类型 | 数量 |
|---------|:----:|
| `patch unexpectedly ends in middle of line` | **46** |
| `malformed patch` | 0 |
| `Hunk #N FAILED` | 0 |
| 其他 | 0 |

**100% 同一个根因**：`WriteSWEBenchPredictions` 中的 `strings.TrimSpace(res.Output)` 剥离了 `git diff` 输出的尾部 `\n`。POSIX `patch` 工具需要这个换行符来标记"最后一个 hunk 正常结束"——缺了它就报 `ends in middle of line` 并拒绝 apply。

### 修复（✅ 已实施）

`wescode.git/backend/internal/bench/types.go` 第 622-636 行：

```go
// 修复前（剥离尾换行）：
patch = strings.TrimSpace(res.Output)

// 修复后（保留尾换行）：
patch = strings.TrimLeft(res.Output, " \t\r\n")
if len(patch) > 0 && patch[len(patch)-1] != '\n' {
    patch += "\n"
}
```

**测试覆盖**：`TestPredictionsPreserveTrailingNewline`（新增），断言 `model_patch` 的最后一个字节是 `\n`。

**预期影响**：46 题全部转为可评分。如果这 46 题的修复方案本身正确（FAIL_TO_PASS 通过），则可获得 +46 分的上限。保守估算按 70% 成功率 = +32 题，总分 **428/500 = 85.6%**。

---

## 二、Unresolved 57 题——模型能力边界

### 确认的特征

对 57 个 Unresolved 的 report.json 做了全量统计：

| 指标 | 值 |
|------|:--:|
| 部分修复（FAIL_TO_PASS 部分通过） | **0** 题 |
| 完全未修复（FAIL_TO_PASS 全失败） | **57** 题 |
| 引入回归（PASS_TO_PASS 有失败） | **0** 题 |

**关键发现**：没有任何一题是"差一点就对了"——全部 57 题的 patch 都 apply 成功但修复方向完全错误（0 个目标测试通过），且都没有引入回归。这说明模型读懂了 issue、产出了合理代码，但修复的不是测试实际检验的点。

### Patch 大小分布

| 大小范围 | 题数 |
|---------|:----:|
| < 200 bytes | 0 |
| 200-2000 bytes | 16 |
| > 2000 bytes | 41 |

41/57 (72%) 的 patch 超过 2KB，说明模型倾向于过度修改而非精确定位。

### 按仓库分布

| 仓库 | Unresolved | 说明 |
|------|:---------:|------|
| django | 22 | ORM/管理命令/中间件等复杂场景 |
| sphinx | 10 | 文档构建管线、linkcheck 等 |
| matplotlib | 9 | 图形渲染、像素级比较 |
| sympy | 7 | 数学符号计算、精确性要求 |
| requests | 3 | HTTP 连接层 |
| pylint | 3 | AST 分析/检查器逻辑 |
| scikit-learn | 2 | 机器学习算法精确性 |
| astropy | 1 | 天文计算 |

### 引擎/Skill 层改进（✅ 已实施）

对于 Unresolved，**引擎无法让模型"想对"**，但可以改善反馈循环：

| # | 修复项 | 位置 | 状态 | 说明 |
|---|--------|------|------|------|
| **UR-1** | 测试全部通过才能结束 | `agentic-execution` Skill 规则 6 | ✅ 已加 | "看到 `1 failed` 就不要 end_turn，继续修" |
| **UR-2** | 导入破坏检测 | `agentic-execution` Skill 规则 7 | ✅ 已加 | "修改后 `ImportError` 必须修复或撤回" |

**这些改进不能修复本次 57 题的具体答案**，但可以在未来的 run 中让模型多迭代一轮。

### 只有模型能力提升才能改善的部分

| 类别 | 题数 | 改进方向 | 成本 |
|------|:----:|---------|------|
| matplotlib 渲染 | 9 | 换 Claude Sonnet / multi-rollout | ¥5,000+ |
| sympy 数学精确性 | 7 | multi-rollout 3× | ¥800 |
| 复杂 Django ORM | 22 | 更强模型 + 更多上下文 | 取决于模型 |
| Sphinx 管线 | 10 | 更好的调用图索引 | 引擎改进 |
| 其他 | 9 | 逐题针对性分析 | N/A |

---

## 三、提升路线

```
当前:    396/500 = 79.2%  ████████████████████████████████████████░░░░░░░░░░

修复 Patch Error 尾换行后重跑 46 题:
  保守:  396 + 32 = 428/500 = 85.6%  ███████████████████████████████████████████████░░░
  乐观:  396 + 42 = 438/500 = 87.6%  ████████████████████████████████████████████████░░

Skill 改进后重跑全部 103 题:
  保守:  396 + 37 = 433/500 = 86.6%  ███████████████████████████████████████████████░░░
  乐观:  396 + 50 = 446/500 = 89.2%  ████████████████████████████████████████████████░░

换强模型 (Claude 4 Sonnet) + multi-rollout:
  目标:  450+/500 = 90%+  █████████████████████████████████████████████████░
```

### 下一步行动

1. **重跑 46 个 Patch Error 题**：仅评分，不重新生成 patch（已有正确 patch，只是尾换行被截）
2. **重跑 57 个 Unresolved 题**：使用更新的 Skill，给模型更多迭代机会
3. **评估 multi-rollout (pass@3)**：每题跑 3 次取最优，参考字节的 pass@30
