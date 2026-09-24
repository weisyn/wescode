# wescode Skill 体系

> 更新日期：2026-09-02

## 与引擎层的关系

wescode **继承** wesgine Tier E 机制 Skill，不在应用层同名覆盖。产品增量用**不同名** Skill，只写这个 Cell 真有的工具。

```
wesgine 引擎（7 个机制 Skill，embed，SkillSourceEngine）
    ↓  Loader：引擎先扫描，Cell 后写覆盖同名
wescode 应用（26 个 Tier C，SeedSkills → cell.Skills().InstallFromDir）
```

禁止再放 `agentic-retrieval` / `agentic-execution` / `skill-authoring` / `delegation` 的同名副本——会把引擎现行契约（`summarize` 须 `tool_search`、安装闭集、`agent(prompt=...)`、L4-only）整份盖掉。

| 引擎（继承） | wescode 产品 delta |
|-------------|-------------------|
| `agentic-retrieval` always-on | `wescode-retrieval`（`depends_on` 共激活）：`project_map` / `search_symbols` / `find_references` / `find_callers` / `impact_analysis` |
| `agentic-execution` always-on | `wescode-execution`：`go build` / `go test` / `pytest` / `tsc` |
| `investigation` | `issue-investigation`：editor / webview / backend 层 + CKG |
| `delegation` | （无副本） |
| `skill-authoring` | （无副本。草稿 `write` 到工作区；模型没有 install。用户用技能页「新建技能」/技能工作台粘贴安装；已装技能 `skill(edit)`） |
| `plan` / `memory-strategy` | （无副本） |

## 设计哲学

```
Skill 价值 = f(模型不确定性, 任务可重复性, 错误成本)
```

只教这个 Cell 走得通的调用。模型 `save`/`note` 落 L4；`kind` 不改层。26 份全部声明 `version` / `min_engine`（引擎 `skill-authoring` 清单会从已装技能抄 frontmatter）。

## Skill 清单（26 个）

### 产品增量（叠在引擎机制上）

| Skill | 触发 | 核心价值 |
|-------|------|---------|
| **wescode-retrieval** | 与 `agentic-retrieval` 共激活 | CKG/LSP 检索；图工具不在 schema 时退回 `grep` |
| **wescode-execution** | 与 `agentic-execution` 共激活 | 语言相关 verify 命令表 |
| **issue-investigation** | 用户报 bug | 三层目录 + CKG；六阶段在引擎 |
| **context-calibration** | 首次接触工作区 | git 信号 → L4 `memory(note)`；**非** always-on |

### Tier 1 — 重度方法论

| Skill | 触发 | 核心价值 |
|-------|------|---------|
| **performance-optimization** | 优化性能时 | profiling-first |
| **refactoring** | 大规模重命名/迁移时 | 渐进式变更 + 逐步编译验证 |
| **integration-audit** | 审查接线时 | Set*/With* 注入追踪 |
| **legacy-navigation** | 遗留代码 | 先理解后改 |
| **code-audit-discipline** | 大范围审计 | `report_finding`（本产品真有此工具） |

### Tier 2 — 标准编程

| Skill | 触发 | 核心价值 |
|-------|------|---------|
| **test-engineering** | 写测试时 | TDD 红绿重构纪律 |
| **security-audit** | 安全审计时 | OWASP Top 10 + 依赖 CVE |
| **code-review-dispatch** | `/code-review-dispatch` | 确定性审查流水线 |
| **code-review** | 审查 diff/PR 时 | 四维度审查 |
| **debug-methodology** | 运行时崩溃/断点 | wescode debug 工具 |
| **documentation-engineering** | 写文档时 | 示例可编译 |
| **pattern-consistency** | 写完代码后对比同包 | 无 `post_write` 钩子，靠模型路由 |
| **autonomous-workflow** | 用户要求自主推进时 | 验证-修复循环；未要求落地则不 commit |

### Tier 3 — 轻量参考

| Skill | 触发 | 保留内容 |
|-------|------|---------|
| **database-design** | Schema/迁移时 | Gate: UP+DOWN 成对 |
| **clean-code** | 实现/重构时 | Gate: 读后再写 + 验证通过 |
| **ui-engineering** | 前端组件时 | Gate: 六态覆盖 + tsc 零错误 |
| **cicd-deployment** | CI/CD 配置时 | Gate: 多阶段 Docker + 零密钥 |
| **git-workflow** | Git 操作时 | Gate: conventional commit |
| **system-design** | 架构设计时 | Gate: 至少两方案对比 |
| **requirements-analysis** | 需求分析时 | Gate: Goal+HappyPath+Metric |

### 平台能力

| Skill | 触发 | 核心价值 |
|-------|------|---------|
| **split-changes** | 拆分大变更时 | plan → approve → 逐 slice 建分支/commit/push/PR |
| **pr-guardian** | PR 守护时 | 三阶段循环：冲突 → 评论 → CI |

## 与 wesclaw 的同步策略

| 维度 | wescode | wesclaw |
|------|---------|---------|
| 代码搜索 | 引擎 `grep`/`glob` + `wescode-retrieval` 图工具 | `grep` / `glob` |
| 编辑验证 | 引擎 act-first + `wescode-execution` 命令表 | 无编译验证 |

机制契约只在引擎改一份。产品仓只追加本 Cell 的工具名。

## always-on 准入标准

必须同时满足：每次 Run 都受益 + token 预算可接受 + 不干扰无关任务。

wescode **产品 Skill 无一 always-on**。引擎 `agentic-retrieval` / `agentic-execution` / `memory-strategy` 已经 always-on。

## 竞品参考

详见 [COMPETITIVE-ANALYSIS.md](COMPETITIVE-ANALYSIS.md)。
