#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RETRY="${SCRIPT_DIR}/github-retry.sh"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

fake_bin="${TMP_DIR}/bin"
mkdir -p "${fake_bin}"

cat > "${fake_bin}/git" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
counter_file="${GITHUB_RETRY_TEST_GIT_COUNTER:?}"
count=0
if [[ -f "${counter_file}" ]]; then
  count="$(cat "${counter_file}")"
fi
count=$((count + 1))
printf '%s' "${count}" > "${counter_file}"

if [[ "${1:-}" == "push" && "${count}" -lt 3 ]]; then
  echo "remote: HTTP 500 Internal Server Error" >&2
  exit 1
fi
echo "pushed"
exit 0
EOF

cat > "${fake_bin}/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
counter_file="${GITHUB_RETRY_TEST_GH_COUNTER:?}"
count=0
if [[ -f "${counter_file}" ]]; then
  count="$(cat "${counter_file}")"
fi
count=$((count + 1))
printf '%s' "${count}" > "${counter_file}"

echo "HTTP 403 Forbidden" >&2
exit 1
EOF

chmod +x "${fake_bin}/git" "${fake_bin}/gh"

export PATH="${fake_bin}:${PATH}"
export GITHUB_RETRY_BASE_DELAY_SECONDS=0
export GITHUB_RETRY_MAX_BACKOFF_SECONDS=0
export GITHUB_RETRY_JITTER_MAX_SECONDS=0
export GITHUB_RETRY_MAX_ATTEMPTS=5
export GITHUB_RETRY_TEST_GIT_COUNTER="${TMP_DIR}/git-counter.txt"
export GITHUB_RETRY_TEST_GH_COUNTER="${TMP_DIR}/gh-counter.txt"

git_out="${TMP_DIR}/git.out"
"${RETRY}" git push origin branch > "${git_out}" 2>&1
if [[ "$(cat "${GITHUB_RETRY_TEST_GIT_COUNTER}")" != "3" ]]; then
  echo "expected git retry to attempt 3 times" >&2
  cat "${git_out}" >&2
  exit 1
fi
if ! grep -q 'action=retry classification=transient_http_5xx' "${git_out}"; then
  echo "expected transient retry classification in git output" >&2
  cat "${git_out}" >&2
  exit 1
fi
if ! grep -q 'action=success terminal_reason=success' "${git_out}"; then
  echo "expected success terminal reason in git output" >&2
  cat "${git_out}" >&2
  exit 1
fi

gh_out="${TMP_DIR}/gh.out"
set +e
"${RETRY}" gh pr edit 123 --title "x" > "${gh_out}" 2>&1
gh_rc=$?
set -e
if (( gh_rc == 0 )); then
  echo "expected gh retry wrapper to fail for non-retryable auth error" >&2
  cat "${gh_out}" >&2
  exit 1
fi
if [[ "$(cat "${GITHUB_RETRY_TEST_GH_COUNTER}")" != "1" ]]; then
  echo "expected gh non-retryable failure to run once" >&2
  cat "${gh_out}" >&2
  exit 1
fi
if ! grep -q 'action=fail_fast classification=permanent_auth_or_permission' "${gh_out}"; then
  echo "expected fail_fast auth classification in gh output" >&2
  cat "${gh_out}" >&2
  exit 1
fi

rm -f "${GITHUB_RETRY_TEST_GH_COUNTER}"
gh_create_out="${TMP_DIR}/gh-create.out"
set +e
"${RETRY}" gh pr create --title "x" --body "y" > "${gh_create_out}" 2>&1
gh_create_rc=$?
set -e
if (( gh_create_rc == 0 )); then
  echo "expected gh pr create wrapper to fail for non-retryable auth error" >&2
  cat "${gh_create_out}" >&2
  exit 1
fi
if [[ "$(cat "${GITHUB_RETRY_TEST_GH_COUNTER}")" != "1" ]]; then
  echo "expected gh pr create non-retryable failure to run once" >&2
  cat "${gh_create_out}" >&2
  exit 1
fi

echo "github retry test passed"
