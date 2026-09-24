#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../editor"

if [ ! -d node_modules ]; then
  echo "→ editor: npm install (first time)…"
  npm install
fi

node --max-old-space-size=8192 ./node_modules/gulp/bin/gulp.js compile
