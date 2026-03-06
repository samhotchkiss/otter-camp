#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUPERVISOR="${SCRIPT_DIR}/autowork-supervisor-watchdog.sh"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

assert_contains() {
  local file="$1"
  local expected="$2"
  if ! grep -Fq "${expected}" "${file}"; then
    echo "expected string not found in ${file}: ${expected}" >&2
    sed -n '1,220p' "${file}" >&2 || true
    exit 1
  fi
}

assert_not_contains() {
  local file="$1"
  local unexpected="$2"
  if grep -Fq "${unexpected}" "${file}"; then
    echo "unexpected string found in ${file}: ${unexpected}" >&2
    sed -n '1,220p' "${file}" >&2 || true
    exit 1
  fi
}

assert_equals() {
  local actual="$1"
  local expected="$2"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "expected '${expected}', got '${actual}'" >&2
    exit 1
  fi
}

stop_holder() {
  local pid="${1:-}"
  if [[ -z "${pid}" ]]; then
    return 0
  fi
  kill "${pid}" 2>/dev/null || true
  wait "${pid}" 2>/dev/null || true
}

setup_issue_dirs() {
  local root="$1"
  mkdir -p \
    "${root}/issues/01-ready" \
    "${root}/issues/02-in-progress" \
    "${root}/issues/03-needs-review" \
    "${root}/issues/04-in-review" \
    "${root}/issues/05-completed" \
    "${root}/state"
}

run_lock_state_unit_test() {
  local root="${TMP_DIR}/unit"
  setup_issue_dirs "${root}"

  (
    export AUTOWORK_REPO_DIR="${REPO_DIR}"
    export AUTOWORK_ISSUES_DIR="${root}/issues"
    export AUTOWORK_STATE_DIR="${root}/state"
    export WATCHDOG_LOCK_STALE_SECONDS=30
    source "${SUPERVISOR}"

    local holder
    sleep 30 &
    holder=$!

    mkdir -p "${LOCK_DIR}"
    printf '%s\n' "${holder}" > "${LOCK_DIR}/pid"
    touch "${LOCK_HEARTBEAT_FILE}"

    local state reason pid age
    IFS=$'\t' read -r state reason pid age < <(inspect_lock_state)
    assert_equals "${state}" "active"
    assert_equals "${reason}" "fresh"
    assert_equals "${pid}" "${holder}"

    sleep 1
    LOCK_STALE_SECONDS=0
    IFS=$'\t' read -r state reason pid age < <(inspect_lock_state)
    assert_equals "${state}" "stale"
    assert_equals "${reason}" "heartbeat_expired"
    assert_equals "${pid}" "${holder}"

    stop_holder "${holder}"
  )
}

run_abandoned_lock_recovery_test() {
  local root="${TMP_DIR}/recovery"
  local output_file="${root}/watchdog.out"
  setup_issue_dirs "${root}"

  mkdir -p "${root}/state/supervisor-watchdog.lock"
  printf '%s\n' "999999" > "${root}/state/supervisor-watchdog.lock/pid"
  touch "${root}/state/supervisor-watchdog.lock/heartbeat"
  sleep 1

  AUTOWORK_REPO_DIR="${REPO_DIR}" \
  AUTOWORK_ISSUES_DIR="${root}/issues" \
  AUTOWORK_STATE_DIR="${root}/state" \
  AUTOWORK_BASE_BRANCH=main \
  WATCHDOG_DRY_RUN=1 \
  WATCHDOG_LOCK_STALE_SECONDS=0 \
  "${SUPERVISOR}" > "${output_file}" 2>&1

  assert_contains "${output_file}" "reclaiming stale watchdog lock"
  assert_contains "${output_file}" "run complete"
  assert_not_contains "${output_file}" "supervisor watchdog already running"
}

run_live_lock_skip_test() {
  local root="${TMP_DIR}/skip"
  local output_file="${root}/watchdog.out"
  local holder
  setup_issue_dirs "${root}"

  sleep 30 &
  holder=$!

  mkdir -p "${root}/state/supervisor-watchdog.lock"
  printf '%s\n' "${holder}" > "${root}/state/supervisor-watchdog.lock/pid"
  touch "${root}/state/supervisor-watchdog.lock/heartbeat"

  AUTOWORK_REPO_DIR="${REPO_DIR}" \
  AUTOWORK_ISSUES_DIR="${root}/issues" \
  AUTOWORK_STATE_DIR="${root}/state" \
  AUTOWORK_BASE_BRANCH=main \
  WATCHDOG_DRY_RUN=1 \
  WATCHDOG_LOCK_STALE_SECONDS=30 \
  "${SUPERVISOR}" > "${output_file}" 2>&1

  assert_contains "${output_file}" "supervisor watchdog already running"
  assert_not_contains "${output_file}" "reclaiming stale watchdog lock"
  stop_holder "${holder}"
}

run_lock_state_unit_test
run_abandoned_lock_recovery_test
run_live_lock_skip_test

echo "autowork supervisor watchdog tests passed"
