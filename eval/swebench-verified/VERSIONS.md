# 版本锁定

## 源码版本

| 仓库 | Commit | 日期 | 说明 |
|------|--------|------|------|
| wescode.git | `2eeff2c8` | 2026-10-04 | SWE-bench Patch 采集修复版 |
| wesgine.git | `093ad57e` | 2026-10-03 | Agent 引擎 v1.0 |
| wesapp.git | 对应 go.mod replace | — | 引擎消费层 SDK |
| weisyn.git | 对应 go.mod replace | — | 平台 SDK + catalog |

## 模型版本

| 参数 | 值 |
|------|------|
| Provider | DeepSeek |
| Model ID | `deepseek-chat` |
| API Base | `https://api.deepseek.com` |
| 模型版本 | deepseek-v4-flash（2026-09 版本） |
| Temperature | 默认（由引擎控制） |
| Max Tokens | 默认（由引擎控制） |

## 评测工具版本

| 工具 | 版本 |
|------|------|
| swebench | 5.0.2 |
| Docker | 20.10+ |
| Python | 3.12.9 |
| Go | 1.24.4 |
| swe-bench-tasks repo | `main` branch (2026-10-03 clone) |

## 定价（计算成本用）

| 类别 | 单价 |
|------|------|
| DeepSeek Chat Input | ¥1 / M tokens |
| DeepSeek Chat Output | ¥2 / M tokens |
| DeepSeek Cache Reads | ¥0.1 / M tokens |
| 汇率 | ¥7.1 = $1 |
