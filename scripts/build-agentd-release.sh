#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
AGENT_DIR="$ROOT_DIR/agent"
OUT_DIR="$AGENT_DIR/dist"

mkdir -p "$OUT_DIR"

build_target() {
  local goos="$1"
  local goarch="$2"
  local out_folder="$3"
  local out_name="$4"
  local target_dir="$OUT_DIR/$out_folder"
  mkdir -p "$target_dir"
  echo "[agentd] building $goos/$goarch -> $target_dir/$out_name"
  (cd "$AGENT_DIR" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -o "$target_dir/$out_name" ./cmd/agentd)
}

build_target "darwin" "amd64" "darwin-amd64" "agentd"
build_target "linux" "amd64" "linux-amd64" "agentd"
build_target "windows" "amd64" "windows-amd64" "agentd.exe"

echo "[agentd] release artifacts:"
find "$OUT_DIR" -maxdepth 2 -type f | sort
