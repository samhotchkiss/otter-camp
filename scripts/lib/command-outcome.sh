#!/usr/bin/env bash
set -euo pipefail

command_outcome__lower() {
  printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]'
}

command_outcome__is_search_cmd() {
  local cmd
  cmd="$(command_outcome__lower "${1:-}")"
  [[ "${cmd}" =~ (^|[[:space:]])(rg|grep|git[[:space:]]+grep|find|fd|locate)([[:space:]]|$) ]]
}

command_outcome__is_build_or_test_cmd() {
  local cmd
  cmd="$(command_outcome__lower "${1:-}")"
  [[ "${cmd}" =~ (^|[[:space:]])(go[[:space:]]+test|go[[:space:]]+build|go[[:space:]]+run|make[[:space:]]+test|make[[:space:]]+build|npm[[:space:]]+test|npm[[:space:]]+run[[:space:]]+build|pnpm[[:space:]]+test|pnpm[[:space:]]+build|yarn[[:space:]]+test|pytest|cargo[[:space:]]+test|cargo[[:space:]]+build|mvn[[:space:]]+test|gradle[[:space:]]+test|bazel[[:space:]]+test)([[:space:]]|$) ]]
}

classify_command_outcome() {
  local exit_code="${1:-1}"
  local command="${2:-}"
  local output="${3:-}"

  if [[ "${exit_code}" =~ ^-?[0-9]+$ ]] && (( exit_code == 0 )); then
    echo "success"
    return 0
  fi

  local output_lc command_lc
  output_lc="$(command_outcome__lower "${output}")"
  command_lc="$(command_outcome__lower "${command}")"

  if [[ "${output_lc}" =~ no[[:space:]]such[[:space:]]file ]] || \
     [[ "${output_lc}" =~ no[[:space:]]matches[[:space:]]found ]] || \
     [[ "${output_lc}" =~ cannot[[:space:]]access ]] || \
     [[ "${output_lc}" =~ pathspec[[:space:]].*did[[:space:]]not[[:space:]]match ]] || \
     [[ "${output_lc}" =~ stat:[[:space:]].*no[[:space:]]such[[:space:]]file ]]; then
    echo "lookup_miss"
    return 0
  fi

  if command_outcome__is_search_cmd "${command_lc}"; then
    if [[ -z "${output_lc//[[:space:]]/}" ]] || \
       [[ "${output_lc}" =~ no[[:space:]]matches ]] || \
       [[ "${output_lc}" =~ no[[:space:]]files[[:space:]]were[[:space:]]searched ]]; then
      echo "search_miss"
      return 0
    fi
  fi

  if command_outcome__is_build_or_test_cmd "${command_lc}" || \
     [[ "${output_lc}" =~ (^|[[:space:]])fail($|[[:space:]]) ]] || \
     [[ "${output_lc}" =~ compilation[[:space:]]failed ]] || \
     [[ "${output_lc}" =~ test[[:space:]]failed ]]; then
    echo "build_or_test_failure"
    return 0
  fi

  echo "infra_failure"
}

summarize_run_jsonl_command_outcomes() {
  local file="$1"
  local total=0 success=0 lookup_miss=0 search_miss=0 build_or_test_failure=0 infra_failure=0
  local nonzero=0 hard_blockers=0

  if [[ ! -f "${file}" ]] || ! command -v jq >/dev/null 2>&1; then
    printf 'total=0 success=0 nonzero=0 lookup_miss=0 search_miss=0 build_or_test_failure=0 infra_failure=0 hard_blockers=0 parser=unavailable'
    return 0
  fi

  local exit_code command aggregated_output outcome
  while IFS=$'\t' read -r exit_code command aggregated_output; do
    [[ -z "${exit_code}" ]] && continue
    total=$((total + 1))
    outcome="$(classify_command_outcome "${exit_code}" "${command}" "${aggregated_output}")"
    case "${outcome}" in
      success) success=$((success + 1)) ;;
      lookup_miss) lookup_miss=$((lookup_miss + 1)) ;;
      search_miss) search_miss=$((search_miss + 1)) ;;
      build_or_test_failure) build_or_test_failure=$((build_or_test_failure + 1)) ;;
      infra_failure) infra_failure=$((infra_failure + 1)) ;;
    esac
  done < <(
    jq -r '
      . as $root
      | select($root.type == "item.completed" and ($root.item.type // "") == "command_execution")
      | [($root.item.exit_code // -1), ($root.item.command // ""), ($root.item.aggregated_output // "")]
      | @tsv
    ' "${file}" 2>/dev/null || true
  )

  nonzero=$(( lookup_miss + search_miss + build_or_test_failure + infra_failure ))
  hard_blockers=$(( build_or_test_failure + infra_failure ))
  printf 'total=%s success=%s nonzero=%s lookup_miss=%s search_miss=%s build_or_test_failure=%s infra_failure=%s hard_blockers=%s parser=jsonl' \
    "${total}" "${success}" "${nonzero}" "${lookup_miss}" "${search_miss}" "${build_or_test_failure}" "${infra_failure}" "${hard_blockers}"
}
