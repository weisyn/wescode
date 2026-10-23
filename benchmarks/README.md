# Benchmarks

本目录是 wescode 公开评测的归档：每道题的 patch、官方 harness 裁决、容器日志与推理轨迹均可核验。

wescode 基于通用引擎 [wesgine](https://github.com/weisyn/wesgine)。下列评测未使用针对某一基准的专用工具。

---

## 可比性

本仓库目前有 **两次独立评测**。它们的 Resolved 率不能横向比较，也不能读成同一条成绩曲线上的前后两次。

| | [SWE-bench Live](https://swe-bench-live.github.io/) | [SWE-bench Verified](https://www.swebench.com/) |
|---|---|---|
| 维护方 | Microsoft | Princeton NLP |
| 题库 | 持续更新的 GitHub issue | 固定 12 个 Python 仓库的历史 issue |
| 本次划分 | Verified，500 题 | Verified，500 题 |
| instance 集合 | 与左列不同 | 与右列不同 |
| 提交通道 | 商业产品可通过 GitHub PR 提交 | 2025-11-18 起仅学术机构 |

两套划分都叫 Verified、规模都是 500，这是命名重合，不是同一数据集。分数只在 **同一基准、同一划分、同一 harness 分母** 下比较。

---

## 协议

任务：给定开源仓库中的真实 GitHub issue，Agent 产出 patch。官方 Docker 镜像执行该仓库原有测试：

1. **FAIL_TO_PASS**：与该 issue 对应的失败测试必须通过。
2. **PASS_TO_PASS**：原先已通过的测试不得回归。

两项同时满足计 **Resolved**。本仓库报告的百分数为 Resolved / *N*；两次评测 *N* = 500。协议均为 **Best@1**（单次 rollout）。

Live 的官方口径是 `success / submitted`。empty、incomplete、failure 全部计入 unresolved，不从分母剔除。

---

## 结果

### SWE-bench Live · Verified 500

公开核验路径。提交：[SWE-bench-Live/submission#61](https://github.com/SWE-bench-Live/submission/pull/61)（维护者抽检后上榜）。

| 项 | 值 |
|---|---|
| 冻结日期 | 2026-10-22 |
| Agent | wescode |
| 模型 | deepseek-flash |
| Resolved | **355 / 500 = 71.0%** |
| 构成 | success 355 · failure 99 · empty 8 · incomplete 38 |
| 同划分对照 | 提交时公开榜第一名：SWE-agent + Claude-4.5-Sonnet，**200 / 500 = 40.0%** |
| 材料 | [`swebench-live/20261022-deepseek-flash/`](swebench-live/20261022-deepseek-flash/) |

### SWE-bench Verified（Princeton NLP）

该榜商业提交通道已关闭。本次结果只在该基准内部对照。

| 项 | 值 |
|---|---|
| 日期 | 2026-10-04 |
| Agent | wescode |
| 模型 | DeepSeek Chat |
| Resolved | **396 / 500 = 79.2%** |
| 费用 | $38.82 |
| 同划分对照 | 当时公开榜并列 79.2%（Sonar / live-SWE-agent + Claude 4.5 Opus；费用约 $500–$630） |
| 材料 | [`swebench-verified/20261004-deepseek-chat/`](swebench-verified/20261004-deepseek-chat/) |

OpenAI 已说明不再将 SWE-bench Verified 作为主要评测，理由包括数据污染。见 [Why we no longer evaluate SWE-bench Verified](https://openai.com/index/why-we-no-longer-evaluate-swe-bench-verified/)。

---

## Live 协议偏差

Live 提交未勾选 PR 模板第 1 条，事实如下：

1. 解题在宿主机 git worktree，不在官方任务容器 `starryzhang/sweb.eval.x86_64.*` 内。Docker 仅用于事后 `evaluation.py`。
2. 6 个 session 在后续 `exec` 中读到宿主机数据集 YAML（含 gold / `test_patch` / `FAIL_TO_PASS`）。首轮 prompt 499/499 未注入。清单见该次 [README](swebench-live/20261022-deepseek-flash/)。
3. 38 incomplete（镜像拉取或评测包装超时）、8 empty（当时 300s 墙）按 unresolved 计入 71.0%。

LLM 有随机性，重跑会有波动。QualityGate 邻域门是本档之后落地的，不写入本次 preds 的能力说明。

---

## 未评测的划分

SWE-bench Live **Lite 300** 是另一划分（与 Verified 重叠 174 题），须单独 rollout，不能从本次 Verified preds 过滤。公开榜上其他系统的 Lite 分数与本仓库的 Verified 500 无关。

其他计划中的基准（均未跑，与上表成绩无关）：SWE-bench Multilingual、SWE-bench Pro、Terminal-Bench 2.1。

---

## 复现

Live 仅复评（preds 已在目录中）：

```bash
python -m evaluation.evaluation \
  --dataset verified.jsonl \
  --patch_dir benchmarks/swebench-live/20261022-deepseek-flash/preds.json \
  --platform linux \
  --workers 1
```

Princeton 该次的完整复现见 [`swebench-verified/20261004-deepseek-chat/README.md`](swebench-verified/20261004-deepseek-chat/README.md)。

---

## 目录

```
benchmarks/
├── swebench-live/20261022-deepseek-flash/      Live Verified 500 · 355/500
│   ├── preds.json · results.json · trajs/ · logs/
│   └── submission/README.md
└── swebench-verified/20261004-deepseek-chat/   Princeton Verified 500 · 396/500
    ├── all_preds.jsonl · trajs/ · logs/ · sessions.db
    └── README.md
```
