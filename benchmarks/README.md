# wescode 评测结果

wescode 在主流 AI 编程评测平台上的跑分数据、分析报告和复现指南。

## 评测总览

| 评测 | 题数 | 语言 | 分数 | 模型 | 日期 | 状态 |
|------|------|------|------|------|------|------|
| [SWE-bench Verified](swebench-verified/) | 500 | Python | **79.2%** | DeepSeek Chat | 2026-10-04 | ✅ 完成 |
| [SWE-bench Multilingual](swebench-multilingual/) | 300 | 9 语言 | — | — | — | ⏳ 计划中 |
| [Terminal-Bench 2.1](terminal-bench/) | 89 | 多语言 | — | — | — | ⏳ 计划中 |
| [Aider Polyglot](aider-polyglot/) | 225 | 6 语言 | — | — | — | ⏳ 计划中 |

## 目录结构

```
benchmarks/
├── README.md                              本文件
├── swebench-verified/                     SWE-bench Verified（500 题 Python）
│   ├── README.md                          评测说明 + 历次成绩
│   └── 20261004-deepseek-chat/            第 1 次跑分
│       ├── README.md                      完整报告
│       ├── all_preds.jsonl                500 题 predictions
│       ├── summary.json                   结果 + per-instance 成本
│       ├── trajs/                         推理轨迹（500 files）
│       └── submission/                    SWE-bench 提交材料
├── swebench-multilingual/                 ⏳
├── terminal-bench/                        ⏳
└── aider-polyglot/                        ⏳
```

## 命名规则

每次跑分以 `YYYYMMDD-<model>` 命名，便于追踪同一评测的不同轮次：

```
swebench-verified/
├── 20261004-deepseek-chat/      # 第 1 次：DeepSeek Chat Best@1
├── 20261010-deepseek-chat-3x/   # 第 2 次：DeepSeek Chat 3-rollout（示例）
└── 20261020-claude-sonnet/      # 第 3 次：换 Claude Sonnet（示例）
```

## 复现

每个跑分目录下的 `README.md` 包含完整的复现步骤。通用依赖：

```bash
cd backend && go build -o bin/wescode ./cmd/wescode/
bin/wescode bench --dataset <dataset-dir> --runs 1 --predictions predictions.jsonl
```

## 注意事项

- **SWE-bench Verified 数据污染声明**：OpenAI [已建议停止使用该基准](https://openai.com/index/why-we-no-longer-evaluate-swe-bench-verified/)，推荐 SWE-bench Pro。我们同时规划 Multilingual 评测以交叉验证
- **LLM 输出随机性**：同一模型每次运行结果有 ±2-3% 波动
- **费用基于 DeepSeek 2026-10 定价**：Input ¥1/M tokens，Output ¥2/M tokens
