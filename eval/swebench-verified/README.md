# SWE-bench Verified 评测 — wescode + DeepSeek Chat

**成绩：396 / 500 = 79.2%**（对标排行榜第 1-3 名）

## 文件导航

| 文件 | 内容 |
|------|------|
| [REPORT.md](REPORT.md) | ★ 评测报告（成绩+按仓库分析+完整题目列表） |
| [MARKET.md](MARKET.md) | 市场分析（排行榜+竞品+提交政策） |
| [IMPROVEMENT.md](IMPROVEMENT.md) | 改进计划（已修复+后续方向） |
| [REPRODUCE.md](REPRODUCE.md) | 完整复现指南 |
| [VERSIONS.md](VERSIONS.md) | 版本锁定（源码+模型+工具） |

## 数据文件

| 文件 | 说明 | 大小 |
|------|------|------|
| `data/all_preds.jsonl` | 500 题 predictions | 2.2 MB |
| `data/results.json` | 完整结果 + per-instance 成本 | 96 KB |
| `data/sessions-rerun77.db` | 77 题重跑的完整 session DB | 14 MB |
| `data/eval-logs-v3.tar.gz` | 评分日志压缩包 | 17 MB |
| `logs/` | 499 个 instance 的评分结果 | 225 MB |
| `trajs/` | 500 个推理轨迹 | 2.8 MB |

## 提交材料

| 文件 | 说明 |
|------|------|
| `submission/metadata.yaml` | SWE-bench 提交元数据 |
| `submission/README.md` | 方法描述（英文） |

## 核心数据

| 指标 | 值 |
|------|------|
| Resolved | **396 / 500 = 79.2%** |
| 模型 | DeepSeek Chat（$0.08/题） |
| 方式 | Best@1 单次 |
| 总成本 | ¥275.59 / $38.82 |
| Agent | wescode（wesgine v1.0） |
| 日期 | 2026-10-04 |
