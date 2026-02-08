#!/usr/bin/env bash
set -euo pipefail

cd /Users/sam/Documents/Dev/otter-camp

RUN_LOG="${RUN_LOG:?RUN_LOG is required}"
STREAM_LOG="${STREAM_LOG:?STREAM_LOG is required}"

printf '[%s] autowork run started\n' "$(date '+%Y-%m-%d %H:%M:%S %Z')"

codex exec \
  --json \
  --dangerously-bypass-approvals-and-sandbox \
  --cd /Users/sam/Documents/Dev/otter-camp \
  --output-last-message /Users/sam/Documents/Dev/otter-camp/.autowork/autowork-last-message.txt \
  - < /Users/sam/Documents/Dev/otter-camp/.autowork/autowork-prompt.txt \
  | tee -a "${RUN_LOG}" "${STREAM_LOG}"

status=$?
printf '[%s] autowork run finished (exit=%s)\n' "$(date '+%Y-%m-%d %H:%M:%S %Z')" "${status}"
exit "${status}"
