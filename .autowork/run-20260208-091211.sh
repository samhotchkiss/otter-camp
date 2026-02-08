#!/usr/bin/env bash
set -euo pipefail

cd /Users/sam/Documents/Dev/otter-camp

printf '[%s] autowork run started\n' "$(date '+%Y-%m-%d %H:%M:%S %Z')"

codex exec \
  --dangerously-bypass-approvals-and-sandbox \
  --cd /Users/sam/Documents/Dev/otter-camp \
  - < /Users/sam/Documents/Dev/otter-camp/.autowork/autowork-prompt.txt

status=$?
printf '[%s] autowork run finished (exit=%s)\n' "$(date '+%Y-%m-%d %H:%M:%S %Z')" "${status}"
exit "${status}"
