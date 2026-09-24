#!/bin/bash
# wescode 完整构建脚本
# 处理 @vscode/spdlog 在 macOS Tahoe (Darwin 25+) + Clang 17+ 下的 C++ consteval 编译问题

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EDITOR_DIR="$(dirname "$SCRIPT_DIR")/editor"
cd "$EDITOR_DIR"

echo "=== wescode 构建脚本 ==="
echo "目录: $EDITOR_DIR"
echo ""

# --- 检查 Node ---
NODE_VER=$(node -v)
echo "Node: $NODE_VER"
NODE_MAJOR=$(echo $NODE_VER | cut -d. -f1 | tr -d 'v')
if [ "$NODE_MAJOR" -ne "22" ] && [ "$NODE_MAJOR" -lt "20" ]; then
    echo "警告: 推荐 Node 20 或 22"
fi

# --- 检查 Python ---
PYTHON_BIN=""
for p in python3.13 python3.12 python3.11 python3; do
    if command -v $p &>/dev/null; then
        PY_VER=$($p -c "import sys; print(f'{sys.version_info.major}.{sys.version_info.minor}')")
        PY_MAJ=$(echo $PY_VER | cut -d. -f1)
        PY_MIN=$(echo $PY_VER | cut -d. -f2)
        if [ "$PY_MAJ" -ge 3 ] && [ "$PY_MIN" -ge 11 ]; then
            PYTHON_BIN=$(command -v $p)
            echo "Python: $($p --version) → $PYTHON_BIN"
            break
        fi
    fi
done
if [ -z "$PYTHON_BIN" ]; then
    echo "错误: 需要 Python 3.11+，未找到"
    exit 1
fi

# --- 中文翻译已内置于 editor/l10n/zh-cn/（无需外部克隆）---
echo ""
echo "=== Step 0: 验证中文翻译 ==="
if [ -f "$EDITOR_DIR/l10n/zh-cn/main.i18n.json" ]; then
    echo "✓ 中文翻译文件就绪（editor/l10n/zh-cn/main.i18n.json）"
else
    echo "错误: 缺少 editor/l10n/zh-cn/main.i18n.json"
    echo "  请确认 git clone/pull 完整"
    exit 1
fi

# --- 清理 ---
echo ""
echo "=== Step 1: 清理 ==="
rm -rf node_modules build/node_modules
echo "✓ 清理完成"

# --- Step 2: 安装根依赖（跳过 native 编译，先拿到源码）---
echo ""
echo "=== Step 2: 安装 npm 依赖（skip native build）==="
npm_config_python="$PYTHON_BIN" npm install --ignore-scripts
echo "✓ npm 依赖安装完成"

# --- Step 3: patch @vscode/spdlog ---
echo ""
echo "=== Step 3: patch @vscode/spdlog (Clang consteval 兼容性) ==="

SPDLOG_FMT_HELPER="node_modules/@vscode/spdlog/deps/spdlog/include/spdlog/details/fmt_helper.h"

if [ ! -f "$SPDLOG_FMT_HELPER" ]; then
    echo "错误: 未找到 $SPDLOG_FMT_HELPER"
    exit 1
fi

# 备份原文件
cp "$SPDLOG_FMT_HELPER" "${SPDLOG_FMT_HELPER}.orig"

# 将 SPDLOG_FMT_STRING("{:02}") 替换为普通字符串 "{:02}"
# 这绕过了 consteval 编译期检查，改为运行时字符串（功能完全一致）
sed -i '' \
    's/fmt_lib::format_to(std::back_inserter(dest), SPDLOG_FMT_STRING("{:02}"), n)/fmt_lib::format_to(std::back_inserter(dest), "{:02}", n)/g' \
    "$SPDLOG_FMT_HELPER"

echo "✓ spdlog patch 完成"

# --- Step 4: 手动重建所有 native 模块 ---
# 使用 npm 内置的 node-gyp v11（系统全局的 v6 有 bug）
LOCAL_NODE_GYP="$(npm config get prefix)/lib/node_modules/npm/bin/node-gyp-bin/node-gyp"
if [ ! -f "$LOCAL_NODE_GYP" ]; then
    LOCAL_NODE_GYP="$(dirname $(dirname $(which npm)))/lib/node_modules/npm/bin/node-gyp-bin/node-gyp"
fi
echo ""
echo "=== Step 4a: 重建 @vscode/spdlog (已 patch) ==="
cd node_modules/@vscode/spdlog
"$LOCAL_NODE_GYP" rebuild
cd "$EDITOR_DIR"
echo "✓ @vscode/spdlog 编译完成"

echo ""
echo "=== Step 4b: 重建 @vscode/sqlite3 ==="
cd node_modules/@vscode/sqlite3
"$LOCAL_NODE_GYP" rebuild
cd "$EDITOR_DIR"
echo "✓ @vscode/sqlite3 编译完成"

echo ""
echo "=== Step 4c: 重建 native-keymap ==="
cd node_modules/native-keymap
"$LOCAL_NODE_GYP" rebuild
cd "$EDITOR_DIR"
echo "✓ native-keymap 编译完成"

echo ""
echo "=== Step 4d: 重建 native-watchdog ==="
if [ -d "node_modules/native-watchdog" ]; then
    cd node_modules/native-watchdog
    "$LOCAL_NODE_GYP" rebuild
    cd "$EDITOR_DIR"
    echo "✓ native-watchdog 编译完成"
fi

echo ""
echo "=== Step 4e: 下载 @vscode/ripgrep 二进制 ==="
# ripgrep 不需要编译，需要运行 postinstall 下载预编译二进制
cd node_modules/@vscode/ripgrep
npm_config_python="$PYTHON_BIN" node install.js 2>/dev/null || \
    npm_config_python="$PYTHON_BIN" npm run postinstall 2>/dev/null || \
    echo "ripgrep 下载脚本运行完毕（如有错误请忽略，不影响核心功能）"
cd "$EDITOR_DIR"
echo "✓ ripgrep 处理完成"

# --- Step 5: 安装所有子目录依赖（build/ extensions/ remote/ 等）---
echo ""
echo "=== Step 5: 安装子目录依赖（build/ + extensions/ + remote/ 等）==="
# 用 npm_command=install 调用 postinstall.js，让它在所有子目录跑 npm install
# 而不是 npm rebuild，避免触发再次重编译 native 模块
# mono-repo 未包含 .vscode/extensions/vscode-selfhost-*，postinstall 末尾可能失败，可忽略
if ! npm_config_python="$PYTHON_BIN" npm_command=install node build/npm/postinstall.js; then
	echo "⚠ postinstall 在可选步骤失败（通常是 vscode-selfhost-* 目录缺失），继续编译…"
fi
echo "✓ 子目录依赖安装完成（核心目录）"

# --- Step 7: 编译 TypeScript ---
echo ""
echo "=== Step 7: 编译 TypeScript ==="
chmod +x "$SCRIPT_DIR/compile-editor.sh"
"$SCRIPT_DIR/compile-editor.sh"
echo "✓ TypeScript 编译完成"

# --- Electron 品牌修补（macOS .app bundle）---
echo ""
echo "=== Step 8: Electron 品牌修补 ==="
"$SCRIPT_DIR/patch-electron-brand.sh"

echo ""
echo "=============================="
echo "✅ 构建完成！启动编辑器:"
echo "   cd $EDITOR_DIR && ./scripts/code.sh"
echo "=============================="
