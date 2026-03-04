#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ISSUE_LANE_CLI="${SCRIPT_DIR}/issue-lane.sh"

if [[ ! -x "${ISSUE_LANE_CLI}" ]]; then
  echo "missing queue helper CLI: ${ISSUE_LANE_CLI}" >&2
  exit 1
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

ISSUES_DIR="${TMP_DIR}/issues"
mkdir -p \
  "${ISSUES_DIR}/01-ready" \
  "${ISSUES_DIR}/02-in-progress" \
  "${ISSUES_DIR}/03-needs-review" \
  "${ISSUES_DIR}/04-in-review" \
  "${ISSUES_DIR}/05-completed"

TASK_FILE="223-demo-task.md"
printf '# demo\n' > "${ISSUES_DIR}/01-ready/${TASK_FILE}"

# Concurrent claim replay: one winner, one idempotent loser.
status_a_file="${TMP_DIR}/status-a.txt"
status_b_file="${TMP_DIR}/status-b.txt"

("${ISSUE_LANE_CLI}" claim "${ISSUES_DIR}" "${TASK_FILE}" > "${status_a_file}") &
pid_a=$!
("${ISSUE_LANE_CLI}" claim "${ISSUES_DIR}" "${TASK_FILE}" > "${status_b_file}") &
pid_b=$!
wait "${pid_a}" "${pid_b}"

status_a="$(cat "${status_a_file}")"
status_b="$(cat "${status_b_file}")"
if [[ "${status_a} ${status_b}" != *"claimed"* ]]; then
  echo "expected one claimed status, got: ${status_a}, ${status_b}" >&2
  exit 1
fi
if [[ "${status_a} ${status_b}" != *"already_claimed"* ]]; then
  echo "expected one already_claimed status, got: ${status_a}, ${status_b}" >&2
  exit 1
fi

# Idempotent stale-path move: second move should not fail hard.
first_move="$("${ISSUE_LANE_CLI}" move "${ISSUES_DIR}" "02-in-progress" "03-needs-review" "${TASK_FILE}")"
second_move="$("${ISSUE_LANE_CLI}" move "${ISSUES_DIR}" "02-in-progress" "03-needs-review" "${TASK_FILE}")"

if [[ "${first_move}" != "claimed" ]]; then
  echo "expected first move claimed, got: ${first_move}" >&2
  exit 1
fi
if [[ "${second_move}" != "already_completed" ]]; then
  echo "expected second move already_completed, got: ${second_move}" >&2
  exit 1
fi

# Missing-file classification.
missing_move="$("${ISSUE_LANE_CLI}" move "${ISSUES_DIR}" "01-ready" "02-in-progress" "404-missing.md")"
if [[ "${missing_move}" != "missing" ]]; then
  echo "expected missing classification, got: ${missing_move}" >&2
  exit 1
fi

# Reconciliation outcome: benign already-completed race.
reconcile_ok="$("${ISSUE_LANE_CLI}" reconcile "${ISSUES_DIR}" "02-in-progress" "03-needs-review" "${TASK_FILE}")"
if [[ "${reconcile_ok}" != "queue_reconciled" ]]; then
  echo "expected queue_reconciled status, got: ${reconcile_ok}" >&2
  exit 1
fi

# Reconciliation outcome: missing file should hard-stop.
reconcile_conflict="$("${ISSUE_LANE_CLI}" reconcile "${ISSUES_DIR}" "01-ready" "02-in-progress" "404-missing.md")"
if [[ "${reconcile_conflict}" != "queue_conflict_hard_stop" ]]; then
  echo "expected queue_conflict_hard_stop status, got: ${reconcile_conflict}" >&2
  exit 1
fi

echo "queue replay test passed"
