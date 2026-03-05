#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATE="${SCRIPT_DIR}/baseline-health-gate.sh"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

RUN_FILE="${TMP_DIR}/run.jsonl"
MATRIX_FILE="${TMP_DIR}/matrix.json"
REGISTRY_FILE="${TMP_DIR}/registry.json"

cat > "${RUN_FILE}" <<'EOF'
{"type":"item.completed","item":{"type":"command_execution","exit_code":1,"command":"go test ./internal/testdb","aggregated_output":"drop test database ottercamp_test timeout: context deadline exceeded"}}
{"type":"item.completed","item":{"type":"command_execution","exit_code":1,"command":"go test ./internal/server -tags integration","aggregated_output":"FAIL\tgithub.com/samhotchkiss/otter-camp/internal/server"}}
{"type":"item.completed","item":{"type":"command_execution","exit_code":0,"command":"go test ./internal/testdb","aggregated_output":"ok"}}
EOF

cat > "${MATRIX_FILE}" <<'EOF'
{
  "version": "test-v1",
  "updated_at": "2026-03-04T00:00:00Z",
  "baseline_health": "healthy",
  "commands": [
    {"id": "unit", "scope": "unit", "command": "go test ./internal/testdb"}
  ]
}
EOF

cat > "${REGISTRY_FILE}" <<'EOF'
{
  "version": "registry-v1",
  "updated_at": "2026-03-04T00:00:00Z",
  "entries": [
    {
      "id": "flake-drop-timeout",
      "status": "active",
      "pattern": "drop test database .*context deadline exceeded",
      "owner": "platform-infra",
      "expires_on": "2030-01-01",
      "evidence": "reports/example.md"
    }
  ]
}
EOF

summary="$("${GATE}" "${RUN_FILE}" "${MATRIX_FILE}" "${REGISTRY_FILE}")"

for token in \
  "gate_status=task_regression_detected" \
  "baseline_health_status=healthy" \
  "matrix_version=test-v1" \
  "registry_version=registry-v1" \
  "observed_test_failures=2" \
  "task_scope_regressions=1" \
  "waived_known_flakes=1" \
  "waived_flake_refs=flake-drop-timeout"; do
  if [[ "${summary}" != *"${token}"* ]]; then
    echo "missing expected token ${token}: ${summary}" >&2
    exit 1
  fi
done

echo "baseline health gate test passed"
