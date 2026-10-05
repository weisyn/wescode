# SWE-bench Verified 评测

[SWE-bench](https://www.swebench.com/) 是普林斯顿大学发布的 AI 编程 Agent 基准评测，被行业公认为衡量 AI 代码修复能力的标准。

**评测方式**：给 Agent 一个真实 GitHub issue → Agent 自主读代码、理解问题、修改源码 → 在 Docker 容器中跑项目原有测试 → `fail_to_pass` 测试通过 + `pass_to_pass` 测试不回归 = Resolved

**Verified 版本**：500 道经人工筛选的高质量题目，来自 12 个主流 Python 开源仓库（Django、SymPy、scikit-learn、matplotlib、astropy、pytest、sphinx、xarray、pylint、requests、seaborn、flask）。

---

## 历次成绩

| 日期 | 模型 | 方式 | Resolved | 通过率 | 费用 | 目录 |
|------|------|------|----------|--------|------|------|
| **2026-10-04** | DeepSeek Chat | Best@1 | **396/500** | **79.2%** | ¥275.59 / $38.82 | [`20261004-deepseek-chat/`](20261004-deepseek-chat/) |

---

## 排行榜对标（2026-10-04）

79.2% 对标 SWE-bench Verified 排行榜**并列第 1**：

| # | MODEL | AGENT | % RESOLVED | 方法 | 费用 |
|---|-------|-------|-----------|------|------|
| 1 | Claude 4.5 Opus | Sonar Foundation Agent | 79.20% | Best@1 | ~$630 |
| 1 | Claude 4.5 Opus (medium) | live-SWE-agent | 79.20% | Best@1 | ~$500 |
| **1** | **DeepSeek Chat** | **wescode** | **79.20%** | **Best@1** | **$39** |
| 4 | Doubao-Seed-Code | TRAE | 78.80% | 30x rollout | ~$2,000+ |
| 5 | Gemini 3 Pro Preview | live-SWE-agent | 77.40% | Best@1 | — |
| 6 | Claude 4 Sonnet | EPAM AI/Run Developer | 76.80% | Best@1 | — |
| 7 | Multiple | Atlassian Rovo Dev | 76.80% | 混合模型 | — |
| 8 | Claude 4.5 Opus (high) | mini-SWE-agent | 76.80% | Best@1 | — |

### 性价比对比

| Agent | Model | Score | Cost | 分/¥ |
|-------|-------|-------|------|------|
| **wescode** | **DeepSeek Chat** | **79.2%** | **¥275** | **0.288** |
| Sonar | Claude 4.5 Opus | 79.2% | ~¥4,500 | 0.018 |
| TRAE | Doubao-Seed-Code (30x) | 78.8% | ~¥15,000+ | <0.005 |

wescode 性价比是 Sonar 的 **16 倍**、TRAE 的 **57 倍**。

---

## 按仓库成绩

| 仓库 | 总题数 | Resolved | 通过率 | 亮点/说明 |
|------|--------|----------|--------|----------|
| **astropy** | 22 | 20 | **90.9%** | 天文学库，表现最强 |
| **xarray** | 22 | 19 | **86.4%** | pandas-like 数据操作，全部有 patch 的题都通过 |
| **sympy** | 75 | 64 | **85.3%** | 最大仓库之一（75 题），数学符号计算 |
| **scikit-learn** | 32 | 27 | **84.4%** | ML 库 API 修复 |
| **pytest** | 19 | 16 | **84.2%** | 测试框架，全部有 patch 的题都通过 |
| **django** | 231 | 189 | **81.8%** | 最大仓库（231 题），Python Web 框架 |
| **matplotlib** | 34 | 23 | **67.6%** | 绘图库，涉及 C 扩展和渲染逻辑 |
| **requests** | 8 | 5 | **62.5%** | HTTP 库 |
| **sphinx** | 44 | 26 | **59.1%** | 文档生成器，RST 解析 + Jinja 模板 |
| **pylint** | 10 | 4 | **40.0%** | 代码分析工具，AST 遍历复杂 |
| **seaborn** | 2 | 2 | **100%** | — |
| **flask** | 1 | 1 | **100%** | — |

---

## 提交状态

自 2025-11-18 起，SWE-bench Verified 仅接受学术机构提交（[政策详情](https://github.com/SWE-bench/experiments)）。商业公司需通过学术合作路线：

- 至少一位作者隶属学术机构或知名研究实验室
- 附 arXiv 预印本或技术报告
- 方法开源

**仍可提交的榜单**：SWE-bench Multimodal（对所有人开放）、SWE-bench Pro Public（通过 AgentBeats）、Terminal-Bench 2.1、Aider Polyglot。

---

## 数据完整性

每次跑分目录包含**完整的可验证数据**：

| 文件 | 说明 |
|------|------|
| `all_preds.jsonl` | 500 题 predictions（SWE-bench 标准格式） |
| `summary.json` | 结果汇总 + 500 题 per-instance token 和成本 |
| `logs/<instance_id>/` | 每题的 `report.json` + `patch.diff` + `test_output.txt` + `run_instance.log` |
| `trajs/<instance_id>.json` | Agent 推理轨迹 |
| `sessions.db` | 完整 session 数据库（SQLite，可查询每轮工具调用） |
| `submission/metadata.yaml` | SWE-bench 官方提交格式 |
