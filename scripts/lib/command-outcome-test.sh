#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/lib/command-outcome.sh
source "${SCRIPT_DIR}/command-outcome.sh"

assert_eq() {
  local got="$1"
  local want="$2"
  local msg="$3"
  if [[ "${got}" != "${want}" ]]; then
    echo "assertion failed: ${msg}; got=${got}, want=${want}" >&2
    exit 1
  fi
}

assert_eq "$(classify_command_outcome 0 "rg -n foo ." "")" "success" "success classification"
assert_eq "$(classify_command_outcome 1 "cat missing.txt" "cat: missing.txt: No such file or directory")" "lookup_miss" "lookup miss classification"
assert_eq "$(classify_command_outcome 1 "rg -n needle internal" "")" "search_miss" "search miss classification"
assert_eq "$(classify_command_outcome 1 "go test ./..." "FAIL\tinternal/task")" "build_or_test_failure" "build/test failure classification"
assert_eq "$(classify_command_outcome 1 "git push origin branch" "fatal: unable to access remote")" "infra_failure" "infra failure classification"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT
RUN_FILE="${TMP_DIR}/run-sample.jsonl"
cat > "${RUN_FILE}" <<'EOF'
{"type":"item.completed","item":{"type":"command_execution","exit_code":0,"command":"rg -n foo .","aggregated_output":"foo"}}
{"type":"item.completed","item":{"type":"command_execution","exit_code":1,"command":"rg -n nomatch .","aggregated_output":""}}
{"type":"item.completed","item":{"type":"command_execution","exit_code":1,"command":"cat missing.md","aggregated_output":"cat: missing.md: No such file or directory"}}
{"type":"item.completed","item":{"type":"command_execution","exit_code":1,"command":"go test ./...","aggregated_output":"FAIL\tpkg"}}
{"type":"item.completed","item":{"type":"command_execution","exit_code":128,"command":"git push","aggregated_output":"fatal: unable to access remote"}}
EOF

summary="$(summarize_run_jsonl_command_outcomes "${RUN_FILE}")"
for expected in \
  "total=5" \
  "success=1" \
  "nonzero=4" \
  "lookup_miss=1" \
  "search_miss=1" \
  "build_or_test_failure=1" \
  "infra_failure=1" \
  "hard_blockers=2"; do
  if [[ "${summary}" != *"${expected}"* ]]; then
    echo "summary missing expected token ${expected}: ${summary}" >&2
    exit 1
  fi
done

echo "command outcome test passed"
