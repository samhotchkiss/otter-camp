#!/usr/bin/env bash
set -euo pipefail

HEALTH_PORT_DEFAULT="${OTTER_BRIDGE_HEALTH_PORT:-8787}"
BRIDGE_HEALTH_URL="${BRIDGE_HEALTH_URL:-http://127.0.0.1:${HEALTH_PORT_DEFAULT}/health}"
BRIDGE_MONITOR_STATE_FILE="${BRIDGE_MONITOR_STATE_FILE:-${TMPDIR:-/tmp}/ottercamp-bridge-monitor.state}"
BRIDGE_MONITOR_TIMEOUT_SECONDS="${BRIDGE_MONITOR_TIMEOUT_SECONDS:-8}"
BRIDGE_RESTART_CMD="${BRIDGE_RESTART_CMD:-launchctl kickstart -k gui/$(id -u)/com.ottercamp.bridge}"
BRIDGE_ALERT_CMD="${BRIDGE_ALERT_CMD:-}"
BRIDGE_ALERT_THRESHOLD="${BRIDGE_ALERT_THRESHOLD:-2}"

log() {
  printf '[bridge-monitor] %s %s\n' "$(date +'%Y-%m-%dT%H:%M:%S%z')" "$*" >&2
}

run_hook() {
  local cmd="$1"
  if [[ -z "${cmd}" ]]; then
    return 0
  fi
  bash -lc "${cmd}"
}

load_state() {
  failures=0
  reason="unknown"
  if [[ ! -f "${BRIDGE_MONITOR_STATE_FILE}" ]]; then
    return
  fi
  while IFS='=' read -r key value; do
    key="$(echo "${key}" | tr -d '[:space:]')"
    value="$(echo "${value}" | sed 's/^ *//;s/ *$//')"
    case "${key}" in
      failures)
        if [[ "${value}" =~ ^[0-9]+$ ]]; then
          failures="${value}"
        fi
        ;;
      reason)
        reason="${value}"
        ;;
    esac
  done < "${BRIDGE_MONITOR_STATE_FILE}"
}

save_state() {
  local current_failures="$1"
  local current_reason="$2"
  mkdir -p "$(dirname "${BRIDGE_MONITOR_STATE_FILE}")"
  cat > "${BRIDGE_MONITOR_STATE_FILE}" <<STATE
failures=${current_failures}
reason=${current_reason}
updated_at=$(date -u +'%Y-%m-%dT%H:%M:%SZ')
STATE
}

extract_health_status() {
  local body="$1"
  local normalized
  normalized="$(echo "${body}" | tr -d '\n\r')"
  local status
  status="$(echo "${normalized}" | sed -n 's/.*"status"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
  printf '%s' "${status}"
}

health_reason="unreachable"
health_detail="curl request failed"

check_health() {
  local body_file
  body_file="$(mktemp)"
  local http_code
  http_code=""

  if ! http_code="$(curl --silent --show-error --max-time "${BRIDGE_MONITOR_TIMEOUT_SECONDS}" --output "${body_file}" --write-out '%{http_code}' "${BRIDGE_HEALTH_URL}")"; then
    health_reason="unreachable"
    health_detail="bridge health endpoint unreachable"
    rm -f "${body_file}"
    return 1
  fi

  local body
  body="$(cat "${body_file}")"
  rm -f "${body_file}"

  if [[ "${http_code}" != "200" ]]; then
    health_reason="unhealthy"
    health_detail="bridge health returned http ${http_code}"
    return 1
  fi

  local status
  status="$(extract_health_status "${body}")"
  case "${status}" in
    healthy)
      health_reason="healthy"
      health_detail="bridge healthy"
      return 0
      ;;
    degraded)
      health_reason="degraded"
      health_detail="bridge degraded"
      return 0
      ;;
    unhealthy)
      health_reason="unhealthy"
      health_detail="bridge unhealthy"
      return 1
      ;;
    *)
      health_reason="unhealthy"
      health_detail="bridge returned unknown status '${status:-missing}'"
      return 1
      ;;
  esac
}

load_state

if check_health; then
  save_state 0 "${health_reason}"
  log "health=${health_reason}; no restart required"
  exit 0
fi

next_failures=$((failures + 1))
save_state "${next_failures}" "${health_reason}"

log "health check failed (${health_reason}): ${health_detail}; running restart command"
if ! run_hook "${BRIDGE_RESTART_CMD}"; then
  log "restart command failed"
fi

if (( next_failures >= BRIDGE_ALERT_THRESHOLD )); then
  export BRIDGE_MONITOR_REASON="${health_reason}"
  export BRIDGE_MONITOR_FAILURES="${next_failures}"
  export BRIDGE_MONITOR_HEALTH_URL="${BRIDGE_HEALTH_URL}"
  if [[ -n "${BRIDGE_ALERT_CMD}" ]]; then
    log "failure threshold reached (${next_failures}); running alert hook"
    if ! run_hook "${BRIDGE_ALERT_CMD}"; then
      log "alert hook failed"
    fi
  else
    log "failure threshold reached (${next_failures}); alert hook not configured"
  fi
fi

exit 1
