#!/usr/bin/env bash
# ============================================================================
# wescode 终端泄漏 — 即时缓解脚本
# 症状：集成终端反复提示"终端进程启动失败"
# 根因：孤儿 zsh -il 进程累积，占满内核 pty 上限（kern.tty.ptmx_max=511），
#       新 pty 创建返回 ENXIO，node-pty 报 "posix_spawnp failed"
# 本脚本：kill 孤儿 zsh（工作目录在 wesgine.git 的）+ 重启 ptyHost
# 注意：仅缓解，根治需运行 fix-terminal-permanent.sh（wescode 退出后）
# ============================================================================
set -uo pipefail

echo "=== [1/3] kill 孤儿 zsh（cwd=wesgine.git 的 zsh -il）==="
count=0
for pid in $(ps aux | grep "[z]sh -il" | awk '{print $2}'); do
  cwd=$(lsof -a -p "$pid" -d cwd -Fn 2>/dev/null | grep '^n' | sed 's/^n//')
  if echo "$cwd" | grep -q "wesgine.git"; then
    kill "$pid" 2>/dev/null && count=$((count+1))
  fi
done
echo "已 kill $count 个孤儿 zsh"

echo "=== [2/3] 重启 ptyHost（WES Code 会自动拉起新实例）==="
pids=$(ps aux | grep "WES Code Helper.*node.mojom.NodeService" | grep -v grep | awk '{print $2}')
if [ -n "$pids" ]; then
  for p in $pids; do kill "$p" 2>/dev/null || true; done
  echo "已重启 ptyHost: $pids"
else
  echo "未找到 ptyHost 进程"
fi

sleep 2
echo "=== [3/3] 验证 ptmx 占用 ==="
total=$(lsof -nP /dev/ptmx 2>/dev/null | wc -l | tr -d ' ')
limit=$(sysctl -n kern.tty.ptmx_max 2>/dev/null || echo 511)
echo "当前 ptmx fd: $total（上限 $limit）"
echo ""
echo "若占用已下降，现在应能正常打开集成终端。"
echo "根治请完全退出 wescode 后运行: scripts/fix-terminal-permanent.sh"
