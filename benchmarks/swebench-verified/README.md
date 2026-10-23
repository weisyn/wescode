# SWE-bench Verified

[Princeton NLP](https://www.swebench.com/) 维护。12 个经典 Python 仓库、人工筛选 500 题。[2025-11-18 起](https://github.com/SWE-bench/experiments/blob/main/README.md) 仅收学术机构，商业系统不能再交 [SWE-bench/experiments](https://github.com/SWE-bench/experiments)。

本次 79.2% 未交该仓。公开渠道：本目录 [`20261004-deepseek-chat/`](20261004-deepseek-chat/)（preds、harness 日志、轨迹）。

## 79.2%　·　396 / 500

与当时公开榜最高分相同。

wescode　·　DeepSeek Chat　·　Best@1　·　2026-10-04　·　$38.82

对照日期 2026-10-04 [公开榜](https://www.swebench.com/)（下表均已提交 experiments）：

| 排名 | Agent | 模型 | Resolved | 方法 | 费用 |
|:---:|------|------|----------|------|------|
| 1 | Sonar Foundation Agent | Claude 4.5 Opus | 79.20% | Best@1 | ~$630 |
| 1 | live-SWE-agent | Claude 4.5 Opus (medium) | 79.20% | Best@1 | ~$500 |
| 3 | TRAE | Doubao-Seed-Code | 78.80% | 30x rollout | ~$2,000+ |
| 4 | live-SWE-agent | Gemini 3 Pro Preview | 77.40% | Best@1 | — |
| 5 | EPAM AI/Run Developer | Claude 4 Sonnet | 76.80% | Best@1 | — |
| 6 | Atlassian Rovo Dev | Multiple | 76.80% | 混合模型 | — |
| 7 | mini-SWE-agent | Claude 4.5 Opus (high) | 76.80% | Best@1 | — |

锁榜之后未进 swebench.com、但公开过分数的例子（评测方、harness、日期与上表不同，不并入排名）：

| 系统 | 分数 | 链接 | 说明 |
|------|------|------|------|
| ESMC + Claude 4.5 Sonnet | 451 / 500 = 90.2% | [experiments PR #374](https://github.com/SWE-bench/experiments/pull/374) | 提交方自报；2025-11-26 开 PR，维护者按政策关闭，未审 |
| Claude Opus 4.8 + Claude Code | 85.8% | [Vals，2026-09-01](https://www.vals.ai/benchmarks/swebench) | 第三方评测，非 experiments |
| Composer 2.5 + Cursor CLI | 79.6% | 同上 | 第三方评测。Cursor 官网报的是 [SWE-bench Multilingual 79.8%](https://cursor.com/composer)，不是 Verified |

Vals 同页其余行多数是 Mini-SWE-agent 测模型（最高 Claude Opus 5 **97.0%**），不是各家自有 Agent 交 Princeton 榜。

详细材料：[`20261004-deepseek-chat/`](20261004-deepseek-chat/)

## 按仓库

| 仓库 | 总题数 | Resolved | 通过率 |
|------|--------|----------|--------|
| astropy | 22 | 20 | 90.9% |
| xarray | 22 | 19 | 86.4% |
| sympy | 75 | 64 | 85.3% |
| scikit-learn | 32 | 27 | 84.4% |
| pytest | 19 | 16 | 84.2% |
| django | 231 | 189 | 81.8% |
| matplotlib | 34 | 23 | 67.6% |
| requests | 8 | 5 | 62.5% |
| sphinx | 44 | 26 | 59.1% |
| pylint | 10 | 4 | 40.0% |
| seaborn | 2 | 2 | 100% |
| flask | 1 | 1 | 100% |

| 文件 | 说明 |
|------|------|
| `all_preds.jsonl` | 500 题 patch |
| `summary.json` | 汇总、每题成本与 token |
| `logs/<id>/` | 评分日志 |
| `trajs/<id>.json` | 推理轨迹 |
| `sessions.db` | session 库 |
