#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REVIEW_RUNNER="${SCRIPT_DIR}/claude-review-autowork.sh"
SUPERVISOR="${SCRIPT_DIR}/autowork-supervisor-watchdog.sh"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

assert_contains() {
  local file="$1"
  local expected="$2"
  if ! grep -Fq "${expected}" "${file}"; then
    echo "expected string not found in ${file}: ${expected}" >&2
    sed -n '1,220p' "${file}" >&2 || true
    exit 1
  fi
}

assert_not_contains() {
  local file="$1"
  local unexpected="$2"
  if grep -Fq "${unexpected}" "${file}"; then
    echo "unexpected string found in ${file}: ${unexpected}" >&2
    sed -n '1,220p' "${file}" >&2 || true
    exit 1
  fi
}

setup_fake_bin() {
  local fake_bin="$1"
  mkdir -p "${fake_bin}"

  cat > "${fake_bin}/claude" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
exit 0
EOF

  cat > "${fake_bin}/tmux" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
log_file="${FAKE_TMUX_LOG:-}"
if [[ -n "${log_file}" ]]; then
  printf '%s\n' "$*" >> "${log_file}"
fi

case "${1:-}" in
  has-session)
    exit 1
    ;;
  new-session|set-window-option|set-option|new-window|respawn-pane)
    exit 0
    ;;
  list-windows|list-panes)
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
EOF

  chmod +x "${fake_bin}/claude" "${fake_bin}/tmux"
}

setup_fixture() {
  local root="$1"
  local repo_dir="${root}/repo"
  local issues_dir="${root}/issues"

  mkdir -p \
    "${repo_dir}/build" \
    "${issues_dir}/01-ready" \
    "${issues_dir}/02-in-progress" \
    "${issues_dir}/03-needs-review" \
    "${issues_dir}/04-in-review" \
    "${issues_dir}/05-completed"

  cat > "${repo_dir}/build/INSTRUCTIONS.md" <<'EOF'
# build instructions
EOF

  cat > "${repo_dir}/build/CONTEXT.md" <<'EOF'
# build context
EOF

  cat > "${issues_dir}/reviewer-instructions.md" <<'EOF'
# reviewer instructions
EOF

  cat > "${issues_dir}/03-needs-review/262-demo.md" <<'EOF'
# demo review task
EOF

  git -C "${repo_dir}" init -q -b main
  git -C "${repo_dir}" config user.email test@example.com
  git -C "${repo_dir}" config user.name test
  git -C "${repo_dir}" add build
  git -C "${repo_dir}" commit -qm init
}

run_direct_reviewer_test() {
  local root="${TMP_DIR}/direct"
  local repo_dir="${root}/repo"
  local issues_dir="${root}/issues"
  local state_dir="${root}/state"
  local fake_bin="${root}/bin"
  local output_file="${root}/reviewer.out"

  mkdir -p "${state_dir}"
  setup_fake_bin "${fake_bin}"
  setup_fixture "${root}"

  FAKE_TMUX_LOG="${root}/tmux.log" \
  PATH="${fake_bin}:${PATH}" \
  AUTO_REVIEW_DRY_RUN=1 \
  AUTO_REVIEW_FORCE=1 \
  AUTO_REVIEW_BASE_BRANCH=main \
  AUTO_REVIEW_CONTROL_REPO_DIR="${repo_dir}" \
  AUTO_REVIEW_ISSUES_DIR="${issues_dir}" \
  AUTO_REVIEW_STATE_DIR="${state_dir}" \
  REVIEW_WORKTREE_DIR="${repo_dir}" \
  "${REVIEW_RUNNER}" > "${output_file}" 2>&1

  assert_contains "${output_file}" "Dry run only; would start tmux window"
  assert_not_contains "${output_file}" "command substitution:"
  assert_not_contains "${output_file}" "command not found"

  assert_contains "${state_dir}/reviewer-prompt.txt" "Command outcome taxonomy:"
  assert_contains "${state_dir}/reviewer-prompt.txt" '`lookup_miss`: path/glob/file discovery miss'
  assert_contains "${state_dir}/reviewer-prompt.txt" '`cat <<'"'"'EOF'"'"' > <file>`'
  assert_contains "${state_dir}/reviewer-prompt.txt" "${issues_dir}/notes.md"
}

run_supervisor_launch_test() {
  local root="${TMP_DIR}/supervisor"
  local repo_dir="${root}/repo"
  local issues_dir="${root}/issues"
  local state_dir="${root}/state"
  local reviewer_repo="${root}/reviewer-repo"
  local fake_bin="${root}/bin"
  local output_file="${root}/watchdog.out"

  mkdir -p "${state_dir}"
  setup_fake_bin "${fake_bin}"
  setup_fixture "${root}"

  FAKE_TMUX_LOG="${root}/tmux.log" \
  PATH="${fake_bin}:${PATH}" \
  AUTOWORK_REPO_DIR="${repo_dir}" \
  AUTOWORK_ISSUES_DIR="${issues_dir}" \
  AUTOWORK_STATE_DIR="${state_dir}" \
  AUTOWORK_BASE_BRANCH=main \
  AUTOWORK_BUILDER_REPO_DIR="${root}/builder-repo" \
  AUTOWORK_REVIEWER_REPO_DIR="${reviewer_repo}" \
  AUTOWORK_REVIEW_RUNNER="${REVIEW_RUNNER}" \
  AUTO_REVIEW_DRY_RUN=1 \
  AUTO_REVIEW_CONTROL_REPO_DIR="${repo_dir}" \
  AUTO_REVIEW_ISSUES_DIR="${issues_dir}" \
  AUTO_REVIEW_STATE_DIR="${state_dir}" \
  AUTO_REVIEW_BASE_BRANCH=main \
  "${SUPERVISOR}" > "${output_file}" 2>&1

  assert_contains "${output_file}" "starting claude reviewer runner"
  assert_contains "${state_dir}/reviewer-runner.log" "Dry run only; would start tmux window"
  assert_not_contains "${state_dir}/reviewer-runner.log" "command substitution:"
  assert_not_contains "${state_dir}/reviewer-runner.log" "command not found"
  assert_contains "${state_dir}/reviewer-prompt.txt" '`cat <<'"'"'EOF'"'"' > <file>`'
}

run_direct_reviewer_test
run_supervisor_launch_test

echo "claude reviewer autowork tests passed"
