# 语言注册表（Language Registry）

配置驱动的多语言支持框架。每种语言的工具链知识（AST 节点映射、编译/测试命令）以声明式 JSON 文件描述，**添加新语言无需修改 Go 代码**。

语言服务器不在这里声明。wescode 是带窗口的 VS Code fork：LSP 属于用户装的扩展，经 `IDELSPBridge` 询问编辑器里已经在跑的那一份；没有窗口则 `NoopLSP`，符号信息走 CKG。`langs/*.json` 禁止出现 `lsp.command`（INV-LSP-09）。

## 支持的语言

| 语言 | 配置文件 | 编译工具 | 测试工具 |
|------|---------|---------|---------|
| Go | `go.json` | `go build` | `go test` |
| TypeScript/JS | `typescript.json` | `tsc --noEmit` | `vitest run` |
| Python | `python.json` | `pyright` | `pytest` |
| Rust | `rust.json` | `cargo check` | `cargo test` |
| Java | `java.json` | `mvn compile` | `mvn test` |
| C++ | `cpp.json` | `cmake --build` | `ctest` |
| C | `c.json` | `make` | `ctest` |
| C# | `csharp.json` | `dotnet build` | `dotnet test` |
| PHP | `php.json` | `php -l` | `phpunit` |
| Ruby | `ruby.json` | `ruby -c` | `rspec` |
| Swift | `swift.json` | `swift build` | `swift test` |
| Kotlin | `kotlin.json` | `gradle compileKotlin` | `gradle test` |
| Dart | `dart.json` | `dart analyze` | `dart test` |

## 添加新语言

1. 创建 `<lang>.json`（参考已有文件的格式）
2. 填写段落：`language`、`calls`、`patterns`、`test_runner`、`compile_runner`（以及可选 `topology`）。不要加 `lsp`——语言服务器由编辑器扩展提供。
3. 重新编译（`go build ./...`）— JSON 通过 `go:embed` 嵌入二进制

无需修改任何 `.go` 文件。注册表在启动时自动加载所有 `*.json`。

## JSON Schema

```jsonc
{
  "language": {
    "name": "Human-readable name",
    "id": "lowercase-id",              // 用于 Registry.ByID() 查询
    "extensions": [".ext"],             // 文件扩展名（含点号）
    "test_file_patterns": ["*_test.*"], // glob 模式，用于 isTestFile 检测
    "marker_files": ["go.mod"],         // 项目根标记文件，用于 DetectProjectType
    "tree_sitter_grammar": "pkg_name"   // tree-sitter Go binding 包名（参考用）
  },

  "calls": {
    "function_call": {
      "node_type": "call_expression",   // tree-sitter 中函数调用的节点类型
      "function_field": "function"      // 被调用函数的子节点字段名
    },
    "method_call": {
      "node_type": "call_expression",   // 方法调用的节点类型（可能与函数调用相同）
      "function_field": "function",     // 函数子节点字段名
      "receiver_field": "object",       // 接收者字段名（obj.method 中的 obj）
      "method_field": "property"        // 方法名字段名（obj.method 中的 method）
    }
  },

  "patterns": {
    "error_handling": {
      "node_types": ["try_statement"],  // 匹配的 AST 节点类型列表
      "description": "try/catch",
      "text_match": "optional regex"    // 可选：附加的文本匹配条件
    }
  },

  "test_runner": {
    "command": "pytest",
    "args": ["-v"],
    "parse_pattern": {
      "failure": "regex to match failed test name",
      "pass": "regex to match passed test name"
    }
  },

  "compile_runner": {
    "command": "tsc",
    "args": ["--noEmit"],
    "parse_pattern": {
      "error": "regex: (file):(line):(col): (message)",
      "undefined_symbol": "regex to extract undefined symbol name"
    }
  },

  "topology": {
    "dependency_files": [
      {
        "file": "go.mod",                  // 依赖清单文件名（支持 glob: "*.csproj"）
        "format": "gomod_replace"           // 解析器标识符，映射到 Go 函数
      }
    ]
  }
}
```

## 消费方

| 消费方 | 读取的字段 | 用途 |
|--------|-----------|------|
| `treesitter/extract.go` | `calls.*` | 调用链提取（`WalkCallExpressionsWithConfig`） |
| `verification/runner.go` | `compile_runner` + `test_runner` | L1 编译 + L2 测试执行 |
| `verification/affected.go` | `language.test_file_patterns` | 测试文件检测（`isTestFile`） |
| `verification/runner.go` | `language.marker_files` | 项目类型探测（`DetectProjectType`） |
| `codeintel/topology.go` | `topology.dependency_files` | 项目拓扑发现（多仓库本地依赖路径提取） |

## 查询 API

```go
import "github.com/weisyn/wescode/internal/codeintel/langs"

reg := langs.Default()

// 按语言 ID 查询
cfg := reg.ByID("python")

// 按文件扩展名查询
cfg := reg.ByExtension(".ts")

// 按文件路径查询
cfg := reg.ByFilePath("/project/src/main.rs")

// 遍历所有注册语言
for _, cfg := range reg.All() {
    fmt.Println(cfg.Language.Name)
}
```

## 拓扑发现

`topology.dependency_files` 声明了如何从该语言的项目清单中提取本地路径依赖。`codeintel.ProjectTopologyDetector` 在每次 `chat/send` 请求时扫描 workDir 下的清单文件，提取兄弟仓库路径注入 `AllowPaths`。

添加新语言的拓扑支持：

1. 在 `<lang>.json` 的 `topology.dependency_files` 中声明清单文件名和格式标识符
2. 在 `codeintel/topology_parsers.go` 中实现解析函数
3. 在 `codeintel/topology.go` 的 `parserRegistry` 中注册

支持的格式标识符：`gomod_replace`、`cargo_toml_path`、`cargo_workspace`、`package_json_workspaces`、`tsconfig_references`、`pnpm_workspace_yaml`、`pyproject_path`、`csproj_project_ref`、`sln_projects`、`gradle_settings`、`swift_package_path`、`pubspec_path`、`gemfile_path`、`composer_path_repo`、`cmake_add_subdirectory`。

## 设计原则

- **编译时嵌入**：JSON 文件通过 `go:embed` 打包进二进制，运行时无文件 I/O
- **查询 O(1)**：启动时建立 ID → Config 和 Extension → Config 两级索引
- **降级安全**：查询返回 nil 时，调用方使用硬编码 fallback 或 NullRunner
- **单一真实来源**：语言特性描述集中在此处，其他包不硬编码语言知识
