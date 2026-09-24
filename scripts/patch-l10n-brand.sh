#!/bin/bash
# 将 vscode-loc 中文语言包中的 VS Code 品牌替换为 WES Code。
# 幂等：可多次运行。
#
# 用途：从上游 microsoft/vscode-loc 同步新翻译后，运行此脚本品牌化，
# 然后将结果复制到 editor/l10n/zh-cn/main.i18n.json 并提交。
#
# 日常开发不需要运行此脚本——品牌化后的翻译已直接在仓库中。

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
VSCODE_LOC_DIR="$ROOT_DIR/vscode-loc"
MAIN_I18N="$VSCODE_LOC_DIR/i18n/vscode-language-pack-zh-hans/translations/main.i18n.json"

if [ ! -f "$MAIN_I18N" ]; then
    echo "⚠ 未找到 $MAIN_I18N，跳过品牌替换"
    echo "  请先运行 setup.sh 或 build.sh 克隆 vscode-loc"
    exit 0
fi

echo "=== 品牌替换（中文语言包）==="

if [[ "$(uname)" == "Darwin" ]]; then
    sed -i '' 's/VS Code/WES Code/g' "$MAIN_I18N"
    sed -i '' 's/Visual Studio Code/WES Code/g' "$MAIN_I18N"
    sed -i '' 's/code\.visualstudio\.com/www.weisyn.com/g' "$MAIN_I18N"
else
    sed -i 's/VS Code/WES Code/g' "$MAIN_I18N"
    sed -i 's/Visual Studio Code/WES Code/g' "$MAIN_I18N"
    sed -i 's/code\.visualstudio\.com/www.weisyn.com/g' "$MAIN_I18N"
fi

# 验证替换结果
REMAINING=$(grep -c "VS Code" "$MAIN_I18N" 2>/dev/null || true)
REMAINING="${REMAINING:-0}"
REMAINING="$(echo "$REMAINING" | tr -d '[:space:]')"
if [ "$REMAINING" != "0" ]; then
    echo "⚠ 仍有 $REMAINING 处 'VS Code' 未被替换（可能在不该替换的上下文中）"
else
    echo "✓ 中文语言包品牌替换完成（VS Code → WES Code）"
fi

# 清除 NLS 翻译缓存（否则旧翻译会从缓存加载，品牌替换不生效）
echo ""
echo "=== 清除 NLS 翻译缓存 ==="
case "$(uname)" in
    Darwin)
        # 开发模式（code.sh）和生产模式的 user data 路径
        for DATA_DIR in \
            "$HOME/Library/Application Support/WES Code Dev" \
            "$HOME/Library/Application Support/WES Code" \
            "$HOME/Library/Application Support/wescode"; do
            if [ -d "$DATA_DIR/clp" ]; then
                rm -rf "$DATA_DIR/clp"
                echo "  ✓ 已清除: $DATA_DIR/clp"
            fi
        done
        ;;
    Linux)
        for DATA_DIR in \
            "${XDG_CONFIG_HOME:-$HOME/.config}/WES Code Dev" \
            "${XDG_CONFIG_HOME:-$HOME/.config}/WES Code" \
            "${XDG_CONFIG_HOME:-$HOME/.config}/wescode"; do
            if [ -d "$DATA_DIR/clp" ]; then
                rm -rf "$DATA_DIR/clp"
                echo "  ✓ 已清除: $DATA_DIR/clp"
            fi
        done
        ;;
esac
echo "✓ NLS 缓存清除完成（下次启动编辑器时重新生成）"
