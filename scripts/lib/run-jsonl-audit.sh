#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/lib/run-jsonl.sh
source "${SCRIPT_DIR}/run-jsonl.sh"

REPAIR=0
if [[ "${1:-}" == "--repair" ]]; then
  REPAIR=1
  shift
fi

if (( $# == 0 )); then
  echo "usage: $0 [--repair] <jsonl-file-or-glob> [more-files-or-globs...]" >&2
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

total=0
ok=0
missing=0
repaired=0

for file in "${sorted_files[@]}"; do
  [[ -f "${file}" ]] || continue
  total=$((total + 1))
  started="$(run_jsonl_started_count "${file}")"
  terminal="$(run_jsonl_terminal_count "${file}")"
  status="ok"
  repair_state="no"

  if (( started > 0 && terminal == 0 )); then
    status="missing_terminal"
    missing=$((missing + 1))
    if (( REPAIR == 1 )); then
      action="$(
        run_jsonl_append_interrupted_terminal \
          "${file}" \
          "jsonl_audit_missing_terminal" \
          "run-jsonl-audit.sh"
      )"
      if [[ "${action}" == "appended" ]]; then
        repair_state="yes"
        repaired=$((repaired + 1))
        status="repaired_missing_terminal"
      fi
    fi
  else
    ok=$((ok + 1))
  fi

  printf 'file=%s status=%s started=%s terminal=%s repaired=%s\n' \
    "$(basename "${file}")" "${status}" "${started}" "${terminal}" "${repair_state}"
done

remaining_missing=$((missing - repaired))
printf 'summary total=%s ok=%s missing=%s repaired=%s remaining_missing=%s\n' \
  "${total}" "${ok}" "${missing}" "${repaired}" "${remaining_missing}"

if (( remaining_missing > 0 )); then
  exit 1
fi
