#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

assert_contains() {
  local file="$1"
  local expected="$2"
  if ! grep -Fq "${expected}" "${file}"; then
    echo "expected string not found in ${file}: ${expected}" >&2
    exit 1
  fi
}

assert_contains \
  "${REPO_DIR}/scripts/codex-autowork.sh" \
  'STATE_DIR="${AUTO_WORK_STATE_DIR:-${HOME}/otter-data/sessions}"'

assert_contains \
  "${REPO_DIR}/scripts/autowork-supervisor-watchdog.sh" \
  'STATE_DIR="${AUTOWORK_STATE_DIR:-${HOME}/otter-data/sessions}"'

assert_contains \
  "${REPO_DIR}/scripts/claude-review-autowork.sh" \
  'STATE_DIR="${AUTO_REVIEW_STATE_DIR:-${HOME}/otter-data/sessions}"'

echo "autowork default state dir checks passed"
