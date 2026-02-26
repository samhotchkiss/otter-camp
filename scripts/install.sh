#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

INSTALL_DIR="${OTTERCAMP_INSTALL_DIR:-${HOME}/.local/bin}"
BIN_PATH="${REPO_DIR}/bin/ottercamp"
TARGET_PATH="${INSTALL_DIR}/ottercamp"

echo "[install] building ottercamp -> ${BIN_PATH}"
mkdir -p "${REPO_DIR}/bin"
(
  cd "${REPO_DIR}"
  go build -o "${BIN_PATH}" ./cmd/ottercamp
)

echo "[install] linking ${TARGET_PATH} -> ${BIN_PATH}"
mkdir -p "${INSTALL_DIR}"
ln -sf "${BIN_PATH}" "${TARGET_PATH}"

if ! "${TARGET_PATH}" tui --help >/dev/null 2>&1; then
  echo "[install] ERROR: installed binary failed tui smoke check" >&2
  exit 1
fi

hash -r 2>/dev/null || true

echo "[install] installed successfully"
echo "[install] command: ${TARGET_PATH}"

if [[ ":${PATH}:" != *":${INSTALL_DIR}:"* ]]; then
  echo "[install] PATH missing ${INSTALL_DIR}. Add this to your shell profile:"
  echo "export PATH=\"${INSTALL_DIR}:\$PATH\""
fi
