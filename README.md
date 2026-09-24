# wescode

**AI 原生智能编程工具** — 在 VS Code 基座上构建，用代码知识图谱替代暴力搜索。

<p align="center">
  <img src="https://img.shields.io/badge/license-Apache%202.0%20%2B%20BSL-blue" />
  <img src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-brightgreen" />
  <img src="https://img.shields.io/badge/vscode-fork-purple" />
</p>

---

## 为什么做 wescode？

AI 编程卡住全行业的三件事：

| 问题 | 行业现状 | wescode 的解法 |
|------|---------|---------------|
| **找路** — 大仓里搜不到该动的点 | grep + read 线性搜索 | **CKG**（代码知识图谱）：O(1) 精确导航 |
| **规矩** — 没写进文件的约束会被改掉 | 静态 .cursorrules | **CSE**（约束满足引擎）：自动发现 + 机械验证 |
| **证明没改坏** — 测试绿了但行为变了 | 无等价方案 | **L2.5**（行为基线门控）：纯确定性 diff 拦截 |

## 特性

- 🧠 **CKG 代码知识图谱** — 10-Pass 管线构建跨语言调用图、依赖图、类型层次
- 🔒 **CSE 约束满足引擎** — 自动推断代码约束，编辑后机械验证
- 📊 **L2.5 行为基线** — 零 LLM token 拦截隐蔽回归
- 🤖 **多 Agent 协作** — 11 个预设角色（Coder / Reviewer / Architect / DBA / ...）
- 🛠 **26 个编程技能** — Code Review / 重构 / 测试 / 性能优化 / 安全审计 / ...
- 📝 **智能编辑引擎** — 三级匹配 + 冲突检测 + 可逆性保障
- 🔌 **Device Agent** — Buffer Overlay + 终端三分类路由 + 编辑器状态透明注入
- 💬 **多模型支持** — OpenAI / Claude / DeepSeek / 本地部署（Ollama / vLLM）

## 架构

```
┌─── Renderer (Chromium) ───────────────────────────┐
│  Chat 面板 · Inline 补全 · CKG Hover/CodeLens     │
│  终端三分类路由 · Buffer 实时同步                    │
└───────────────────┬───────────────────────────────┘
                    │ Electron IPC
┌───────────────────▼───────────────────────────────┐
│  Electron Main — 后端进程管理 + LSP 编解码          │
└───────────────────┬───────────────────────────────┘
                    │ stdio JSON-RPC
┌───────────────────▼───────────────────────────────┐
│  Go Backend                                        │
│  ├── rpc/        ~215 个 RPC 方法                  │
│  ├── engine/     1 workspace = 1 Cell 隔离          │
│  ├── codeintel/  CKG + CSE + 25 个智能工具 [BSL]    │
│  ├── editengine/ 三级匹配 + 可逆编辑 [BSL]          │
│  └── verification/ QualityGate L0-L2.5 [BSL]       │
└───────────────────────────────────────────────────┘
```

## 快速开始

### 下载预编译版本

前往 [Releases](https://github.com/weisyn/wescode/releases) 下载对应平台的安装包。

### 从源码构建

```bash
# 克隆
git clone https://github.com/weisyn/wescode.git
cd wescode

# 安装依赖
cd editor && npm install && cd ..
cd web && npm install && cd ..

# 构建
make build   # 构建 Go 后端 + Web 前端
make editor  # 编译 Editor TypeScript（首次约 3 分钟）

# 启动
make run
```

### 配置 LLM Provider

首次启动后，点击左侧 ⚡ 图标添加 Provider：

```yaml
# 或手动编辑 ~/.config/wescode/config.yaml (Linux)
# ~/Library/Application Support/wescode/config.yaml (macOS)
providers:
  - name: deepseek
    type: openai_compat
    base_url: https://api.deepseek.com
    api_key: sk-xxxx
    model: deepseek-chat
    is_default: true
```

## Agent 预设角色

wescode 内置 11 个 Agent 角色，覆盖软件开发全流程：

| 角色 | 擅长 |
|------|------|
| `coder` | 通用编程、功能实现 |
| `reviewer` | 代码审查、质量把关 |
| `architect` | 系统设计、架构决策 |
| `frontend` | 前端开发、UI/UX |
| `golang` / `java` | 语言专精开发 |
| `dba` | 数据库设计与优化 |
| `devops` | CI/CD、部署运维 |
| `security` | 安全审计、漏洞修复 |
| `test` | 测试策略与编写 |
| `pm` | 需求分析、项目管理 |

## 编程技能

26 个预置技能，选择性加载到对话中：

`code-review` · `test-engineering` · `refactoring` · `debug-methodology` ·
`performance-optimization` · `security-audit` · `clean-code` · `database-design` ·
`system-design` · `git-workflow` · `cicd-deployment` · `documentation-engineering` ·
`integration-audit` · `legacy-navigation` · `pattern-consistency` ·
`requirements-analysis` · `ui-engineering` · `autonomous-workflow` · `split-changes` ·
`pr-guardian` · `code-audit-discipline` · `context-calibration` ·
`issue-investigation` · `wescode-retrieval` · `wescode-execution` · ...

## Headless CLI（命令行模式）

```bash
# 单次对话
wescode run "修复这个 bug" --workdir /path/to/repo

# 批量评测（SWE-bench）
wescode bench --dataset ./tests/bench/swebench-100 --runs 1
```

## 许可证

- **主体代码**：[Apache License 2.0](LICENSE)
- **代码智能核心**（`codeintel/` · `editengine/` · `verification/`）：[BSL 1.1](backend/internal/codeintel/LICENSE-BSL)
  - 个人使用和 50 人以下团队：免费
  - 3 年后自动转为 Apache 2.0

## 贡献

欢迎贡献！请阅读 [CONTRIBUTING.md](CONTRIBUTING.md) 了解：

- 如何提交 Bug Report
- 如何贡献 Skill / Agent 预设
- 代码风格与提交规范

## 社区

- 💬 [GitHub Discussions](https://github.com/weisyn/wescode/discussions)
- 🐛 [Issue Tracker](https://github.com/weisyn/wescode/issues)
- 📖 [设计文档](design/README.md)

---

*wescode 由 [Weisyn](https://github.com/weisyn) 团队开发维护。*
