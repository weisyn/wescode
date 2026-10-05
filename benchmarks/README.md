# WES Code 评测结果

本目录包含 WES Code 在各主流 AI 编程评测上的成绩、数据和复现方法。

## 评测索引

| 评测 | 最新成绩 | 最新 Run | 模型 | 说明 |
|------|---------|---------|------|------|
| [SWE-bench Verified](swebench-verified/) | **74.0%**（370/500） | [Run 001](swebench-verified/run-001-20261004/) | DeepSeek Chat | 500 题，Best@1，¥590 |

## 目录结构

```
benchmarks/
├── README.md                            # 本文件
├── swebench-verified/                   # SWE-bench Verified（500 题）
│   ├── README.md                        # 评测说明 + 历次成绩对比
│   ├── run-001-20261004/                # 第 1 次：74.0%
│   │   ├── README.md                    # 本次 run 的完整报告
│   │   ├── metadata.yaml                # SWE-bench 官方提交格式
│   │   ├── summary.json                 # 机器可读摘要
│   │   └── all_preds.jsonl              # predictions（大文件，见说明）
│   ├── run-002-YYYYMMDD/                # 第 2 次：修复 patch 格式后
│   └── ...
├── swebench-multilingual/               # （待做）SWE-bench Multilingual
├── terminal-bench/                      # （待做）Terminal-Bench 2.1
├── aider-polyglot/                      # （待做）Aider Polyglot
└── lhtb/                                # （待做）Long-Horizon Terminal-Bench
```

## 大文件策略

- `all_preds.jsonl`（predictions）和 `logs/`（评分日志）体积较大
- 开源 repo 中只保留 `summary.json` 和 `README.md`
- 完整数据通过 [GitHub Releases](https://github.com/weisyn/wescode/releases) 附件发布
- 也可从源码仓 `backend/results/` 获取原始数据

## 复现

```bash
# 1. 构建 wescode
cd backend && go build -o bin/wescode ./cmd/wescode

# 2. 运行 SWE-bench Verified 评测（需要 LLM API key）
bin/wescode bench \
  --dataset swebench-verified \
  --runs 1 \
  --predictions ./predictions.jsonl

# 3. 使用官方 harness 评分（需要 Docker）
pip install swebench
swebench eval verified \
  --predictions predictions.jsonl \
  --run-id wescode-run-001 \
  -j 2
```

## 评测原则

1. **Best@1**：单次运行，不做 multi-rollout
2. **透明**：公开 predictions + 评分日志 + 复现步骤
3. **标准**：使用官方评分 harness，不修改评分逻辑
4. **诚实**：如实报告所有失败原因（patch error / unresolved / empty）
