# wescode Benchmarks

wescode 在主流 AI 编程评测平台上的跑分数据、分析报告和完整复现指南。

**所有数据公开透明**——predictions、评分日志、推理轨迹、session 数据库均在本目录内，任何人可以复现和验证。

---

## 最新成绩

### SWE-bench Verified — 79.2%（396/500）

> **Agent**: wescode（基于 wesgine v1.0 引擎）
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

**对标 SWE-bench 排行榜并列第 1**——与 Claude 4.5 Opus + Sonar Foundation Agent 的 79.20% 持平，但使用的是 DeepSeek Chat（费用约为前者的 1/16）。

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
| [**SWE-bench Verified**](swebench-verified/) | 500 | Python | GitHub Issue 修复 | **79.2%** | ✅ 完成 |
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
    │ deepseek │           │  缓存)   │
    │  -chat   │           │          │
    └────┬─────┘           └──────────┘
         │
         ▼
    ┌──────────────────────────────────────┐
    │           Execution Layer            │
    │                                      │
    │  read  write  edit  exec  grep       │
    │  apply_patch  search_files  ...      │
    │          (17 个标准工具)              │
    └────┬─────────────────────────────────┘
         │
         ▼
    ┌──────────┐    ┌──────────┐
    │ Govern   │    │ Memory   │
    │ (路径安全│    │ (跨 turn │
    │  治理)   │    │  记忆)   │
    └──────────┘    └──────────┘
```

### 关键特性

| 特性 | 说明 |
|------|------|
| **通用 Agent 引擎** | wesgine 是通用引擎，不针对 SWE-bench 做特化 |
| **标准工具集** | 17 个 wesgine 内置工具（read/write/edit/exec/grep 等），无 SWE-bench 专用工具 |
| **Write Verification Nudge** | 引擎检测"写了文件但没验证"时自动 nudge（ADR-311） |
| **Patch 采集** | 临时索引捕获新增/修改/删除文件，`git apply --check` 验证 |
| **Cell 隔离** | 每个 bench case 在独立的 Cell 中运行，物理隔离 |

---

## 目录结构

```
benchmarks/
├── README.md                              本文件
├── .gitignore                             覆盖根目录的 *.log 排除
│
├── swebench-verified/                     SWE-bench Verified（500 题）
│   ├── README.md                          评测说明 + 历次成绩
│   └── 20261004-deepseek-chat/            2026-10-04 跑分
│       ├── README.md                      完整报告（架构+分析+复现+诚实声明）
│       ├── all_preds.jsonl                500 题 predictions（1.7MB）
│       ├── summary.json                   结果 + per-instance 成本（96KB）
│       ├── bench-report.json              bench 运行报告（392KB）
│       ├── run.json                       swebench eval 元数据
│       ├── sessions.db                    完整 session 数据库（14MB，SQLite）
│       ├── trajs/                         500 个推理轨迹（11MB）
│       ├── logs/                          499 个评分日志目录（236MB）
│       │   └── <instance_id>/
│       │       ├── report.json            评分结果
│       │       ├── patch.diff             生成的补丁
│       │       ├── test_output.txt        测试输出
│       │       └── run_instance.log       运行日志
│       └── submission/
│           └── metadata.yaml              SWE-bench 提交格式
│
├── swebench-multilingual/                 ⏳ 计划中
├── terminal-bench/                        ⏳ 计划中
└── aider-polyglot/                        ⏳ 计划中
```

### 命名规则

每次跑分以 `YYYYMMDD-<model>` 命名，便于追踪同一评测的多轮迭代：

```
swebench-verified/
├── 20261004-deepseek-chat/       # DeepSeek Chat Best@1
├── 20261020-deepseek-chat-3x/    # DeepSeek Chat 3-rollout（未来）
└── 20261030-claude-sonnet/       # Claude Sonnet（未来）
```

---

## 快速复现

```bash
# 1. 构建 wescode
cd backend && go build -o bin/wescode ./cmd/wescode/

# 2. 配置 DeepSeek API
cat > ~/.config/wescode/config.yaml << 'EOF'
providers:
  - name: deepseek
    type: openai_compat
    base_url: https://api.deepseek.com
    model: deepseek-chat
    api_key: <your-key>
    is_default: true
EOF

# 3. 生成 predictions（~5-7h，~¥275）
bin/wescode bench \
  --dataset tests/bench/swebench/batches \
  --runs 1 \
  --predictions predictions.jsonl

# 4. 评分（~3-4h，无 API 费用）
pip install swebench
git clone --depth 1 https://github.com/SWE-bench/swe-bench-tasks.git
swebench eval verified \
  -p predictions.jsonl \
  --run-id wescode \
  --task-repo ./swe-bench-tasks \
  -j 2
```

→ 完整复现指南见 [`swebench-verified/20261004-deepseek-chat/README.md`](swebench-verified/20261004-deepseek-chat/README.md)

---

## 诚实声明

1. **SWE-bench Verified 存在数据污染风险**——OpenAI [已建议停止使用](https://openai.com/index/why-we-no-longer-evaluate-swe-bench-verified/)，推荐 SWE-bench Pro。我们计划同时跑 Multilingual 和 Terminal-Bench 交叉验证
2. **DeepSeek Chat 的训练数据可能包含部分题目解答**——这是该基准的已知缺陷，非 wescode 特有
3. **LLM 输出有随机性**——每次运行结果可能有 ±2-3% 波动
4. **46 题 patch 格式错误是引擎层 bug**——有效题的通过率为 87.4%（396/453）
5. **所有数据完全公开**——predictions、评分日志、推理轨迹均在本目录内，任何人可以验证

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
