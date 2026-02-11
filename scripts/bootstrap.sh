#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

cd "${REPO_ROOT}"

echo "🦦 Otter Camp bootstrap (one command)"
echo

bash "${SCRIPT_DIR}/setup.sh" --yes "$@"

echo
echo "Starting Otter Camp services..."
make start

echo
echo "Ready: http://localhost:${PORT:-4200}"
