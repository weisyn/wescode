#!/usr/bin/env bash
# 把 editor/node_modules 里的 Electron 原生模块重编为指定架构。
#
# 为什么需要它：gulp 的 packageTask 把 node_modules 里的 .node 文件**原样**
# 拷进 ASAR（gulpfile.vscode.js 的 deps 流），所以打 Intel 包之前必须先把
# 这些模块编成 x64，否则 Intel 用户一开终端就崩——而构建过程零报错。
#
# macOS 的 clang 支持原生交叉编译（arm64 主机可产出 x86_64），不需要 Rosetta。
#
# 用法:
#   ./scripts/rebuild-native-modules.sh arm64
#   ./scripts/rebuild-native-modules.sh x64
#
# 环境变量:
#   BACKUP_DIR   备份当前产物到该目录（便于事后还原），默认不备份
#   RESTORE_DIR  直接从该目录还原，跳过编译（配合 BACKUP_DIR 使用）

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
EDITOR_DIR="$ROOT_DIR/editor"

MODULES=(node-pty native-keymap @vscode/spdlog @vscode/sqlite3 native-watchdog)

die() { echo "错误: $*" >&2; exit 1; }

TARGET_ARCH="${1:-}"
case "$TARGET_ARCH" in
	arm64|x64) ;;
	*) die "用法: $0 <arm64|x64>" ;;
esac

ELECTRON_VER="$(node -e "console.log(require('$EDITOR_DIR/package.json').devDependencies.electron)")"
[ -n "$ELECTRON_VER" ] || die "无法读取 Electron 版本"

# ── 还原模式 ────────────────────────────────────────────────────────────────
if [ -n "${RESTORE_DIR:-}" ]; then
	[ -d "$RESTORE_DIR" ] || die "还原目录不存在: $RESTORE_DIR"
	echo "→ 从 $RESTORE_DIR 还原原生模块…"
	for m in "${MODULES[@]}"; do
		src="$RESTORE_DIR/$m"
		dst="$EDITOR_DIR/node_modules/$m/build/Release"
		[ -d "$src" ] || continue
		mkdir -p "$dst"
		cp -R "$src"/* "$dst/" 2>/dev/null || true
		echo "  ✓ $m"
	done
	exit 0
fi

# ── 备份 ────────────────────────────────────────────────────────────────────
if [ -n "${BACKUP_DIR:-}" ]; then
	echo "→ 备份当前产物到 $BACKUP_DIR…"
	for m in "${MODULES[@]}"; do
		src="$EDITOR_DIR/node_modules/$m/build/Release"
		[ -d "$src" ] || continue
		mkdir -p "$BACKUP_DIR/$m"
		cp -R "$src"/* "$BACKUP_DIR/$m/" 2>/dev/null || true
	done
	echo "  ✓ 已备份"
fi

# ── 预置 Electron headers 到 node-gyp 缓存 ──────────────────────────────────
# node-gyp 自己下载 headers 时对 electronjs.org 的 301 重定向 + TLS 不稳定
# （国内网络尤甚），失败信息是 "Client network socket disconnected"。
# 用 curl -L 先取好放进缓存，node-gyp 命中缓存就不再联网。
CACHE="$HOME/Library/Caches/node-gyp/$ELECTRON_VER"
if [ ! -d "$CACHE/include/node" ]; then
	echo "→ 预置 Electron $ELECTRON_VER headers 到 node-gyp 缓存…"
	mkdir -p "$CACHE"
	TMP_TGZ="$(mktemp -t electron-headers).tar.gz"
	TMP_DIR="$(mktemp -d)"
	if curl -sL --max-time 60 -o "$TMP_TGZ" \
		"https://electronjs.org/headers/v$ELECTRON_VER/node-v$ELECTRON_VER-headers.tar.gz"; then
		tar xzf "$TMP_TGZ" -C "$TMP_DIR"
		[ -d "$TMP_DIR/node_headers/include" ] && cp -R "$TMP_DIR/node_headers/include" "$CACHE/"
		curl -sL --max-time 20 -o "$CACHE/installVersion" \
			"https://electronjs.org/headers/v$ELECTRON_VER/installVersion" 2>/dev/null || echo 9 > "$CACHE/installVersion"
		mkdir -p "$CACHE/arm64" "$CACHE/x64"
		curl -sL --max-time 20 -o "$CACHE/arm64/node.lib" \
			"https://electronjs.org/headers/v$ELECTRON_VER/arm64/node.lib" 2>/dev/null || true
		curl -sL --max-time 20 -o "$CACHE/x64/node.lib" \
			"https://electronjs.org/headers/v$ELECTRON_VER/x64/node.lib" 2>/dev/null || true
		echo "  ✓ headers 就绪"
	else
		echo "  ⚠ headers 下载失败，node-gyp 将自行尝试"
	fi
	rm -rf "$TMP_TGZ" "$TMP_DIR"
fi

# ── 编译 ────────────────────────────────────────────────────────────────────
echo "→ 重编原生模块为 ${TARGET_ARCH}（Electron ${ELECTRON_VER}）…"
failed=()
for m in "${MODULES[@]}"; do
	dir="$EDITOR_DIR/node_modules/$m"
	[ -d "$dir" ] || { echo "  - ${m}（未安装，跳过）"; continue; }
	printf "  %-22s " "$m"
	if (cd "$dir" && CXXFLAGS="-std=c++20" npx node-gyp rebuild \
		--target="$ELECTRON_VER" --arch="$TARGET_ARCH" \
		--dist-url=https://electronjs.org/headers --runtime=electron \
		>/dev/null 2>&1); then
		f="$(find "$dir/build/Release" -name '*.node' 2>/dev/null | head -1)"
		if [ -n "$f" ]; then
			echo "✓ $(file -b "$f" | grep -oE 'arm64|x86_64')"
		else
			echo "✓（无 .node 产物）"
		fi
	else
		echo "✗ 编译失败"
		failed+=("$m")
	fi
done

if [ ${#failed[@]} -gt 0 ]; then
	die "以下模块编译失败: ${failed[*]}"
fi

# ── 校验 ────────────────────────────────────────────────────────────────────
# 期望的 file(1) 架构串：x64 → x86_64，arm64 → arm64
want="$TARGET_ARCH"
[ "$TARGET_ARCH" = "x64" ] && want="x86_64"

echo ""
echo "→ 校验全部模块架构…"
bad=0
for m in "${MODULES[@]}"; do
	f="$(find "$EDITOR_DIR/node_modules/$m/build/Release" -name '*.node' 2>/dev/null | head -1)"
	[ -n "$f" ] || continue
	got="$(file -b "$f" | grep -oE 'arm64|x86_64' | head -1)"
	if [ "$got" != "$want" ]; then
		echo "  ✗ ${m}: ${got}（应为 ${want}）"
		bad=1
	fi
done
[ "$bad" = 0 ] || die "架构校验未通过"
echo "  ✓ 全部为 $want"
