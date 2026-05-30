#!/usr/bin/env bash
# Local developer workflow for testing, release packaging, and local install.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

VERSION="${VERSION:-0.1.0}"
OUT_DIR="${OUT_DIR:-${ROOT_DIR}/dist}"
RELEASE_NAME="${RELEASE_NAME:-traio-${VERSION}-macos}"
INSTALL_DIR="${INSTALL_DIR:-${HOME}/Applications}"
INSTALLED_APP_NAME="${INSTALLED_APP_NAME:-Traio.app}"

release_app_path() {
  printf "%s/%s.app" "$OUT_DIR" "$RELEASE_NAME"
}

usage() {
  cat <<EOF
Traio 本地使用脚本

用法:
  scripts/local.sh help
  scripts/local.sh test
  scripts/local.sh dev
  scripts/local.sh dev-fresh
  scripts/local.sh clean
  scripts/local.sh release
  scripts/local.sh install
  scripts/local.sh all

命令:
  test      本地测试: go test ./... + flutter test
  dev       本地开发启动: 构建 bin/traio-server 后启动 Flutter macOS
  dev-fresh 完整清理缓存后 dev（端口/首页异常时用）
  clean     清理 dev/安装缓存（保留 ibkr-gateway 与 data/）
  release   打 macOS release: 默认先 test，再 make macos-release
  install   安装到本机: 复制 release app 到 ${INSTALL_DIR}/${INSTALLED_APP_NAME}
  all       test + release + install

常用例子:
  scripts/local.sh test
  VERSION=0.2.0 scripts/local.sh release
  VERSION=0.2.0 scripts/local.sh install
  INSTALL_DIR=/Applications scripts/local.sh install
  SKIP_TESTS=1 scripts/local.sh release

当前参数:
  VERSION=${VERSION}
  OUT_DIR=${OUT_DIR}
  RELEASE_NAME=${RELEASE_NAME}
  release app=$(release_app_path)
  INSTALL_DIR=${INSTALL_DIR}
  INSTALLED_APP_NAME=${INSTALLED_APP_NAME}

说明:
  - release 产物来自 Makefile 的 macos-release。
  - install 如果找不到 release app，会先自动执行 release。
  - 默认安装到 ~/Applications，避免需要 sudo；如需全局安装可设置 INSTALL_DIR=/Applications。
EOF
}

run_test() {
  cd "$ROOT_DIR"
  make test
}

run_dev() {
  cd "$ROOT_DIR"
  make dev
}

run_dev_fresh() {
  cd "$ROOT_DIR"
  make dev-fresh
}

run_clean() {
  cd "$ROOT_DIR"
  make clean-local
}

run_release() {
  cd "$ROOT_DIR"
  if [[ "${SKIP_TESTS:-0}" != "1" ]]; then
    make test
  fi
  make macos-release OUT_DIR="$OUT_DIR" VERSION="$VERSION" RELEASE_NAME="$RELEASE_NAME"
}

run_install() {
  local app_path
  app_path="$(release_app_path)"

  if [[ ! -d "$app_path" ]]; then
    echo "未找到 release app: $app_path"
    echo "先执行 release..."
    run_release
  fi

  mkdir -p "$INSTALL_DIR"
  rm -rf "${INSTALL_DIR}/${INSTALLED_APP_NAME}"
  cp -R "$app_path" "${INSTALL_DIR}/${INSTALLED_APP_NAME}"
  echo "installed -> ${INSTALL_DIR}/${INSTALLED_APP_NAME}"
}

cmd="${1:-help}"
case "$cmd" in
  help|-h|--help)
    usage
    ;;
  test)
    run_test
    ;;
  dev)
    run_dev
    ;;
  dev-fresh)
    run_dev_fresh
    ;;
  clean)
    run_clean
    ;;
  release)
    run_release
    ;;
  install)
    run_install
    ;;
  all)
    run_release
    run_install
    ;;
  *)
    echo "unknown command: $cmd" >&2
    echo >&2
    usage >&2
    exit 2
    ;;
esac
