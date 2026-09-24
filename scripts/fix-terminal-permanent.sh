#!/usr/bin/env bash
# ============================================================================
# wescode 终端泄漏 — 根治脚本
# 在【完全退出 wescode 之后】运行：
#   1. 备份 state.vscdb（workspace storage）
#   2. 清空 terminal.integrated.layoutInfo 中累积的僵尸终端 tab
#      （这些 tab 每次 ptyHost 重启都会触发恢复 spawn zsh → ptmx 耗尽）
#   3. 检查残留孤儿 zsh
# 运行后重新启动 wescode，终端布局恢复干净。
# ============================================================================
set -uo pipefail

WS_DIR="$HOME/Library/Application Support/wescode/User/workspaceStorage"
WSDB="$(find "$WS_DIR" -name state.vscdb 2>/dev/null | head -1)"

echo "=== 检查 wescode 是否仍在运行 ==="
if pgrep -f "WES Code.app/Contents/MacOS/Electron" >/dev/null 2>&1; then
  echo "警告: wescode 仍在运行！请先完全退出 wescode 再运行本脚本，否则清理会被运行中的进程覆盖。"
  read -r -p "强制继续？[y/N] " ans
  [[ "$ans" == "y" || "$ans" == "Y" ]] || { echo "已取消"; exit 1; }
fi

if [ -z "$WSDB" ]; then
  echo "错误: 未找到 state.vscdb（$WS_DIR 下）"
  exit 1
fi
echo "找到: $WSDB"

echo ""
echo "=== [1/3] 备份 state.vscdb ==="
BACKUP="$WSDB.bak-$(date +%Y%m%d%H%M%S)"
cp "$WSDB" "$BACKUP"
echo "备份完成: $BACKUP"

echo ""
echo "=== [2/3] 清空持久化终端 tab（layoutInfo）==="
python3 - "$WSDB" <<'PYEOF'
import sqlite3, json, sys
db = sys.argv[1]
conn = sqlite3.connect(db)
cur = conn.cursor()
row = cur.execute(
    "SELECT value FROM ItemTable WHERE key='terminal.integrated.layoutInfo'"
).fetchone()
if row:
    d = json.loads(row[0])
    n = len(d.get('tabs', []))
    d['tabs'] = []
    cur.execute(
        "UPDATE ItemTable SET value=? WHERE key='terminal.integrated.layoutInfo'",
        (json.dumps(d, ensure_ascii=False),),
    )
    conn.commit()
    print(f"已清空 {n} 个持久化终端 tab")
else:
    print("未找到 layoutInfo（终端从未使用过？）")
conn.close()
PYEOF

echo ""
echo "=== [3/3] 检查残留孤儿 zsh ==="
left=$(ps aux | grep "[z]sh -il" | grep -c "wesgine.git" || true)
if [ "$left" -gt 0 ]; then
  echo "发现 $left 个孤儿 zsh（wesgine.git 工作目录），清理中..."
  for pid in $(ps aux | grep "[z]sh -il" | awk '{print $2}'); do
    cwd=$(lsof -a -p "$pid" -d cwd -Fn 2>/dev/null | grep '^n' | sed 's/^n//')
    if echo "$cwd" | grep -q "wesgine.git"; then
      kill "$pid" 2>/dev/null || true
    fi
  done
else
  echo "无残留孤儿 zsh"
fi

echo ""
echo "=============================================="
echo "清理完成。现在重新启动 wescode。"
echo "若仍有问题，备份文件: $BACKUP"
echo "=============================================="
