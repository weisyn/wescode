# Benchmarks

wescode 公开评测结果。每次评测的 patch、官方 harness 日志与推理轨迹均可核验。

---

## SWE-bench Live

[Microsoft](https://swe-bench-live.github.io/) 维护，持续更新的软件工程任务，评测对象是 Agent + 模型。商业产品经 GitHub PR 提交，维护者抽检后上榜。本次划分：**Verified 500**。

<div align="center">

### 71.0%　·　355 / 500

提交时公开榜最高分为 40.0%。已交维护者审核（[PR #61](https://github.com/SWE-bench-Live/submission/pull/61)）。本划分是 **Verified 500**，只对照该榜该划分；不与 Lite 300 对照。

wescode　·　deepseek-flash　·　Best@1　·　2026-10-22

</div>

提交时公开榜（维护者基线，2025-12-31）：

| 排名 | Agent | 模型 | Resolved |
|:---:|------|------|----------|
| 1 | SWE-agent | Claude-4.5-Sonnet | 200 / 500 = 40.0% |
| 2 | SWE-agent | GPT5-Thinking | 152 / 500 = 30.4% |

材料：[`swebench-live/20261022-deepseek-flash/`](swebench-live/20261022-deepseek-flash/)。

---

## SWE-bench Verified

[Princeton NLP](https://www.swebench.com/) 维护，12 个经典 Python 仓库、人工筛选 500 题。[2025-11-18 起](https://github.com/SWE-bench/experiments/blob/main/README.md) 仅收学术机构，商业系统不能再交 [SWE-bench/experiments](https://github.com/SWE-bench/experiments)。

本次 79.2% 未交该仓。公开渠道：本仓库 [`swebench-verified/20261004-deepseek-chat/`](swebench-verified/20261004-deepseek-chat/)（preds、harness 日志、轨迹）。

<div align="center">

### 79.2%　·　396 / 500

与当时公开榜最高分相同。

wescode　·　DeepSeek Chat　·　Best@1　·　2026-10-04　·　$38.82

</div>

对照日期 2026-10-04 [公开榜](https://www.swebench.com/)（下表均已提交 experiments、均为自有 Agent）。只对照这一张榜、这一天已收录的 Agent 提交；不与未审自报、其他评测方、模型评测混排。Live 与 Verified 是两张榜，互不对照。

| 排名 | Agent | 模型 | Resolved | 方法 | 费用 |
|:---:|------|------|----------|------|------|
| 1 | Sonar Foundation Agent | Claude 4.5 Opus | 79.20% | Best@1 | ~$630 |
| 1 | live-SWE-agent | Claude 4.5 Opus (medium) | 79.20% | Best@1 | ~$500 |
| 3 | TRAE | Doubao-Seed-Code | 78.80% | 30x rollout | ~$2,000+ |
| 4 | live-SWE-agent | Gemini 3 Pro Preview | 77.40% | Best@1 | — |
| 5 | EPAM AI/Run Developer | Claude 4 Sonnet | 76.80% | Best@1 | — |
| 6 | Atlassian Rovo Dev | Multiple | 76.80% | 混合模型 | — |

---

## 材料

```
benchmarks/
├── swebench-live/20261022-deepseek-flash/      71.0% · Live Verified 500
└── swebench-verified/20261004-deepseek-chat/   79.2% · Princeton Verified 500
```
