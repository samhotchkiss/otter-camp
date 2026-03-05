#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/lib/command-outcome.sh
source "${SCRIPT_DIR}/command-outcome.sh"

usage() {
  cat <<'USAGE'
usage: baseline-health-gate.sh <run-jsonl> <baseline-matrix-json> <flake-registry-json>
USAGE
}

if (( $# != 3 )); then
  usage
  exit 2
fi

RUN_JSONL="$1"
BASELINE_MATRIX="$2"
FLAKE_REGISTRY="$3"

if [[ ! -f "${BASELINE_MATRIX}" ]]; then
  printf 'gate_status=artifact_missing baseline_health_status=unknown matrix_version=missing registry_version=unknown task_scope_regressions=0 waived_known_flakes=0 waived_flake_refs=none\n'
  exit 0
fi

if [[ ! -f "${FLAKE_REGISTRY}" ]]; then
  printf 'gate_status=artifact_missing baseline_health_status=unknown matrix_version=unknown registry_version=missing task_scope_regressions=0 waived_known_flakes=0 waived_flake_refs=none\n'
  exit 0
fi

if ! jq -e '.' "${BASELINE_MATRIX}" >/dev/null 2>&1; then
  printf 'gate_status=artifact_invalid baseline_health_status=unknown matrix_version=invalid registry_version=unknown task_scope_regressions=0 waived_known_flakes=0 waived_flake_refs=none\n'
  exit 0
fi

if ! jq -e '.' "${FLAKE_REGISTRY}" >/dev/null 2>&1; then
  printf 'gate_status=artifact_invalid baseline_health_status=unknown matrix_version=unknown registry_version=invalid task_scope_regressions=0 waived_known_flakes=0 waived_flake_refs=none\n'
  exit 0
fi

matrix_version="$(jq -r '.version // "unknown"' "${BASELINE_MATRIX}")"
baseline_health_status="$(jq -r '.baseline_health // "unknown"' "${BASELINE_MATRIX}")"
baseline_command_count="$(jq -r '(.commands // []) | length' "${BASELINE_MATRIX}")"
registry_version="$(jq -r '.version // "unknown"' "${FLAKE_REGISTRY}")"
today_utc="$(date -u +%F)"

active_flakes=()
while IFS=$'\t' read -r flake_id flake_pattern; do
  [[ -z "${flake_id}" ]] && continue
  active_flakes+=("${flake_id}"$'\t'"${flake_pattern}")
done < <(
  jq -r --arg today "${today_utc}" '
    (.entries // [])
    | .[]
    | select((.status // "inactive") == "active" and (.expires_on // "0000-00-00") >= $today)
    | "\(.id)\t\(.pattern)"
  ' "${FLAKE_REGISTRY}"
)

task_scope_regressions=0
waived_known_flakes=0
observed_test_failures=0
declare -a waived_refs=()

if [[ -f "${RUN_JSONL}" ]] && command -v jq >/dev/null 2>&1; then
  while IFS=$'\t' read -r exit_code command aggregated_output; do
    [[ -z "${exit_code}" ]] && continue
    outcome="$(classify_command_outcome "${exit_code}" "${command}" "${aggregated_output}")"
    if [[ "${outcome}" != "build_or_test_failure" ]]; then
      continue
    fi

    observed_test_failures=$((observed_test_failures + 1))
    combined_text="${command}"$'\n'"${aggregated_output}"

    flake_match=""
    for row in "${active_flakes[@]}"; do
      flake_id="${row%%$'\t'*}"
      flake_pattern="${row#*$'\t'}"
      if [[ "${combined_text}" =~ ${flake_pattern} ]]; then
        flake_match="${flake_id}"
        break
      fi
    done

    if [[ -n "${flake_match}" ]]; then
      waived_known_flakes=$((waived_known_flakes + 1))
      waived_refs+=("${flake_match}")
    else
      task_scope_regressions=$((task_scope_regressions + 1))
    fi
  done < <(
    jq -r '
      . as $root
      | select($root.type == "item.completed" and ($root.item.type // "") == "command_execution")
      | [($root.item.exit_code // -1), ($root.item.command // ""), ($root.item.aggregated_output // "")]
      | @tsv
    ' "${RUN_JSONL}" 2>/dev/null || true
  )
fi

waived_refs_csv="none"
if (( ${#waived_refs[@]} > 0 )); then
  waived_refs_csv="$(printf '%s\n' "${waived_refs[@]}" | sort -u | paste -sd ',' -)"
fi

gate_status="pass"
if (( task_scope_regressions > 0 )); then
  gate_status="task_regression_detected"
elif [[ "${baseline_health_status}" != "healthy" ]]; then
  gate_status="baseline_degraded"
fi

printf 'gate_status=%s baseline_health_status=%s matrix_version=%s registry_version=%s baseline_command_count=%s observed_test_failures=%s task_scope_regressions=%s waived_known_flakes=%s waived_flake_refs=%s\n' \
  "${gate_status}" \
  "${baseline_health_status}" \
  "${matrix_version}" \
  "${registry_version}" \
  "${baseline_command_count}" \
  "${observed_test_failures}" \
  "${task_scope_regressions}" \
  "${waived_known_flakes}" \
  "${waived_refs_csv}"
