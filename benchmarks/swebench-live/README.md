# SWE-bench Live 评测

[SWE-bench Live](https://swe-bench-live.github.io/) 是微软发布的持续更新 AI 编程 Agent 基准。商业产品可以走 GitHub PR 交榜，维护者抽检。

**评测方式**：给 Agent 一个真实 GitHub issue → Agent 自主改源码 → 官方 `evaluation.py` 在 `starryzhang/sweb.eval.x86_64.*` 容器里跑项目测试 → `success / submitted` = Resolved。

**Verified 500** 与 **Lite 300** 是两套冻结 split，不是同一批题（HF 重叠 174 道）。分数不能横比，也不能和 Princeton [SWE-bench Verified](../swebench-verified/) 的 79.2% 写进同一张表。

---

## 历次成绩

| 日期 | split | 模型 | 方式 | Resolved | 通过率 | 目录 |
|------|-------|------|------|----------|--------|------|
| **2026-10-22** | Verified 500 | deepseek-flash | Best@1 | **355/500** | **71.0%** | [`20261022-deepseek-flash/`](20261022-deepseek-flash/) |

当时 Verified 500 榜首是 SWE-agent + Claude-4.5-Sonnet 200/500 = 40.0%。本档已开 [PR #61](https://github.com/SWE-bench-Live/submission/pull/61)，维护者审完才上榜。

Lite 300 尚未跑。必须新 rollout，禁止从 Verified preds 过滤。

---

## 数据完整性

每次跑分目录包含完整可验证材料：

| 文件 | 说明 |
|------|------|
| `preds.json` | instance → patch |
| `results.json` | 官方 harness 裁决 |
| `logs/<instance_id>/` | `report.json` + `status.json` + `post_patch_log.txt` |
| `trajs/<instance_id>/trajectory.txt` | Agent 推理轨迹 |
| `FAILURE-ANALYSIS.md` | 未通过题能力分析 |
| `submission/README.md` | 交榜英文 README |
