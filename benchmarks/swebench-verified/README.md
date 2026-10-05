# SWE-bench Verified 评测

> **评测说明**：[SWE-bench Verified](https://www.swebench.com/verified.html) 是普林斯顿大学团队发布的 500 题人工筛选子集，来自 12 个 Python 开源仓库的真实 GitHub Issue。
>
> **注意**：OpenAI 于 2025 年声明 SWE-bench Verified 存在数据污染，[建议改用 SWE-bench Pro](https://openai.com/index/why-we-no-longer-evaluate-swe-bench-verified/)。我们同时计划评测 SWE-bench Multilingual 和 Terminal-Bench 2.1 作为补充。

---

## 历次成绩

| Run | 日期 | 分数 | 模型 | 方法 | 费用 | 改进说明 |
|-----|------|------|------|------|------|---------|
| [001](run-001-20261004/) | 2026-10-04 | **74.0%**（370/500） | DeepSeek Chat | Best@1 | ¥590 | 首次完整 500 题评测 |
| _002_ | _计划中_ | _目标 ~84%_ | _DeepSeek Chat_ | _Best@1_ | — | _修复 patch 格式后重跑_ |

## 成绩趋势

```
Run 001 (2026-10-04):  74.0%  ██████████████████████████████████████░░░░░░░░░░░░
Run 002 (计划中):      ~84%?  ██████████████████████████████████████████░░░░░░░░
```

## 与排行榜对比

> 数据截至 2026-10-02，来源：[swebench.com](https://www.swebench.com/)

| 排名 | 模型 | Agent | 分数 |
|------|------|-------|------|
| 1 | Claude 4.5 Opus | Sonar Foundation Agent | 79.20% |
| 2 | Claude 4.5 Opus | live-SWE-agent | 79.20% |
| 3 | Doubao-Seed-Code | TRAE（30x rollout） | 78.80% |
| 4 | Gemini 3 Pro | live-SWE-agent | 77.40% |
| … | … | … | … |
| **~8** | **DeepSeek Chat** | **WES Code** | **74.0%** |

### 性价比对比

| Agent | 模型 | 分数 | 费用 | 分/元 |
|-------|------|------|------|-------|
| **WES Code** | **DeepSeek Chat** | **74.0%** | **¥590** | **0.125** |
| Sonar | Claude 4.5 Opus | 79.2% | ~¥4,500 | 0.018 |
| TRAE | Doubao-Seed-Code（30x） | 78.8% | ~¥15,000+ | <0.005 |

## 评分环境

| 组件 | 配置 |
|------|------|
| Agent 运行 | macOS（本地 Mac） |
| 评分服务器 | 阿里云 ECS ecs.e-c1m4.xlarge（4 核 16G） |
| Docker | 20.10+ |
| swebench | 5.0.2 |
| 评分并行度 | -j 2 |

## 复现

```bash
# 安装 WES Code
cd backend && go build -o bin/wescode ./cmd/wescode

# 生成 predictions（需要 DeepSeek API key）
bin/wescode bench \
  --dataset swebench-verified \
  --runs 1 \
  --predictions ./benchmarks/swebench-verified/run-XXX/all_preds.jsonl

# 评分（需要 Docker + 大量磁盘空间）
pip install swebench
swebench eval verified \
  --predictions ./benchmarks/swebench-verified/run-XXX/all_preds.jsonl \
  --run-id wescode-run-XXX \
  -j 2
```
