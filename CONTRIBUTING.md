# 贡献指南

感谢你对 wescode 的关注！我们欢迎来自社区的贡献。

## 可以贡献什么

### ✅ 欢迎的贡献

| 类型 | 说明 |
|------|------|
| 🐛 Bug 修复 | 发现了问题？提交 Issue + PR |
| 🛠 新 Skill | 为特定编程场景编写技能模板 |
| 🤖 Agent 角色 | 添加新的 Agent 预设（如 Rust 专家、移动端开发等） |
| 🌐 国际化 | 翻译 UI 文案 |
| 📖 文档改进 | 修正文档错误、补充使用示例 |
| 🧪 测试用例 | 补充单元测试和集成测试 |
| 🎨 UI 改进 | 改善 Chat 面板、设置页等用户体验 |
| 🔌 语言支持 | 改进 tree-sitter 语言解析器配置 |

### ⚠️ 需要先讨论的贡献

| 类型 | 说明 |
|------|------|
| 新 RPC 方法 | 添加新的 JSON-RPC 接口 |
| 架构变更 | 修改模块边界或依赖方向 |
| 新工具 | 添加新的 Agent 工具 |

请先提交 Issue 或 Discussion，说明你的设计思路。

### ❌ 不接受的贡献

- 对 `codeintel/`、`editengine/`、`verification/` 目录的非 bug-fix PR
  - 这些目录在 BSL 1.1 许可证下，核心算法变更由维护团队管理
  - Bug 修复 PR 欢迎提交

## 开发流程

### 环境准备

```bash
# 依赖
- Go 1.22+
- Node.js 22+
- macOS / Linux（Windows 需 WSL）

# 克隆
git clone https://github.com/weisyn/wescode.git
cd wescode

# 构建
make build
make run
```

### 提交规范

使用 Conventional Commits：

```
feat(backend): 添加 Rust 语言解析支持
fix(web): 修复设置页 Provider 表单验证
docs: 更新快速开始指南
style(editor): 调整 Chat 面板输入框样式
refactor(rpc): 统一错误码格式
test(codeintel): 补充 CKG 索引边界测试
```

### Pull Request 流程

1. Fork 本仓库
2. 创建功能分支：`git checkout -b feat/my-feature`
3. 提交变更并确保通过检查：
   ```bash
   cd backend && go build ./... && go vet ./...
   make editor  # tsc 检查
   ```
4. 推送并创建 PR
5. 等待 Review

## 添加 Skill 指南

Skill 是 wescode 最容易贡献的部分。在 `backend/presets/skills/` 下创建目录：

```
backend/presets/skills/my-skill/
└── SKILL.md
```

SKILL.md 格式：

```markdown
---
name: my-skill
description: 一句话描述（≤60 字符）
version: 1.0.0
author: 你的名字
---

# My Skill

## 适用场景
描述这个 Skill 最适合什么时候使用...

## 操作步骤
一步一步的指导...
```

## 添加 Agent 角色

在 `backend/presets/agents/` 下创建 YAML 文件：

```yaml
# backend/presets/agents/rust.yaml
name: rust
display_name: Rust 专家
description: 精通 Rust 语言，擅长内存安全和性能优化
system_prompt: |
  你是一位 Rust 编程专家...
skills:
  - performance-optimization
  - security-audit
  - clean-code
```

## 行为准则

请参阅 [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)。

## 许可证

提交 PR 即表示你同意将贡献按照本项目的许可证条款（Apache 2.0 或 BSL 1.1，取决于目标目录）进行授权。
