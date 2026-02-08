#!/usr/bin/env bash
set -euo pipefail

cd /Users/sam/Documents/Dev/otter-camp

RUN_LOG="${RUN_LOG:?RUN_LOG is required}"
RUN_JSON_LOG="${RUN_JSON_LOG:?RUN_JSON_LOG is required}"
STREAM_LOG="${STREAM_LOG:?STREAM_LOG is required}"

printf '[%s] autowork run started\n' "$(date '+%Y-%m-%d %H:%M:%S %Z')"

if command -v jq >/dev/null 2>&1; then
  codex exec \
    --json \
    --dangerously-bypass-approvals-and-sandbox \
    --cd /Users/sam/Documents/Dev/otter-camp \
    --output-last-message /Users/sam/Documents/Dev/otter-camp/.autowork/autowork-last-message.txt \
    - < /Users/sam/Documents/Dev/otter-camp/.autowork/autowork-prompt.txt \
    | tee -a "${RUN_JSON_LOG}" \
    | jq -r '
      def txt: tostring | gsub("\\r"; "");
      if .type == "turn.started" then
        "---- turn started ----"
      elif .type == "item.completed" and .item.type == "agent_message" then
        "[agent] " + (.item.text // "")
      elif .type == "item.started" and .item.type == "command_execution" then
        "[cmd] " + (.item.command // "")
      elif .type == "item.completed" and .item.type == "command_execution" then
        "[cmd done] exit=" + ((.item.exit_code // -1)|tostring) + " " + (.item.command // "") +
        (if (.item.aggregated_output // "") != "" then "\n" + (.item.aggregated_output|txt) else "" end)
      elif .type == "item.completed" and .item.type == "reasoning" then
        "[plan] " + (.item.text // "")
      elif .type == "error" then
        "[error] " + ((.error.message // .message // "unknown error")|txt)
      else empty end
    ' \
    | tee -a "${RUN_LOG}" "${STREAM_LOG}"
  status=${PIPESTATUS[0]}
else
  codex exec \
    --dangerously-bypass-approvals-and-sandbox \
    --cd /Users/sam/Documents/Dev/otter-camp \
    --output-last-message /Users/sam/Documents/Dev/otter-camp/.autowork/autowork-last-message.txt \
    - < /Users/sam/Documents/Dev/otter-camp/.autowork/autowork-prompt.txt \
    | tee -a "${RUN_LOG}" "${STREAM_LOG}"
  status=$?
fi

printf '[%s] autowork run finished (exit=%s)\n' "$(date '+%Y-%m-%d %H:%M:%S %Z')" "${status}"
exit "${status}"
