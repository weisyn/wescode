# wescode Benchmarks

wescode 在主流 AI 编程 Agent 评测上的跑分数据、分析报告和完整复现材料。

**所有评分数据公开**——predictions、评分日志、推理轨迹均在对应跑分目录内，任何人可以复现和验证。

两套 SWE-bench **不是同一批题**，数字不能横比、不能写进同一张「wescode 79%」表。

---

## 最新成绩

### 1. SWE-bench Live Verified 500 — 71.0%（355/500）

> **平台**：微软 [SWE-bench Live](https://swe-bench-live.github.io/)（商业可交公开榜）
> **Agent**: wescode（基于 wesgine v1.0 引擎）
> **Model**: deepseek-flash
> **Method**: Best@1 单次运行，非 multi-rollout
> **Date**: 2026-10-22

```
Resolved:     ███████████████████████████████████░░░░░░░░░░░░░░░  71.0% (355)
Unresolved:   ███████████████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░  29.0% (145)
  failure 99 · empty 8 · incomplete 38
```

当时 Verified 500 榜首 40.0%（SWE-agent + Claude-4.5-Sonnet）。提交：[PR #61](https://github.com/SWE-bench-Live/submission/pull/61)（维护者审完才上榜）。

→ 详细报告：[`swebench-live/20261022-deepseek-flash/README.md`](swebench-live/20261022-deepseek-flash/README.md)

### 2. Princeton SWE-bench Verified — 79.2%（396/500）

> **平台**：Princeton [SWE-bench](https://www.swebench.com/)（2025-11 起学术锁，**不能**交公开榜）
> **Agent**: wescode
> **Model**: DeepSeek Chat（API model ID: `deepseek-chat`）
> **Method**: Best@1 单次运行，非 multi-rollout
> **Cost**: ¥275.59 / $38.82（平均 $0.08/题）
> **Date**: 2026-10-04

```
Resolved:     ████████████████████████████████████████░░░░░░░░░░  79.2% (396)
Unresolved:   ██████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░  11.4% ( 57)
Patch Error:  █████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░   9.2% ( 46)
Empty Patch:  ░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░   0.2% (  1)
```

**对标当时 SWE-bench 排行榜并列第 1**——与 Claude 4.5 Opus + Sonar Foundation Agent 的 79.20% 持平，费用约为前者的 1/16。这是学术锁之前的内部评分，**不是** Live 榜上的数字。

| # | MODEL | AGENT | % RESOLVED | 费用 |
|---|-------|-------|-----------|------|
| 1 | Claude 4.5 Opus | Sonar Foundation Agent | 79.20% | ~$630 |
| 1 | Claude 4.5 Opus (medium) | live-SWE-agent | 79.20% | ~$500 |
| **1** | **DeepSeek Chat** | **wescode** | **79.20%** | **$39** |
| 4 | Doubao-Seed-Code | TRAE（30x rollout） | 78.80% | ~$2,000+ |

→ 详细报告：[`swebench-verified/20261004-deepseek-chat/README.md`](swebench-verified/20261004-deepseek-chat/README.md)

---

## 评测平台

| 评测 | 题数 | 语言 | 定位 | 分数 | 状态 |
|------|------|------|------|------|------|
| [**SWE-bench Live Verified**](swebench-live/) | 500 | 多仓 Python | 微软持续更新 split；可交公开榜 | **71.0%** | ✅ 完成 · [PR #61](https://github.com/SWE-bench-Live/submission/pull/61) |
| [**SWE-bench Verified**](swebench-verified/) | 500 | Python | Princeton 学术锁；12 个主流仓 | **79.2%** | ✅ 完成（不能交公开榜） |
| SWE-bench Live Lite | 300 | 多仓 Python | Live 另一套 split | — | ⏳ 须新 rollout |
| SWE-bench Multilingual | 300 | 9 语言 | 跨语言代码修复 | — | ⏳ 计划中 |
| SWE-bench Pro | 731 | Python | 企业级难度（Scale AI） | — | ⏳ 计划中 |
| Terminal-Bench 2.1 | 89 | 多语言 | 容器化终端真实任务 | — | ⏳ 计划中 |
| Aider Polyglot | 225 | 6 语言 | Exercism 编程题 | — | ⏳ 计划中 |

---

## wescode Agent 架构

wescode 基于 **wesgine v1.0**（服务端多租户 AI Agent 引擎），以 VS Code 为基座构建。

```
    ┌───────────────────────────────────────┐
    │         SWE-bench Issue               │
    │   "Fix bug in django/db/models..."    │
    └─────────────────┬─────────────────────┘
                      │
                      ▼
    ┌───────────────────────────────────────┐
    │         wescode bench CLI             │
    │   git clone → Agent Loop → git diff   │
    └─────────────────┬─────────────────────┘
                      │
          ┌───────────┴───────────┐
          ▼                       ▼
    ┌──────────┐           ┌──────────┐
    │ Cognitive │           │ Context  │
    │ (LLM API)│           │ (压缩/   │
    │          │           │  缓存)   │
    └────┬─────┘           └──────────┘
         │
         ▼
    ┌──────────────────────────────────────┐
    │           Execution Layer            │
    │  read  write  edit  exec  grep       │
    │  apply_patch  search_files  ...      │
    └──────────────────────────────────────┘
```

### 关键特性

| 特性 | 说明 |
|------|------|
| **通用 Agent 引擎** | wesgine 是通用引擎，不针对某一套 SWE-bench 做特化 |
| **标准工具集** | 17 个 wesgine 内置工具（read/write/edit/exec/grep 等） |
| **Write Verification Nudge** | 引擎检测「写了文件但没验证」时自动 nudge（ADR-311） |
| **Patch 采集** | 临时索引捕获新增/修改/删除文件 |
| **Cell 隔离** | 每个 bench case 在独立的 Cell 中运行 |

Live Verified 本档：解题在宿主机 git worktree，官方 Docker 只用于事后评分。详见该次 [`submission/README.md`](swebench-live/20261022-deepseek-flash/submission/README.md)。

---

## 目录结构

```
benchmarks/
├── README.md                              本文件
├── .gitignore                             覆盖根目录的 *.log 排除
│
├── swebench-live/                         微软 SWE-bench Live（可交公开榜）
│   ├── README.md                          评测说明 + 历次成绩
│   └── 20261022-deepseek-flash/           2026-10-22 Verified 500 · 71.0%
│       ├── README.md                      成绩报告
│       ├── preds.json                     499 题 predictions（50MB）
│       ├── results.json                   官方 harness 裁决
│       ├── trajs/                         499 个推理轨迹
│       ├── logs/                          491 个评分日志目录（~1.1G）
│       ├── FAILURE-ANALYSIS.md            未通过题分析
│       └── submission/README.md           交榜英文 README
│
├── swebench-verified/                     Princeton SWE-bench Verified（学术锁）
│   ├── README.md                          评测说明 + 历次成绩
│   └── 20261004-deepseek-chat/            2026-10-04 · 79.2%
│       ├── README.md                      完整报告
│       ├── all_preds.jsonl                500 题 predictions
│       ├── summary.json / bench-report.json / run.json
│       ├── sessions.db                    session 数据库
│       ├── trajs/                         推理轨迹
│       ├── logs/                          评分日志
│       └── submission/metadata.yaml
```

### 命名规则

每次跑分以 `YYYYMMDD-<model>` 命名：

```
swebench-live/
└── 20261022-deepseek-flash/       # Live Verified 500

swebench-verified/
└── 20261004-deepseek-chat/        # Princeton Verified 500
```

---

## 快速复现

Princeton 那次：见 [`swebench-verified/20261004-deepseek-chat/README.md`](swebench-verified/20261004-deepseek-chat/README.md)。

Live 那次（只复现**评分**，preds 已在目录里）：

```bash
python -m evaluation.evaluation \
  --dataset verified.jsonl \
  --patch_dir benchmarks/swebench-live/20261022-deepseek-flash/preds.json \
  --platform linux \
  --workers 1
```

---

## 诚实声明

1. **两套 SWE-bench 不能横比**——Princeton Verified 与 Live Verified 题库不同；Live Lite 300 又是第三套。79.2% 和 71.0% 不是同一次考试的两个分数。
2. **Princeton SWE-bench Verified 存在数据污染风险**——OpenAI [已建议停止使用](https://openai.com/index/why-we-no-longer-evaluate-swe-bench-verified/)。该榜自 2025-11 起仅接受学术机构提交。
3. **Live 本档未勾 PR 模板第 1 条**——解题在宿主机 worktree；6 个 session 经 `exec` 读到数据集 YAML。见该次 README。
4. **LLM 输出有随机性**——每次运行结果可能有波动。
5. **所有评分数据公开**——predictions、评分日志、推理轨迹均在本目录内。

---

## 引用

```bibtex
@inproceedings{jimenez2024swebench,
  title={SWE-bench: Can Language Models Resolve Real-world GitHub Issues?},
  author={Jimenez, Carlos E and Yang, John and Wettig, Alexander and Yao, Shunyu
          and Pei, Kexin and Press, Ofir and Narasimhan, Karthik},
  booktitle={The Twelfth International Conference on Learning Representations},
  year={2024}
}
```
