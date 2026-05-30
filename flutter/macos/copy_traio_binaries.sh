#!/bin/bash
# Embed Traio Go binaries (and optional IBKR gateway) into the macOS .app bundle.
set -euo pipefail

BIN_DIR="${BIN_DIR:-bin}"
SRC_BIN="${SRCROOT}/../../${BIN_DIR}"
DEST="${BUILT_PRODUCTS_DIR}/${PRODUCT_NAME}.app/Contents/Resources"
mkdir -p "$DEST"

copy_bin() {
  local name="$1"
  if [[ -f "${SRC_BIN}/${name}" ]]; then
    cp "${SRC_BIN}/${name}" "${DEST}/${name}"
    chmod +x "${DEST}/${name}"
    echo "embedded ${name}"
  else
    echo "warning: ${SRC_BIN}/${name} not found — run 'make build-binaries' from repo root"
  fi
}

copy_bin traio-server
copy_bin traio-mcp

GW_SRC="${SRCROOT}/../../third_party/clientportal.gw"
if [[ -f "${GW_SRC}/bin/run.sh" ]]; then
  rm -rf "${DEST}/third_party/clientportal.gw"
  mkdir -p "${DEST}/third_party"
  cp -R "${GW_SRC}" "${DEST}/third_party/clientportal.gw"
  echo "embedded IBKR gateway"
fi
