#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUNNER="${SCRIPT_DIR}/codex-autowork.sh"

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

assert_not_exists() {
  local path="$1"
  if [[ -e "${path}" ]]; then
    echo "unexpected path exists: ${path}" >&2
    exit 1
  fi
}

setup_fake_bin() {
  local fake_bin="$1"
  mkdir -p "${fake_bin}"

  cat > "${fake_bin}/codex" <<'EOF'
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

  chmod +x "${fake_bin}/codex" "${fake_bin}/tmux"
}

setup_fixture() {
  local root="$1"
  local repo_dir="${root}/repo"
  local issues_dir="${root}/issues"

  mkdir -p \
    "${repo_dir}/build" \
    "${repo_dir}/config" \
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

  cat > "${repo_dir}/config/autowork-baseline-test-matrix.json" <<'EOF'
{}
EOF

  cat > "${repo_dir}/config/autowork-flake-registry.json" <<'EOF'
{}
EOF

  cat > "${issues_dir}/instructions.md" <<'EOF'
# shared instructions
EOF

  git -C "${repo_dir}" init -q -b main
  git -C "${repo_dir}" config user.email test@example.com
  git -C "${repo_dir}" config user.name test
  git -C "${repo_dir}" add build config
  git -C "${repo_dir}" commit -qm init
}

run_builder_dry_run() {
  local root="$1"
  local repo_dir="${root}/repo"
  local issues_dir="${root}/issues"
  local state_dir="${root}/state"
  local fake_bin="${root}/bin"
  local output_file="${root}/builder.out"

  mkdir -p "${state_dir}"
  setup_fake_bin "${fake_bin}"

  FAKE_TMUX_LOG="${root}/tmux.log" \
  PATH="${fake_bin}:${PATH}" \
  AUTO_WORK_DRY_RUN=1 \
  AUTO_WORK_FORCE=1 \
  AUTO_WORK_BASE_BRANCH=main \
  AUTO_WORK_CONTROL_REPO_DIR="${repo_dir}" \
  AUTO_WORK_WORKTREE_DIR="${repo_dir}" \
  AUTO_WORK_ISSUES_DIR="${issues_dir}" \
  AUTO_WORK_STATE_DIR="${state_dir}" \
  "${RUNNER}" > "${output_file}" 2>&1
}

run_literal_prompt_generation_test() {
  local root="${TMP_DIR}/literal-prompt"
  local repo_dir="${root}/repo"
  local issues_dir="${root}/issues"
  local prompt_file="${root}/state/autowork-prompt.txt"
  local output_file="${root}/builder.out"

  setup_fixture "${root}"

  cat > "${issues_dir}/02-in-progress/302-literal.md" <<'TASKEOF'
# literal prompt task

Markdown examples that must remain literal for the agent:
- `test -f <path>`
- `ls <path>`
- `rg -n "<pattern>" <path> || true`

```sh
cat <<'EOF' > /tmp/pr-body.md
example
EOF
gh pr create --body-file /tmp/pr-body.md
```
TASKEOF

  run_builder_dry_run "${root}"

  assert_contains "${output_file}" "Dry run only; would start tmux window"
  assert_not_contains "${output_file}" "command substitution:"
  assert_not_contains "${output_file}" "command not found"
  assert_not_contains "${output_file}" "gh pr create --body-file /tmp/pr-body.md"

  assert_contains "${prompt_file}" '`cat <<'"'"'EOF'"'"' > /tmp/pr-body.md`'
  assert_contains "${prompt_file}" '`gh pr create --body-file /tmp/pr-body.md`'
  assert_contains "${prompt_file}" "rg --files ${repo_dir} | rg '<name-or-fragment>'"
  assert_contains "${prompt_file}" "${issues_dir}/notes.md"
}

run_issue_body_shell_examples_test() {
  local root="${TMP_DIR}/issue-body-shell"
  local repo_dir="${root}/repo"
  local issues_dir="${root}/issues"
  local prompt_file="${root}/state/autowork-prompt.txt"
  local output_file="${root}/builder.out"
  local trigger_file="${root}/task-body-triggered"

  setup_fixture "${root}"

  {
    cat <<'TASKEOF'
# shell-like issue body task

This task body intentionally includes shell-looking markdown examples:
- `test -f <path>`
- `ls <path>`
- `rg -n "<pattern>" <path> || true`
- `gh pr edit --body-file /tmp/pr-body.md`

```sh
cat <<'EOF' >> /tmp/pr-body.md
example
EOF
```
TASKEOF
    printf -- '- $(touch %s)\n' "${trigger_file}"
  } > "${issues_dir}/02-in-progress/302-shell-like.md"

  run_builder_dry_run "${root}"

  assert_contains "${output_file}" "Dry run only; would start tmux window"
  assert_not_contains "${output_file}" "command substitution:"
  assert_not_contains "${output_file}" "command not found"
  assert_not_contains "${output_file}" "gh pr edit --body-file /tmp/pr-body.md"
  assert_not_exists "${trigger_file}"

  assert_contains "${prompt_file}" "cat <<'EOF' >> ${issues_dir}/notes.md"
  assert_contains "${prompt_file}" '`scripts/lib/github-retry.sh gh pr edit <args...>`'
}

run_literal_prompt_generation_test
run_issue_body_shell_examples_test

echo "codex autowork tests passed"
