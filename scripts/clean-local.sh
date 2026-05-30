#!/usr/bin/env bash
# Remove Traio dev/install caches. Keeps ibkr-gateway and data/ (settings, DB).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUNTIME_DIR="${TRAIO_RUNTIME_DIR:-${HOME}/Library/Application Support/Traio}"
FLUTTER="${FLUTTER:-/Users/nite/env/flutter/bin/flutter}"

usage() {
  cat <<EOF
清理 Traio 本地开发/安装缓存（保留 ibkr-gateway 与 data/）

用法:
  scripts/clean-local.sh          # 完整清理（Flutter build + runtime 状态文件 + dist/安装包）
  scripts/clean-local.sh --state # 仅清理 runtime 状态（server.json、pid、停服）

保留:
  \${RUNTIME_DIR}/ibkr-gateway/
  \${RUNTIME_DIR}/data/
EOF
}

stop_server() {
  curl -fsS -X POST "http://127.0.0.1:38180/api/v1/server/shutdown" >/dev/null 2>&1 || true
  sleep 0.5
  if [[ -f "${RUNTIME_DIR}/traio-server.pid" ]]; then
    kill "$(tr -d '[:space:]' < "${RUNTIME_DIR}/traio-server.pid")" 2>/dev/null || true
    sleep 0.5
  fi
  pkill -f "${ROOT_DIR}/bin/traio-server" 2>/dev/null || true
}

clean_state() {
  stop_server
  rm -f "${RUNTIME_DIR}/server.json" "${RUNTIME_DIR}/traio-server.pid"
  echo "runtime state cleaned (ibkr-gateway + data kept)"
}

clean_build() {
  cd "${ROOT_DIR}/flutter"
  "${FLUTTER}" clean
  rm -rf "${ROOT_DIR}/flutter/build" "${ROOT_DIR}/flutter/.dart_tool"
  echo "flutter build cache cleaned"
}

clean_install() {
  rm -rf "${ROOT_DIR}/dist/"*.app 2>/dev/null || true
  rm -rf "${HOME}/Applications/Traio.app" 2>/dev/null || true
  rm -rf "${HOME}/Applications"/traio-*.app 2>/dev/null || true
  echo "local install/dist apps removed"
}

mode="${1:-all}"
case "$mode" in
  help|-h|--help)
    usage
    ;;
  --state|state)
    clean_state
    ;;
  all|"")
    clean_state
    clean_build
    clean_install
    echo "done — run: make dev"
    ;;
  *)
    echo "unknown option: $mode" >&2
    usage >&2
    exit 2
    ;;
esac
