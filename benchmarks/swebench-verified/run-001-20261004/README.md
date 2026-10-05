# SWE-bench Verified — Run 001：74.0%（370/500）

> **日期**：2026-10-02 ~ 2026-10-04
> **Agent**：WES Code（基于 [wesgine](https://github.com/weisyn/wescode) v1.0 引擎）
> **模型**：DeepSeek Chat（deepseek-v4-flash）
> **方法**：Best@1（单次运行，无 multi-rollout / ensembling）
> **API 费用**：¥590（~$81 USD）
> **开源**：Agent 源码 [github.com/weisyn/wescode](https://github.com/weisyn/wescode)，predictions 见本目录 `all_preds.jsonl`

---

## 一、总体成绩

| 指标 | 值 | 说明 |
|------|------|------|
| **Resolved（通过）** | **370** | patch 应用成功且全部测试通过（fail_to_pass + pass_to_pass） |
| **Unresolved（未通过）** | **53** | patch 能 apply 但测试失败 |
| **Patch Error（格式错误）** | **74** | patch 无法 apply（详见§四） |
| **Empty Patch（空补丁）** | **3** | Agent 未生成任何代码修改 |
| | | |
| **总分（排行榜计分）** | **370/500 = 74.0%** | 按 SWE-bench 官方标准：Patch Error 和 Empty Patch 计为未通过 |
| **有效题通过率** | **370/423 = 87.5%** | 仅计算 patch 能 apply 的题目 |

```
Resolved:     370 (74.0%)  ████████████████████████████████████░░░░░░░░░░░░░░░
Unresolved:    53 (10.6%)  █████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░
Patch Error:   74 (14.8%)  ███████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░
Empty Patch:    3 ( 0.6%)  ░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░
```

---

## 二、排行榜定位

> 数据截至 2026-10-02，来源：[swebench.com](https://www.swebench.com/)

| 排名 | 模型 | Agent | 公司 | 分数 | 方法 | 费用估算 |
|------|------|-------|------|------|------|---------|
| 1 | Claude 4.5 Opus | Sonar Foundation Agent | SonarSource 🇨🇭 | 79.20% | Best@1 | ~$630 |
| 2 | Claude 4.5 Opus | live-SWE-agent | UIUC 🇺🇸 | 79.20% | Best@1 | ~$630 |
| 3 | Doubao-Seed-Code | TRAE | 字节跳动 🇨🇳 | 78.80% | 30x rollout | ~$2,000+ |
| 4 | Gemini 3 Pro | live-SWE-agent | Google 🇺🇸 | 77.40% | Best@1 | — |
| 5 | Claude 4 Sonnet | EPAM AI/Run | EPAM 🇺🇸 | 76.80% | Best@1 | — |
| … | … | … | … | … | … | … |
| **~8** | **DeepSeek Chat** | **WES Code** | **微迅互联 🇨🇳** | **74.0%** | **Best@1** | **¥590** |

### 性价比对比

WES Code 使用 DeepSeek Chat（国产模型，API 费率远低于 Claude/GPT），在 Best@1 条件下达到了与 Claude Opus + 专业 Agent 相当的成绩。

| Agent | 模型 | 分数 | 费用 | 性价比（分/元） |
|-------|------|------|------|---------------|
| **WES Code** | **DeepSeek Chat** | **74.0%** | **¥590** | **0.125** |
| Sonar | Claude 4.5 Opus | 79.2% | ~¥4,500 | 0.018 |
| TRAE | Doubao（30x rollout） | 78.8% | ~¥15,000+ | <0.005 |

---

## 三、技术方案

### 3.1 Agent 架构

WES Code 是基于 [wesgine](https://github.com/weisyn/wescode) v1.0 引擎的 AI 编程工具，以开源 VS Code 为基座构建。在 SWE-bench 评测中，使用 `wescode bench` 命令以 headless 模式运行。

```
wescode bench（headless CLI）
    │
    ├── Agent Loop（wesgine v1.0 引擎）
    │   ├── 认知层（Cognitive）：模型调用 + 流式响应
    │   ├── 上下文层（Context）：代码理解 + 文件读取
    │   ├── 执行层（Execution）：工具调用 + 文件编辑
    │   └── 治理层（Governance）：安全边界 + 路径控制
    │
    ├── 内置工具集
    │   ├── read        —— 读取文件内容
    │   ├── write       —— 创建/覆盖文件
    │   ├── edit        —— 精确编辑文件（string-replace）
    │   ├── apply_patch —— 应用 unified diff
    │   ├── exec        —— 执行 shell 命令
    │   ├── grep        —— 代码搜索
    │   └── search_files—— 文件搜索
    │
    └── Patch 输出：git diff → predictions.jsonl
```

### 3.2 评测配置

```yaml
model: deepseek-chat (deepseek-v4-flash)
provider: DeepSeek API (api.deepseek.com)
max_iterations: 90          # Agent 最大迭代轮次
timeout_per_instance: 600s  # 单题超时 10 分钟
method: best@1              # 单次运行
rollouts: 1                 # 不做 multi-rollout
ensembling: none            # 不做 ensembling
thinking_level: none        # 不使用 extended thinking
```

### 3.3 关键设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| 模型 | DeepSeek Chat（非 Claude/GPT） | 性价比优先；验证国产模型在 SWE-bench 上的能力 |
| 方法 | Best@1（非 multi-rollout） | 反映真实用户场景：用户发一个请求，期望一次解决 |
| Ensembling | 不使用 | 行业实践表明 ensembling 提升 3-8pp 但费用翻倍，真实场景不可用 |
| 检索 | 无 embedding RAG | Agent 通过 `grep`/`search_files` 导航代码库，与 [Augment 的发现](https://www.augmentcode.com/blog/1-open-source-agent-on-swe-bench-verified-by-combining-claude-3-7-and-o1)一致 |
| 文件编辑 | `edit`（string-replace）为主 | 比 whole-file rewrite 精确，token 消耗低 |

### 3.4 与其他 Agent 方案的对比

| 特征 | WES Code | [Augment SWE-bench Agent](https://www.augmentcode.com/blog/1-open-source-agent-on-swe-bench-verified-by-combining-claude-3-7-and-o1) | [SWE-agent](https://github.com/SWE-bench/SWE-agent) |
|------|----------|------|------|
| 核心模型 | DeepSeek Chat | Claude 3.7 Sonnet | Claude 4.5 Opus |
| Ensembling | 无 | o1 majority vote（5 rollout） | 无 |
| 规划工具 | 无额外规划 | Sequential Thinking MCP | 无 |
| 代码导航 | grep + search_files | grep + find | 自建搜索 |
| 文件编辑 | string-replace edit | string-replace | string-replace |
| 分数 | 74.0%（Best@1） | 65.4%（5x + o1 vote） | 79.2%（Best@1，Opus） |
| 费用 | ¥590 | 未公开 | ~$630 |

---

## 四、失败分析

### 4.1 Patch Error（74 题，14.8%）——最大的提分空间

74 题的 patch 无法被 `git apply` 应用到目标仓库，**这是引擎层面的技术问题，不是模型能力问题**。

**根因分类**：

| 类型 | 数量 | 说明 |
|------|------|------|
| Bug A：model_patch 是文字报告 | 10 | `subcmd_bench.go` 在 diff 为空时错误地将模型最终文本响应当作 patch |
| Bug B：diff 格式不标准 | 64 | 行尾换行符处理不一致、多文件 diff 拼接问题、上下文行偏移 |

**按仓库分布**：

| 仓库 | Patch Error | 占比 | 仓库 | Patch Error | 占比 |
|------|-------------|------|------|-------------|------|
| django | 34 | 14.7% | pylint | 4 | **40.0%** |
| sphinx | 8 | 18.2% | scikit-learn | 4 | 12.5% |
| pytest | 7 | **36.8%** | astropy | 4 | 18.2% |
| sympy | 5 | 6.7% | xarray | 3 | 13.6% |
| matplotlib | 5 | 14.7% | | | |

**修复后预期**：假设 74 题中 65% 能通过（与已通过题的比例一致），分数可提升至 **370 + 48 = 418/500 = 83.6%**。

### 4.2 Unresolved（53 题，10.6%）——模型/Agent 能力边界

patch 格式正确但测试未通过，说明**修复方案本身有误**。这是模型能力和 Agent 策略的边界。

**按仓库分布**：django(18), sphinx(10), matplotlib(9), sympy(7), pylint(3), requests(3), scikit-learn(2), astropy(1)

### 4.3 Empty Patch（3 题，0.6%）

3 题全部来自 sympy（sympy-24539, sympy-24562, sympy-24661），Agent 在规定时间内未能生成有效修改。

---

## 五、按仓库详细成绩

| 仓库 | 总题数 | Resolved | Unresolved | Patch Error | Empty | 有效通过率 | 总通过率 |
|------|--------|----------|------------|-------------|-------|-----------|---------|
| **django** | 231 | 179 | 18 | 34 | 0 | 91.0% | **77.5%** |
| **sympy** | 75 | 60 | 7 | 5 | 3 | 89.6% | **80.0%** |
| **sphinx** | 44 | 26 | 10 | 8 | 0 | 72.2% | **59.1%** |
| **matplotlib** | 34 | 20 | 9 | 5 | 0 | 69.0% | **58.8%** |
| **scikit-learn** | 32 | 26 | 2 | 4 | 0 | 92.9% | **81.3%** |
| **astropy** | 22 | 17 | 1 | 4 | 0 | 94.4% | **77.3%** |
| **xarray** | 22 | 19 | 0 | 3 | 0 | 100.0% | **86.4%** |
| **pytest** | 19 | 12 | 0 | 7 | 0 | 100.0% | **63.2%** |
| **pylint** | 10 | 3 | 3 | 4 | 0 | 50.0% | **30.0%** |
| **requests** | 8 | 5 | 3 | 0 | 0 | 62.5% | **62.5%** |
| **seaborn** | 2 | 2 | 0 | 0 | 0 | 100.0% | **100%** |
| **flask** | 1 | 1 | 0 | 0 | 0 | 100.0% | **100%** |

**亮点**：
- xarray（86.4%）、scikit-learn（81.3%）、sympy（80.0%）：WES Code 对数据科学和数学库表现突出
- django（77.5%）：最大仓库（231 题），高通过率说明 Python Web 框架修复能力强
- pytest、xarray 有效通过率均为 100%——所有能正确生成 patch 的题目全部通过

**薄弱点**：
- pylint（30.0%）：AST 遍历逻辑复杂，patch 格式错误率高达 40%
- sphinx（59.1%）、matplotlib（58.8%）：涉及 RST 解析、C 扩展和复杂渲染逻辑

---

## 六、评测环境与流程

### 6.1 环境

| 组件 | 配置 |
|------|------|
| **Agent 运行环境** | macOS（Apple Silicon，本地 Mac） |
| **评分服务器** | 阿里云 ECS ecs.e-c1m4.xlarge（4 核 CPU、16 GB RAM、600 GB ESSD，广州区） |
| **Docker** | 20.10+（评分用 Docker 容器化环境） |
| **Python** | 3.12.9 |
| **swebench** | 5.0.2（官方评分 harness） |
| **评分并行度** | `-j 2` |
| **Docker 镜像** | 263 个从 Docker Hub 拉取，237 个本地 `docker buildx` 构建（pip 使用清华源） |

### 6.2 流程

```
步骤 1: wescode bench 生成 predictions
        ├── 本地 Mac 运行
        ├── 分 8 个批次执行（每批 ~62 题）
        ├── 调用 DeepSeek Chat API
        ├── 每题限时 600 秒
        └── 输出: all_preds.jsonl（500 行，每行一个 prediction）

步骤 2: 上传 predictions 到评分服务器

步骤 3: swebench eval 在 Docker 中评分
        ├── 每题启动独立 Docker 容器
        ├── 应用 model_patch → 运行 fail_to_pass 测试 → 运行 pass_to_pass 测试
        └── 输出: report.json × 500（每题一份）

步骤 4: 汇总结果
        └── resolved: 370 / not_resolved: 53 / patch_error: 74 / empty: 3
```

### 6.3 耗时与费用

| 阶段 | 耗时 | 费用 |
|------|------|------|
| Predictions 生成（本地 Mac） | ~10 小时 | ¥590（DeepSeek API） |
| Docker 镜像构建（服务器） | ~24 小时 | ~¥30（阿里云 ECS） |
| 评分（服务器） | ~6 小时 | 含在 ECS 费用中 |
| **总计** | **~40 小时（跨 3 天）** | **~¥620** |

### 6.4 Predictions 格式

符合 [SWE-bench 官方要求](https://github.com/SWE-bench/SWE-bench/blob/main/docs/guides/evaluation.md)的 JSONL 格式：

```json
{
  "instance_id": "mwaskom__seaborn-3069",
  "model_name_or_path": "wescode-deepseek-chat",
  "model_patch": "diff --git a/seaborn/_core/plot.py b/seaborn/_core/plot.py\nindex 4f0290a..69298f4 100644\n--- a/seaborn/_core/plot.py\n+++ b/seaborn/_core/plot.py\n@@ -1631,6 +1631,7 @@ class Plotter:\n..."
}
```

---

## 七、提分路径

| 阶段 | 行动 | 预期分数 | 费用 |
|------|------|---------|------|
| **当前** | Run 001 | 74.0% | ¥590 |
| **短期**（修 patch 格式） | 修复 `subcmd_bench.go` Bug A + B → 重跑 | **~84%** | ~¥590 |
| **中期**（multi-rollout） | DeepSeek Chat × 3 rollout | **~87%** | ~¥1,800 |
| **长期**（换模型） | Claude Sonnet 4 × Best@1 | **~88-90%** | ~¥5,000 |

---

## 八、数据文件清单

| 文件 | 说明 | 大小 | 格式 |
|------|------|------|------|
| [`all_preds.jsonl`](all_preds.jsonl) | 500 题 predictions | 1.7 MB | JSONL（SWE-bench 官方格式） |
| [`summary.json`](summary.json) | 机器可读结果摘要 | 3 KB | JSON |
| [`metadata.yaml`](metadata.yaml) | SWE-bench 排行榜提交元数据 | 0.4 KB | YAML |
| 评分日志 | swebench harness 输出（report.json × 500） | ~50 MB | 通过 GitHub Release 提供 |

---

## 九、已知限制与诚实声明

1. **SWE-bench Verified 数据污染问题**：OpenAI 于 2025 年[声明 SWE-bench Verified 存在严重数据污染](https://openai.com/index/why-we-no-longer-evaluate-swe-bench-verified/)，建议改用 SWE-bench Pro。我们计划后续补充 SWE-bench Multilingual 和 Terminal-Bench 2.1 评测作为交叉验证。
2. **Patch 格式问题是引擎 bug**：74 题（14.8%）的 Patch Error 源于 WES Code bench 模式的 patch 捕获逻辑缺陷，而非模型能力不足。修复后预期可大幅提分。
3. **仅覆盖 Python**：SWE-bench Verified 只包含 Python 仓库。WES Code 的 CKG 支持 12 种语言，但本次评测无法体现跨语言能力。
4. **单一模型**：本次仅使用 DeepSeek Chat。不同模型（Claude、GPT 等）的表现可能显著不同。
5. **无 Extended Thinking**：DeepSeek Chat 本次未启用 thinking 模式，启用后可能有提升（也可能不会，Augment [发现 Sonnet 3.7 的 thinking 模式在 SWE-bench 上无效](https://www.augmentcode.com/blog/1-open-source-agent-on-swe-bench-verified-by-combining-claude-3-7-and-o1)）。

---

## 十、复现步骤

### 10.1 从源码构建 WES Code

```bash
git clone https://github.com/weisyn/wescode.git
cd wescode/backend
go build -o bin/wescode ./cmd/wescode
```

### 10.2 配置模型

在 `~/.config/wescode/config.yaml` 中配置 DeepSeek API：

```yaml
providers:
  - name: deepseek
    type: openai
    base_url: https://api.deepseek.com
    models:
      - name: deepseek-chat
        context_window: 64000
    api_key_ref:
      source: env
      key: DEEPSEEK_API_KEY
```

### 10.3 运行评测

```bash
# 生成 predictions（需要 DeepSeek API key）
export DEEPSEEK_API_KEY=your-key-here
bin/wescode bench \
  --dataset swebench-verified \
  --runs 1 \
  --verbose \
  --predictions ./all_preds.jsonl

# 或：使用本目录提供的 predictions 直接评分
```

### 10.4 评分

```bash
# 安装 SWE-bench 评分 harness
pip install swebench

# 评分（需要 Docker + 大量磁盘空间，首次需构建镜像）
swebench eval verified \
  --predictions ./all_preds.jsonl \
  --run-id wescode-run-001 \
  -j 2

# 查看报告
swebench report wescode-run-001 -d verified
```

---

## 附录 A：未通过题目完整列表

<details>
<summary>53 道 Unresolved 题目（patch 正确但测试失败）</summary>

| # | instance_id | 仓库 |
|---|-------------|------|
| 1 | astropy__astropy-13033 | astropy |
| 2 | django__django-10097 | django |
| 3 | django__django-11141 | django |
| 4 | django__django-11749 | django |
| 5 | django__django-13417 | django |
| 6 | django__django-13449 | django |
| 7 | django__django-13512 | django |
| 8 | django__django-13516 | django |
| 9 | django__django-13837 | django |
| 10 | django__django-14017 | django |
| 11 | django__django-14155 | django |
| 12 | django__django-14170 | django |
| 13 | django__django-14311 | django |
| 14 | django__django-14315 | django |
| 15 | django__django-14376 | django |
| 16 | django__django-14534 | django |
| 17 | django__django-15252 | django |
| 18 | django__django-15525 | django |
| 19 | django__django-16454 | django |
| 20 | matplotlib__matplotlib-14623 | matplotlib |
| 21 | matplotlib__matplotlib-20488 | matplotlib |
| 22 | matplotlib__matplotlib-20676 | matplotlib |
| 23 | matplotlib__matplotlib-20826 | matplotlib |
| 24 | matplotlib__matplotlib-21568 | matplotlib |
| 25 | matplotlib__matplotlib-22871 | matplotlib |
| 26 | matplotlib__matplotlib-24627 | matplotlib |
| 27 | matplotlib__matplotlib-24870 | matplotlib |
| 28 | matplotlib__matplotlib-25960 | matplotlib |
| 29 | psf__requests-1724 | requests |
| 30 | psf__requests-2317 | requests |
| 31 | psf__requests-2931 | requests |
| 32 | pylint-dev__pylint-4970 | pylint |
| 33 | pylint-dev__pylint-6528 | pylint |
| 34 | pylint-dev__pylint-7277 | pylint |
| 35 | scikit-learn__scikit-learn-14087 | scikit-learn |
| 36 | scikit-learn__scikit-learn-25747 | scikit-learn |
| 37 | sphinx-doc__sphinx-10435 | sphinx |
| 38 | sphinx-doc__sphinx-10466 | sphinx |
| 39 | sphinx-doc__sphinx-10614 | sphinx |
| 40 | sphinx-doc__sphinx-11445 | sphinx |
| 41 | sphinx-doc__sphinx-11510 | sphinx |
| 42 | sphinx-doc__sphinx-7454 | sphinx |
| 43 | sphinx-doc__sphinx-7462 | sphinx |
| 44 | sphinx-doc__sphinx-7590 | sphinx |
| 45 | sphinx-doc__sphinx-7985 | sphinx |
| 46 | sphinx-doc__sphinx-8475 | sphinx |
| 47 | sympy__sympy-12489 | sympy |
| 48 | sympy__sympy-13878 | sympy |
| 49 | sympy__sympy-13974 | sympy |
| 50 | sympy__sympy-15017 | sympy |
| 51 | sympy__sympy-15345 | sympy |
| 52 | sympy__sympy-16597 | sympy |
| 53 | sympy__sympy-18199 | sympy |

</details>

<details>
<summary>74 道 Patch Error 题目（patch 无法 apply）</summary>

| # | instance_id | 仓库 | # | instance_id | 仓库 |
|---|-------------|------|---|-------------|------|
| 1 | django__django-11066 | django | 38 | astropy__astropy-7336 | astropy |
| 2 | django__django-11333 | django | 39 | matplotlib__matplotlib-23299 | matplotlib |
| 3 | django__django-11400 | django | 40 | matplotlib__matplotlib-24149 | matplotlib |
| 4 | django__django-11451 | django | 41 | matplotlib__matplotlib-24177 | matplotlib |
| 5 | django__django-11477 | django | 42 | matplotlib__matplotlib-25479 | matplotlib |
| 6 | django__django-11555 | django | 43 | matplotlib__matplotlib-26342 | matplotlib |
| 7 | django__django-11885 | django | 44 | scikit-learn__scikit-learn-13328 | scikit-learn |
| 8 | django__django-12050 | django | 45 | scikit-learn__scikit-learn-14894 | scikit-learn |
| 9 | django__django-12273 | django | 46 | scikit-learn__scikit-learn-25102 | scikit-learn |
| 10 | django__django-12276 | django | 47 | scikit-learn__scikit-learn-25973 | scikit-learn |
| 11 | django__django-12754 | django | 48 | sphinx-doc__sphinx-10673 | sphinx |
| 12 | django__django-13012 | django | 49 | sphinx-doc__sphinx-8035 | sphinx |
| 13 | django__django-13033 | django | 50 | sphinx-doc__sphinx-8120 | sphinx |
| 14 | django__django-13212 | django | 51 | sphinx-doc__sphinx-8269 | sphinx |
| 15 | django__django-13297 | django | 52 | sphinx-doc__sphinx-9229 | sphinx |
| 16 | django__django-13513 | django | 53 | sphinx-doc__sphinx-9320 | sphinx |
| 17 | django__django-13569 | django | 54 | sphinx-doc__sphinx-9461 | sphinx |
| 18 | django__django-13670 | django | 55 | sphinx-doc__sphinx-9658 | sphinx |
| 19 | django__django-13794 | django | 56 | sympy__sympy-13031 | sympy |
| 20 | django__django-13933 | django | 57 | sympy__sympy-14248 | sympy |
| 21 | django__django-14007 | django | 58 | sympy__sympy-15976 | sympy |
| 22 | django__django-14034 | django | 59 | sympy__sympy-17630 | sympy |
| 23 | django__django-14238 | django | 60 | sympy__sympy-18698 | sympy |
| 24 | django__django-15127 | django | 61 | pydata__xarray-3677 | xarray |
| 25 | django__django-15280 | django | 62 | pydata__xarray-4966 | xarray |
| 26 | django__django-15563 | django | 63 | pydata__xarray-7393 | xarray |
| 27 | django__django-15957 | django | 64 | pylint-dev__pylint-4551 | pylint |
| 28 | django__django-16315 | django | 65 | pylint-dev__pylint-4604 | pylint |
| 29 | django__django-16429 | django | 66 | pylint-dev__pylint-6386 | pylint |
| 30 | django__django-16631 | django | 67 | pylint-dev__pylint-6903 | pylint |
| 31 | django__django-16819 | django | 68 | pytest-dev__pytest-10356 | pytest |
| 32 | django__django-16877 | django | 69 | pytest-dev__pytest-5787 | pytest |
| 33 | django__django-16950 | django | 70 | pytest-dev__pytest-5840 | pytest |
| 34 | django__django-17084 | django | 71 | pytest-dev__pytest-6197 | pytest |
| 35 | astropy__astropy-14096 | astropy | 72 | pytest-dev__pytest-7205 | pytest |
| 36 | astropy__astropy-14309 | astropy | 73 | pytest-dev__pytest-7982 | pytest |
| 37 | astropy__astropy-14995 | astropy | 74 | pytest-dev__pytest-8399 | pytest |

</details>

<details>
<summary>3 道 Empty Patch 题目（Agent 未产出修改）</summary>

| # | instance_id | 仓库 |
|---|-------------|------|
| 1 | sympy__sympy-24539 | sympy |
| 2 | sympy__sympy-24562 | sympy |
| 3 | sympy__sympy-24661 | sympy |

</details>

---

## 附录 B：引用

如果您使用了本评测数据，请引用：

```bibtex
@misc{wescode2026swebench,
  title={WES Code: AI Coding Agent Achieving 74.0\% on SWE-bench Verified with DeepSeek Chat},
  author={Weisyn Team},
  year={2026},
  url={https://github.com/weisyn/wescode/tree/main/benchmarks/swebench-verified/run-001-20261004}
}
```

SWE-bench 原始论文：

```bibtex
@inproceedings{jimenez2024swebench,
  title={{SWE}-bench: Can Language Models Resolve Real-world Github Issues?},
  author={Carlos E Jimenez and John Yang and Alexander Wettig and Shunyu Yao and Kexin Pei and Ofir Press and Karthik R Narasimhan},
  booktitle={The Twelfth International Conference on Learning Representations},
  year={2024},
  url={https://openreview.net/forum?id=VTF8yNQM66}
}
```
