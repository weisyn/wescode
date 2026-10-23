<div align="center">

# 微软 SWE-bench Live

**能交公开榜的那场考试** · 不是普林斯顿那套 79.2%

[官网](https://swe-bench-live.github.io/) · 商业产品走 GitHub PR，维护者抽检

</div>

> [!IMPORTANT]
> 本目录只有 **微软 Live**。普林斯顿学术卷 79.2% 在 [`../swebench-verified/`](../swebench-verified/)。
> 两套都叫 Verified 500，**题目不是同一批**，71% 不是从 79.2% 掉下来的。

给 Agent 一个真实 GitHub issue → 它自己改代码 → 官方容器里跑项目原有测试 → `success / submitted` = 修好。

---

## 这场该跟谁比

<p align="center">
  <img src="https://img.shields.io/badge/wescode-71.0%25%20(355%2F500)-2ea44f?style=for-the-badge" alt="71.0%"/>
  <img src="https://img.shields.io/badge/同场当时第一名-40.0%25-lightgrey?style=for-the-badge" alt="40%"/>
</p>

当时 Verified 500 公开榜第一名是 SWE-agent + Claude-4.5-Sonnet **200/500 = 40.0%**。wescode 是 **355/500 = 71.0%**。

提交：[PR #61](https://github.com/SWE-bench-Live/submission/pull/61)（维护者审完才上榜）。

| 日期 | 试卷 | 模型 | 方式 | 修好 | 该对比的分数 | 目录 |
|------|------|------|------|------|--------------|------|
| **2026-10-22** | Verified 500 | deepseek-flash | Best@1 | **355/500 · 71.0%** | 同场当时第一 **40.0%** | [`20261022-deepseek-flash/`](20261022-deepseek-flash/) |

**Lite 300** 是微软另一套题（和 Verified 重叠 174 道），还没跑。必须新跑，不能从本次 preds 里过滤。别人榜上的 Lite 71% 也不是我们这套 Verified 500。

---

## 每次跑分目录里有什么

| 文件 | 说明 |
|------|------|
| `preds.json` | 每道题的补丁 |
| `results.json` | 官方打分 |
| `logs/<id>/` | 测试输出 |
| `trajs/<id>/trajectory.txt` | 推理过程 |
| `FAILURE-ANALYSIS.md` | 没过的题卡在哪 |
| `submission/README.md` | 交给微软的英文说明 |
