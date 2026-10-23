# 20261022-deepseek-flash 材料清单

冻结分数来自官方 `results.json`：`success 355 / submitted 500 = 71.0%`。

公开提交：[PR #61](https://github.com/SWE-bench-Live/submission/pull/61)。

## 本目录

| 路径 | sha256 | 说明 |
|------|--------|------|
| `results.json` | `b2be40573cb5c0b4ffef5f34c08ed6b104b951e5ee33ee08593cc2b4a6ad4d72` | 31 669 B；官方裁决 |
| `preds.json` | `e76c757f849a8218e9a832557eb2ca50a4c6b025deaac716f15996585cd0e245` | 52 859 674 B（50.41 MiB） |
| `trajs/` | — | 499 个 `trajectory.txt`，约 42 MiB |
| `logs/` | — | 491 目录：453 套三文件 + 38 空目录（incomplete）；约 1.1G |
| `submission/README.md` | `5b55f7fa6964b8611d5e22260cb8d0766d50d142c70eff4386b20826ce3e0f48` | 7 688 B；交榜英文 README |
| `FAILURE-ANALYSIS.md` | — | 未通过题分析（fill 前 98 道 failure 快照） |
| `failure-catalog.json` | — | 同上 |

GitHub 警告阈值 50MB、硬拒绝 100MB。超过 50MB、低于 100MB 的文件：

| 路径 | 约 |
|------|---:|
| `preds.json` | 50 MiB |
| `logs/bridgecrewio__checkov-6933/post_patch_log.txt` | 73 MiB |
| `logs/bridgecrewio__checkov-6895/post_patch_log.txt` | 73 MiB |
| `logs/flexget__flexget-4306/post_patch_log.txt` | 58 MiB |
| `logs/bridgecrewio__checkov-7124/post_patch_log.txt` | 53 MiB |

没有 ≥100MB 文件。
