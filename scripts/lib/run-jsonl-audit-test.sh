#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AUDIT="${SCRIPT_DIR}/run-jsonl-audit.sh"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

ok_file="${TMP_DIR}/run-ok.jsonl"
missing_file="${TMP_DIR}/run-missing.jsonl"

cat > "${ok_file}" <<'EOF'
{"type":"thread.started"}
{"type":"turn.started"}
{"type":"turn.completed"}
EOF

cat > "${missing_file}" <<'EOF'
{"type":"thread.started"}
{"type":"turn.started"}
{"type":"item.completed","item":{"type":"agent_message"}}
EOF

if "${AUDIT}" "${TMP_DIR}/run-*.jsonl" > "${TMP_DIR}/audit.out" 2>&1; then
  echo "expected audit without repair to fail for missing terminal" >&2
  cat "${TMP_DIR}/audit.out" >&2
  exit 1
fi

if ! grep -q 'status=missing_terminal' "${TMP_DIR}/audit.out"; then
  echo "expected missing_terminal classification in audit output" >&2
  cat "${TMP_DIR}/audit.out" >&2
  exit 1
fi

"${AUDIT}" --repair "${TMP_DIR}/run-*.jsonl" > "${TMP_DIR}/repair.out"
if ! grep -q 'status=repaired_missing_terminal' "${TMP_DIR}/repair.out"; then
  echo "expected repaired_missing_terminal classification in repair output" >&2
  cat "${TMP_DIR}/repair.out" >&2
  exit 1
fi

if ! grep -q '"type":"run.interrupted"' "${missing_file}"; then
  echo "expected synthetic run.interrupted terminal event after repair" >&2
  cat "${missing_file}" >&2
  exit 1
fi

"${AUDIT}" "${TMP_DIR}/run-*.jsonl" > "${TMP_DIR}/post.out"
if ! grep -q 'remaining_missing=0' "${TMP_DIR}/post.out"; then
  echo "expected no missing terminal events after repair" >&2
  cat "${TMP_DIR}/post.out" >&2
  exit 1
fi

echo "run jsonl audit test passed"
