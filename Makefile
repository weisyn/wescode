# wescode — build system
#
# ─── 日常开发 ──────────────────────────────────────────────────────────────────
#   make run           全量构建（backend + web + editor）后启动应用      ← 日常入口
#   make build         增量构建 backend + web（跳过 editor 编译，~5s）
#   make backend       仅构建 Go 二进制（Go 自带缓存，~1s）
#   make web           仅构建 Vite 前端（增量）
#   make editor        仅编译 Editor TypeScript（增量，首次 ~3min）
#   make watch-web     Vite 监听模式（web/src 变动自动 rebuild dist）
#   make vet           go vet + tsc 静态检查（含 notify 契约）
#   make notify-contract  仅查 Go↔TS 通知契约（~10s，加通知后先跑这个）
#   make clean         删除 backend/bin + web/dist（editor/out 保留）
#   make clean-all     同上 + 删 editor/out（慎用，重建需 ~3min）
#   make mac-arm64     交叉编译后端 → macOS Apple Silicon
#   make mac-intel     交叉编译后端 → macOS Intel (x64)
#   make windows       交叉编译后端 → Windows x64
#
#
# ─── 打包 ─────────────────────────────────────────────────────────────────────
#   make package-mac-arm64      macOS M 系列 .app
#   make package-mac-intel      macOS Intel .app
#   make package-dmg-arm64      macOS M 系列 .dmg
#   make package-dmg-intel      macOS Intel .dmg
#   make package-win            Windows 应用目录
#   make package-win-installer  Windows .exe 安装包

# ── 配置 ────────────────────────────────────────────────────────────────────────
BACKEND_DIR := backend
BACKEND_BIN := backend/bin/wescode
BACKEND_BIN_WIN := backend/bin/wescode.exe
UPDATE_SERVER_BIN_LINUX := backend/bin/wescode-update-server-linux-amd64

WEB_DIR  := web
WEB_OUT  := web/dist/assets/index.js
# WESUI_DIR: set this to your local wesui checkout

EDITOR_DIR := editor
EDITOR_OUT := editor/out/main.js

GOOS_DARWIN  := darwin
GOOS_WIN     := windows
GOARCH_ARM64 := arm64
GOARCH_AMD64 := amd64

# ── 平台检测 + SHELL ──────────────────────────────────────────────────────────
# macOS/Linux: 使用 /bin/bash（支持进程替换等 bash 特性）
# Windows w64devkit: 使用自带 sh（POSIX 兼容，不需要 bash）
ifeq ($(OS),Windows_NT)
  IS_WIN := 1
  FIND := find
  BACKEND_SRCS := $(shell find backend -name '*.go' -not -path '*/vendor/*' 2>/dev/null)
  WEB_SRCS := $(shell find web/src -type f 2>/dev/null) $(shell find $(WESUI_DIR)/src -type f 2>/dev/null)
  EDITOR_SRCS := $(shell find editor/src -name '*.ts' -not -path '*/__tests__/*' -not -path '*/test/*' 2>/dev/null)
  NODE_BIN := node
  # w64devkit sh 会优先找到 npm（bash 脚本）而非 npm.cmd，显式指定 .cmd 后缀
  NPM_BIN := npm.cmd
else
  SHELL := /bin/bash
  UNAME_S := $(shell uname -s 2>/dev/null || echo Windows)
  ifneq (,$(findstring MINGW,$(UNAME_S))$(findstring MSYS,$(UNAME_S))$(findstring CLANG,$(UNAME_S)))
    FIND := /usr/bin/find
  else
    FIND := find
  endif
  BACKEND_SRCS := $(shell $(FIND) backend -name '*.go' -not -path '*/vendor/*') \
                  $(shell $(FIND) backend/presets -name '*.yaml' 2>/dev/null)
  WEB_SRCS := $(shell $(FIND) web/src -type f 2>/dev/null) $(shell $(FIND) $(WESUI_DIR)/src -type f 2>/dev/null)
  EDITOR_SRCS := $(shell $(FIND) editor/src -name '*.ts' -not -path '*/__tests__/*' -not -path '*/test/*' 2>/dev/null)
  NODE22_BIN_DIR := $(shell \
	if [ -x "/opt/homebrew/opt/node@22/bin/node" ]; then echo "/opt/homebrew/opt/node@22/bin"; \
	elif [ -x "/usr/local/opt/node@22/bin/node" ]; then echo "/usr/local/opt/node@22/bin"; \
	else echo ""; fi)
  NODE_BIN := $(shell if [ -n "$(NODE22_BIN_DIR)" ] && [ -x "$(NODE22_BIN_DIR)/node" ]; then echo "$(NODE22_BIN_DIR)/node"; else command -v node; fi)
  NPM_BIN := $(shell if [ -n "$(NODE22_BIN_DIR)" ] && [ -x "$(NODE22_BIN_DIR)/npm" ]; then echo "$(NODE22_BIN_DIR)/npm"; else command -v npm; fi)
endif

DIST_DIR := dist
PACKAGE_SCRIPT := scripts/package.sh

# ── .PHONY ───────────────────────────────────────────────────────────────────
.PHONY: all run dev build backend web editor watch-web vet lint lint-editor-guard fmt-check \
        notify-contract layout-tree tool-schemas doc-filerefs truncation-counts notready-wiring \
        clean clean-all clean-dist \
        mac-arm64 mac-intel windows \
        bump release-arm64 release-intel \
        package-mac-arm64 package-mac-intel \
        package-dmg-arm64 package-dmg-intel package-release-arm64 package-release-intel \
        package-win package-win-installer package-release-win \
        release-win publish-arm64 publish-intel publish-win update-server update-server-linux \
        _launch _native-modules _wesui-deps

# ── 默认目标 ─────────────────────────────────────────────────────────────────
all: run

# ── run：全量增量构建 → 启动 ─────────────────────────────────────────────────
# backend 是 PHONY（Go 自己判断是否重编），web/editor 按文件时间戳增量。
run: backend $(WEB_OUT) $(EDITOR_OUT)
	@$(MAKE) _launch

_launch: _native-modules
ifdef IS_WIN
	@echo "→ 启动 wescode…"
	cd $(EDITOR_DIR) && cmd /c scripts\\code.bat
else
	@rm -rf "$(HOME)/Library/Application Support/wescode/clp" "$(HOME)/Library/Application Support/code-oss-dev/clp" "$(HOME)/.wescode/clp" 2>/dev/null || true
	@echo "→ 启动 wescode…"
	cd $(EDITOR_DIR) && NODE_BIN="$(NODE_BIN)" PATH="$(dir $(NODE_BIN)):$$PATH" ./scripts/code.sh 2> >(grep -Ev "ERROR:gl_display|eglQueryDeviceAttribEXT" >&2)
endif

# ── 原生模块检查（每次启动前执行，独立于 TypeScript 编译）──────────────────
# 必须用 node-gyp 显式指定 --target=<electron> --runtime=electron，
# npm rebuild 不会正确传递 .npmrc 中的 Electron target（ABI 不匹配）。
ELECTRON_VER := $(shell node -e "console.log(require('./$(EDITOR_DIR)/package.json').devDependencies.electron)" 2>/dev/null)

_native-modules:
ifdef IS_WIN
	@echo "[skip] Windows 原生模块检查（使用预编译或 npm rebuild）"
else
	@HOST_ARCH=$$(uname -m | sed 's/x86_64/x64/' | sed 's/aarch64/arm64/'); \
	OS=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
	ok=true; \
	for mod in node-pty native-keymap @vscode/spdlog @vscode/sqlite3 native-watchdog; do \
		nf=$$(find $(EDITOR_DIR)/node_modules/$$mod/build/Release -name "*.node" 2>/dev/null | head -1); \
		if [ -z "$$nf" ] || ! file "$$nf" | grep -q "$$HOST_ARCH"; then ok=false; break; fi; \
	done; \
	if [ "$$ok" = false ]; then \
		echo "→ 原生模块需要安装（Electron $(ELECTRON_VER), $$HOST_ARCH）…"; \
		if command -v cc >/dev/null 2>&1; then \
			for mod in node-pty native-keymap @vscode/spdlog @vscode/sqlite3 native-watchdog; do \
				echo "  → $$mod"; \
				(cd $(EDITOR_DIR)/node_modules/$$mod && CXXFLAGS="-std=c++20" PATH="$(dir $(NODE_BIN)):$$PATH" \
					npx node-gyp rebuild --target=$(ELECTRON_VER) --arch=$$HOST_ARCH \
					--dist-url=https://electronjs.org/headers --runtime=electron 2>&1 | tail -1); \
			done; \
		elif [ -d "$(EDITOR_DIR)/prebuilds/$$OS-$$HOST_ARCH" ]; then \
			echo "  无编译器，从预编译兜底…"; \
			for mod in node-pty native-keymap; do \
				if [ -d "$(EDITOR_DIR)/prebuilds/$$OS-$$HOST_ARCH/$$mod" ]; then \
					mkdir -p $(EDITOR_DIR)/node_modules/$$mod/build/Release; \
					cp -f $(EDITOR_DIR)/prebuilds/$$OS-$$HOST_ARCH/$$mod/* $(EDITOR_DIR)/node_modules/$$mod/build/Release/; \
					echo "  ✓ $$mod（预编译）"; \
				fi; \
			done; \
		else \
			echo "  ⚠ 无编译器且无预编译，终端功能不可用"; \
		fi; \
		echo "✓ 原生模块就绪"; \
	fi
endif

# ── dev：增量构建后直接启动（backend + web + editor，editor 未改时秒级跳过）──
# 曾经这里跳过 editor 只打 skew 警告，结果 settingsPane.ts 等 editor 侧改动
# 静默不生效——make dev 报成功、UI 里新加的菜单不出现。现在统一依赖
# $(EDITOR_OUT)，Make 时间戳增量判断：没改 editor → 零开销；改了 → 自动编译。
dev: backend $(WEB_OUT) $(EDITOR_OUT)
	@$(MAKE) _launch

# ── build：全量增量构建（backend + web + editor，不启动）────────────────────
# 依赖与 run / dev 一致：editor 未改时只做时间戳检查（秒级），改了才编。
build: backend $(WEB_OUT) $(EDITOR_OUT)
	@echo "✓ build 完成（backend + web + editor）"

# ── backend：Go 二进制（PHONY，交给 Go 缓存判断增量）────────────────────────
ifdef IS_WIN
backend:
	@mkdir -p $(BACKEND_DIR)/bin
	cd $(BACKEND_DIR) && go build -o bin/wescode.exe ./cmd/wescode
	@echo "✓ backend: $(BACKEND_BIN_WIN)"
else
backend:
	@mkdir -p $(BACKEND_DIR)/bin
	cd $(BACKEND_DIR) && go build -o bin/wescode ./cmd/wescode
	@echo "✓ backend: $(BACKEND_BIN)"
endif

# ── wesui：确保共享组件库依赖已安装（tsc 按文件物理位置解析 node_modules）──
_wesui-deps:
	@test -d "$(WESUI_DIR)/node_modules/react" || (echo "installing wesui deps..." && cd "$(WESUI_DIR)" && $(NPM_BIN) install --prefer-offline)

# ── web：Vite 增量构建 ────────────────────────────────────────────────────────
$(WEB_OUT): $(WEB_SRCS) web/package.json web/package-lock.json | _wesui-deps
	cd $(WEB_DIR) && $(NPM_BIN) install && $(NPM_BIN) run build
	@echo "✓ web: $(WEB_OUT)"

web: $(WEB_OUT)

# ── editor：TypeScript 增量编译（首次 ~3min，后续按文件时间戳跳过）───────────
$(EDITOR_OUT): $(EDITOR_SRCS)
	@echo "→ 编译 Editor TypeScript（首次约 3 分钟）…"
ifdef IS_WIN
	@test -d "$(EDITOR_DIR)/node_modules" || (echo "editor: npm install..." && cd "$(EDITOR_DIR)" && $(NPM_BIN) install)
	cd $(EDITOR_DIR) && node --max-old-space-size=8192 node_modules/gulp/bin/gulp.js compile
else
	@chmod +x scripts/compile-editor.sh
	@./scripts/compile-editor.sh
endif
	@echo "✓ editor: $(EDITOR_OUT)"

editor: $(EDITOR_OUT)

# ── watch-web：Vite 监听模式（web/src 变动自动 rebuild，无需重启）──────────
# 配合 wescode 开发：修改 React 代码后自动更新 web/dist，手动在 VSCode 中
# Ctrl+Shift+P → "Reload Webview" 即可看到最新 UI。
watch-web:
	cd $(WEB_DIR) && npm run build -- --watch

# ── vet：静态检查 ─────────────────────────────────────────────────────────────
vet: _wesui-deps fmt-check notify-contract layout-tree tool-schemas doc-filerefs check-inv-citations truncation-counts notready-wiring
	cd $(BACKEND_DIR) && go build ./... && go vet ./...
	cd $(WEB_DIR) && npx tsc --noEmit
	@echo "✓ vet 通过"

# ── tool-schemas：模型看得到的 Layer C 工具集 == 附录的 layer: C 条目集 ──────
# 与 layout-tree / notify-contract 同族（文档声称的东西编译器看不见），但这条守的
# 缺陷两个方向都出现过：附录曾缺 22 个已注册工具，同时多出 6 个根本不是 tool 的
# 条目（私有 JSON-RPC 与透明 HostEnvironment）。照它去实现的人会得出「模型能直接
# 调 wescode_show_diff」这个结论，然后花时间查为什么调不通。
# 判据取自 engine.go 的 doReg，不取自任何手抄清单。
tool-schemas:
	@python3 scripts/check-tool-schemas.py

# ── doc-filerefs：文档里点名的源文件必须真的存在 ─────────────────────────────
# layout-tree 守目录在不在树里（索引完整性），这条守文件引用指不指得到东西
# （索引准确性）。2026-09-04 普查一次撞到 5 个错名，其中 `internal/engine/host.go`
# 的形状最典型：文件被合进 deviceagent/，`host_test.go` 留了下来，三份文档跟着
# 那个消失的名字走；`internal/deviceagent/doc.go` 更是被两处 [x] 声称承载架构文档，
# 而它从未存在。编译器对此一言不发——文档不参与编译。
doc-filerefs:
	@python3 scripts/check-doc-filerefs.py

# ── check-inv-citations：注释里引用的不变量 ID 必须在注册表里有定义 ──────────
# 与 doc-filerefs 同族（引用必须解析），但守的是 ID 而非路径：一句引用了不存在
# 或已被悄悄重定义的不变量的注释，编译通过、测试全绿，然后以权威腔调撒谎。
# 脚本自己的 docstring 早就写着「wired into make vet」——而它此前不在这里，
# 正是它被写来防的那件事。
check-inv-citations:
	@python3 scripts/check-inv-citations.py

# ── layout-tree：AGENTS.md 的布局树必须与文件系统一致 ────────────────────────
# 与 notify-contract 同族：都是"文档声称的东西编译器看不见"。这条守的是索引——
# 树漏掉 notify/ 或 22-logging.md 时，按树找路的人走不到那里，而这份文档整篇
# 在讲找路税。双向：文件系统多出来要补树，树里点名的文件不存在要改树。
layout-tree:
	@python3 scripts/check-layout-tree.py

# ── truncation-counts：截断之后报出去的数字必须是截断前的总数 ─────────────────
# 与上面几条不同族：那些守"文档声称的东西编译器看不见"，这条守**编译器看得见一半**。
# 截断器签名已改成返回 (shown, total)，新调用点必须写出 total 才能编译——但裸切片
# （`x = x[:limit]`，上限来源各不相同故走不到 helper）绕过它，而那条路径上已出现过 3 处。
# 2026-09-20 普查：11 个工具、13 处报的是截断后的数字，`find_references` 说 3 处引用而
# 实际 30，于是模型直接改签名。详见 design/27-presentation-contract.md。
truncation-counts:
	@python3 scripts/check-truncation-counts.py

# ── notready-wiring：能力不可用的分支必须置 notReady（PC-02）──────────────────
# 与 truncation-counts 同族（都守"漏了不报错"），但这条守的是**语义**而非数字：
# "索引还没建好"与"查到 0 个"在前端同形，而 notReady 是分支主动说的，漏掉只是让
# 信封落 empty。2026-09-20 首次普查 28 条里 6 条没接线，全是同一次批量迁移漏的。
notready-wiring:
	@python3 scripts/check-notready-wiring.py

# ── notify-contract：Go↔TS 通知契约（构建期，非运行期）──────────────────────
# 加一个 notify.Method 而忘了在 electron-main 接线，此前的症状是「那个功能悄悄
# 什么都不做」——channelEvent（扫码登录 QR 到不了设置页）与
# codeintel/index-complete（调用图缓存永不全局失效）都这么上过线。
#
# 两跳，顺序承重：
#   1. go test ./internal/notify — 断言生成的 TS 联合类型等于 method.go，
#      且每个 Method 在 backend/ 里真有发送方。跳过它，第 2 跳会拿一份过期的
#      联合类型自信地判「全覆盖」。
#   2. 作用域内 tsc — dispatch 的 switch 以 assertNever 收尾，所以联合里少一个
#      分支就是类型错误。
#
# 只 typecheck 一个文件是**范围声明**，不是第二份契约（名字只活在 Go 里）：
# tsc 跟随 import，这一个文件拉起 ~700 个文件 / ~7s，而整个 workbench 是
# ~5000 / ~3min。`make editor` 会覆盖它，但 vet 不跑 editor，于是 vet 曾能在
# 一棵编译不过的树上全绿。
notify-contract:
	cd $(BACKEND_DIR) && go test ./internal/notify
	@test -d "$(EDITOR_DIR)/node_modules" || (echo "editor: npm install..." && cd "$(EDITOR_DIR)" && $(NPM_BIN) install)
	cd $(EDITOR_DIR) && ./node_modules/.bin/tsc -p src/tsconfig.wescode-notify.json
	@echo "✓ notify 契约：Go 的每个通知在 electron-main 都有分支"

# ── fmt-check：gofmt 门（fail-closed）────────────────────────────────────────
fmt-check:
	@out=$$(cd $(BACKEND_DIR) && gofmt -l .); \
	if [ -n "$$out" ]; then \
	  echo "✗ gofmt 未格式化："; echo "$$out"; \
	  echo "  修复：cd $(BACKEND_DIR) && gofmt -w ."; \
	  exit 1; \
	fi
	@echo "✓ gofmt 通过"

# ── audit-inv：强制工程纪律（扫描代码注释中的 @inv-* 指令）─────────────────
audit-inv:
ifdef IS_WIN
	@python scripts/audit-inv.py
else
	@python3 scripts/audit-inv.py
endif

# ── lint：golangci-lint 深度静态分析 + 编辑器侧回归闸门 ──────────────────────
lint: lint-editor-guard
	cd $(BACKEND_DIR) && golangci-lint run --timeout 120s ./...

# LSP bridge 不得开编辑器（"启动打开一个文件"回归）。独立配置，不复用
# editor/eslint.config.js —— 那份对 wescode/ 现有 33 个版权头与分层错误报错，
# 闸门挂上去会红在与它守护的东西无关的地方，然后被人关掉。
lint-editor-guard:
	@test -d "$(EDITOR_DIR)/node_modules" || (echo "editor: npm install..." && cd "$(EDITOR_DIR)" && $(NPM_BIN) install)
	cd $(EDITOR_DIR) && ./node_modules/.bin/eslint --no-config-lookup \
	  -c eslint.guard.config.js src/vs/workbench/contrib/wescode/browser/lsp/
	@echo "✓ editor guard: LSP bridge 未开编辑器"

# ── 交叉编译后端 ─────────────────────────────────────────────────────────────
mac-arm64:
	@mkdir -p $(BACKEND_DIR)/bin
	cd $(BACKEND_DIR) && GOOS=$(GOOS_DARWIN) GOARCH=$(GOARCH_ARM64) \
	  go build -o bin/wescode ./cmd/wescode
	@echo "✓ macOS arm64: $(BACKEND_BIN)"

mac-intel:
	@mkdir -p $(BACKEND_DIR)/bin
	cd $(BACKEND_DIR) && GOOS=$(GOOS_DARWIN) GOARCH=$(GOARCH_AMD64) \
	  go build -o bin/wescode ./cmd/wescode
	@echo "✓ macOS x64: $(BACKEND_BIN)"

windows:
	@mkdir -p $(BACKEND_DIR)/bin
	cd $(BACKEND_DIR) && GOOS=$(GOOS_WIN) GOARCH=$(GOARCH_AMD64) \
	  go build -o bin/wescode.exe ./cmd/wescode
	@echo "✓ Windows x64: $(BACKEND_BIN_WIN)"

# ── 版本号 + 打包（社区开发者用 package-* 即可）───────────────────────────────

bump:
	@node -e " \
	  const fs = require('fs'); \
	  const p = 'editor/product.json'; \
	  const j = JSON.parse(fs.readFileSync(p, 'utf8')); \
	  const v = j.productVersion; \
	  const m = v.match(/^(.+[-.])(\\d+)\$$/) ; \
	  if (!m) { console.error('无法自增: ' + v + '（需以 .N 或 -N 结尾）'); process.exit(1); } \
	  const next = m[1] + (parseInt(m[2]) + 1); \
	  j.productVersion = next; \
	  fs.writeFileSync(p, JSON.stringify(j, null, '\\t') + '\\n'); \
	  console.log('bump: ' + v + ' → ' + next); \
	"

# release-arm64 / release-intel: internal release pipeline (not included in OSS)
# Use package-mac-arm64 / package-mac-intel to build locally.

# ── 安装包打包（详见 scripts/package.sh）────────────────────────────────────
# Electron 原生模块必须在目标平台本机构建；Windows 目标须在 Windows 上执行。
package-mac-arm64:
	@chmod +x $(PACKAGE_SCRIPT)
	@$(PACKAGE_SCRIPT) darwin arm64 app

package-mac-intel:
	@chmod +x $(PACKAGE_SCRIPT)
	@$(PACKAGE_SCRIPT) darwin x64 app

package-dmg-arm64:
	@chmod +x $(PACKAGE_SCRIPT)
	@$(PACKAGE_SCRIPT) darwin arm64 dmg

package-dmg-intel:
	@chmod +x $(PACKAGE_SCRIPT)
	@$(PACKAGE_SCRIPT) darwin x64 dmg

package-release-arm64:
	@chmod +x $(PACKAGE_SCRIPT)
	@$(PACKAGE_SCRIPT) darwin arm64 release

package-release-intel:
	@chmod +x $(PACKAGE_SCRIPT)
	@$(PACKAGE_SCRIPT) darwin x64 release

package-win:
ifeq ($(OS),Windows_NT)
	cmd /c "scripts\package-win.cmd win32 x64 app"
else
	@chmod +x $(PACKAGE_SCRIPT)
	@$(PACKAGE_SCRIPT) win32 x64 app
endif

package-win-installer:
ifeq ($(OS),Windows_NT)
	cmd /c "scripts\package-win.cmd win32 x64 setup"
else
	@chmod +x $(PACKAGE_SCRIPT)
	@$(PACKAGE_SCRIPT) win32 x64 setup
endif

package-release-win:
ifeq ($(OS),Windows_NT)
	cmd /c "scripts\package-win.cmd win32 x64 release"
else
	@chmod +x $(PACKAGE_SCRIPT)
	@$(PACKAGE_SCRIPT) win32 x64 release
endif

# publish / release-win / update-server: internal pipeline (not included in OSS)

clean-dist:
	rm -rf $(DIST_DIR)
	@echo "✓ 已删除 $(DIST_DIR)/"

# editor/out 重建耗时 ~3min，默认保留
clean:
	rm -rf $(BACKEND_DIR)/bin
	rm -rf $(WEB_DIR)/dist
	@echo "✓ 已删除 backend/bin + web/dist（editor/out 已保留）"

clean-all: clean
	rm -rf $(EDITOR_DIR)/out
	@echo "✓ 已删除 editor/out（下次 make run 需重新编译 TypeScript）"
