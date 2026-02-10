#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DESKTOP_DIR="$ROOT_DIR/desktop"

TARGET="${1:-local}"

echo "[runner-installer] preparing agentd release binaries..."
"$ROOT_DIR/scripts/build-agentd-release.sh"

echo "[runner-installer] installing desktop dependencies..."
cd "$DESKTOP_DIR"
npm install

case "$TARGET" in
  local)
    case "$(uname -s)" in
      Darwin)
        npm run dist:mac
        ;;
      Linux)
        npm run dist:linux
        ;;
      MINGW*|MSYS*|CYGWIN*|Windows_NT)
        npm run dist:win
        ;;
      *)
        echo "[runner-installer] unsupported local platform; pass one of: mac|win|linux|all"
        exit 1
        ;;
    esac
    ;;
  mac)
    npm run dist:mac
    ;;
  win)
    npm run dist:win
    ;;
  linux)
    npm run dist:linux
    ;;
  all)
    npm run dist:all
    ;;
  *)
    echo "Usage: $0 [local|mac|win|linux|all]"
    exit 1
    ;;
esac

echo "[runner-installer] done. Output:"
find "$DESKTOP_DIR/dist" -maxdepth 2 -type f | sort
