#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/lib/command-outcome.sh
source "${SCRIPT_DIR}/command-outcome.sh"

if (( $# == 0 )); then
  echo "usage: $0 <jsonl-file-or-glob> [more-files-or-globs...]" >&2
  exit 2
fi

files=()
for pattern in "$@"; do
  while IFS= read -r match; do
    [[ -z "${match}" ]] && continue
    files+=("${match}")
  done < <(compgen -G "${pattern}" || true)
done

if (( ${#files[@]} == 0 )); then
  echo "no files matched" >&2
  exit 2
fi

IFS=$'\n' read -r -d '' -a sorted_files < <(printf '%s\n' "${files[@]}" | sort -u && printf '\0')

files_count=0
agg_total=0
agg_success=0
agg_nonzero=0
agg_lookup_miss=0
agg_search_miss=0
agg_build_or_test_failure=0
agg_infra_failure=0
agg_hard_blockers=0

for file in "${sorted_files[@]}"; do
  [[ -f "${file}" ]] || continue
  files_count=$((files_count + 1))
  summary="$(summarize_run_jsonl_command_outcomes "${file}")"
  printf 'file=%s %s\n' "$(basename "${file}")" "${summary}"

  for kv in ${summary}; do
    key="${kv%%=*}"
    value="${kv#*=}"
    case "${key}" in
      total) agg_total=$((agg_total + value)) ;;
      success) agg_success=$((agg_success + value)) ;;
      nonzero) agg_nonzero=$((agg_nonzero + value)) ;;
      lookup_miss) agg_lookup_miss=$((agg_lookup_miss + value)) ;;
      search_miss) agg_search_miss=$((agg_search_miss + value)) ;;
      build_or_test_failure) agg_build_or_test_failure=$((agg_build_or_test_failure + value)) ;;
      infra_failure) agg_infra_failure=$((agg_infra_failure + value)) ;;
      hard_blockers) agg_hard_blockers=$((agg_hard_blockers + value)) ;;
    esac
  done
done

printf 'aggregate files=%s total=%s success=%s nonzero=%s lookup_miss=%s search_miss=%s build_or_test_failure=%s infra_failure=%s hard_blockers=%s\n' \
  "${files_count}" \
  "${agg_total}" \
  "${agg_success}" \
  "${agg_nonzero}" \
  "${agg_lookup_miss}" \
  "${agg_search_miss}" \
  "${agg_build_or_test_failure}" \
  "${agg_infra_failure}" \
  "${agg_hard_blockers}"
