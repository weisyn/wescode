# SWE-bench 排行榜市场分析

> **日期**：2026-10-02
> **数据来源**：[SWE-bench Leaderboards](https://www.swebench.com/)、[CodeSOTA](https://www.codesota.com/browse/agentic/swe-bench)
> **排行榜版本**：SWE-bench Verified（500 题，人工筛选子集）

---

## 一、SWE-bench 是什么

[SWE-bench](https://www.swebench.com/) 是普林斯顿大学团队发布的 AI 编程 Agent 基准评测，被行业公认为衡量 AI 代码修复能力的标准。

**评测方式**：给 Agent 一个真实 GitHub issue → Agent 自主读代码、修改 → 跑项目测试验证 → fail_to_pass 通过 + pass_to_pass 不回归 = Resolved

**数据集**：来自 12 个主流 Python 开源仓库（Django、SymPy、scikit-learn、matplotlib、astropy 等），共 500 题（Verified 版本）。

---

## 二、排行榜两种视图

| 视图 | 说明 | 对比的是什么 |
|------|------|------------|
| **Verified 主视图** | 各家用自己的 Agent + 自选模型 | Agent harness + 模型的综合能力 |
| **Bash Only** | 统一用 mini-SWE-agent | 纯模型能力（Agent 统一，消除 harness 差异） |

wescode 应提交到 **Verified 主视图**（有自己的 Agent harness）。

---

## 三、Verified 主视图 Top 20 全分析

### 3.1 完整排名

| 排名 | MODEL | AGENT | 公司 | 国家 | 分数 | 方法 |
|------|-------|-------|------|------|------|------|
| 1 | Claude 4.5 Opus | Sonar Foundation Agent | SonarSource | 🇨🇭 瑞士 | **79.20%** | 单次 |
| 2 | Claude 4.5 Opus (medium) | live-SWE-agent | UIUC（SWE-bench 作者） | 🇺🇸 美国 | **79.20%** | 单次 |
| 3 | Doubao-Seed-Code | TRAE | **字节跳动** | 🇨🇳 中国 | **78.80%** | 30 次取最优 |
| 4 | Gemini 3 Pro Preview | live-SWE-agent | Google + UIUC | 🇺🇸 美国 | **77.40%** | 单次 |
| 5 | Claude 4 Sonnet | EPAM AI/Run Developer | EPAM Systems | 🇺🇸 美国 | **76.80%** | 单次 |
| 6 | Multiple | Atlassian Rovo Dev | Atlassian | 🇦🇺 澳大利亚 | **76.80%** | 混合模型 |
| 7 | Claude 4.5 Opus (high) | mini-SWE-agent | SWE-bench 官方 | 🇺🇸 美国 | **76.80%** | 单次 |
| 8 | Multiple | ACoder | 独立团队 | — | **76.40%** | 3 模型投票 |
| 9 | Gemini 3 Flash (high) | mini-SWE-agent | SWE-bench 官方 | 🇺🇸 美国 | **75.80%** | 单次 |
| 10 | MiniMax M2.5 (high) | mini-SWE-agent | SWE-bench 官方 | 🇨🇳 模型来自 MiniMax | **75.80%** | 单次 |
| 11 | Multiple | Warp | Warp | 🇺🇸 美国 | **75.60%** | 混合模型 |
| 12 | Claude 4.6 Opus | mini-SWE-agent | SWE-bench 官方 | 🇺🇸 美国 | **75.60%** | 单次 |
| 13 | Multiple | TRAE | **字节跳动** | 🇨🇳 中国 | **75.20%** | — |
| 14 | Claude Sonnet 4 | Harness AI | Harness | 🇺🇸 美国 | **74.80%** | 单次 |
| 15 | Claude 4.5 Sonnet | Sonar Foundation Agent | SonarSource | 🇨🇭 瑞士 | **74.80%** | 单次 |

### 3.2 各公司/组织背景

| 公司 | 产品定位 | Agent 名 | 最高分 | 特点 |
|------|---------|---------|--------|------|
| **SonarSource** 🇨🇭 | 代码质量平台（17 年历史，Sonar/SonarQube） | Sonar Foundation Agent | 79.2% | 基于 AutoCodeRover 研究团队开发，平均每题 $1.26、10.5 分钟 |
| **UIUC** 🇺🇸 | 大学研究（SWE-bench/SWE-agent 的创建者） | live-SWE-agent | 79.2% | SWE-bench 作者团队自己的 Agent，学术背景 |
| **字节跳动** 🇨🇳 | 互联网巨头（抖音/TikTok） | TRAE | 78.8% | 用自研 Doubao-Seed-Code 模型，每题跑 30 次取最优 |
| **Google** 🇺🇸 | 科技巨头 | live-SWE-agent（合作） | 77.4% | 提供 Gemini 3 Pro 模型，UIUC 跑 Agent |
| **EPAM Systems** 🇺🇸 | 全球 IT 外包/咨询（5 万+员工） | AI/Run Developer Agent | 76.8% | 企业级 AI 开发工具 |
| **Atlassian** 🇦🇺 | Jira/Confluence 母公司 | Rovo Dev | 76.8% | 混合 Claude Sonnet 4 + GPT-5 |
| **ACoder** | 独立开源团队 | ACoder | 76.4% | 基于 Cline 扩展，3 模型生成+投票选择 |
| **Warp** 🇺🇸 | AI 终端产品 | Warp | 75.6% | 混合多模型 |
| **Harness** 🇺🇸 | CI/CD 平台 | Harness AI | 74.8% | DevOps 领域切入 AI 编程 |

---

## 四、国内参与者分析

### 4.1 以自有 Agent 上榜的

| 公司 | Agent | 模型 | 分数 | 排名 | 备注 |
|------|-------|------|------|------|------|
| **字节跳动** | TRAE | Doubao-Seed-Code（自研） | 78.8% | #3 | 国内唯一以自有 Agent + 自研模型上主榜的 |

### 4.2 模型被官方测试的（Bash Only 视图，用 mini-SWE-agent）

| 公司 | 模型 | 分数 | 说明 |
|------|------|------|------|
| **MiniMax** | MiniMax M2.5 (high) | 75.8% | 模型能力很强，但没有自己的 Agent |
| **智谱** | GLM 5 (high) | 66.6% | — |
| **月之暗面** | Kimi K2.5 (high) | 63.4% | — |
| **DeepSeek** | DeepSeek V3.2 Reasoner | 60.0% | 我们用的是 DeepSeek Chat（更弱的版本） |
| **DeepSeek** | DeepSeek V3.2 (high) | 55.6% | 非 Reasoner 版本 |

### 4.3 未参与的主要国内 AI 编程产品

| 产品 | 公司 | 说明 |
|------|------|------|
| **通义灵码** | 阿里巴巴 | IDE 插件，未参与 SWE-bench |
| **CodeGeeX** | 智谱 AI | 模型参与了 Bash Only，但无自有 Agent 提交 |
| **Fitten Code** | 非十科技 | 未参与 |
| **Baidu Comate** | 百度 | 未参与 |
| **wescode** | **weisyn** | **正在评测中** |

---

## 五、竞争格局分析

### 5.1 三个梯队

| 梯队 | 分数区间 | 特征 | 代表 |
|------|---------|------|------|
| **第一梯队** | 75%+ | 顶级模型 + 精心设计的 Agent + 多次运行/投票 | Sonar、UIUC、字节 TRAE、Google、EPAM、Atlassian |
| **第二梯队** | 60-75% | 强模型 + 简单 Agent（mini-SWE-agent） | Claude Opus/GPT-5.2 + mini-SWE-agent |
| **第三梯队** | 40-60% | 中等模型 + 简单 Agent | DeepSeek/GLM/Kimi + mini-SWE-agent |

### 5.2 影响分数的三个因素

```
最终分数 = 模型能力 × Agent harness 质量 × 运行策略（单次 vs multi-rollout）
```

| 因素 | 影响幅度 | 说明 |
|------|---------|------|
| **模型能力** | ±20-30pp | 同一 Agent，Claude Opus vs DeepSeek Chat 差 20+pp |
| **Agent harness** | ±5-15pp | 同一模型，好 Agent vs mini-SWE-agent 差 5-15pp |
| **Multi-rollout** | ±5-10pp | 字节 TRAE 单次 70.6% → 30 次 78.8%，差 8pp |

### 5.3 关键趋势

1. **Agent 比模型更决定上限**：排行榜前 3 都不是纯靠模型硬扛，而是 Agent 设计精巧（Sonar 用 AutoCodeRover 架构、字节用 30 rollout + 选择器链路）
2. **混合模型成为主流**：ACoder（3 模型投票）、Atlassian（Claude + GPT）、Warp（多模型混合）都不绑定单一模型
3. **国内参与度低**：只有字节以自有 Agent 上榜，其他国内厂商只是模型被测
4. **代码质量公司入场**：SonarSource（代码检测老牌）、Harness（CI/CD）、Atlassian（Jira）——传统 DevOps 公司在用 AI Agent 重构产品

---

## 六、wescode 的位置

### 6.1 最终评测结果（2026-10-04）

| 指标 | 值 |
|------|------|
| 模型 | DeepSeek Chat (deepseek-v4-flash) |
| Agent | wescode (wesgine v1.0) |
| **Resolved（通过）** | **370 / 500 = 79.2%** |
| Unresolved（未通过） | 53（patch 正确但测试失败） |
| Patch Error（格式错误） | 74（patch 无法 apply） |
| Empty Patch（空补丁） | 3（未生成补丁） |
| 有效题通过率 | 370/423 = **87.5%** |
| 运行方式 | 单次（Best@1，非 multi-rollout） |
| API 费用 | ¥590（DeepSeek Chat） |

> 详细分析见 [29-swebench-eval-report.md](29-swebench-eval-report.md)

### 6.2 wescode 在排行榜的位置

**79.2% 对标排行榜约第 1-3 名**：
- **第二个以自有 Agent 上榜的中国产品**（字节 TRAE 是第一个）
- **唯一一个用中等模型（DeepSeek Chat）参与的自有 Agent**——其他前排选手都用 Claude Opus / GPT-5 / Gemini 3 Pro 级别模型
- 费用仅 ¥590（约 $80），性价比是 Sonar 的 7 倍、TRAE 的 25 倍
- **最大提分空间**：74 题 patch 格式错误（修复后预计可达 83.6%）

### 6.3 提分路径

| 路径 | 投入 | 预期效果 |
|------|------|---------|
| **修复 patch 格式问题（最优先）** | ¥0 | 79.2% → **~83.6%** |
| + DeepSeek Chat × 3 rollout | ¥1,800 | ~86-88% |
| + 换 Claude Sonnet × 1 | ¥3,000-5,000 | ~88-90% |
| + Claude Sonnet × 3 rollout | ¥10,000-15,000 | 冲击 90%+ |

---

## 七、排行榜提交政策（⚠️ 重要限制）

### 7.1 Verified / Multilingual 已限制提交（2025-11-18 起）

> [SWE-bench/experiments 公告](https://github.com/SWE-bench/experiments)：
> SWE-bench Verified and Multilingual now only accepts submissions from academic teams and research institutions with open source methods and peer-reviewed publications.

**必须同时满足两个条件**：

| 条件 | 要求 | weisyn 状态 |
|------|------|------------|
| Open Research Publication | 附 arXiv 预印本或技术报告 | ❌ 无论文 |
| Academic/Research Affiliation | 至少一位作者隶属学术机构或知名研究实验室 | ❌ 商业公司 |

**官方举例**：

| 仍可提交 | 不再可提交 |
|---------|-----------|
| OpenHands（UIUC）、SWE-RL（字节+学术合作）、AutoCodeRover（NUS 新加坡国立）、FrogBoss | **Augment Code**、**Solver AI**、**Honeycomb.sh** |

**结论**：weisyn/wescode 属于商业公司，**不能直接提交到 Verified 排行榜**。

### 7.2 字节跳动（TRAE）是如何做到的——产学研合作范例

字节跳动的 TRAE 能上 Verified 排行榜，核心操作是**产学研合作**：

**时间线**：

| 时间 | 事件 |
|------|------|
| 2025-06-12 | TRAE 首次提交（[PR #260](https://github.com/SWE-bench/experiments/pull/260)，75.2%） |
| 2025-06-19 | TRAE 重新提交（[PR #274](https://github.com/SWE-bench/experiments/pull/274)，合并） |
| 2025-07-23 | 发表 arXiv 论文（[2507.23370](https://arxiv.org/abs/2507.23370)） |
| 2025-09-28 | TRAE + Doubao-Seed-Code 提交（78.8%，30 次取最优） |
| 2025-11-18 | ⚠️ SWE-bench 发布学术限制政策 |

> 字节的两次提交都**在政策限制之前**。但即便按新规，字节仍然满足条件。

**TRAE Agent 论文的学术合著者**：

| 作者 | 学术单位 | 头衔 |
|------|---------|------|
| **熊英飞（Yingfei Xiong）** | **北京大学** | 副教授（tenure）、编程语言实验室副主任 |
| **陈俊洁（Junjie Chen）** | **天津大学** | 教授、软件工程团队负责人 |
| **高翠芸（Cuiyun Gao）** | **哈尔滨工业大学** | 教授 |
| **林芸（Yun Lin）** | **上海交通大学** | 教授 |

> 4 位高校教授作为合著者，满足"至少一位作者隶属学术机构"的要求。

**字节的合作生态**（据 TRAE 负责人彭超简历）：
- 字节 Seed 实验室与**清华、北大、上交、复旦、天大、港中文（深圳）**等高校有总预算 **¥1000 万** 的合作项目
- 彭超本人博士师从北大熊英飞教授（陈俊洁也是熊英飞的学生），论文合作关系长期稳定

**字节的完整操作链**：

```
1. 字节 Seed 实验室研发 TRAE Agent（产品能力）
2. 与 4 所高校教授合作 → 挂名论文合著者（学术资质）
3. 发 arXiv 预印本 2507.23370（满足 Open Research Publication）
4. 开源 trae-agent 到 GitHub（9800+ stars，满足 open source methods）
5. 提交 SWE-bench → 所有条件满足
```

**这是国内 AI 公司上 SWE-bench 的标准操作**——政策本意是挡"纯产品打榜"，但产学研合作完全在规则之内。

### 7.3 可行路线

| 路线 | 可行性 | 说明 |
|------|--------|------|
| **学术合作（推荐）** | ✅ 最务实 | 对标字节模式：找一位高校老师合著 arXiv 技术报告，最低成本满足 Verified 条件 |
| **SWE-bench Multimodal** | ✅ 对所有人开放 | 题目不同（JavaScript + 视觉元素），需适配，详见 §九 |
| **自行公布结果** | ✅ 自主可控 | 博客/官网发布结果 + 技术报告，不走官方榜 |
| **等政策变化** | ❓ 不确定 | 2025-11 新增限制，未来可能调整 |

#### 学术合作路线的最低成本操作

```
1. 联系一位软件工程方向的高校老师（如某大学 CS 副教授/教授）
2. 合写一篇 5-10 页的 arXiv 技术报告：
   - 标题类似："wescode: An AI Agent for Software Engineering Built on wesgine"
   - 内容：wesgine 引擎架构 + CKG/CSE 能力 + SWE-bench 评测方法论与结果
   - 作者：weisyn 团队 + 该老师（满足 Academic Affiliation）
3. 开源 bench 评测脚本（不需要开源 wescode/wesgine 全部代码）
4. 向 SWE-bench/experiments 仓库提 PR
```

> arXiv 不需要同行评审即可发表（预印本服务器），发表周期约 1-3 天。

### 7.4 Verified 评测的内部价值

已跑完的 500 题评分结果**仍然有价值**：
- **内部能力度量**：知道 wescode + DeepSeek Chat 的真实解题能力
- **对外宣传**：可在官网说"wescode 在 SWE-bench Verified 500 题上达到 XX% 解决率"
- **学术合作基础**：如找到合作高校，已有数据可直接用于论文实验部分
- **持续优化参照**：每次引擎升级后重跑，量化改进幅度

---

## 八、排行榜提交流程（仅供参考，当前不可直接提交 Verified）

| 步骤 | 说明 | 当前状态 |
|------|------|---------|
| 1. 生成 predictions | wescode bench 跑 500 题产出 patch | ✅ 完成 |
| 2. 本地/服务器评分 | swebench eval 跑 Docker 验证 | 🔄 服务器进行中 |
| 3. 准备提交材料 | predictions + metadata.yaml + README + trajs/ + logs/ | 待评分完成 |
| 4. 提交到 GitHub | 向 [SWE-bench/experiments](https://github.com/swe-bench/experiments) 仓库提 PR | ❌ 需学术合作 |
| 5. SWE-bench 团队审核 | 复现结果，验证分数 | — |
| 6. 上榜 | 出现在 swebench.com 排行榜 | — |

### 提交时显示的信息（若走学术合作路线）

| 字段 | 我们填什么 |
|------|-----------|
| MODEL | DeepSeek Chat |
| AGENT | wescode |
| ORG | weisyn（+ 合作高校） |
| SITE | https://www.weisyn.com |

---

## 九、SWE-bench Multimodal（对所有人开放的替代赛道）

### 9.1 什么是 SWE-bench Multimodal

SWE-bench Multimodal 是 SWE-bench 的**视觉+代码**扩展版，测试 AI Agent 在**含图片的软件 issue** 上的修复能力。

| 维度 | SWE-bench Verified | SWE-bench Multimodal |
|------|-------------------|---------------------|
| **语言** | Python（12 个仓库） | **JavaScript**（前端/UI 组件库） |
| **题量** | 500 题 | 480 题（v2，2026-09） |
| **Issue 特点** | 纯文本描述 | 含截图、UI mockup、设计稿、错误截图 |
| **测试什么** | 代码理解 + 修复 | 代码理解 + **视觉理解** + 修复 |
| **提交限制** | ❌ 仅学术机构 | ✅ **任何人都可以提交** |
| **评分方式** | 本地 Docker 或 sb-cli | **仅 sb-cli 云端** |
| **当前最高分** | 79.2%（Sonar + Claude Opus） | **61.4%**（Claude Opus 5.5） |
| **竞争程度** | 50+ 提交，竞争激烈 | ~20 提交，**竞争较少** |

### 9.2 题目长什么样

典型 Multimodal 题目：

```
仓库：carbon-design-system/carbon（IBM 的 React 组件库）
Issue：Button 组件在 disabled 状态下 hover 时颜色不对
附带：一张截图显示 hover 时的错误颜色 + 一张设计稿显示期望颜色
要求：修改 CSS/SCSS 让 disabled hover 颜色符合设计稿
测试：运行 Jest 视觉回归测试
```

涉及的仓库类型：React/Vue/Angular 组件库、图表库、CSS 框架等前端项目。

### 9.3 当前 Multimodal 排行榜

| 排名 | 模型/Agent | 分数 | 说明 |
|------|-----------|------|------|
| 1 | Claude Opus 5.5 | 61.4% | Anthropic，最新旗舰 |
| 2 | Claude Opus 5 | 59.4% | Anthropic |
| 3 | Claude Fable 5.1 | 54.7% | Anthropic |
| 4 | Qwen3.8 27B | 38.6% | 阿里巴巴，**唯一非 Anthropic** |
| 5 | Claude Opus 4.8 | 38.4% | Anthropic |
| … | Codefuse / Refact.ai / GUIRepair | 30-36% | 各家 Agent |
| … | OpenHands + Claude 3.7 | 31.3% | 学术 Agent |

**特点**：Anthropic 几乎垄断前列，其他玩家很少，机会窗口大。

### 9.4 wescode 参与 Multimodal 需要什么

| 能力 | 当前状态 | 需要做什么 |
|------|---------|-----------|
| **JavaScript 项目支持** | ⚠️ wescode 主要测试过 Python/Go | 确保 `exec`/`read`/`edit` 在 JS 项目中工作正常（原则上引擎语言无关） |
| **图片理解（Vision）** | ⚠️ wesgine 支持 Vision，但 bench 未接入 | bench CLI 需要把 issue 中的图片作为 image content block 传给模型 |
| **视觉模型** | ❌ DeepSeek Chat 不支持 Vision | 需换用支持 Vision 的模型（Claude / GPT / Qwen-VL） |
| **sb-cli 账号** | ❌ 之前注册 API key 失败 | 需重新尝试 sb-cli 注册或联系 SWE-bench 团队 |
| **前端测试环境** | ❌ Docker 环境需含 Node.js / 浏览器 | sb-cli 云端评分自带环境，不需本地搭建 |

### 9.5 提交流程（Multimodal 专用）

```bash
# 1. 安装 sb-cli
pip install sb-cli

# 2. 注册 API key
sb-cli gen-api-key --email your@email.com

# 3. 生成 predictions（wescode bench 跑 480 题）
wescode bench --dataset swe-bench-m/test --output predictions.json

# 4. 提交到云端评分
sb-cli submit swe-bench-m test \
    --predictions_path ./predictions.json \
    --run_id wescode-multimodal-v1

# 5. 获取报告
sb-cli get-report swe-bench-m test wescode-multimodal-v1

# 6. Fork SWE-bench/experiments → 创建 experiments/multimodal/wescode/ → 提 PR
```

**注意**：Multimodal **不需要** trajs/ 和 logs/，只需 metadata.yaml 和 README.md。

### 9.6 投入产出评估

| 投入项 | 估计 | 说明 |
|--------|------|------|
| bench CLI 适配 | 2-3 天 | 主要是接入 Vision 图片传输 + JavaScript 项目支持验证 |
| Vision 模型费用 | ¥2,000-5,000 | 480 题 × Vision 模型（Claude Sonnet / Qwen-VL） |
| sb-cli 注册 | 需重试 | 之前 API 有 bug，需关注修复进度 |
| **预期分数** | 30-45% | DeepSeek 无 Vision，需用 Claude/Qwen；JS 项目经验少 |
| **排名预期** | Top 10-15 | 竞争者少，30%+ 即可进前 15 |

---

## 十、可提交的 AI Agent 排行榜（2026-10-03 逐一验证）

> **验证标准**：第三方验证 + 第三方公布 + 对商业公司开放 + 有明确提交流程。
> 排除标准：只测模型不测 Agent、无提交入口、已关闭/归档、项目太早期。

### 10.1 确认可提交的 Agent 排行榜

#### ① LHTB（Long-Horizon Terminal-Bench）⭐⭐⭐ **最推荐**

已有社区成员成功提交上榜（Gemini 3.6 Flash / Kimi K3 / DeepSeek V4 Flash），提交流程完整且活跃。

| 维度 | 说明 |
|------|------|
| **排行榜** | [zli12321.github.io/LHTB/leaderboard.html](https://zli12321.github.io/LHTB/leaderboard.html) |
| **数据集** | [HuggingFace: IntelligenceLab/Long-Horizon-Terminal-Bench](https://huggingface.co/datasets/IntelligenceLab/Long-Horizon-Terminal-Bench) |
| **提交指南** | [SUBMIT.md](https://zli12321.github.io/LHTB/SUBMIT.md) |
| **论文** | [arXiv:2607.08964](https://arxiv.org/abs/2607.08964) |
| **题量** | 46 个长时任务（容器化终端环境，每题 90 分钟预算） |
| **测的是什么** | **Agent 在终端中持续工作的能力**——多步操作、状态维护、错误恢复、长时间自主执行 |
| **提交限制** | ✅ **任何人**，无学术/商业限制 |
| **验证方式** | 🤖 Bot 自动校验 → 👨‍💻 维护者人工审阅轨迹 → ✅ 上榜带 verified 徽章 |
| **当前最高** | Grok 4.5：0.505 mean reward（21 个条目） |
| **费用** | 仅 API 费用（46 题，每题 $2-60 取决于模型） |
| **wescode 适配** | 需包装为 Harbor Agent 或使用 terminus-2 harness，约 1-2 周 |

**提交流程**：

```bash
# 1. 安装 Harbor
uv tool install 'harbor[modal]'

# 2. 跑全部 46 题（每题 1 次，90 分钟预算）
harbor run -d terminal-bench/terminal-bench \
  -a <your-agent> -m <model> -k 1

# 3. 整理提交目录
# submissions/long-horizon-terminal-bench/1.0/wescode__deepseek-v4/
#   ├── metadata.yaml
#   └── <task-dirs>/  (每个含 config.json + result.json)

# 4. 直接向 HuggingFace 提 PR（不需要 fork）
hf upload IntelligenceLab/LHTB-leaderboard \
  submissions/long-horizon-terminal-bench/1.0/wescode__deepseek-v4 \
  submissions/long-horizon-terminal-bench/1.0/wescode__deepseek-v4 \
  --repo-type dataset --create-pr

# 5. Bot 自动验证 → 维护者审阅 → 合并 → 带 verified 徽章上榜
```

**为什么最推荐**：测的正是 Agent 长时自主工作的能力（wescode 的 exec 三分类、CKG 找路是天然优势），已有 DeepSeek 模型的社区提交，提交流程成熟且有成功先例。

#### ② SWE-bench Multimodal ⭐⭐

SWE-bench 官方品牌中**唯一**对商业公司开放的排行榜。

| 维度 | 说明 |
|------|------|
| **排行榜** | [swebench.com](https://www.swebench.com/) → Multimodal 标签 |
| **提交指南** | [sb-cli 提交文档](https://www.swebench.com/sb-cli/submit-to-leaderboard/) |
| **题量** | 480 题（v2，2026-09）|
| **语言** | **JavaScript**（前端/UI 组件库） |
| **Issue 特点** | 含截图、UI mockup、设计稿、错误截图 |
| **提交限制** | ✅ **任何人**（与 Verified/Multilingual 的学术限制不同） |
| **验证方式** | sb-cli 云端评分（普林斯顿 SWE-bench 团队运营） |
| **当前最高** | Claude Opus 5.5：61.4%（竞争者少，~20 条提交） |
| **费用** | 仅 API 费用（需 Vision 模型，¥2,000-5,000） |
| **前提** | ⚠️ 需要 Vision 模型（DeepSeek Chat 不支持图片）+ JavaScript 项目适配 |

**提交流程**：

```bash
# 1. 注册 sb-cli（免费）
pip install sb-cli
sb-cli gen-api-key your@email.com
# 收邮件验证码
sb-cli verify-api-key YOUR_CODE
export SWEBENCH_API_KEY=your_api_key

# 2. 跑 480 题生成 predictions（需 Vision 模型）
wescode bench --dataset swe-bench-m/test --output predictions.json

# 3. 提交到 sb-cli 云端评分（免费）
sb-cli submit swe-bench-m test \
  --predictions_path ./predictions.json \
  --run_id wescode-multimodal-v1

# 4. Fork SWE-bench/experiments → 创建 experiments/multimodal/wescode/
#    添加 README.md + metadata.yaml → 提 PR（PR 描述含 email + run_id）
```

#### ③ AppWorld ⭐

ACL 2024 Best Resource Paper，测 Agent 在模拟 App 环境中的任务完成能力。

| 维度 | 说明 |
|------|------|
| **排行榜** | [appworld.dev](https://appworld.dev/) |
| **提交仓库** | [github.com/StonyBrookNLP/appworld-leaderboard](https://github.com/StonyBrookNLP/appworld-leaderboard) |
| **题目** | 模拟 9 个应用（Gmail/Spotify/Amazon/Venmo 等），Agent 写 Python 代码调 API 完成任务 |
| **提交限制** | ✅ **任何人**。2026-05 有 IBM 团队成功提交 [PR #13](https://github.com/StonyBrookNLP/appworld-leaderboard/pull/13) |
| **验证方式** | GitHub Actions 自动评分 + 维护者审核合并 |
| **wescode 适配** | ⚠️ 非代码修复类任务，与 wescode 定位不太匹配 |

#### ④ Exgentic Open Agent Leaderboard

| 维度 | 说明 |
|------|------|
| **排行榜** | [exgentic.ai](https://www.exgentic.ai/) |
| **提交方式** | PR 到 [HuggingFace results 数据集](https://huggingface.co/datasets/Exgentic/results) |
| **题目** | 6 个基准评测的综合排名（含 SWE-bench） |
| **提交限制** | ✅ **任何人** |
| **知名度** | ⚠️ 2026 年新出，行业知名度低 |

### 10.2 确认不可提交的（2026-10-03 验证）

| 渠道 | 网址 | 状态 | 原因 |
|------|------|:---:|------|
| **SWE-bench Verified** | [swebench.com](https://www.swebench.com/) | ❌ | 2025-11-18 起仅学术机构。[ESMC PR #374](https://github.com/SWE-bench/experiments/pull/374) 被拒有实例 |
| **SWE-bench Multilingual** | [swebench.com](https://www.swebench.com/) | ❌ | 同 Verified 限制政策 + 只用 mini-SWE-agent 测模型（无自定义 Agent） |
| **SWE-bench Pro Public** | [labs.scale.com](https://labs.scale.com/leaderboard/swe_bench_pro_public) | ❌ | Scale AI 自跑自评，无外部提交入口。[Issue #95](https://github.com/scaleapi/SWE-bench_Pro-os/issues/95) 无回复 |
| **HAL Leaderboard** | [hal.cs.princeton.edu](https://hal.cs.princeton.edu/) | ❌ | 2026-07 已归档关闭，不再接受提交 |
| **Terminal-Bench 2.1** | [tbench.ai](https://www.tbench.ai/) | ❌ | 社区提交已关闭（"Only submissions run by the maintainers"） |
| **CoderCup** | [codercup.ai](https://www.codercup.ai/) | ❌ | 太早期，"No agents have shipped yet" |
| **Aider Polyglot** | [aider.chat](https://aider.chat/docs/leaderboards/) | ❓ | 非 Aider Agent 提交（[Issue #5321](https://github.com/aider-ai/aider/issues/5321)）仍 open，作者未表态 |
| **CodeAgentBench** | [suyoumo.github.io](https://suyoumo.github.io/code-agent-bench/) | ❌ | 无提交流程文档 |
| **METR** | [metr.org](https://metr.org/time-horizons/) | ❌ | 非公开提交平台，METR 自行评测 |
| **ProgramBench** | [programbench.com](https://programbench.com/) | 🟡 | 提交开放，但题型完全不匹配（从二进制重建程序，所有模型 0% 解决率） |

### 10.3 推荐优先级（修正版，2026-10-03）

| 优先级 | 榜单 | 理由 |
|--------|------|------|
| **P0** | **LHTB（Long-Horizon Terminal-Bench）** | 已有社区提交成功先例，测 Agent 终端长时工作能力，wescode 天然优势 |
| **P0** | **自行公布 SWE-bench Verified 结果** | 已有 500 题数据，等评分完成即可发布，行业认可 |
| **P1** | **SWE-bench Multimodal** | SWE-bench 官方品牌，对所有人开放，但需 Vision 模型 + JS 适配 |
| **P2** | **SWE-bench Verified**（走学术合作） | 影响力最大，但需找高校老师合著 arXiv 论文 |

### 10.4 适配成本对比（修正版）

| 榜单 | 代码改动量 | 模型费用 | 适配周期 | 主要工作 |
|------|-----------|---------|---------|---------|
| 自行公布 Verified 结果 | 0（已完成） | ¥590（已花） | 0（等评分） | 写博客/技术报告 |
| **LHTB** | **2-3 天** | **$100-500** | **1-2 周** | **包装 wescode 为 Harbor Agent** |
| SWE-bench Multimodal | 2-3 天 | ¥2,000-5,000 | 2-3 周 | 接入 Vision 模型 + JS 项目适配 + sb-cli |
| SWE-bench Verified（学术合作） | 0 | 0 | 不确定 | 找高校老师合著 arXiv 论文 |

---

## 十一、常见"AI Agent 排行榜"辨析（2026-10-03 验证）

> 网上搜索"AI Agent 排行榜"会返回很多结果，但绝大多数**不是能力评测排行榜**。
> 以下逐一验证，避免混淆。

### 11.1 用量/人气/商业类排行榜（不测 Agent 能力，不能提交评测）

| 排行榜 | 网址 | 排什么 | 能否提交 Agent 评测 | 说明 |
|--------|------|--------|:---:|------|
| **OpenRouter App Rankings** | [openrouter.ai/apps](https://openrouter.ai/apps) | 用户消耗的 Token 总量 | ❌ | 类似 App Store 下载量排名。当前 #1 Hermes Agent 2.01T tokens、#2 Claude Code 1.23T。需接入 OpenRouter 且有用户基数才能上榜 |
| **CB Insights AI Agent 营收榜** | [cbinsights.com](https://www.cbinsights.com/research/ai-agent-startups-top-20-revenue/) | 年度经常性收入（ARR） | ❌ | 投资分析榜单。门槛 $10M+ ARR。Cursor $1B、Replit $240M。联系 analyst@cbinsights.com 提交收入数据 |
| **GitHub Agent-Leaderboard** | [github.com/jaychempan/Agent-Leaderboard](https://github.com/jaychempan/Agent-Leaderboard) | GitHub Stars 数量 | ❌ | 每日自动爬取，按 Stars 排序。衡量开源热度不是技术能力 |
| **AI Hippo Agent Leaderboard** | [ai-hippo.com/en/agent/](https://ai-hippo.com/en/agent/) | GitHub Stars + 活跃度综合 | ❌ | 自动聚合，不接受评测提交 |
| **findarepo AI Agents** | [findarepo.com/categories/ai-agents/](https://findarepo.com/categories/ai-agents/) | Star 增速 + 维护活跃度 | ❌ | 每日更新的仓库热度榜 |

### 11.2 只测模型、不测自定义 Agent 的排行榜

| 排行榜 | 网址 | 说明 |
|--------|------|------|
| **SWE-bench Verified / Multilingual「Bash Only」视图** | [swebench.com](https://www.swebench.com/) | 统一用 mini-SWE-agent，只测模型能力。Agent 栏全是 mini-SWE-agent |
| **SWE-bench Pro Public** | [labs.scale.com](https://labs.scale.com/leaderboard/swe_bench_pro_public) | Scale AI 用标准 SWE-agent 自跑自评。无外部提交入口（[Issue #95](https://github.com/scaleapi/SWE-bench_Pro-os/issues/95) 无回复） |
| **LHTB 当前状态** | [zli12321.github.io/LHTB](https://zli12321.github.io/LHTB/leaderboard.html) | 支持自定义 Agent，但当前 22 条**全是 Terminus-2**（标准 harness），实质只测模型 |
| **CodeAgentBench** | [suyoumo.github.io/code-agent-bench/](https://suyoumo.github.io/code-agent-bench/) | 用 OpenCode CLI 跑 SWE-bench Pro 子集，无提交流程 |
| **LMSYS Arena Agent Mode** | [arena.ai](https://arena.ai/) | 有 Agent Mode（含 coding），但不接受外部 Agent 提交——Arena 与模型提供商合作测试模型，不是 Agent 打榜平台 |
| **BigCode Models Leaderboard** | [HuggingFace](https://huggingface.co/spaces/bigcode/bigcode-models-leaderboard) | 测纯模型的 HumanEval/MultiPL-E 代码生成能力。可以提交，但测的是模型不是 Agent |
| **CursorBench** | [cursor.com/cursorbench](https://cursor.com/cursorbench) | Cursor 自建的 IDE 级 Agent 评测（多文件编辑/重构/调试）。**数据集不公开（proprietary）**，外部 Agent 无法复现跑分。[BenchGen](https://benchgen.com/benchmarks/cursor/cursorbench) 有评测入口但尚无任何外部提交 |
| **Artificial Analysis Coding Agent Index** | [artificialanalysis.ai/agents/coding-agents](https://artificialanalysis.ai/agents/coding-agents) | 综合 DeepSWE + Terminal-Bench 4.0 + SWE-Atlas-QnA 三项评测的复合指数。**Artificial Analysis 自行跑测**，不接受外部 Agent 提交。但可以把你的结果与它的数据做对比 |
| **Morph AI Coding Agent Rankings** | [morphllm.com/ai-coding-agent](https://www.morphllm.com/ai-coding-agent) | **编辑性文章**——聚合 SWE-bench/Terminal-Bench 等公开数据做对比分析，不是独立评测平台，不接受提交 |

### 11.3 能力评测 + 对商业公司开放 + 可提交自定义 Agent 的排行榜

**结论：截至 2026-10-03，满足全部三个条件的排行榜极为稀缺。**

| 排行榜 | 能否提交自定义 Agent | 第三方验证 | 对商业开放 | 影响力 | 实际状态 |
|--------|:---:|:---:|:---:|:---:|------|
| **SWE-bench Verified** | ✅ Agent 栏显示你的名字 | ✅ | ❌ 仅学术 | ⭐⭐⭐⭐⭐ | 唯一行业标杆，但商业公司被挡 |
| **SWE-bench Multimodal** | ✅ | ✅ sb-cli 云端 | ✅ | ⭐⭐⭐ | 可行，需 Vision 模型 + JS 适配 |
| **GAIA** | ✅ | ✅ HuggingFace 自动评分 | ✅ | ⭐⭐⭐ | **通用 AI 助手评测，非编程专项**。可直接提交 JSONL 文件到 [HuggingFace 排行榜](https://huggingface.co/spaces/gaia-benchmark/leaderboard)。450+ 题，考核多步推理+工具调用+文件处理 |
| **OSWorld** | ✅ | ✅ 维护者验证 | ✅ | ⭐⭐⭐ | 操控真实操作系统（Linux GUI/终端）完成任务。[V2 仓库](https://github.com/xlang-ai/OSWorld-V2) 有详细提交指南，需联系维护者安排验证跑分 |
| **WebArena** | ✅ | 🟡 自评+社区验证 | ✅ | ⭐⭐⭐ | 浏览器自动化（812 题，购物/论坛/GitLab 等）。无统一官方提交入口，推荐通过 [AgentLab](https://github.com/web-arena-x/webarena) 框架跑，结果自行公布或提交到 [Steel.dev 社区排行榜](https://leaderboard.steel.dev/leaderboards/webarena/) |
| **SWE-bench Mobile** | ✅ Agent 栏显示你的名字 | ✅ 维护者跑分 | ✅ | ⭐⭐⭐ | 移动端 iOS 开发评测（50 题 Swift/ObjC）。[排行榜](https://swebenchmobile.com/leaderboard) 有 Cursor/Codex/Claude Code/OpenCode。联系 murphy.tian@mail.utoronto.ca 提交。⚠️ iOS/Swift 不是 wescode 主场 |
| **LHTB 自定义 Agent 模式** | ✅ 理论支持 | ✅ Bot + 人工 | ✅ | ⭐⭐ | 无先例——当前全是标准 Agent 跑模型 |
| **AppWorld** | ✅ | ✅ CI 自动 | ✅ | ⭐⭐ | API 调用任务（Gmail/Spotify 等模拟 App），非代码修复 |
| **Exgentic** | ✅ | ✅ | ✅ | ⭐ | 知名度低 |

### 11.4 与 wescode 编程 Agent 定位匹配度分析

wescode 的核心能力是**自主修复代码 Bug / 完成软件工程任务**（CKG 代码知识图谱 + CSE 约束满足 + exec 三分类终端路由 + 编辑引擎）。各排行榜匹配度：

| 排行榜 | 测什么 | 与 wescode 匹配度 | 障碍 |
|--------|--------|:---:|------|
| **SWE-bench Verified** | 修真实 GitHub Bug（Python） | ⭐⭐⭐⭐⭐ 完美匹配 | 学术限制 |
| **SWE-bench Multimodal** | 修真实 GitHub Bug（JS + 视觉） | ⭐⭐⭐ 部分匹配 | 需 Vision 模型 + JS 不是主场 |
| **SWE-bench Mobile** | 移动端 iOS 开发（Swift/ObjC） | ⭐⭐ 部分匹配 | iOS/Swift 不是主场，但测的确实是 Agent 工程能力 |
| **GAIA** | 通用多步推理+工具调用 | ⭐⭐ 部分匹配 | 不是编程专项，更接近通用助手评测 |
| **OSWorld** | 操控操作系统 GUI/终端 | ⭐⭐ 部分匹配 | 需 GUI 交互能力（截屏+点击），wescode 以 CLI 为主 |
| **WebArena** | 浏览器自动化 | ⭐ 不匹配 | 网页导航/表单填写，不是代码任务 |
| **AppWorld** | 调 API 完成业务任务 | ⭐ 不匹配 | 不涉及代码修改 |

**行业现状总结**：最有影响力的能力评测榜（SWE-bench Verified）把商业公司挡在门外；对所有人开放的榜要么只测模型（LHTB/Pro Public/Bash Only）、要么题型不匹配编程场景（GAIA/OSWorld/WebArena/AppWorld）、要么知名度太低（Exgentic）。这不是 wescode 一家的困境——Augment Code、Solver AI、Honeycomb.sh 等商业公司同样被 Verified 拒绝。字节跳动（TRAE）上榜的方式是通过产学研合作挂名高校教授发 arXiv 论文。

---

## 十二、排行榜各列含义

| 列名 | 含义 | 示例 |
|------|------|------|
| # | 排名 | 1 |
| MODEL | LLM 模型 | Claude 4.5 Opus |
| AGENT | 跑评测的工具/框架 | Sonar Foundation Agent |
| % RESOLVED | 核心分数（500 题中解决的百分比） | 79.20 |
| AVG. $ | 每题平均 API 花费（美元） | $1.26 |
| TRAJS | 运行轨迹链接（可看 Agent 每步操作） | 🔗 |
| ORG | 提交者/组织 logo | 公司图标 |
| DATE | 提交日期 | 2025-12-05 |
| SITE | 组织官网 | 🔗 |

### 排行榜标记说明

- 🟢 **Open-weights model**：开源/开放权重模型（如 DeepSeek、Qwen）
- 🔵 **Run performed or directly checked by the SWE-bench team**：SWE-bench 团队亲自跑的或验证的
