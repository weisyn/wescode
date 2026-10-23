# SWE-bench Live

[Microsoft](https://swe-bench-live.github.io/) 维护。持续更新的软件工程任务，评测 Agent + 模型。商业产品经 GitHub PR 提交，维护者抽检后上榜。

## Verified 500　·　71.0%

提交时公开榜最高分为 40.0%。已交维护者审核（[PR #61](https://github.com/SWE-bench-Live/submission/pull/61)）。

wescode　·　deepseek-flash　·　Best@1　·　2026-10-22

提交时公开榜（维护者基线，2025-12-31）：

| 排名 | Agent | 模型 | Resolved |
|:---:|------|------|----------|
| 1 | SWE-agent | Claude-4.5-Sonnet | 200 / 500 = 40.0% |
| 2 | SWE-agent | GPT5-Thinking | 152 / 500 = 30.4% |

详细材料：[`20261022-deepseek-flash/`](20261022-deepseek-flash/)

| 文件 | 说明 |
|------|------|
| `preds.json` | 每道题的 patch |
| `results.json` | 官方 harness 裁决 |
| `logs/<id>/` | 容器测试输出 |
| `trajs/<id>/trajectory.txt` | 推理轨迹 |
| `FAILURE-ANALYSIS.md` | 未通过题分类 |
| `submission/README.md` | 交榜英文说明 |
