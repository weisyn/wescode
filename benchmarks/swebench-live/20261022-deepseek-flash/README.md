# SWE-bench Live · Verified 500 · 2026-10-22

[Microsoft SWE-bench Live](https://swe-bench-live.github.io/) · wescode / deepseek-flash · Best@1

**355 / 500 = 71.0%**　·　提交时公开榜最高分 40.0%　·　已交审核（[PR #61](https://github.com/SWE-bench-Live/submission/pull/61)）

| 项 | 值 |
|---|---|
| 冻结日期 | 2026-10-22（`results.json`）；包装 2026-10-23 |
| Agent / 模型 | wescode / `deepseek-flash` |
| 协议 | Best@1，单次 rollout |
| submitted | 500 行 / 499 唯一 id（`conan-io__conan-18153` 官方重复一行） |
| success / failure / empty / incomplete | **355** / 99 / 8 / 38 |
| Resolved | **355 / 500 = 71.0%**（官方 `success / submitted`；empty / incomplete / failure 计 unresolved） |
| 同划分对照 | 提交时公开榜第一名：SWE-agent + Claude-4.5-Sonnet，200/500 = 40.0% |
| 提交 | [PR #61](https://github.com/SWE-bench-Live/submission/pull/61) |

不报告 339/67.8%、355/462，或 `success / (500 − incomplete)`。TianxiCode 等系统公开的 71% 若来自 **Lite 300**，与本 split 无关。

---

## 本目录材料（完整评分包）

| 路径 | 说明 |
|------|------|
| [`results.json`](results.json) | 官方 harness 裁决。sha256 `b2be4057…ad4d72` |
| [`preds.json`](preds.json) | 499 个 instance → patch。50.41 MiB |
| [`trajs/{id}/trajectory.txt`](trajs/) | 499 份推理轨迹 |
| [`logs/{id}/`](logs/) | 491 个目录：453 套 `report.json` + `status.json` + `post_patch_log.txt`；38 个 incomplete 空目录 |
| [`FAILURE-ANALYSIS.md`](FAILURE-ANALYSIS.md) | 未通过题能力分析（fill 前 98 道 failure 快照） |
| [`failure-catalog.json`](failure-catalog.json) | 同上，机器可读 |
| [`submission/README.md`](submission/README.md) | 交榜英文 README（与 PR 内同一份） |
| [`MANIFEST.md`](MANIFEST.md) | 哈希 |

4 个 `post_patch_log.txt` 在 53–73 MiB（GitHub 警告阈值 50MB，硬拒绝 100MB）。本档没有 ≥100MB 文件。

---

## 与 Princeton 评测的关系

Princeton SWE-bench Verified（2026-10-04，DeepSeek Chat，396/500）是另一基准，材料在 [`../../swebench-verified/20261004-deepseek-chat/`](../../swebench-verified/20261004-deepseek-chat/)。本档失败形态以 PASS_TO_PASS 回归为主（fill 前 98 道 failure 中 84 道）；不与那次 Resolved 率对照。

---

## 提交时主动披露（未勾 PR 模板第 1 条）

1. **解题在宿主机 git worktree**，不在官方 `starryzhang/sweb.eval.x86_64.*` 任务容器里。Docker 只用于事后 `evaluation.py`。
2. **6 个 session** 后来用 `exec` 读到了宿主机数据集 YAML（含 gold / `test_patch` / `FAIL_TO_PASS`）：`cfn-lint-3981` failure；`patroni-3338` / `pybamm-4865` / `xarray-9971` / `tox-3409` / `csvkit-1274` success。首轮 prompt 499/499 干净。
3. CI `evaluate_preds.yml` **没有** `/verified/` 分支，抽检不会自动跑。请维护者手工处理。
4. 38 incomplete：36 道 `docker pull` exit 124；2 道 telegram eval wrap 1800s。冻结，未重跑。
5. 8 道 empty：全部当时 300s 墙（源码后来改 1800s，**不属本档**）。

QualityGate 邻域门是本档 **之后** 才落地的，不能写进这次 preds 的能力说明。Lite 300 必须 **新 rollout**，禁止从本 preds 过滤。

细节见 [`submission/README.md`](submission/README.md)。

---

## 复现（评分，不是再解题）

```bash
# 官方 harness；preds / results 就在本目录
python -m evaluation.evaluation \
  --dataset verified.jsonl \
  --patch_dir preds.json \
  --platform linux \
  --workers 1
```

Agent 侧当时是 `wescode bench` + `Drive` / `RunChat`，一题一次。
