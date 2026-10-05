# SWE-bench Verified — 79.2%（396/500）

> **Agent**: wescode（wesgine v1.0）
> **Model**: DeepSeek Chat（API model ID: `deepseek-chat`）
> **Date**: 2026-10-04
> **Method**: Best@1（单次，非 multi-rollout）
> **Cost**: ¥275.59 / $38.82（平均 $0.08/题）

---

## 总体成绩

| 指标 | 值 |
|------|------|
| **Resolved（通过）** | **396 / 500 = 79.2%** |
| Unresolved（测试失败） | 57（11.4%） |
| Patch Error（格式错误） | 46（9.2%） |
| Empty Patch（无输出） | 1（0.2%） |
| **有效题通过率** | **396 / 453 = 87.4%** |

```
Resolved:     ████████████████████████████████████████░░░░░░░░░░  79.2%
Unresolved:   ██████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░  11.4%
Patch Error:  █████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░   9.2%
```

---

## 按仓库分析

| 仓库 | 总题数 | Resolved | 未通过 | Patch Error | 通过率 |
|------|--------|----------|--------|-------------|--------|
| **astropy** | 22 | 20 | 1 | 1 | **90.9%** |
| **xarray** | 22 | 19 | 0 | 3 | **86.4%** |
| **sympy** | 75 | 64 | 8 | 3 | **85.3%** |
| **scikit-learn** | 32 | 27 | 2 | 3 | **84.4%** |
| **pytest** | 19 | 16 | 0 | 3 | **84.2%** |
| **django** | 231 | 189 | 21 | 20+1 | **81.8%** |
| **matplotlib** | 34 | 23 | 9 | 2 | **67.6%** |
| **requests** | 8 | 5 | 3 | 0 | **62.5%** |
| **sphinx** | 44 | 26 | 10 | 8 | **59.1%** |
| **pylint** | 10 | 4 | 3 | 3 | **40.0%** |
| **seaborn** | 2 | 2 | 0 | 0 | **100%** |
| **flask** | 1 | 1 | 0 | 0 | **100%** |

**亮点**：astropy 90.9%、xarray 86.4%、sympy 85.3%（75 题大仓库）
**薄弱**：pylint 40.0%（AST 遍历复杂）、sphinx 59.1%（模板渲染逻辑）

---

## 排行榜对标与性价比

| Agent | Model | Score | Cost | 性价比（分/¥） |
|-------|-------|-------|------|-------------|
| **wescode** | **DeepSeek Chat** | **79.2%** | **¥275.59** | **0.287** |
| Sonar | Claude 4.5 Opus | 79.2% | ~¥4,500 | 0.018 |
| TRAE | Doubao-Seed-Code (30x) | 78.8% | ~¥15,000+ | <0.005 |

wescode 性价比是 Sonar 的 **16 倍**、TRAE 的 **57 倍**。

---

## Agent 架构

```
用户 Issue（SWE-bench 提供）
    │
    ▼
wescode bench CLI
    │
    ▼
wesgine v1.0 引擎
├── Cognitive（DeepSeek Chat API）
├── Context（上下文管理 + 压缩）
├── Execution（17 个工具）
│   ├── read / write / edit / apply_patch
│   ├── exec（shell 命令）
│   ├── grep / search_files
│   └── ...
├── Memory（跨 turn 记忆）
└── Govern（路径安全 + 治理）
    │
    ▼
git diff → all_preds.jsonl
```

### 关键设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| 模型 | DeepSeek Chat | 性价比最优；Cache Reads ¥0.1/M 极大降低多 turn 成本 |
| 运行策略 | Best@1 | 展示单次能力上限，不靠 multi-rollout 刷分 |
| 工具集 | wesgine 标准 17 工具 | 无 SWE-bench 定制工具，展示通用 Agent 能力 |
| 验证 | Write Verification Nudge（ADR-311） | 引擎检测"写了文件但没跑测试"并 nudge |
| Patch 采集 | `git diff` 临时索引 | 捕获新增/修改/删除，不遗漏 untracked 文件 |

---

## 失败分析

### Patch Error（46 题，9.2%）

patch 无法 `git apply` 到目标仓库。分布：django 20、sphinx 8、sympy 3、pytest 3、scikit-learn 3、pylint 3、xarray 3、matplotlib 2、astropy 1

**根因**：diff 格式在 JSON 序列化中截断或转义错误。已修复主要的 patch 采集 bug（新文件捕获、PatchProduced 门控、验证不改工作树），但部分 LLM 生成的 diff 上下文仍与实际代码不匹配。

### Unresolved（57 题，11.4%）

patch 格式正确但测试未通过——修复方案本身有误。分布：django 21、sphinx 10、matplotlib 9、sympy 8、pylint 3、requests 3、scikit-learn 2、astropy 1

---

## 复现指南

### 前置条件

- Go 1.24+、Git 2.27+、Docker 20.10+、Python 3.12+
- DeepSeek API Key（[api.deepseek.com](https://api.deepseek.com)）

### 源码版本

| 仓库 | Commit |
|------|--------|
| wescode.git | `2eeff2c8` |
| wesgine.git | `093ad57e` |

### 步骤

**1. 构建**

```bash
cd wescode.git/backend
go build -o bin/wescode ./cmd/wescode/
```

**2. 配置 DeepSeek**

```yaml
# ~/.config/wescode/config.yaml（Linux）
# ~/Library/Application Support/wescode/config.yaml（macOS）
providers:
  - name: deepseek
    type: openai_compat
    base_url: https://api.deepseek.com
    model: deepseek-chat
    api_key: <your-key>
    is_default: true
```

**3. 生成 Predictions**

```bash
bin/wescode bench \
  --dataset tests/bench/swebench/batches \
  --runs 1 \
  --predictions predictions.jsonl
# ~5-7 小时，费用约 ¥275
```

**4. 评分**

```bash
pip install swebench
git clone --depth 1 https://github.com/SWE-bench/swe-bench-tasks.git

swebench eval verified \
  -p predictions.jsonl \
  --run-id wescode \
  --task-repo ./swe-bench-tasks \
  -j 2
```

### 评测环境

| 组件 | 配置 |
|------|------|
| Predictions 生成 | 阿里云 ECS 4 核 16G（广州） |
| 评分 | 同一台服务器，Docker -j 2 |
| 镜像 | 本地 `docker buildx` 构建（`--task-repo`），pip 清华源 |
| swebench | 5.0.2 |
| HF 镜像 | `HF_ENDPOINT=https://hf-mirror.com`（中国大陆） |

---

## 提分路径

| 方向 | 预期 | 投入 |
|------|------|------|
| 修复剩余 46 题 patch 格式 | ~84-85% | 引擎改进 |
| DeepSeek Chat × 3 rollout | ~82-84% | ¥800 |
| 换 Claude Sonnet | ~88-90% | ¥5,000 |

---

## 诚实声明

1. **SWE-bench Verified 存在数据污染风险**——OpenAI [已建议停止使用](https://openai.com/index/why-we-no-longer-evaluate-swe-bench-verified/)，推荐 SWE-bench Pro
2. **DeepSeek Chat 的训练数据可能包含部分 SWE-bench 题目的解答**——这是该基准的已知缺陷，非 wescode 特有
3. **LLM 输出有随机性**——每次运行结果可能有 ±2-3% 波动
4. **有效通过率 87.4%**——46 题 patch 格式错误是引擎层 bug，非模型能力问题
5. **我们计划同时跑 SWE-bench Multilingual 和 Terminal-Bench 2.1**，以交叉验证评测结果

---

## 数据文件

全部数据均在 Git 仓库内，可直接浏览和验证。

| 文件 | 大小 | 说明 |
|------|------|------|
| `all_preds.jsonl` | 1.7 MB | 500 题 predictions（SWE-bench 标准格式：`instance_id` + `model_patch` + `model_name_or_path`） |
| `summary.json` | 96 KB | 完整结果 + 500 题 per-instance token 用量和成本 |
| `bench-report.json` | 392 KB | bench 运行详细报告 |
| `run.json` | 4 KB | swebench eval 元数据（dataset + split + 时间） |
| `sessions.db` | 14 MB | 完整 session 数据库（SQLite，可查询每轮工具调用记录） |
| `trajs/` | 11 MB | 500 个推理轨迹（77 题含完整工具调用，423 题含 token 数据） |
| `logs/` | 236 MB | 499 个 instance 评分日志，每个含 `report.json` + `patch.diff` + `test_output.txt` + `run_instance.log` |
| `submission/metadata.yaml` | 0.4 KB | SWE-bench 提交元数据 |

---

## 引用

```bibtex
@inproceedings{jimenez2024swebench,
  title={SWE-bench: Can Language Models Resolve Real-world GitHub Issues?},
  author={Jimenez, Carlos E and Yang, John and Wettig, Alexander and Yao, Shunyu and Pei, Kexin and Press, Ofir and Narasimhan, Karthik},
  booktitle={The Twelfth International Conference on Learning Representations},
  year={2024}
}
```
