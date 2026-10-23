<div align="center">

# 普林斯顿 SWE-bench Verified

**经典学术卷 · 榜已锁** · 不是微软 Live 那套 71.0%

[官网](https://www.swebench.com/)

</div>

> [!IMPORTANT]
> 本目录只有 **普林斯顿学术卷**。能交公开榜的微软 Live 71.0% 在 [`../swebench-live/`](../swebench-live/)。
> 两套都叫 Verified 500，**题目不是同一批**，不要和 71% 横比。

给 Agent 一个真实 GitHub issue → 它自己读代码、改源码 → Docker 里跑项目原有测试 → 该过的过、原来就过的不挂 = 修好。

**题目来源**：500 道人工筛选题，12 个主流 Python 仓（Django、SymPy、scikit-learn、matplotlib、astropy、pytest、sphinx、xarray、pylint、requests、seaborn、flask）。

自 2025-11-18 起，这张榜只收学术机构（[政策](https://github.com/SWE-bench/experiments)）。公司产品的公开核验路径是 [微软 SWE-bench Live](../swebench-live/)。

---

## 这场该跟谁比

<p align="center">
  <img src="https://img.shields.io/badge/wescode-79.2%25%20(396%2F500)-C9A227?style=for-the-badge" alt="79.2%"/>
  <img src="https://img.shields.io/badge/同场当时-并列第一-C9A227?style=for-the-badge" alt="tied #1"/>
</p>

| 日期 | 模型 | 方式 | 修好 | 费用 | 该对比的分数 | 目录 |
|------|------|------|------|------|--------------|------|
| **2026-10-04** | DeepSeek Chat | Best@1 | **396/500 · 79.2%** | ¥275.59 / $38.82 | 同场并列第一 **79.2%** | [`20261004-deepseek-chat/`](20261004-deepseek-chat/) |

普通人怎么读：老试卷、大家练了很多年、分数普遍高。wescode 用 **$39** 的 DeepSeek Chat，打平当时 **~$500–$630** 的 Claude Opus 组合。

---

## 排行榜对标（2026-10-04，本场内部对照）

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

### 性价比

| Agent | Model | Score | Cost | 分/¥ |
|-------|-------|-------|------|------|
| **wescode** | **DeepSeek Chat** | **79.2%** | **¥275** | **0.288** |
| Sonar | Claude 4.5 Opus | 79.2% | ~¥4,500 | 0.018 |
| TRAE | Doubao-Seed-Code (30x) | 78.8% | ~¥15,000+ | <0.005 |

wescode 性价比大约是 Sonar 的 **16 倍**、TRAE 的 **57 倍**。

---

## 按仓库

| 仓库 | 总题数 | Resolved | 通过率 |
|------|--------|----------|--------|
| **astropy** | 22 | 20 | **90.9%** |
| **xarray** | 22 | 19 | **86.4%** |
| **sympy** | 75 | 64 | **85.3%** |
| **scikit-learn** | 32 | 27 | **84.4%** |
| **pytest** | 19 | 16 | **84.2%** |
| **django** | 231 | 189 | **81.8%** |
| **matplotlib** | 34 | 23 | **67.6%** |
| **requests** | 8 | 5 | **62.5%** |
| **sphinx** | 44 | 26 | **59.1%** |
| **pylint** | 10 | 4 | **40.0%** |
| **seaborn** | 2 | 2 | **100%** |
| **flask** | 1 | 1 | **100%** |

---

## 每次跑分目录里有什么

| 文件 | 说明 |
|------|------|
| `all_preds.jsonl` | 500 题补丁 |
| `summary.json` | 汇总 + 每题成本和 token |
| `logs/<id>/` | 评分日志 |
| `trajs/<id>.json` | 推理轨迹 |
| `sessions.db` | 完整 session 库 |
| `submission/metadata.yaml` | 提交格式 |
