# SWE-bench Verified

[Princeton NLP](https://www.swebench.com/) 维护。12 个经典 Python 仓库、人工筛选 500 题。[2025-11-18 起](https://github.com/SWE-bench/experiments/blob/main/README.md) 仅收学术机构，商业系统不能再交 [SWE-bench/experiments](https://github.com/SWE-bench/experiments)。

本次 79.2% 未交该仓。公开渠道：本目录 [`20261004-deepseek-chat/`](20261004-deepseek-chat/)（preds、harness 日志、轨迹）。

## 79.2%　·　396 / 500

与当时公开榜最高分相同。

wescode　·　DeepSeek Chat　·　Best@1　·　2026-10-04　·　$38.82

对照日期 2026-10-04 [公开榜](https://www.swebench.com/)（下表均已提交 experiments、均为自有 Agent）。只对照这一张榜、这一天已收录的 Agent 提交；不与未审自报、其他评测方、模型评测混排。Live 与 Verified 是两张榜，互不对照。

| 排名 | Agent | 模型 | Resolved | 方法 | 费用 |
|:---:|------|------|----------|------|------|
| 1 | Sonar Foundation Agent | Claude 4.5 Opus | 79.20% | Best@1 | ~$630 |
| 1 | live-SWE-agent | Claude 4.5 Opus (medium) | 79.20% | Best@1 | ~$500 |
| 3 | TRAE | Doubao-Seed-Code | 78.80% | 30x rollout | ~$2,000+ |
| 4 | live-SWE-agent | Gemini 3 Pro Preview | 77.40% | Best@1 | — |
| 5 | EPAM AI/Run Developer | Claude 4 Sonnet | 76.80% | Best@1 | — |
| 6 | Atlassian Rovo Dev | Multiple | 76.80% | 混合模型 | — |

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
