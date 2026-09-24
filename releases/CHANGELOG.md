# WES Code 版本历史

> 版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)：`主版本.次版本.修订号[-预发布标识]`
>
> 预发布标识：`preview.N`（体验版）→ `beta.N`（公测版）→ `rc.N`（候选版）→ 正式版（无标识）

---

## 0.1.0-preview.4 — 2026-09-24

**定位**：引擎修复版。wescode 应用代码与 preview.3 相同，新增内容全部来自 wesgine `8a57de81..5c4d8325`（13 文件，+197 / -39）

### 变更内容

#### 网络策略（wesgine）

- **fix** `internal/execution/builtin/fetch_url.go`：SSRF 检查写死 `AllowPrivate=false`，不看治理层 NetworkPolicy。于是设置页选「允许全部」（编程场景推荐）时抓不到 `localhost` 上的开发服务器；选「仅内网」时 `fetch_url` 什么都访问不了——外网被策略拒、内网被 SSRF 拒。改为经 `ToolContext.FetchURLAllowPrivate`（源自 `Deps.NetworkPolicyAllowPrivate`，实时读策略）决定；`169.254.169.254` 等云元数据地址在放行私网时仍拦截
- **fix** `adapter/cron/deliver.go`、`adapter/headless-browser/manager.go`：定时任务 Webhook 投递与无头浏览器（含 302 跳转校验）是同一个 bug，同样改法

#### 会话持久化（wesgine）

- **fix** `internal/observe/tracer.go`：用户消息 / 助手消息 / tool_result / run summary 写库失败时退避 200ms 重试一次。SQLite WAL 读写竞争下 `busy_timeout` 超时会让这条消息直接丢掉，表现为重开会话时历史里缺一条；`InsertMessage` 按主键幂等，重试不会重复写
- **fix** `internal/observe/tracer.go`：Cell Drain 关库前先 `MarkClosed()`，之后到达的 `EmitEvent` 直接丢弃。此前活过 `Loop.Wait(10s)` 的 Run 会往已关闭的库里写，刷出 `sql: database is closed` WARN

#### 发布工具链（wescode，不进安装包）

- **fix** `cmd/update-server`：下载 URL 逐段百分号编码。产物名从 preview.3 起含空格，原样拼接的 URL 被 Squirrel.Mac 判为非法（`[NSURL URLWithString:]` 返回 nil），症状是「检查更新正常、下载不动」。服务端修复，重新部署 update-server 即生效，与客户端版本无关
- **feat** `scripts/publish-release.sh`：缺 rsync 时降级到 scp；Git Bash 下经 `cygpath` 转路径给 node——Windows 本机可直接发布
- **feat** `Makefile` / `scripts/package-win.cmd`：新增 `release-win` / `publish-win`；`package-win.cmd --publish` 路由到 `publish-release.sh`

### 构建状态

| 平台 | 架构 | 构建 | 签名 | 公证 | 可分发 |
|------|------|------|------|------|--------|
| macOS | Apple Silicon (arm64) | ⏳ 待打包 | ⏳ | ⏳ | ⏳ |
| macOS | Intel (x64) | ⏳ 待打包 | ⏳ | ⏳ | ⏳ |
| Windows | x64 | ⏳ 待打包 | ⏳ | 无此步骤 | ⏳ |

### 产物

打包后回填。

Commit: 待回填

---

## 0.1.0-preview.3 — 2026-09-22

**定位**：可逆性 + 呈现契约 + CKG 增强（preview.2 以来 156 文件，+9938 / -2067）

### 变更内容

#### 可逆性（EE-13 ~ EE-19，[design/26-reversibility.md](../design/26-reversibility.md)）

- **feat** `editengine/shadow.go`：影子 git 仓库成为恢复点的唯一存储后端——git 目录在 wescode 数据目录、work-tree 指向用户项目，所以用户项目里不出现 `.git`，`git status` / `git stash list` 保持干净。此前 git 项目与非 git 项目走两条后备路径，且保护最弱的那条（内存备份、进程一死就没）正好是没 `git init` 的用户所在的那条
- **feat** `engine/postrun_quality.go`：Run 结束后的质量回填

#### 呈现契约（PC-01 ~ PC-06，[design/27-presentation-contract.md](../design/27-presentation-contract.md)）

- **feat** `codeintel/presentation.go`：工具结果携带 `{ category, grade, data }` 信封，走 `tool.ToolResult.StructuredData`（**不走** `Metadata`——`engine.ToolResultData` 无此字段，声明了也到不了前端）。此前 51 个 Layer C 工具里 49 个落进 `GenericResult` 的裸 `<pre>`
- **feat** `codeintel/{callgraph,list,actionable}_wire.go`：三类信封的 wire 契约
- **feat** `CallGraphPart.tsx`：调用图卡片渲染 + `callGraphEnvelope.contract.test.ts` 钉信封形状
- **feat** `scripts/check-notready-wiring.py` / `check-truncation-counts.py`：两个新闸门

#### CKG

- **feat** `codeintel/index.go`：新增可达性分类（`ClassifyReachability` / `ClassifyReachabilityForFiles` / `FileReachabilityProfile`），孤儿代码判定不再只看有无调用者
- **feat** `codeintel/{doc_ref,tool_doc_ref,overlay_kb}.go` + `schema.go`：`doc_references` 表，代码与文档的关联索引

#### 开发者画像（[design/25-developer-profile.md](../design/25-developer-profile.md)）

- **feat** `rpc/handler_profile.go` + `web/src/pages/settings/profile.tsx`：六维评分面板（权威设计在 weisyn）

#### 基础设施

- **feat** `store/{migrate,appdb_migrations,authdb_migrations}.go`：INV-MIGRATE-01 落地——`wescode-app.db` / `wescode_auth.db` 的 schema 变更走 `MigrationStep`，`PRAGMA user_version` 事务内原子递增；迁移失败归档旧库重来，boot 永远成功
- **feat** `scripts/full-release.sh` + `make bump` / `release-arm64` / `release-intel`：一键 bump + 打包 + 发布
- **fix** `engine/channels.go`：`ChannelSnapshotView` 补 `QRContent` 字段 + 订阅后回放 `qr_pending`——微信二维码此前两条数据路径（快照 / 事件）同时失效，快照路径字段被丢弃、事件路径输给订阅竞态
- **fix** `web/src/locales/en.json`：`changelogSkills` 的英文写着 12 个技能，实际 26 个

### 构建状态

| 平台 | 架构 | 构建 | 签名 | 公证 | 可分发 |
|------|------|------|------|------|--------|
| macOS | Apple Silicon (arm64) | ✅ | ✅ Developer ID | ✅ Accepted + Stapled | ✅ 已上线 |
| macOS | Intel (x64) | ✅ | 未记录 | 未记录 | ✅ 已上线 |
| Windows | x64 | ✅ | 未记录 | 无此步骤 | ✅ 已上线 |

三个平台 09-22 均已写入线上 `stable.json`（同一 commit）。Intel 与 Windows 在各自机器上打包发布，签名状态未回传到本记录。

### 产物

| 文件 | SHA256 |
|------|--------|
| `WES Code-0.1.0-preview.3-darwin-arm64.zip` | `937ed14935b4ede23ed4ab5b9ea8dd6238c310a7febc3b555a2f245731b31627` |
| `WES Code-0.1.0-preview.3-darwin-arm64.dmg` | `c847d4917b946de08f932192ce66202a99cb4f09f025f1e60881cc8635df4d13` |
| `WES Code-0.1.0-preview.3-darwin-x64.zip` | `d17244cd4b1e67d2b750d575ac9924bdb52ec8fe63b4b24a9204f22e633f78df` |
| `WES Code-0.1.0-preview.3-win32-x64-setup.exe` | `bf8537e03b0de4fb608116252cedfbe3f5cede340e17696c6507921b64fd9f07` |

Commit: `56203483a49d610d388584f2ee917e488b89dc4b`

---

## 0.1.0-preview.2 — 2026-09-08

**定位**：preview.1 修正构建（同版本号覆盖构建，包含 preview.1 发布后的修复）

### 变更内容

- **feat** `handler_editor.go`：EditorState 路径归一化——RPC 边界处统一正斜杠（FocusFile / OpenFiles / VisibleEditors / RecentEdits / GlobalErrors / GitStagedFiles），修复 CKG 焦点文件邻近度加权失效
- **feat** `platform/shell.go`：新增 `platform.ShellCommand` 抽象，收敛跨平台 shell 调用（Windows `cmd /c` / POSIX `sh -c`）
- **feat** `wescodePath.ts`：Editor 侧路径工具（`isAbsolutePath` / `fileBasename`），修复 Windows 三种绝对路径判定
- **feat** `callgraphProviders.ts`：CodeLens Provider 路径过滤修正
- **fix** `WescodeChatInput.tsx`：Chat 输入组件修正
- **fix** `bench/types.go`：benchmark 类型修正
- **fix** `wsintel/probe.go`：workspace 智能探测修正

### 构建状态

| 平台 | 架构 | 构建 | 签名 | 公证 | 可分发 |
|------|------|------|------|------|--------|
| macOS | Apple Silicon (arm64) | ✅ | ✅ Developer ID | ✅ Accepted + Stapled | ✅ **可发版** |

### 操作记录

| 时间 | 操作 | 结果 |
|------|------|------|
| 09-08 07:39 | `make package-dmg-arm64`（~41 分钟） | ✅ 604MB .app |
| 09-08 08:21 | `sign-macos-arm64.sh`（签名 + zip） | ✅ zip sha256: `8c41e94c...` |
| 09-08 08:21 | `build-dmg.py`（带背景图 DMG） | ✅ |
| 09-08 08:23 | 公证 zip（Submission `805fe82c`） | ✅ Accepted |
| 09-08 08:23 | 公证 dmg（Submission `62649140`） | ✅ Accepted + Stapled |
| 09-08 08:27 | 体检 `preflight-macos-release.sh` | ✅ 6/6 全绿 |
| 09-08 08:28 | 发布 CDN | ⏳ SSH 超时，待重试 |

### 产物

| 文件 | SHA256 |
|------|--------|
| `WES Code-0.1.0-preview.2-darwin-arm64.zip` | `8c41e94c39fabe79150d1674fcffae9c8de1309569d7edf60e3a058f5f62fe8d` |
| `WES Code-0.1.0-preview.2-darwin-arm64.dmg` | `4575c598d904b9fda39b5bdf366b39472dde223e14bfc7172e677f8884a45ed2` |

Commit: `9714904003e9249ec4541e774a5445eedffedf2f`

---

## 0.1.0-preview.1 — 2026-09-07

**定位**：首个体验版（内部验证 + 早期用户体验收集）

### 平台与签名状态

| 平台 | 架构 | 构建 | 签名 | 公证 | 可分发 |
|------|------|------|------|------|--------|
| macOS | Apple Silicon (arm64) | ✅ | ✅ Developer ID | ✅ Accepted + Stapled | ✅ **可发版** |
| macOS | Intel (x64) | ✅ | ⏳ 待签名 | ⏳ 待公证 | 内部可用（`xattr -cr`） |
| Windows | x64 | ✅ 已产出 247.8MB setup.exe | ⏳ 待证书 | 无此步骤 | ⏳ 待签名 + 待 443 |

Windows 包已在 09-08 打出并逐项验证（见下方「首个 Windows 产物」），
但**两个前置未完成**：无代码签名证书，服务端 443 未部署。逐条见
[windows-x64-publish.md](./windows-x64-publish.md) 的「现状」与「发版前置条件」。

> **Windows 无 32 位构建**。`win32` 是平台名（Win32 API）不是位数，`win32-x64` 读作
> 「Windows / x64」；上游 VS Code 早已删除 ia32 支持。唯一目标是 `win32 x64`。

### 包含内容

- **编辑器基座**：Code OSS 1.100.0 Fork
- **Go 后端**：wesgine v1.0 引擎（CKG 代码知识图谱 + tree-sitter 解析）
- **Web 控制台**：React 19 + Vite 7
- **中文语言包**：zh-CN 内置
- **预装技能**：26 个编程 Tier C 技能

### 本版修复

- **P1** 修复 `Service.Close()` 与后台索引 goroutine 的 `tsPool` 数据竞态（engine.go / background.go）
- **P2** 修复 `ClearProject` 的 root 校验：拒绝 `/` 等危险路径 + LIKE 通配符转义 + 错误检查（codeintel/index.go）
- **P2** 修复 `handleUpdateCronJob` 对类型错误字段的静默忽略：与 `provider_id` 分支对齐，统一返回 invalidParams（rpc/handler_cron.go）
- **info** 删除 `EnsureCronSession` 死代码 + 误导性注释（engine/data.go）

### 打包与基础设施（本版新建）

- **DMG 安装界面**：品牌背景图 + 图标对称布局 + 拖拽安装引导（对标 QQ/微信）。构建契约见 [dmg-installer-ui.md](./dmg-installer-ui.md)
- **跨架构打包**：支持在 Apple Silicon 上产出可用的 Intel 包（`scripts/rebuild-native-modules.sh`）。方法见 [cross-arch-build.md](./cross-arch-build.md)
- **签名证书**：新建 Developer ID Application 证书（G2 Sub-CA，SHA-1 `CAD585B5...`，到期 2031/09/08）。流程见 [create-developer-id-certificate.md](./create-developer-id-certificate.md)
- **公证凭据**：`wescode-notary` keychain profile 已配置（`xcrun notarytool`）
- **发版体检**：`scripts/preflight-macos-release.sh`——6 项只读检查，随时可跑

### Mac App Store 评估（2026-09-07）

**结论：wescode 不适合上 Mac App Store，走公证直接分发（与 VS Code / Cursor 同路线）。**

| 阻塞项 | 原因 |
|--------|------|
| App Sandbox 未启用 | Mac App Store 强制要求，当前 entitlements 无 `com.apple.security.app-sandbox` |
| 终端（node-pty）与沙盒冲突 | PTY 终端需要 `fork` 子进程，沙盒默认禁止 |
| Go 后端子进程 | spawn `backend/bin/wescode` 二进制，沙盒限制子进程执行 |
| 文件系统自由访问 | 编辑器核心能力需读写任意目录，沙盒只允许用户授权路径 |

行业参照：VS Code（微软官方）、Cursor、Sublime Text **均不在 Mac App Store 上**，根因相同。
如未来需上架（如精简版去掉终端），需重建 Mac App Distribution + Mac Installer Distribution 证书并做沙盒化适配。

### Windows x64 兼容性与打包（2026-09-07 本版新建）

完整手册：**[windows-x64-publish.md](./windows-x64-publish.md)**（已在真实 Windows 10 x64 机器上逐条实测）

#### 打包链路

- **安装方式定为 user setup**：装到 `%LOCALAPPDATA%`、无 UAC、支持后台静默更新，与 VS Code 官方默认一致。
  平台 key `win32-x64-user`。**首发后不可改**——两种 setup 的 AppId 与 key 都不同，改了会让老客户端
  去问一个 manifest 里不存在的 key，而 update-server 对未知 key 返回 204，症状是「检查更新」永远回「已是最新」
- **安装包版本号改用 `product.productVersion`**：此前用 `package.json` 的引擎版号，
  「应用和功能」里永远显示 `1.100.0`。`VersionInfoVersion` 另取剥掉预发布后缀的 `x.y.z`（Inno 只接受数字）
- **品牌漂移修正**：`electron.ts` 已改成 Weisyn，但 gulp 实际加载的编译产物 `electron.js` 停在初始导入版本，
  于是 `WES Code.exe` 的 CompanyName 是 `Microsoft Corporation`。共 4 处值漂移（含 `darwinHelpBook*`），
  已对齐——**这一条同时影响 macOS 包**（copyright 进 Info.plist）
- **`make package-win-installer` 可用**：Makefile 有 Windows 分支派发到 Git Bash，旧文档「不要用 make」已过时
- **`app` 格式不再依赖 `zip`**：Git for Windows 不带 `zip`，而该分支只在 Windows 上跑，
  于是 30 分钟构建后死在最后一步。回退到 Win10+ 自带的 bsdtar
- **`publish-release.sh` 加依赖前置检查**：缺 rsync（Git Bash 没有）时原先的失败点在脚本尾部——
  远程锁已拿到、sha 已算完才报 command not found，还要手动去解锁

#### 自动更新安全（Windows 独有）

`updateUrl` 改 HTTPS，nginx 加 443 块（80 保留明文不做 301，供已发布的 mac 客户端）。

Windows 比 macOS 更依赖这条：更新链是「明文下载 setup.exe → 校验 sha256 → `/verysilent` 静默执行」，
而 sha256 与安装包走同一条信道，中间人同时控制两者，校验不构成防护；安装包未签名时，
从下载到执行没有任何一层会拒绝。macOS 侧由 Squirrel 校验新 bundle 的代码签名兜底，
**Inno Setup 路径没有等价校验**。所以对 Windows 而言 HTTPS 与签名不是两件独立的优化，是同一道防线的两半。

#### 运行时兼容性（7 类，均为 Windows 专属）

| 问题 | 症状 |
|------|------|
| CKG `file_path` 存原生反斜杠，而所有包路径谓词是字面量 `LIKE '%/pkg/%'` | 跨包歧义组召回失效（违反 INV-CKG-EDGE-01）、`%/e2e/%` 排除失效（测试代码混进检索结果）、`ClearProject` 静默空操作。收敛到 `codeintel.IndexPath` 单点 + schemaVersion 6 清库重建 |
| `BufferStore.normalizePath` 未做大小写归一 | 编辑器 fsPath 与工具路径大小写不同时 overlay miss，AI `read` 静默读到磁盘旧内容——正是 Buffer Overlay 要防的那件事 |
| index worker 只设 `Cancel` 未设 `WaitDelay` | Windows 无进程组、靠 taskkill 杀树，孙进程持着 stdout 时 `Wait()` 无期限阻塞。两半收敛到 `setProcGroup` 单点 |
| 前端绝对路径判定 `startsWith('/')` | Windows 三种绝对路径形式（`F:\x`、`F:/x`、`\\server\share`）都不以 `/` 开头 → 点调用图节点/文件链接报 file not found；拖拽附件还多一层（`file:///F:/x` 剥 scheme 后剩 `/F:/x`，能过判定却不是路径） |
| `RelatedChangeHint` 拿 `filepath.Dir` 结果比 Go 包路径 | `internal\auth` ≠ `internal/auth` → 测试失败时「你这轮改的就是这个包」的提示永不出现 |
| `wsintel` Markers 用原生分隔符 | 进系统提示词的字符串变 `backend\go.mod`，并打乱刻意维持的排序（该列表进 prompt cache，顺序需跨机器稳定） |
| **EditorState 的路径未在 RPC 边界归一**（`handler_editor.go`）| 正斜杠归一引入的回归，也是它最不显眼的一处。前端发 `uri.fsPath`（原生），而下游拿它去比 CKG：`sym.FilePath == state.FocusFile`（两处）、`fragments[i].Path == focusFile`、以及 `PathProximity` 的 `splitSlash` 分段。症状不是报错——检索静默失去焦点文件的邻近度加权、相关符号去重和「用户问的是不是这个文件」的判断，模型拿到更差的上下文而没有任何提示。归一放在 `handleEditorState` 一处（`FocusFile` / `OpenFiles` / `VisibleEditors` / `RecentEdits[].Path` / `GlobalErrors[].File` / `GitStagedFiles`），而不是让每个比较点各自记得 |
| `.git/` 过滤与两处 basename 显示 | `filePath.includes('.git/')` 与 `split('/').pop()` 在原生路径上失效 → CodeLens 对 `.git\` 下的文件照发 CKG 查询；「AI 修改: 」标签和附件失败提示显示整段路径而非文件名。收敛到 `isUnderGitDir()` / `fileBasename()` |
| `wescode bench` 硬编码 `sh -c` | Windows 无 sh，且报错是「找不到可执行文件」，读起来像脚本里的命令缺失。收敛到 `platform.ShellCommand` |

#### 闸门本身在 Windows 上不可用（4 类，都是「在成功那一刻失败」）

| 闸门 | 根因 |
|------|------|
| `make fmt-check` | `core.autocrlf=true` 让 372 个 `.go` 文件 checkout 成 CRLF，而 gofmt 要求 LF → 每个文件都被标成未格式化，而它建议的 `gofmt -w .` 会产生全仓 diff。仓根加 `.gitattributes` 钉 `eol=lf` |
| `make notify-contract`（INV-WS-12） | 同一根因：闸门按字节比生成物，CRLF 必然不等；而它打印的「Regenerate」会把文件写成 LF、提交一次全文件行尾变更。`editor/.gitattributes` 钉住该文件 |
| `check-tool-schemas.py` / `check-layout-tree.py` | Windows 上 Python stdout 用 ANSI 代码页（GBK），编不了成功标记 `✓`——检查通过了，脚本以 exit 1 退出 |
| `check-doc-filerefs.py` | `read_text()` 未指定编码，用 GBK 读 UTF-8 文档。GBK 是双字节编码，`（` 的尾字节会**吞掉紧跟的反引号**，token 边界消失 → 引用提取不到、豁免看起来未使用。**判断在两个方向上都不可信**，而它自己的 vacuity guard 挡不住（纯 ASCII 引用够多）。修完后扫到 357 个引用、16 条豁免全部在用 |

#### 引擎侧（wesgine）——两条会随 Windows 构建发出去的 bug

这两条不在 wescode 仓，但 wesgine 是 `go mod replace` 编译进产物的，所以是 Windows 发版阻塞项。

**① Tier E 技能在 Windows 构建里全部加载失败**（`internal/context/skill/loader.go`）

`splitFrontMatter` 要求 `strings.HasPrefix(content, "---\n")`，而 CRLF 文件以 `---\r\n` 开头 →
判定为假 → 技能被当成没有 front-matter → 因缺 description 被拒。

要紧的是 `skills/` 是编译时 `//go:embed` 进二进制的：**二进制里的字节取决于构建机的 checkout**。
Git for Windows 默认 `core.autocrlf=true`，所以在 Windows 上打的包会把 CRLF 的 SKILL.md 嵌进去，
运行时 `plan` / `agentic-retrieval` / `agentic-execution` / `delegation` / `memory-strategy` /
`investigation` / `skill-authoring` **七个引擎技能一个都不可用**，只留一条 WARN 日志——
症状读起来是「模型变笨了」。已让解析器接受 CRLF（这是第一道防线，因为你控制不了每台构建机的 git 配置），
并给 wesgine 补 `.gitattributes` 钉 `eol=lf` 作为第二道。连带修好 5 个 `skill not found` 类测试失败。

**② Windows 的 Cell 锁从来无法点名持锁者**（`internal/cell/resilience/lock_windows.go`）

Windows 字节范围锁是**强制的**而非 advisory：范围被独占时，其他句柄对该范围的 `ReadFile`
返回 ERROR_LOCK_VIOLATION。而锁的是**字节 0**，`writeLockHolderInfo` 的 `pid=` 恰好写在那里，
于是 `readLockPID` 对外部持锁者永远返回 0，错误信息里永远没有 PID。
拒绝是对的，诊断在结构上不可能——**比单纯漏掉更坏，因为错误里没有任何迹象表明它找过 PID**。
把锁移到 2^63 偏移（内容之外，SQLite 同一手法）：排他性不变（双方锁同一范围），持锁者变得可读。

同时补齐 Windows 侧缺的两件事：`readLockPID` 从 `lock_unix.go` 移到共享的 `lock.go`
（它只是文本解析，放在 unix 文件里导致 `lock_cross_process_test.go` 在 Windows 上编译不过，
于是唯一真正用第二个进程压真锁的测试**在锁原语最不同的那个平台上从未运行**），
以及同进程可重入分支（`LockFileEx` 按句柄计，第二个句柄会失败，而 Unix 侧对这种情况返回 no-op 锁；
答得不一样会让可重入性变成平台相关）。陈旧锁回收 Windows 不需要——内核在进程死亡时关闭句柄即释放锁，
这一条已写进注释说明是原语属性而非遗漏。

另外顺手修掉三条同类（都在 wesgine）：`readLockPID` 从 unix 文件移到共享文件后，
`internal/handledoc` 的生成物 stale 确认也是 CRLF（转 LF 即过，git 无 diff）；
`safeJoin` 对 POSIX 根路径在 Windows 上是「静默容纳」而非拒绝（containment 成立、无逃逸，
但同一个归档在两个平台行为不同）；`IsUnderTmp` 的 `/tmp` 分支用了平台分隔符，
在 Windows 上比较 `"/tmp\"` 这个不可能的前缀，于是该分支恒死——而模型在任何平台都会吐 POSIX 路径。

wesgine 在 Windows 上从 **81 通过 / 9 失败** 变为 **85 通过 / 5 失败**。残留 5 条均**不是** wescode 发版阻塞：

| 残留 | 性质 |
|------|------|
| `TestGetOrCreate_RegistryFaultIsNotACreate` | `t.TempDir()` 删不掉 `.cell.lock`——该错误路径有句柄未关。测试卫生，同时提示一处句柄泄漏 |
| `TestReconcileEngine_SurvivesUnwritableRoot` | Windows 上 `os.Chmod` 不能把目录变成不可写，测试造不出被测条件 |
| `TestChangesToOutputFiles_ExcludesDeleted` | POSIX 路径 fixture（`/workspace` 在 Windows 上不是绝对路径） |
| `TestLoadOrCreateKey_Permissions`（`mode 777 want 700`） | Windows 无 Unix 权限位，`os.MkdirAll(p, 0o700)` 对权限是 no-op。**显式约束确实未强制**；实际保护来自 `%LOCALAPPDATA%` 的 ACL 继承。要做到与 Unix 等价需写 Windows ACL |
| `internal/pathaccess` 8 条 | 同为 POSIX fixture。**生产逻辑已核为 Windows 正确**——`checker.go:309-319` 有 `EqualFold` + `ToLower` 前缀比较，不存在 DenyPaths 大小写绕过。这是测试覆盖缺口，不是漏洞 |

#### 测试缺陷（此前 Windows 上从未绿过）

- `isTestBinary()` 用 `HasSuffix(base, ".test")`，Windows 上是 `codeintel.test.exe` → 恒为 false →
  **测试进程把自己当 index worker 拉起来**、flag 解析失败退出 2。`internal/codeintel` 整个包从未在 Windows 上通过，
  而它恰好是 spawn 代码平台差异最大的包
- `watcher_test.go` 6 处 `debounce = time.Nanosecond` 假设相邻两次 `time.Now()` 必然递增。
  Windows 上实测 `b.Sub(a) = 0s`，于是 `0 >= 1ns` 为假、`flushPending` 空转返回——没有行、没有日志，
  读起来像「分类器把什么都拒了」
- `bench_ckg_test.go` 未关 SQLite 句柄 → `t.TempDir()` 清理失败（Windows 拒绝删除有句柄的文件；
  POSIX 能 unlink 已打开的文件所以一直被掩盖）
- `agent_store_test.go` 的 fixture 缺 `busy_timeout`（生产有），20 个并发 Put 全部 `SQLITE_BUSY` 且错误被丢弃 →
  报成「restored 0 rows」，读起来像表损坏。pragma 抽成 `appDBPragmas` 单点
- `diff_test.go` 的注释断言「leading-separator roots are absolute on both platforms」——
  Windows 上 `filepath.IsAbs(\abs\x.go)` 是 **false**（无盘符即 drive-relative），
  于是被测的 passthrough 分支根本没被触发

**结果**：`go test ./...` 在 Windows 上全部包通过；`go build` / `go vet` / 三个 Python 闸门 /
`notify-contract` 的 tsc / web tsc / editor tsc 全部 exit 0。

#### 首个 Windows 产物（09-08）

`dist/windows-x64/WES Code-0.1.0-preview.1-win32-x64-setup.exe`，247.8 MB，
commit `b06d9f35`。逐项验证：

| 验证项 | 结果 |
|---|---|
| 安装方式 | 打进包的 product.json `target = "user"` → user setup，平台 key `win32-x64-user` |
| 版本号 | setup.exe `ProductVersion = 0.1.0-preview.1`（「应用和功能」显示这个）、`FileVersion = 0.1.0`（Inno 只接受纯数字）——不再是 1.100.0 |
| 品牌 | setup.exe 与内层 `WES Code.exe` 的 `CompanyName = Weisyn`、`LegalCopyright = Weisyn`（原先两处都是 Microsoft） |
| 更新身份 | `commit` 是 40 位 hash 且与 HEAD 一致、`updateUrl = https://file.weisyn.com/wescode` |
| 打包闸 | D-8（产物 `main.js` 无 Linux XDG 字面量）两次均过 |
| 资源 | Go 后端 `wescode.exe`、`web/dist`、zh-cn 语言包、CLI shim `bin/wescode.cmd` 全部就位 |
| 语言包闸门 | `✓ wescode (86 文件, 606 个 t(), 503 个 key, en 503 个)` |
| 签名 | `No signature found`（预期——无证书） |

**这一版用的是无 mangle 构建口径，与 macOS 那份不同**，原因见下。

#### mangler 在这台 Windows 机器上不可用（构建口径的已知偏差）

`vscode-win32-x64-min` 的 `compile-src` 带 mangler（TypeScript→TypeScript 全程序重命名，
只为压缩产物体积）。在这台机器上它**跑 85 分钟未完成且无任何进度输出**：
CPU 满跑一核、内存稳定在 6.9GB、`out-build` 零文件、40 秒窗口内零磁盘 I/O
（纯内存计算，符合它用 TS 语言服务对 10081 个导出符号逐个求引用的形状）。

**排除了内存假设**：把 `--max-old-space-size` 从 8192 提到 12288 后峰值反而是 6.7GB，
从未触顶——所以不是 GC 死循环，堆改动已撤回。

改走上游自己的 PR CI 路径（不是自造绕路）：

```bash
cd editor
npm run gulp -- compile-build-pr        # = compile-build 但 disableMangle，6.9 分钟，0 错误
npm run gulp -- vscode-win32-x64-min-ci # -ci 变体不含 compileBuildTask，复用 out-build，2.9 分钟
cd .. && SKIP_GULP=1 SKIP_WEB=1 SKIP_BACKEND=1 bash scripts/package.sh win32 x64 setup
```

**代价比预期小得多**：产物 247.8MB 里 Go 后端单独占 233MB，Electron 二进制占大头，
mangler 影响的那部分 JS 在这个量级上是噪声。所以「跳过 mangle」对 Windows 更像一个
可以长期成立的选择，而不只是权宜——但它需要一次并排体积对比来定论，尚未做。

发版前要么补这个对比、要么在一台更快的机器上测出 mangle 步骤的真实时间预算。

#### 一个被纠正的判断

先前把「CKG 边解析覆盖率 30.3%」归因给正斜杠 bug。归一后是 30.4%，基本没动——
30% 是这个代码库自身的属性（大量 `fmt.Errorf` 之类的标准库/外部调用目标在 symbols 表里没有行，
设计上解析不了），不是 Windows 回归。真正被修复的是具体的正确性保证：
`TestPass4_CrossPackageAmbiguousGroupRecall` 从红转绿、高确定性 orphan 从 380 降到 366
而候选总数不变 499（`NOT LIKE '%/e2e/%'` 排除终于生效的形状）。

### 已知限制

- **Windows 包未签名**：SmartScreen 会警告，企业 WDAC/AppLocker 会拒绝。需先采购证书（OV/EV 选型见 Windows 手册）
- **服务端 443 未部署**：客户端 `updateUrl` 已改 `https://`，但 nginx 的 443 块需要证书才能启用。
  在此之前不要 publish——`publish-release.sh` 会把 `baseUrl` 改成 https 并对**所有平台**生效，包括已发布的 mac 条目
- 扩展市场指向 Open VSX（非 Microsoft Marketplace）
- 自动更新服务器（`file.weisyn.com`）尚未部署
- Intel (x64) 包已构建但未签名/未公证
- Windows ARM64 未支持（`ArchitecturesAllowed=x64`，安装包在 ARM64 上会被 Inno 拒绝）
- 绿色版 zip 收不到自动更新（平台 key 为 `win32-x64-archive`，manifest 里无此键）

### 操作记录

| 时间 | 操作 | 结果 |
|------|------|------|
| 09-07 08:10 | 版本号 `0.0.1` → `0.1.0-preview.1` | ✅ |
| 09-07 08:14 | macOS arm64 打包（5 步流水线） | ✅ 604MB .app |
| 09-07 08:36 | DMG 安装界面第一版（AppleScript） | ❌ 背景图不显示 |
| 09-07 09:21 | DMG 改用 `build-dmg.py`（在卷上写 .DS_Store） | ✅ 背景图 + 图标就位 |
| 09-07 09:44 | DMG 布局微调（图标上移避免文字重叠） | ✅ |
| 09-07 09:56 | macOS x64 打包（跨架构，约 52 分钟） | ✅ 双架构产物齐备 |
| 09-07 10:57 | x64 包架构校验（Go + Electron + 6 个 .node 全部 x86_64） | ✅ |
| 09-07 10:57 | 开发环境还原（原生模块 → arm64） | ✅ |
| 09-07 11:46 | 生成 CSR（在构建机上，私钥留本地） | ✅ |
| 09-07 11:52 | Apple 后台创建新证书（G2 Sub-CA） | ✅ |
| 09-07 11:55 | 导入 .cer + 安装 G2 中间证书 | ✅ `1 valid identities found` |
| 09-07 12:00 | 导出 .p12 备份到桌面 | ✅ 密码 `407407` |
| 09-07 12:00 | 删除旧证书（`73D12B...`，无私钥） | ✅ |
| 09-07 12:11 | 配置公证凭据 `wescode-notary` | ✅ Credentials validated |
| 09-07 12:15 | arm64 签名（Developer ID + Hardened Runtime） | ✅ 深度校验通过 |
| 09-07 12:18 | 重打签名后的 DMG（build-dmg.py） | ✅ |
| 09-07 12:18 | 公证 zip（Submission `a5582418`） | ✅ Accepted |
| 09-07 12:35 | 公证 DMG（Submission `9a303e68`） | ✅ Accepted |
| 09-07 12:36 | Staple DMG | ✅ |
| 09-07 12:36 | 最终体检 `preflight-macos-release.sh` | ✅ 6/6 全绿 |
| 09-07 12:40 | Mac App Store 适配度评估 | 结论：不适合，走直接分发 |
| 09-07 15:00 | Windows 侧全面审查（打包 / 签名 / 更新 / 运行时） | 环境齐备，缺证书 |
| 09-07 15:30 | 六项发版前阻塞项（品牌 / 版本号 / user setup / HTTPS / BufferStore / WaitDelay） | ✅ |
| 09-07 16:00 | CKG `file_path` 正斜杠归一 + schemaVersion 6 | ✅ codeintel 首次在 Windows 全绿 |
| 09-07 17:00 | Windows 专属测试缺陷清扫（7 类运行时 + 4 类闸门 + 5 类测试） | ✅ `go test ./...` 全绿 |
| 09-07 18:00 | 行尾 renormalize + `.gitattributes` | ✅ `fmt-check` 首次可过（并抓到 1 处真实格式问题） |
| 09-07 18:20 | Windows 发版手册重写 + CHANGELOG 对齐 | ✅ 本节 |
| 09-07 19:00 | 前端 POSIX 路径假设清扫（14 条候选，核后 3 条为真） | ✅ 含一条归一引入的回归（EditorState） |
| 09-08 07:35 | Windows 首次完整打包（带 mangle） | ❌ 85 分钟未完成、零进度输出 |
| 09-08 08:28 | 提堆到 12GB 重试 | ❌ 峰值仅 6.7GB，排除内存假设，改动已撤回 |
| 09-08 08:39 | 改走 `compile-build-pr`（无 mangle） | ✅ 6.9 分钟 / 0 错误 / 9127 文件 |
| 09-08 08:47 | `vscode-win32-x64-min-ci` | ✅ 2.9 分钟 |
| 09-08 09:25 | 资源注入 + Inno Setup（user setup） | ✅ 247.8MB setup.exe |
| 09-08 09:30 | 产物八项验证（见「首个 Windows 产物」） | ✅ 全部通过 |

---

## 版本管理规范

### 产物目录结构

```
dist/
├── macos-arm64/                    ← make package-dmg-arm64
│   ├── WES Code.app
│   ├── WES Code-{VERSION}-darwin-arm64.zip    ← 自动更新
│   └── WES Code-{VERSION}-darwin-arm64.dmg    ← 首次安装
├── macos-x64/                      ← make package-dmg-intel
│   └── ...
└── windows-x64/                    ← make package-win-installer
    ├── app/                                        ← 绿色版（解压即用，收不到自动更新）
    └── WES Code-{VERSION}-win32-x64-setup.exe      ← user setup，自动更新 + 首次安装
```

### 发布流程

```bash
# 1. 打包（在目标平台本机执行）
make package-dmg-arm64          # macOS Apple Silicon
make package-dmg-intel          # macOS Intel（须在 Intel Mac 或交叉编译）

# 2. 签名（macOS，需 Developer ID 证书）
./scripts/sign-macos-arm64.sh

# 3. 重打签名后的 DMG（签名脚本产出的 zip 可用，DMG 需用 build-dmg.py 重打带背景图版本）
python3 scripts/build-dmg.py dist/macos-arm64/WES Code-<VER>-darwin-arm64.dmg \
  "dist/macos-arm64/WES Code.app" "WES Code" editor/resources/darwin/dmg-background.png

# 4. 公证
xcrun notarytool submit dist/macos-arm64/WES Code-<VER>-darwin-arm64.zip --keychain-profile wescode-notary --wait
xcrun notarytool submit dist/macos-arm64/WES Code-<VER>-darwin-arm64.dmg --keychain-profile wescode-notary --wait
xcrun stapler staple dist/macos-arm64/WES Code-<VER>-darwin-arm64.dmg

# 5. 体检（全绿才可发版）
./scripts/preflight-macos-release.sh arm64

# 6. 发布到 CDN
WESCODE_REMOTE=user@server:/data ./scripts/publish-release.sh darwin arm64
```

Windows 侧（**必须在 Windows 本机打包**，Electron 原生模块不能交叉编译）：

```bash
# 1. 打包（Git Bash 或 make，两者等价）
bash scripts/package.sh win32 x64 setup

# 2. 签名（Authenticode，无公证步骤）
#    ⚠️ 当前只签外层 setup.exe；内层 33 个 PE 的签名脚本待补
.\scripts\sign-windows-x64.ps1

# 3. 发布（在 macOS 上执行——Git Bash 没有 rsync）
WESCODE_REMOTE=user@server:/data ./scripts/publish-release.sh win32 x64
```

逐步操作、前置条件与常见问题见 **[windows-x64-publish.md](./windows-x64-publish.md)**。

### 版本号修改位置

唯一真相：`editor/product.json` 的 `productVersion` 字段。
引擎版本：`editor/package.json` 的 `version` 字段（扩展兼容性，与产品版本独立）。

### DMG 安装界面

由 `scripts/build-dmg.py` 构建（背景图 + 图标布局 + 拖拽安装引导）。
构建顺序不可变、两个已知坑、防回归判据见 **[dmg-installer-ui.md](./dmg-installer-ui.md)**。

**注意**：签名脚本 `sign-macos-app.sh` 内部用 `create-dmg` 打 DMG，但我们没安装它。
签名后需用 `build-dmg.py` 单独重打 DMG 才有背景图。已写入上方发布流程第 3 步。

依赖：`pip3 install ds_store`（含 `mac_alias`）。

### 分发方式

| 方式 | 状态 | 说明 |
|------|------|------|
| **公证直接分发（DMG + zip）** | ✅ 当前路线 | Developer ID 签名 + Apple 公证，与 VS Code / Cursor 同 |
| Mac App Store | ❌ 不适用 | 沙盒与编辑器核心能力（终端/子进程/文件系统）根本性冲突 |
| 自动更新（Squirrel） | ⏳ 待部署 | 客户端已内置，需部署 `file.weisyn.com` update-server |
