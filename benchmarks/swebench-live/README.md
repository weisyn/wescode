# SWE-bench Live

维护方：[Microsoft](https://swe-bench-live.github.io/)。商业产品经 GitHub PR 提交，维护者抽检后上榜。

本目录只存放 **SWE-bench Live**。Princeton NLP 的 SWE-bench Verified（396/500 = 79.2%）在 [`../swebench-verified/`](../swebench-verified/)。两套划分都叫 Verified、都是 500 题，instance 集合不同，Resolved 率不可比。

协议：Agent 产出 patch → 官方容器执行仓库原有测试（FAIL_TO_PASS + PASS_TO_PASS）→ 官方口径 `success / submitted`。empty / incomplete / failure 计入 unresolved。

---

## 已完成

| 日期 | 划分 | 模型 | 协议 | Resolved | 同划分对照 | 目录 |
|------|------|------|------|----------|------------|------|
| 2026-10-22 | Verified 500 | deepseek-flash | Best@1 | **355/500 = 71.0%** | 提交时公开榜第一名 200/500 = 40.0%（SWE-agent + Claude-4.5-Sonnet） | [`20261022-deepseek-flash/`](20261022-deepseek-flash/) |

提交：[PR #61](https://github.com/SWE-bench-Live/submission/pull/61)。

## 未评测

**Lite 300** 是另一划分（与 Verified 重叠 174 题）。须新 rollout，不能从本次 Verified preds 过滤。公开榜上其他系统的 Lite 分数与本目录 Verified 500 无关。

---

## 每次跑分目录

| 文件 | 说明 |
|------|------|
| `preds.json` | 每道题的 patch |
| `results.json` | 官方 harness 裁决 |
| `logs/<id>/` | 容器测试输出 |
| `trajs/<id>/trajectory.txt` | 推理轨迹 |
| `FAILURE-ANALYSIS.md` | 未通过题分类 |
| `submission/README.md` | 交榜英文说明 |
