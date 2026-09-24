#!/bin/bash
# wescode 安装包构建脚本
#
# 用法:
#   ./scripts/package.sh darwin arm64 [app|dmg|release]
#   ./scripts/package.sh darwin x64   [app|dmg|release]
#   ./scripts/package.sh win32 x64    [app|setup|release]
#
# 产物输出到 dist/ 目录。
#
# 环境变量（gulp 已成功、仅需重跑后续步骤时）:
#   SKIP_WEB=1      跳过 web/dist 构建（需已有 web/dist）
#   SKIP_BACKEND=1  跳过 Go 交叉编译（需已有 backend/bin 二进制）
#   SKIP_GULP=1     跳过 Electron gulp（需已有 VSCode-darwin-* / VSCode-win32-*）

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
EDITOR_DIR="$ROOT_DIR/editor"
BACKEND_DIR="$ROOT_DIR/backend"
WEB_DIR="$ROOT_DIR/web"
DIST_DIR="$ROOT_DIR/dist"

PLATFORM="${1:-}"
ARCH="${2:-}"
FORMAT="${3:-app}"

usage() {
	cat <<'EOF'
用法: ./scripts/package.sh <platform> <arch> [format]

平台:
  darwin arm64   macOS Apple Silicon (M1/M2/M3)
  darwin x64     macOS Intel
  win32  x64     Windows x64

格式 (format):
  app            仅构建应用目录（默认）
  dmg            macOS: .app + .zip（自动更新）+ .dmg（首次安装）
  setup          Windows: 应用目录 + .exe 安装包
  release        同 dmg / setup，并写入 dist/.../release-manifest.json

示例:
  ./scripts/package.sh darwin arm64 dmg
  ./scripts/package.sh darwin arm64 release

注意:
  - Electron 原生模块必须在目标平台本机构建（不可跨平台交叉编译 editor）
  - macOS Intel 包需在 Intel Mac 上构建，或在 arm64 Mac 上设置 ALLOW_CROSS_ARCH=1（不推荐）
  - Windows 包必须在 Windows 上构建
  - gulp 已成功但后续步骤失败时: SKIP_GULP=1 SKIP_WEB=1 SKIP_BACKEND=1 ./scripts/package.sh ...

EOF
}

die() { echo "错误: $*" >&2; exit 1; }

if [ -z "$PLATFORM" ] || [ -z "$ARCH" ]; then
	usage
	exit 1
fi

case "$PLATFORM-$ARCH" in
	darwin-arm64|darwin-x64|win32-x64) ;;
	*) die "不支持的平台/架构: $PLATFORM $ARCH" ;;
esac

case "$FORMAT" in
	app|dmg|setup|release) ;;
	*) die "未知格式: ${FORMAT}（可用: app, dmg, setup, release）" ;;
esac

# release 等价于各平台完整发布包
if [ "$FORMAT" = "release" ]; then
	if [ "$PLATFORM" = "darwin" ]; then FORMAT=dmg; else FORMAT=setup; fi
	WRITE_RELEASE_MANIFEST=1
else
	WRITE_RELEASE_MANIFEST=0
fi

if [ "$PLATFORM" = "darwin" ] && [ "$FORMAT" = "setup" ]; then
	die "macOS 请使用 dmg 格式，而非 setup"
fi
if [ "$PLATFORM" = "win32" ] && [ "$FORMAT" = "dmg" ]; then
	die "Windows 请使用 setup 格式，而非 dmg"
fi

# ── Node.js ──────────────────────────────────────────────────────────────────
NODE22_BIN_DIR=""
if [ -x "/opt/homebrew/opt/node@22/bin/node" ]; then
	NODE22_BIN_DIR="/opt/homebrew/opt/node@22/bin"
elif [ -x "/usr/local/opt/node@22/bin/node" ]; then
	NODE22_BIN_DIR="/usr/local/opt/node@22/bin"
fi
if [ -n "$NODE22_BIN_DIR" ]; then
	NODE_BIN="$NODE22_BIN_DIR/node"
	NPM_BIN="$NODE22_BIN_DIR/npm"
else
	NODE_BIN="$(command -v node)"
	NPM_BIN="$(command -v npm)"
fi
[ -x "$NODE_BIN" ] || die "未找到 Node.js，请安装 Node 22"

# ── 平台检查 ───────────────────────────────────────────────────────────────────
HOST_OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
HOST_ARCH="$(uname -m | sed 's/x86_64/x64/' | sed 's/aarch64/arm64/')"

if [ "$PLATFORM" = "darwin" ] && [ "$HOST_OS" != "darwin" ]; then
	die "macOS 安装包必须在 macOS 上构建"
fi
if [ "$PLATFORM" = "win32" ]; then
	case "$HOST_OS" in
		mingw*|msys*|cygwin*|windows*) ;;
		*) die "Windows 安装包必须在 Windows 上构建（当前: $HOST_OS）" ;;
	esac
fi

if [ "$PLATFORM" = "darwin" ] && [ "$ARCH" != "$HOST_ARCH" ] && [ "${ALLOW_CROSS_ARCH:-}" != "1" ]; then
	die "架构不匹配: 目标 ${ARCH}，本机 ${HOST_ARCH}。Intel 包请在 Intel Mac 上构建，或设置 ALLOW_CROSS_ARCH=1"
fi

# ── 版本信息 ───────────────────────────────────────────────────────────────────
# 产品发版号 → product.json productVersion；引擎版本 → package.json version（扩展兼容）
# Windows Git Bash 下路径是 /c/... 格式，Node.js 不认；用 cygpath 转换为原生路径
_node_path() {
	if command -v cygpath >/dev/null 2>&1; then
		cygpath -m "$1"
	else
		echo "$1"
	fi
}
EDITOR_DIR_NATIVE="$(_node_path "$EDITOR_DIR")"

read_product_version() {
	"$NODE_BIN" -e "
const p=require('$EDITOR_DIR_NATIVE/product.json');
console.log(p.productVersion || require('$EDITOR_DIR_NATIVE/package.json').version);
" 2>/dev/null || echo "0.0.0"
}
VERSION="$(read_product_version)"
ENGINE_VERSION="$("$NODE_BIN" -e "console.log(require('$EDITOR_DIR_NATIVE/package.json').version)" 2>/dev/null || echo "0.0.0")"
PRODUCT_NAME="$("$NODE_BIN" -e "console.log(require('$EDITOR_DIR_NATIVE/product.json').nameShort)" 2>/dev/null || echo "WES Code")"
COMMIT="$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || echo unknown)"

if [ "$PLATFORM" = "darwin" ]; then
	GULP_TASK="vscode-darwin-${ARCH}-min"
	BUILD_DIR="$(dirname "$EDITOR_DIR")/VSCode-darwin-${ARCH}"
	OUT_SUBDIR="macos-${ARCH}"
	BACKEND_BIN="$BACKEND_DIR/bin/wescode"
	GO_OS=darwin
	if [ "$ARCH" = "arm64" ]; then GO_ARCH=arm64; else GO_ARCH=amd64; fi
else
	GULP_TASK="vscode-win32-${ARCH}-min"
	BUILD_DIR="$(dirname "$EDITOR_DIR")/VSCode-win32-${ARCH}"
	OUT_SUBDIR="windows-${ARCH}"
	BACKEND_BIN="$BACKEND_DIR/bin/wescode.exe"
	GO_OS=windows
	GO_ARCH=amd64
fi

OUT_DIR="$DIST_DIR/$OUT_SUBDIR"
if [ -e "$DIST_DIR" ] && [ ! -d "$DIST_DIR" ]; then
	die "dist 存在但不是目录: $DIST_DIR"
fi
ensure_out_dir() {
	mkdir -p "$OUT_DIR" || die "无法创建输出目录: $OUT_DIR"
	[ -d "$OUT_DIR" ] || die "输出目录无效: $OUT_DIR"
}
ensure_out_dir

echo "=== wescode 打包 ==="
echo "平台:   $PLATFORM-$ARCH"
echo "格式:   $FORMAT"
echo "产品版本: $VERSION"
echo "引擎版本: $ENGINE_VERSION"
echo "Commit: $COMMIT"
echo "输出:   $OUT_DIR"
echo ""

# ── Step 1: Web ───────────────────────────────────────────────────────────────
if [ "${SKIP_WEB:-}" = "1" ] && [ -d "$WEB_DIR/dist" ]; then
	echo "→ [1/5] 跳过 Web（SKIP_WEB=1，已有 web/dist）"
else
	echo "→ [1/5] 构建 Webview (web/dist)…"
	cd "$WEB_DIR"
	"$NPM_BIN" install --silent
	"$NPM_BIN" run build
fi
echo "✓ web/dist 就绪"
echo ""

# ── Step 2: Go 后端 ───────────────────────────────────────────────────────────
# 构建指纹：记录源码树状态 + 目标架构，打包时校验防止用旧二进制。
# 指纹 = "GOOS/GOARCH wescode-backend-tree wesgine-HEAD wesapp-HEAD"
# 四段任一变化 = 必须重编。
FINGERPRINT_FILE="$BACKEND_DIR/bin/.build-fingerprint"

_current_fingerprint() {
	local bt wt at
	bt="$(git -C "$ROOT_DIR" rev-parse HEAD:backend 2>/dev/null || echo unknown)"
	wt="$(git -C "$ROOT_DIR/../wesgine.git" rev-parse HEAD 2>/dev/null || echo unknown)"
	at="$(git -C "$ROOT_DIR/../wesapp.git" rev-parse HEAD 2>/dev/null || echo unknown)"
	echo "${GO_OS}/${GO_ARCH} backend:${bt} wesgine:${wt} wesapp:${at}"
}

_write_fingerprint() {
	_current_fingerprint > "$FINGERPRINT_FILE"
}

_verify_fingerprint() {
	# 1) 架构必须匹配（最严重的错误：arm64 包里塞了 x86_64 二进制）
	local actual_arch
	actual_arch="$(file "$BACKEND_BIN" 2>/dev/null)"
	case "$GO_ARCH" in
		arm64) echo "$actual_arch" | grep -qi 'arm64' || die "架构不匹配: 目标 arm64，二进制是 $(echo "$actual_arch" | grep -oE 'x86_64|arm64|amd64'). 删除 SKIP_BACKEND=1 重编" ;;
		amd64) echo "$actual_arch" | grep -qiE 'x86_64|x86-64' || die "架构不匹配: 目标 amd64，二进制是 $(echo "$actual_arch" | grep -oE 'x86_64|arm64|amd64'). 删除 SKIP_BACKEND=1 重编" ;;
	esac

	# 2) 源码指纹必须匹配（wesgine/wesapp/backend 有变动 = 二进制过期）
	if [ ! -f "$FINGERPRINT_FILE" ]; then
		echo "  ⚠ 无构建指纹（首次构建或指纹丢失），无法校验源码版本"
		echo "  建议: 去掉 SKIP_BACKEND=1 重编一次以建立指纹"
		return 0
	fi
	local saved current
	saved="$(cat "$FINGERPRINT_FILE")"
	current="$(_current_fingerprint)"
	if [ "$saved" != "$current" ]; then
		echo "  ✗ 二进制过期！源码已变动"
		echo "    构建时: $saved"
		echo "    当前:   $current"
		die "Go 二进制与源码不匹配。去掉 SKIP_BACKEND=1 重新编译"
	fi
	echo "  ✓ 指纹匹配: $current"
}

if [ "${SKIP_BACKEND:-}" = "1" ] && [ -f "$BACKEND_BIN" ]; then
	echo "→ [2/5] 跳过 Go 后端（SKIP_BACKEND=1），校验已有二进制…"
	_verify_fingerprint
else
	echo "→ [2/5] 交叉编译 Go 后端 ($PLATFORM-$ARCH)…"
	mkdir -p "$BACKEND_DIR/bin"
	(
		cd "$BACKEND_DIR"
		# ARM64 Windows 上用 x64 Go 模拟运行时 CGO 默认关闭，需显式开启（tree-sitter 需要 CGO）
		CGO_ENABLED=1 GOOS="$GO_OS" GOARCH="$GO_ARCH" go build -o "bin/$(basename "$BACKEND_BIN")" ./cmd/wescode
	)
	_write_fingerprint
	echo "  指纹: $(_current_fingerprint)"
fi
echo "✓ $BACKEND_BIN"
echo ""

# ── Step 3: Editor 生产构建 ───────────────────────────────────────────────────
if [ "${SKIP_GULP:-}" = "1" ]; then
	echo "→ [3/5] 跳过 Electron gulp（SKIP_GULP=1）"
	[ -d "$BUILD_DIR" ] || die "SKIP_GULP=1 但构建目录不存在: $BUILD_DIR"
else
	echo "→ [3/5] 构建 Electron 应用 (gulp ${GULP_TASK}, 首次约 30-60 分钟)..."
	cd "$EDITOR_DIR"
	export PATH="$(dirname "$NODE_BIN"):$PATH"
	# editor/ 下无 .git，gulp getVersion 读不到 commit；注入 BUILD_SOURCEVERSION 供 product.json 写入
	export BUILD_SOURCEVERSION="$COMMIT"
	"$NPM_BIN" run gulp -- "$GULP_TASK"
fi
echo "✓ Electron 应用: $BUILD_DIR"
echo ""

# ── Step 4: 打入 wescode 资源 ─────────────────────────────────────────────────
echo "→ [4/5] 打入 wescode 资源 (web/dist + backend)…"

patch_product_json() {
	local product_json="$1"
	[ -f "$product_json" ] || die "未找到 product.json: $product_json"
	local pj_native="$(_node_path "$product_json")"
	"$NODE_BIN" <<EOF
const fs = require('fs');
const p = '$pj_native';
const j = JSON.parse(fs.readFileSync(p, 'utf8'));
j.commit = '$COMMIT';
j.productVersion = '$VERSION';
fs.writeFileSync(p, JSON.stringify(j, null, '\t') + '\n');
EOF
	echo "  product.json commit → $COMMIT, productVersion → $VERSION"
}

bundle_macos() {
	local app_path="$1"
	local resources="$app_path/Contents/Resources"

	[ -d "$app_path" ] || die "未找到 .app: $app_path"

	patch_product_json "$resources/app/product.json"

	mkdir -p "$resources/web"
	rm -rf "$resources/web/dist"
	cp -R "$WEB_DIR/dist" "$resources/web/"

	mkdir -p "$resources/app/bin"
	cp "$BACKEND_BIN" "$resources/app/bin/wescode"
	chmod +x "$resources/app/bin/wescode"

	# 内置 zh-cn 语言包（生产包 app/out 旁需有 app/l10n，见 vs/base/node/nls.ts）
	local l10n_src="$EDITOR_DIR/l10n/zh-cn/main.i18n.json"
	local l10n_dst="$resources/app/l10n/zh-cn"
	mkdir -p "$l10n_dst"
	[ -f "$l10n_src" ] || die "未找到中文语言包: $l10n_src"
	cp "$l10n_src" "$l10n_dst/main.i18n.json"

	echo "  web  → $resources/web/dist"
	echo "  backend → $resources/app/bin/wescode"
	echo "  l10n → $l10n_dst/main.i18n.json"
}

bundle_windows() {
	local app_dir="$1"
	local resources="$app_dir/resources"

	[ -d "$resources/app" ] || die "未找到 Windows 应用目录: $resources/app"

	# ── 打包闸（D-8 防复发）──
	# 路径权威在 Go（wesgine/xdg）；Electron 主进程不得再计算/注入
	# WESCODE_DATA_DIR。任何把 Linux XDG 字面量（~/.local/share/wescode）
	# 编译进 Windows 产物 main.js 的改动都会在这里直接失败。
	# 注意：darwin 分支的 ~/Library/... 字面量允许保留，只有 XDG 禁止。
	local main_js="$resources/app/out/main.js"
	[ -f "$main_js" ] || die "未找到 Electron 主进程脚本: $main_js"
	if grep -qF '.local/share/wescode' "$main_js"; then
		die "Windows 产物 main.js 含 Linux XDG 路径字面量（.local/share/wescode）— 路径权威已被破坏（D-8 复发），请修复 editor/src/vs/platform/wescode/electron-main/backendProcess.ts"
	fi
	echo "  ✓ 打包闸: main.js 无 Linux XDG 路径字面量"

	patch_product_json "$resources/app/product.json"

	mkdir -p "$resources/web"
	rm -rf "$resources/web/dist"
	cp -R "$WEB_DIR/dist" "$resources/web/"

	mkdir -p "$resources/app/bin"
	cp "$BACKEND_BIN" "$resources/app/bin/wescode.exe"

	local l10n_src="$EDITOR_DIR/l10n/zh-cn/main.i18n.json"
	local l10n_dst="$resources/app/l10n/zh-cn"
	mkdir -p "$l10n_dst"
	[ -f "$l10n_src" ] || die "未找到中文语言包: $l10n_src"
	cp "$l10n_src" "$l10n_dst/main.i18n.json"

	echo "  web  → $resources/web/dist"
	echo "  backend → $resources/app/bin/wescode.exe"
	echo "  l10n → $l10n_dst/main.i18n.json"
}

if [ "$PLATFORM" = "darwin" ]; then
	[ -d "$BUILD_DIR" ] || die "构建目录不存在: $BUILD_DIR"
	APP_NAME="$(ls "$BUILD_DIR" | grep -E '\.app$' | head -1)"
	[ -n "$APP_NAME" ] || die "在 $BUILD_DIR 中未找到 .app"
	SRC_APP="$BUILD_DIR/$APP_NAME"
	DST_APP="$OUT_DIR/$APP_NAME"

	[ -d "$SRC_APP" ] || die "源 .app 不存在: $SRC_APP"
	ensure_out_dir
	rm -rf "$DST_APP"
	cp -R "$SRC_APP" "$DST_APP"
	bundle_macos "$DST_APP"
	echo "✓ 应用: $DST_APP"
else
	SRC_DIR="$BUILD_DIR"
	DST_DIR="$OUT_DIR/app"

	[ -d "$SRC_DIR" ] || die "源应用目录不存在: $SRC_DIR"
	ensure_out_dir
	rm -rf "$DST_DIR"
	cp -R "$SRC_DIR" "$DST_DIR"
	bundle_windows "$DST_DIR"
	echo "✓ 应用: $DST_DIR"
fi
echo ""

echo ""

# macOS zip（VS Code autoUpdater 使用 zip，不是 dmg）
create_macos_zip() {
	local zip_name="WES Code-${VERSION}-darwin-${ARCH}.zip"
	local zip_path="$OUT_DIR/$zip_name"
	rm -f "$zip_path"
	echo "→ 打包 zip（自动更新）: ${zip_name}"
	( cd "$OUT_DIR" && zip -Xry "$zip_name" "$APP_NAME" >/dev/null )
	local sha
	sha="$(shasum -a 256 "$zip_path" | awk '{print $1}')"
	echo "✓ zip: $zip_path"
	echo "  sha256: $sha"
	MACOS_ZIP_PATH="$zip_path"
	MACOS_ZIP_SHA256="$sha"
	MACOS_ZIP_REL="darwin/${ARCH}/$zip_name"
}

write_release_manifest() {
	local vscode_platform="$1"
	local artifact_path="$2"
	local artifact_sha="$3"
	local rel_path="$4"
	local manifest_path="$OUT_DIR/release-manifest.json"
	"$NODE_BIN" <<EOF
const fs = require('fs');
const m = {
  commit: '$COMMIT',
  version: '$VERSION',
  productVersion: '$VERSION',
  timestamp: Math.floor(Date.now() / 1000),
  baseUrl: 'https://releases.example.com/wescode',
  platform: '$vscode_platform',
  artifact: {
    path: '$rel_path',
    localPath: '$artifact_path',
    sha256: '$artifact_sha',
  },
};
fs.writeFileSync('$manifest_path', JSON.stringify(m, null, 2) + '\n');
console.log('✓ release-manifest: $manifest_path');
EOF
}

# ── Step 5: 安装包 ────────────────────────────────────────────────────────────
echo "→ [5/5] 生成安装包…"

if [ "$FORMAT" = "app" ]; then
	# Windows app 格式自动打 zip（便于分发）
	if [ "$PLATFORM" = "win32" ]; then
		ZIP_NAME="WES Code-${VERSION}-win32-${ARCH}.zip"
		ZIP_PATH="$OUT_DIR/$ZIP_NAME"
		rm -f "$ZIP_PATH"
		echo "→ 打包 zip: $ZIP_NAME"
		# `zip` is not part of Git for Windows, and this branch only ever runs
		# there (the platform check above rejects win32 elsewhere), so the whole
		# format used to die at the last step with "zip: command not found"
		# after a 30-minute build. bsdtar ships in System32 on Win10+ and
		# writes a real zip when the output name ends in .zip.
		# The macOS zip below deliberately keeps `zip -Xry`: -X drops the extra
		# attributes that break notarization, and zip preserves the symlinks
		# inside the .app that an archiver may not.
		if command -v zip >/dev/null 2>&1; then
			(cd "$OUT_DIR" && zip -qry "$ZIP_NAME" "app")
		elif tar --version 2>/dev/null | grep -q bsdtar; then
			(cd "$OUT_DIR" && tar -a -c -f "$ZIP_NAME" "app")
		else
			die "找不到 zip，也没有能写 zip 的 bsdtar（Win10+ 自带 C:\\Windows\\System32\\tar.exe）"
		fi
		echo "✓ zip: $ZIP_PATH"
		echo ""
		echo "产物:"
		echo "  $OUT_DIR/app/          ← 绿色版（解压即用）"
		echo "  $ZIP_PATH   ← 发给别人"
	else
		echo "✓ 完成（应用目录）"
		echo ""
		echo "产物: $OUT_DIR"
	fi
	exit 0
fi

if [ "$FORMAT" = "dmg" ]; then
	create_macos_zip

	DMG_NAME="WES Code-${VERSION}-darwin-${ARCH}.dmg"
	DMG_PATH="$OUT_DIR/$DMG_NAME"
	rm -f "$DMG_PATH"
	echo "→ 打包 dmg（首次安装）: ${DMG_NAME}"

	# ── 专业 macOS DMG 安装体验（对标 QQ/微信）─────────────────────────────
	# 由 build-dmg.py 完成：创建读写 DMG → 挂载 → 拷内容 → 在卷上写 .DS_Store
	# → 卸载 → 转压缩。
	#
	# 为什么不能用 `hdiutil create -srcfolder`：它会在新卷上重新生成 .DS_Store，
	# 把预置的那份丢掉，于是 backgroundType 退回 0（背景图不显示）。同理
	# backgroundImageAlias 必须从**已挂载卷上的真实文件**生成，从 staging
	# 目录生成的 alias 指向构建机器路径，分发后失效。
	# 判据取自 QQ 安装盘的 .DS_Store（backgroundType=2 + 768B alias + iconSize=128）。
	DMG_BG="$SCRIPT_DIR/../editor/resources/darwin/dmg-background.png"
	if [ ! -f "$DMG_BG" ]; then
		echo "  → 生成 DMG 背景图…"
		swift "$SCRIPT_DIR/create-dmg-background.swift" 2>/dev/null || true
	fi

	if python3 "$SCRIPT_DIR/build-dmg.py" "$DMG_PATH" "$DST_APP" "$PRODUCT_NAME" "$DMG_BG"; then
		:
	else
		echo "  ⚠ build-dmg.py 失败，降级到 hdiutil（无背景图）"
		DMG_STAGE="$OUT_DIR/.dmg-stage"
		rm -rf "$DMG_STAGE" && mkdir -p "$DMG_STAGE"
		cp -R "$DST_APP" "$DMG_STAGE/" && ln -s /Applications "$DMG_STAGE/Applications"
		hdiutil create -volname "$PRODUCT_NAME" -srcfolder "$DMG_STAGE" \
			-ov -format UDZO "$DMG_PATH" >/dev/null
		rm -rf "$DMG_STAGE"
		echo "✓ DMG (降级): $DMG_PATH"
	fi


	if [ "$WRITE_RELEASE_MANIFEST" = "1" ]; then
		vscode_platform="darwin-arm64"
		[ "$ARCH" = "x64" ] && vscode_platform="darwin"
		write_release_manifest "$vscode_platform" "$MACOS_ZIP_PATH" "$MACOS_ZIP_SHA256" "$MACOS_ZIP_REL"
	fi

	echo ""
	echo "产物:"
	echo "  $DST_APP"
	echo "  $MACOS_ZIP_PATH          ← 自动更新上传此 zip"
	echo "  $DMG_PATH                ← 首次安装分发给用户"
	echo ""
	echo "发布: ./scripts/publish-release.sh darwin ${ARCH}"
	exit 0
fi

if [ "$FORMAT" = "setup" ]; then
	# bundle 已写入 BUILD_DIR（setup 从此目录读取）
	bundle_windows "$BUILD_DIR"

	# user setup：装到 %LOCALAPPDATA%，无 UAC，支持后台静默更新。
	# 安装目标与更新平台 key 必须成对——客户端的 key 由装进 resources/app 的
	# product.json 的 target 字段派生（updateService.win32.ts buildUpdateFeedUrl），
	# 改一个不改另一个会让客户端问一个 stable.json 里不存在的 key，症状是
	# 「检查更新」永远回「已是最新」而不是报错。
	WIN_SETUP_TARGET=user
	WIN_UPDATE_PLATFORM="win32-${ARCH}-user"

	echo "→ 运行 Inno Setup (gulp vscode-win32-${ARCH}-${WIN_SETUP_TARGET}-setup)…"
	cd "$EDITOR_DIR"
	"$NPM_BIN" run gulp -- "vscode-win32-${ARCH}-${WIN_SETUP_TARGET}-setup"

	SETUP_DIR="$EDITOR_DIR/.build/win32-${ARCH}/${WIN_SETUP_TARGET}-setup"
	SETUP_EXE=""
	if [ -d "$SETUP_DIR" ]; then
		SETUP_EXE="$(find "$SETUP_DIR" -maxdepth 1 -name '*.exe' -type f | head -1)"
	fi
	[ -n "$SETUP_EXE" ] || die "未找到安装包 .exe，请确认 Inno Setup 已安装"

	DST_SETUP="$OUT_DIR/WES Code-${VERSION}-win32-${ARCH}-setup.exe"
	cp "$SETUP_EXE" "$DST_SETUP"
	echo "✓ 安装包: $DST_SETUP"

	SETUP_SHA="$(shasum -a 256 "$DST_SETUP" | awk '{print $1}')"
	SETUP_REL="win32/x64/$(basename "$DST_SETUP")"
	if [ "$WRITE_RELEASE_MANIFEST" = "1" ]; then
		write_release_manifest "$WIN_UPDATE_PLATFORM" "$DST_SETUP" "$SETUP_SHA" "$SETUP_REL"
	fi

	echo ""
	echo "产物:"
	echo "  $DST_DIR"
	echo "  $DST_SETUP               ← 自动更新 + 首次安装"
	echo ""
	echo "发布: ./scripts/publish-release.sh win32 x64"
	exit 0
fi
