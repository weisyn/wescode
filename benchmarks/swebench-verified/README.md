# SWE-bench Verified

维护方：[Princeton NLP](https://www.swebench.com/)。500 题，12 个 Python 仓库（Django、SymPy、scikit-learn、matplotlib、astropy、pytest、sphinx、xarray、pylint、requests、seaborn、flask）。

本目录只存放 **Princeton SWE-bench Verified**。Microsoft SWE-bench Live（355/500 = 71.0%）在 [`../swebench-live/`](../swebench-live/)。两套划分都叫 Verified、都是 500 题，instance 集合不同，Resolved 率不可比。

自 2025-11-18 起，该榜只收学术机构（[政策](https://github.com/SWE-bench/experiments)）。商业产品的公开提交通道是 [SWE-bench Live](../swebench-live/)。

OpenAI 已说明不再将本基准作为主要评测：[Why we no longer evaluate SWE-bench Verified](https://openai.com/index/why-we-no-longer-evaluate-swe-bench-verified/)。

协议：Agent 产出 patch → Docker 执行仓库原有测试（FAIL_TO_PASS + PASS_TO_PASS）。

---

## 已完成

| 日期 | 模型 | 协议 | Resolved | 费用 | 同划分对照 | 目录 |
|------|------|------|----------|------|------------|------|
| 2026-10-04 | DeepSeek Chat | Best@1 | **396/500 = 79.2%** | $38.82 | 当时公开榜并列 79.2%（Sonar / live-SWE-agent + Claude 4.5 Opus，约 $500–$630） | [`20261004-deepseek-chat/`](20261004-deepseek-chat/) |

## 同划分对照（2026-10-04）

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

---

## 每次跑分目录

| 文件 | 说明 |
|------|------|
| `all_preds.jsonl` | 500 题 patch |
| `summary.json` | 汇总、每题成本与 token |
| `logs/<id>/` | 评分日志 |
| `trajs/<id>.json` | 推理轨迹 |
| `sessions.db` | session 库 |
| `submission/metadata.yaml` | 提交格式 |
