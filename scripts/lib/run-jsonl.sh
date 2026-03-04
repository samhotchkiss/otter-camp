#!/usr/bin/env bash
set -euo pipefail

run_jsonl__json_escape() {
  local value="${1:-}"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  value="${value//$'\n'/\\n}"
  value="${value//$'\r'/\\r}"
  value="${value//$'\t'/\\t}"
  printf '%s' "${value}"
}

run_jsonl_started_count() {
  local file="$1"
  [[ -f "${file}" ]] || {
    echo 0
    return 0
  }

  if command -v jq >/dev/null 2>&1; then
    jq -s -r '[.[] | select(.type == "turn.started")] | length' "${file}" 2>/dev/null || echo 0
    return 0
  fi

  grep -Eoc '"type"[[:space:]]*:[[:space:]]*"turn\.started"' "${file}" 2>/dev/null || echo 0
}

run_jsonl_terminal_count() {
  local file="$1"
  [[ -f "${file}" ]] || {
    echo 0
    return 0
  }

  if command -v jq >/dev/null 2>&1; then
    jq -s -r '[.[] | select(.type == "turn.completed" or .type == "turn.failed" or .type == "run.interrupted")] | length' "${file}" 2>/dev/null || echo 0
    return 0
  fi

  grep -Eoc '"type"[[:space:]]*:[[:space:]]*"(turn\.completed|turn\.failed|run\.interrupted)"' "${file}" 2>/dev/null || echo 0
}

run_jsonl_missing_terminal_state() {
  local file="$1"
  local started terminal
  started="$(run_jsonl_started_count "${file}")"
  terminal="$(run_jsonl_terminal_count "${file}")"
  if (( started > 0 && terminal == 0 )); then
    return 0
  fi
  return 1
}

run_jsonl_append_interrupted_terminal() {
  local file="$1"
  local reason="${2:-runner_exit_without_terminal_event}"
  local source="${3:-unknown}"
  local exit_code="${4:-}"
  local timestamp="${5:-$(date -u '+%Y-%m-%dT%H:%M:%SZ')}"
  local terminal
  terminal="$(run_jsonl_terminal_count "${file}")"
  if (( terminal > 0 )); then
    echo "already_terminal"
    return 0
  fi

  mkdir -p "$(dirname "${file}")"
  touch "${file}"

  local exit_json="null"
  if [[ -n "${exit_code}" && "${exit_code}" =~ ^-?[0-9]+$ ]]; then
    exit_json="${exit_code}"
  fi

  printf '{"type":"run.interrupted","synthetic":true,"timestamp":"%s","reason":"%s","source":"%s","exit_code":%s}\n' \
    "$(run_jsonl__json_escape "${timestamp}")" \
    "$(run_jsonl__json_escape "${reason}")" \
    "$(run_jsonl__json_escape "${source}")" \
    "${exit_json}" >> "${file}"

  echo "appended"
}
