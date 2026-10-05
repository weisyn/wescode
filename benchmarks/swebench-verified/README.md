# SWE-bench Verified 评测

[SWE-bench Verified](https://www.swebench.com/) 是普林斯顿大学发布的 AI 编程 Agent 基准评测，500 道来自 12 个 Python 开源仓库的真实 GitHub Issue 修复任务。

## 历次成绩

| 日期 | 模型 | 方式 | Resolved | 通过率 | 费用 | 目录 |
|------|------|------|----------|--------|------|------|
| **2026-10-04** | DeepSeek Chat | Best@1 | **396/500** | **79.2%** | ¥275.59 | [`20261004-deepseek-chat/`](20261004-deepseek-chat/) |

## 排行榜对标（2026-10-04）

| # | MODEL | AGENT | % RESOLVED |
|---|-------|-------|-----------|
| 1 | Claude 4.5 Opus | Sonar Foundation Agent | 79.20% |
| 2 | Claude 4.5 Opus (medium) | live-SWE-agent | 79.20% |
| **—** | **DeepSeek Chat** | **wescode** | **79.20%** |
| 3 | Doubao-Seed-Code | TRAE（30x rollout） | 78.80% |
| 4 | Gemini 3 Pro Preview | live-SWE-agent | 77.40% |

wescode 的独特之处：**用 DeepSeek Chat（¥275 / $39）达到了与 Claude 4.5 Opus（$500+）相同的分数**，且为 Best@1 单次运行。

## 提交限制

自 2025-11-18 起，SWE-bench Verified 仅接受学术机构提交。商业公司需通过学术合作路线（arXiv 论文 + 高校合著者）。详见各跑分目录下的分析。
