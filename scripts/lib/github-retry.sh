#!/usr/bin/env bash
set -euo pipefail

GITHUB_RETRY_MAX_ATTEMPTS="${GITHUB_RETRY_MAX_ATTEMPTS:-5}"
GITHUB_RETRY_BASE_DELAY_SECONDS="${GITHUB_RETRY_BASE_DELAY_SECONDS:-1}"
GITHUB_RETRY_MAX_BACKOFF_SECONDS="${GITHUB_RETRY_MAX_BACKOFF_SECONDS:-16}"
GITHUB_RETRY_JITTER_MAX_SECONDS="${GITHUB_RETRY_JITTER_MAX_SECONDS:-1}"

github_retry_usage() {
  cat <<'USAGE'
Usage:
  github-retry.sh git push <args...>
  github-retry.sh gh pr create <args...>
  github-retry.sh gh pr edit <args...>

Retries only transient GitHub failures (HTTP 5xx, network transport issues) with
bounded exponential backoff + jitter.
USAGE
}

github_retry_log() {
  printf 'github_retry %s\n' "$*"
}

github_retry_supported_command() {
  local cmd=("$@")
  if (( ${#cmd[@]} < 2 )); then
    return 1
  fi

  if [[ "${cmd[0]}" == "git" && "${cmd[1]}" == "push" ]]; then
    return 0
  fi

  if (( ${#cmd[@]} >= 3 )) && [[ "${cmd[0]}" == "gh" && "${cmd[1]}" == "pr" ]] && [[ "${cmd[2]}" =~ ^(create|edit)$ ]]; then
    return 0
  fi

  return 1
}

github_retry_classify_failure() {
  local output_lc
  output_lc="$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]')"

  if [[ "${output_lc}" =~ http[[:space:]]*5[0-9]{2} ]] || \
     [[ "${output_lc}" =~ status[[:space:]]*5[0-9]{2} ]] || \
     [[ "${output_lc}" =~ internal[[:space:]]server[[:space:]]error ]] || \
     [[ "${output_lc}" =~ bad[[:space:]]gateway ]] || \
     [[ "${output_lc}" =~ service[[:space:]]unavailable ]] || \
     [[ "${output_lc}" =~ gateway[[:space:]]timeout ]]; then
    echo "transient_http_5xx"
    return 0
  fi

  if [[ "${output_lc}" =~ timeout ]] || \
     [[ "${output_lc}" =~ timed[[:space:]]out ]] || \
     [[ "${output_lc}" =~ connection[[:space:]]reset ]] || \
     [[ "${output_lc}" =~ connection[[:space:]]refused ]] || \
     [[ "${output_lc}" =~ temporary[[:space:]]failure ]] || \
     [[ "${output_lc}" =~ could[[:space:]]not[[:space:]]resolve[[:space:]]host ]] || \
     [[ "${output_lc}" =~ network[[:space:]]is[[:space:]]unreachable ]] || \
     [[ "${output_lc}" =~ tls[[:space:]]handshake[[:space:]]timeout ]] || \
     [[ "${output_lc}" =~ unexpected[[:space:]]eof ]]; then
    echo "transient_network"
    return 0
  fi

  if [[ "${output_lc}" =~ authentication[[:space:]]failed ]] || \
     [[ "${output_lc}" =~ bad[[:space:]]credentials ]] || \
     [[ "${output_lc}" =~ permission[[:space:]]denied ]] || \
     [[ "${output_lc}" =~ forbidden ]] || \
     [[ "${output_lc}" =~ not[[:space:]]authorized ]] || \
     [[ "${output_lc}" =~ resource[[:space:]]not[[:space:]]accessible ]]; then
    echo "permanent_auth_or_permission"
    return 0
  fi

  if [[ "${output_lc}" =~ validation[[:space:]]failed ]] || \
     [[ "${output_lc}" =~ invalid[[:space:]](argument|option|value) ]] || \
     [[ "${output_lc}" =~ unknown[[:space:]](flag|option) ]] || \
     [[ "${output_lc}" =~ usage: ]]; then
    echo "permanent_invalid_args"
    return 0
  fi

  echo "unknown_failure"
}

github_retry_is_retryable() {
  local classification="${1:-unknown_failure}"
  case "${classification}" in
    transient_http_5xx|transient_network)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

github_retry_backoff_seconds() {
  local attempt="${1:-1}"
  local delay="${GITHUB_RETRY_BASE_DELAY_SECONDS}"
  local i
  for ((i=1; i<attempt; i++)); do
    delay=$((delay * 2))
    if (( delay >= GITHUB_RETRY_MAX_BACKOFF_SECONDS )); then
      delay="${GITHUB_RETRY_MAX_BACKOFF_SECONDS}"
      break
    fi
  done
  printf '%s' "${delay}"
}

github_retry_jitter_seconds() {
  if (( GITHUB_RETRY_JITTER_MAX_SECONDS <= 0 )); then
    printf '0'
    return 0
  fi
  printf '%s' "$(( RANDOM % (GITHUB_RETRY_JITTER_MAX_SECONDS + 1) ))"
}

main() {
  if (( $# == 0 )); then
    github_retry_usage
    exit 2
  fi

  local cmd=("$@")
  if ! github_retry_supported_command "${cmd[@]}"; then
    github_retry_usage
    github_retry_log "outcome=unsupported_command command=\"${cmd[*]}\""
    exit 2
  fi

  local attempt output rc classification retryable backoff jitter sleep_for
  for ((attempt=1; attempt<=GITHUB_RETRY_MAX_ATTEMPTS; attempt++)); do
    set +e
    output="$("${cmd[@]}" 2>&1)"
    rc=$?
    set -e

    if [[ -n "${output}" ]]; then
      printf '%s\n' "${output}"
    fi

    if (( rc == 0 )); then
      github_retry_log "attempt=${attempt}/${GITHUB_RETRY_MAX_ATTEMPTS} action=success terminal_reason=success"
      return 0
    fi

    classification="$(github_retry_classify_failure "${output}")"
    if github_retry_is_retryable "${classification}"; then
      retryable=1
    else
      retryable=0
    fi

    if (( retryable == 1 && attempt < GITHUB_RETRY_MAX_ATTEMPTS )); then
      backoff="$(github_retry_backoff_seconds "${attempt}")"
      jitter="$(github_retry_jitter_seconds)"
      sleep_for=$(( backoff + jitter ))
      github_retry_log "attempt=${attempt}/${GITHUB_RETRY_MAX_ATTEMPTS} action=retry classification=${classification} backoff_seconds=${sleep_for} exit_code=${rc}"
      if (( sleep_for > 0 )); then
        sleep "${sleep_for}"
      fi
      continue
    fi

    if (( retryable == 1 )); then
      github_retry_log "attempt=${attempt}/${GITHUB_RETRY_MAX_ATTEMPTS} action=fail classification=${classification} terminal_reason=retry_exhausted exit_code=${rc}"
    else
      github_retry_log "attempt=${attempt}/${GITHUB_RETRY_MAX_ATTEMPTS} action=fail_fast classification=${classification} terminal_reason=non_retryable exit_code=${rc}"
    fi
    return "${rc}"
  done

  github_retry_log "attempt=${GITHUB_RETRY_MAX_ATTEMPTS}/${GITHUB_RETRY_MAX_ATTEMPTS} action=fail terminal_reason=unexpected_loop_exit"
  return 1
}

main "$@"
