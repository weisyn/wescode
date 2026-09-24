#!/bin/bash
# Patch native modules that fail to compile with newer Clang versions.
# Called by npm postinstall or manually before make run.
set -e

EDITOR_DIR="${1:-$(dirname "$(dirname "$0")")/editor}"

# @vscode/spdlog: disable FMT_CONSTEVAL (incompatible with Apple Clang 16+)
SPDLOG_CORE="$EDITOR_DIR/node_modules/@vscode/spdlog/deps/spdlog/include/spdlog/fmt/bundled/core.h"
if [ -f "$SPDLOG_CORE" ] && grep -q "define FMT_CONSTEVAL consteval" "$SPDLOG_CORE"; then
    sed -i '' '/#    define FMT_CONSTEVAL consteval/c\
#    define FMT_CONSTEVAL' "$SPDLOG_CORE"
    echo "✓ patched spdlog FMT_CONSTEVAL"
fi
