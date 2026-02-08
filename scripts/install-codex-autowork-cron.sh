#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="/Users/sam/Documents/Dev/otter-camp"
RUNNER_SCRIPT="${REPO_DIR}/scripts/codex-autowork.sh"
STATE_DIR="${REPO_DIR}/.autowork"
CRON_LOG="${STATE_DIR}/cron.log"

mkdir -p "${STATE_DIR}"

if [[ ! -x "${RUNNER_SCRIPT}" ]]; then
  echo "Runner script missing or not executable: ${RUNNER_SCRIPT}" >&2
  exit 1
fi

CRON_LINE="*/5 * * * * ${RUNNER_SCRIPT} >> ${CRON_LOG} 2>&1"

CURRENT="$(crontab -l 2>/dev/null || true)"
UPDATED="$(printf '%s\n' "${CURRENT}" | grep -v 'scripts/codex-autowork.sh' || true)"

{
  printf '%s\n' "${UPDATED}" | sed '/^$/N;/^\n$/D'
  printf '%s\n' "${CRON_LINE}"
} | crontab -

echo "Installed cron entry:"
crontab -l | grep 'scripts/codex-autowork.sh' || true
