#!/bin/bash
# wescode 开发环境搭建脚本
# 需要：Node.js 20.18+、Python 3.12+、系统 C++ 编译器

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
EDITOR_DIR="$ROOT_DIR/editor"

echo "=== wescode 开发环境搭建 ==="
echo "Editor dir: $EDITOR_DIR"
echo ""

# 检查 Node 版本
NODE_MAJOR=$(node -v | cut -d. -f1 | tr -d 'v')
if [ "$NODE_MAJOR" -lt 20 ]; then
    echo "错误: 需要 Node.js 20+，当前: $(node -v)"
    echo "建议: nvm install 20 && nvm use 20"
    exit 1
fi
echo "✓ Node.js $(node -v)"

# 检查 Python
PYTHON_VERSION=$(python3 --version 2>/dev/null | cut -d' ' -f2)
PYTHON_MAJOR=$(echo "$PYTHON_VERSION" | cut -d. -f1)
PYTHON_MINOR=$(echo "$PYTHON_VERSION" | cut -d. -f2)
if [ "$PYTHON_MAJOR" -lt 3 ] || [ "$PYTHON_MINOR" -lt 12 ]; then
    echo "警告: node-gyp 需要 Python 3.12+，当前: $PYTHON_VERSION"
    echo "建议: brew install python@3.12"
    echo "继续尝试..."
fi
echo "✓ Python $PYTHON_VERSION"

# 安装依赖
echo ""
echo "=== 安装依赖 (npm install) ==="
cd "$EDITOR_DIR"
npm install

# 编译
echo ""
echo "=== 编译 TypeScript ==="
NODE_OPTIONS="--max-old-space-size=8192" npm run compile

echo ""
echo "=== 完成！==="
echo ""
echo "启动开发版编辑器:"
echo "  cd $EDITOR_DIR && ./scripts/code.sh"
echo ""
echo "启动增量编译 (watch 模式):"
echo "  cd $EDITOR_DIR && npm run watch"
