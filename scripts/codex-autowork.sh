#!/usr/bin/env bash
set -euo pipefail

PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${HOME}/.npm-global/bin"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONTROL_REPO_DIR="${AUTO_WORK_CONTROL_REPO_DIR:-$(cd "${SCRIPT_DIR}/.." && pwd)}"
WORKTREE_DIR="${AUTO_WORK_WORKTREE_DIR:-${CONTROL_REPO_DIR}}"
SHARED_ISSUES_DIR="${AUTO_WORK_ISSUES_DIR:-${CONTROL_REPO_DIR}/issues}"
REQUIRED_BASE_BRANCH="${AUTO_WORK_BASE_BRANCH:-v2}"
ISSUE_LANE_CLI="${SCRIPT_DIR}/issue-lane.sh"
SESSION_NAME="${AUTO_WORK_SESSION_NAME:-codex-autowork}"
STATE_DIR="${AUTO_WORK_STATE_DIR:-${HOME}/otter-data/sessions}"
RUNNER_LOG="${STATE_DIR}/runner.log"
PROMPT_FILE="${STATE_DIR}/autowork-prompt.txt"
AUTOWORK_OUTPUT_FILE="${STATE_DIR}/autowork-last-message.txt"
STREAM_LOG="${STATE_DIR}/stream.log"
DOC_ISSUES_INSTRUCTIONS="${SHARED_ISSUES_DIR}/instructions.md"
DOC_BUILD_INSTRUCTIONS="${CONTROL_REPO_DIR}/build/INSTRUCTIONS.md"
DOC_BUILD_CONTEXT="${CONTROL_REPO_DIR}/build/CONTEXT.md"
BASELINE_MATRIX_FILE="${CONTROL_REPO_DIR}/config/autowork-baseline-test-matrix.json"
FLAKE_REGISTRY_FILE="${CONTROL_REPO_DIR}/config/autowork-flake-registry.json"
CONTEXT_CACHE_FILE="${STATE_DIR}/startup-context-cache.env"
CONTEXT_CACHE_MODE="miss"
CONTEXT_CACHE_CHANGED_DOCS="cache_uninitialized"
CONTEXT_CACHE_PREFACE=""

# shellcheck source=scripts/lib/issue-queue.sh
source "${SCRIPT_DIR}/lib/issue-queue.sh"

mkdir -p "${STATE_DIR}"
touch "${RUNNER_LOG}" "${STREAM_LOG}"

now() {
  date '+%Y-%m-%d %H:%M:%S %Z'
}

log() {
  printf '[%s] %s\n' "$(now)" "$*" | tee -a "${RUNNER_LOG}"
}

hash_file() {
  local file="$1"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "${file}" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "${file}" | awk '{print $1}'
    return
  fi
  cksum "${file}" | awk '{print $1}'
}

setup_startup_context_cache() {
  local cached_issues_hash="" cached_build_instructions_hash="" cached_build_context_hash=""
  local current_issues_hash current_build_instructions_hash current_build_context_hash
  local changed_docs=()

  if [[ -f "${CONTEXT_CACHE_FILE}" ]]; then
    # shellcheck disable=SC1090
    source "${CONTEXT_CACHE_FILE}" || true
  fi

  current_issues_hash="$(hash_file "${DOC_ISSUES_INSTRUCTIONS}")"
  current_build_instructions_hash="$(hash_file "${DOC_BUILD_INSTRUCTIONS}")"
  current_build_context_hash="$(hash_file "${DOC_BUILD_CONTEXT}")"

  if [[ -n "${cached_issues_hash}" && -n "${cached_build_instructions_hash}" && -n "${cached_build_context_hash}" ]] \
    && [[ "${cached_issues_hash}" == "${current_issues_hash}" ]] \
    && [[ "${cached_build_instructions_hash}" == "${current_build_instructions_hash}" ]] \
    && [[ "${cached_build_context_hash}" == "${current_build_context_hash}" ]]; then
    CONTEXT_CACHE_MODE="hit"
    CONTEXT_CACHE_CHANGED_DOCS="none"
  else
    CONTEXT_CACHE_MODE="miss"
    [[ "${cached_issues_hash:-}" != "${current_issues_hash}" ]] && changed_docs+=("issues/instructions.md")
    [[ "${cached_build_instructions_hash:-}" != "${current_build_instructions_hash}" ]] && changed_docs+=("build/INSTRUCTIONS.md")
    [[ "${cached_build_context_hash:-}" != "${current_build_context_hash}" ]] && changed_docs+=("build/CONTEXT.md")
    if (( ${#changed_docs[@]} == 0 )); then
      changed_docs=("cache_uninitialized")
    fi
    CONTEXT_CACHE_CHANGED_DOCS="$(IFS=','; echo "${changed_docs[*]}")"
  fi

  cat > "${CONTEXT_CACHE_FILE}" <<EOF
cached_issues_hash='${current_issues_hash}'
cached_build_instructions_hash='${current_build_instructions_hash}'
cached_build_context_hash='${current_build_context_hash}'
cached_generated_at='$(date '+%Y-%m-%dT%H:%M:%S%z')'
EOF

  if [[ "${CONTEXT_CACHE_MODE}" == "hit" ]]; then
    CONTEXT_CACHE_PREFACE=$(
      cat <<EOF
Startup context cache: HIT.
Doc hashes unchanged for:
- ${DOC_ISSUES_INSTRUCTIONS}
- ${DOC_BUILD_INSTRUCTIONS}
- ${DOC_BUILD_CONTEXT}
Use the cached briefing in this prompt; do not re-read full doc bodies unless an inconsistency appears.
EOF
    )
  else
    CONTEXT_CACHE_PREFACE=$(
      cat <<EOF
Startup context cache: MISS (changed: ${CONTEXT_CACHE_CHANGED_DOCS}).
Re-read these docs fully this run before acting:
- ${DOC_ISSUES_INSTRUCTIONS}
- ${DOC_BUILD_INSTRUCTIONS}
- ${DOC_BUILD_CONTEXT}
EOF
    )
  fi

  log "startup-context cache=${CONTEXT_CACHE_MODE} changed=${CONTEXT_CACHE_CHANGED_DOCS}"
}

write_prompt_file() {
  {
    if [[ -n "${CONTEXT_CACHE_PREFACE}" ]]; then
      printf '%s\n\n' "${CONTEXT_CACHE_PREFACE}"
    fi

    printf 'Read and follow %s exactly.\n\n' "${DOC_ISSUES_INSTRUCTIONS}"

    cat <<'EOF'
Primary operating docs:
EOF
    printf -- '- %s/build/INSTRUCTIONS.md\n' "${CONTROL_REPO_DIR}"
    printf -- '- %s/build/CONTEXT.md\n' "${CONTROL_REPO_DIR}"
    printf -- '- The active task file in %s/02-in-progress\n\n' "${SHARED_ISSUES_DIR}"

    cat <<'EOF'
Critical workspace rule:
EOF
    printf -- '- Perform all git work and code changes in %s only.\n' "${WORKTREE_DIR}"
    printf -- '- Keep %s anchored to base branch %s when starting each run.\n' "${WORKTREE_DIR}" "${REQUIRED_BASE_BRANCH}"
    printf -- '- The shared queue is at %s; move task files between lanes there.\n\n' "${SHARED_ISSUES_DIR}"

    cat <<'EOF'
Lane workflow (strict):
EOF
    printf '1. If any task already exists in %s/02-in-progress, resume that task first (do not pull a new task).\n' "${SHARED_ISSUES_DIR}"
    printf '2. On a fresh run with empty 02-in-progress and 05-completed, start with 001-project-scaffold.md.\n'
    printf '3. When selecting from %s/01-ready, prioritize tasks that contain a top-level "## Reviewer Required Changes" block before net-new tasks (lowest task number first).\n' "${SHARED_ISSUES_DIR}"
    printf '4. Claim from %s/01-ready with `%s claim %s <task-file>` (respect Depends on).\n' "${SHARED_ISSUES_DIR}" "${ISSUE_LANE_CLI}" "${SHARED_ISSUES_DIR}"
    printf '5. Use `%s move %s <src-lane> <dst-lane> <task-file>` for lane transitions; treat `claimed`, `already_claimed`, and `already_completed` as idempotent non-fatal states.\n' "${ISSUE_LANE_CLI}" "${SHARED_ISSUES_DIR}"
    printf '6. Implement exactly scoped requirements from the task file.\n'
    printf '7. Run required tests from the task (unit/integration/e2e as specified).\n'
    printf '8. For CLI tasks, run a CLI smoke check after build.\n'
    printf '9. For reviewer-rework tasks: treat every item in "## Reviewer Required Changes" as mandatory acceptance criteria, then remove that top-level block once all items are resolved.\n'
    printf '10. Preserve a concise resolution summary in %s/notes.md (task file, fixes applied, tests run).\n' "${SHARED_ISSUES_DIR}"
    printf '11. Move completed tasks to %s/03-needs-review.\n\n' "${SHARED_ISSUES_DIR}"

    cat <<'EOF'
Routing + test conventions:
- API routes: /v1/*
- Health routes: /health/live, /health/ready (aliases: /health, /ready)
- Test reset route: POST /test/reset (test mode only)

Implementation directives:
- In task 001, initialize go.mod with module path: github.com/samhotchkiss/otter-camp
- If integration tests require testdb.New(t) and it does not exist yet, complete task 002 first; do not stub around it.
- Use one branch/PR per task by default.
EOF
    printf -- '- Create each task branch from %s and target %s in PRs.\n' "${REQUIRED_BASE_BRANCH}" "${REQUIRED_BASE_BRANCH}"
    cat <<'EOF'
- Commit and push each completed task branch to origin.
EOF
    printf -- '- Open/update a PR per task targeting branch %s.\n' "${REQUIRED_BASE_BRANCH}"
    cat <<'EOF'
- Add or update tests for every implemented task scope (unit/integration/e2e as the task requires); do not skip test authoring silently.
- Do not treat a task as finished until it is review-ready and pushed.
- Ignore historical "open blocker" wording in build/DEPENDENCY-GRAPH.md and build/SUMMARY.md; build/ISSUES.md is resolved and task files are authoritative.
- In thin-spec areas, make reasonable engineering judgments and document them in code comments and task notes; do not block unless a critical invariant would be violated.

Execution policy:
EOF
    printf -- '- Continue until no actionable tasks remain in %s/01-ready.\n' "${SHARED_ISSUES_DIR}"
    printf -- '- If blocked, append clear blocker notes to %s/notes.md, then continue with the next actionable task.\n\n' "${SHARED_ISSUES_DIR}"

    cat <<'EOF'
Baseline health gate artifacts:
EOF
    printf -- '- Baseline test matrix: %s\n' "${BASELINE_MATRIX_FILE}"
    printf -- '- Flake registry (owner + expiry + evidence): %s\n' "${FLAKE_REGISTRY_FILE}"
    cat <<'EOF'
- Treat unmatched build/test failures as task-scope regressions.
- Treat only active, non-expired registry matches as waived known flakes (always cite flake IDs).

Queue mutation reconciliation protocol:
- If queue files change externally while you are running, do not stop immediately.
- Snapshot lane state (`01-ready` through `05-completed`) and identify the affected task.
EOF
    printf -- '- Run `%s reconcile %s <src-lane> <dst-lane> <task-file>`.\n' "${ISSUE_LANE_CLI}" "${SHARED_ISSUES_DIR}"
    cat <<'EOF'
- Continue automatically on `queue_reconciled`.
- Escalate only on `queue_conflict_hard_stop` (document why invariants failed).

Command path guardrails:
- Required pattern: discover -> open.
EOF
    printf '  - Discover candidate paths first: `rg --files %s | rg '"'"'<name-or-fragment>'"'"'`.\n' "${WORKTREE_DIR}"
    cat <<'EOF'
  - Verify selected path exists: `test -f <path>` (or `ls <path>`).
  - Only then run `sed/cat` on that file.
- Command outcome taxonomy (use these exact classes in reasoning and notes):
  - `lookup_miss`: path/discovery misses
  - `search_miss`: no-result search/discovery command
  - `build_or_test_failure`: actual regressions
  - `infra_failure`: transport/auth/tooling/runtime failures not tied to code correctness
- Exploratory miss policy:
  - For discovery commands expected to miss, prefer non-blocking form with `|| true`.
  - Examples: `rg -n "<pattern>" <path> || true`, `find <dir> -name "<glob>" || true`, `ls <path-that-may-not-exist> || true`.
  - Never add `|| true` to build/test/verification commands.

Shell quoting guardrails:
- Never inline markdown payloads in quoted shell one-liners for PRs or notes.
- For PR descriptions, always use file-backed payloads:
  - `cat <<'EOF' > /tmp/pr-body.md` ... `EOF`
  - `gh pr create --body-file /tmp/pr-body.md`
- For notes appends, always use a single-quoted heredoc delimiter:
EOF
    printf '  - `cat <<'"'"'EOF'"'"' >> %s/notes.md` ... `EOF`\n' "${SHARED_ISSUES_DIR}"
    cat <<'EOF'
- If any command fails due quoting/substitution, rerun with the safe file/heredoc template.

GitHub transport retry policy:
- For `git push`, `gh pr create`, and `gh pr edit`, do not call raw commands directly.
- Use shared retry wrapper:
  - `scripts/lib/github-retry.sh git push <remote> <refspec>`
  - `scripts/lib/github-retry.sh gh pr create <args...>`
  - `scripts/lib/github-retry.sh gh pr edit <args...>`
- Treat wrapper `action=fail_fast` as non-retryable (auth/permission/invalid args); fix root cause before proceeding.
EOF
  } > "${PROMPT_FILE}"
}

ensure_on_base_branch() {
  if ! git -C "${WORKTREE_DIR}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    log "worktree is not a git repository: ${WORKTREE_DIR}"
    exit 1
  fi

  local current_branch
  current_branch="$(git -C "${WORKTREE_DIR}" rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
  if [[ -z "${current_branch}" ]]; then
    log "unable to determine current branch in ${WORKTREE_DIR}"
    exit 1
  fi

  if [[ "${current_branch}" == "${REQUIRED_BASE_BRANCH}" ]]; then
    return 0
  fi

  if ! git -C "${WORKTREE_DIR}" show-ref --verify --quiet "refs/heads/${REQUIRED_BASE_BRANCH}"; then
    if ! git -C "${WORKTREE_DIR}" show-ref --verify --quiet "refs/remotes/origin/${REQUIRED_BASE_BRANCH}"; then
      git -C "${WORKTREE_DIR}" fetch origin --prune >/dev/null 2>&1 || true
    fi
    if git -C "${WORKTREE_DIR}" show-ref --verify --quiet "refs/remotes/origin/${REQUIRED_BASE_BRANCH}"; then
      log "creating local base branch '${REQUIRED_BASE_BRANCH}' from origin/${REQUIRED_BASE_BRANCH}"
      if ! git -C "${WORKTREE_DIR}" branch "${REQUIRED_BASE_BRANCH}" "origin/${REQUIRED_BASE_BRANCH}" >/dev/null 2>&1; then
        log "failed to create local base branch '${REQUIRED_BASE_BRANCH}' from origin/${REQUIRED_BASE_BRANCH}"
        exit 1
      fi
    else
      log "required base branch not found locally or on origin: ${REQUIRED_BASE_BRANCH}"
      exit 1
    fi
  fi

  if ! git -C "${WORKTREE_DIR}" diff --quiet || ! git -C "${WORKTREE_DIR}" diff --cached --quiet; then
    log "worktree is on '${current_branch}' with local changes; cannot auto-switch to '${REQUIRED_BASE_BRANCH}'"
    log "resolve local changes first, then rerun autowork"
    exit 1
  fi

  log "switching worktree branch '${current_branch}' -> '${REQUIRED_BASE_BRANCH}'"
  if ! git -C "${WORKTREE_DIR}" switch "${REQUIRED_BASE_BRANCH}" >/dev/null 2>&1; then
    log "failed to switch to required base branch '${REQUIRED_BASE_BRANCH}'"
    exit 1
  fi
}

in_progress_task_count() {
  if [[ ! -d "${SHARED_ISSUES_DIR}/02-in-progress" ]]; then
    echo 0
    return
  fi
  find "${SHARED_ISSUES_DIR}/02-in-progress" -maxdepth 1 -type f -name '*.md' | wc -l | tr -d ' '
}

has_active_autowork_codex() {
  local matches
  matches="$({
    ps -axo pid=,command= | awk -v marker="${AUTOWORK_OUTPUT_FILE}" '
      {
        pid=$1
        $1=""
        sub(/^ /, "", $0)
        cmd=$0
        if (cmd ~ /codex-autowork\.sh/) next
        if (cmd ~ /codex app-server/) next
        if ((cmd ~ /^codex([[:space:]]|$)/ ||
             cmd ~ /\/bin\/codex([[:space:]]|$)/ ||
             cmd ~ /\/codex\/codex([[:space:]]|$)/) && index(cmd, marker) > 0) {
          print pid " " cmd
        }
      }
    '
  })"
  if [[ -n "${matches}" ]]; then
    log "Detected active autowork Codex process; skipping this run."
    printf '%s\n' "${matches}" | tee -a "${RUNNER_LOG}"
    return 0
  fi
  return 1
}

completed_task_numbers() {
  local completed_dir="${SHARED_ISSUES_DIR}/05-completed"
  [[ -d "${completed_dir}" ]] || return 0
  find "${completed_dir}" -maxdepth 1 -type f -name '*.md' \
    | sed -E 's#.*/([0-9]{3})-.*\.md#\1#' \
    | sort -u
}

extract_dep_numbers() {
  local file="$1"
  local raw
  raw="$(
    awk '
      BEGIN { IGNORECASE=1 }
      /^##[[:space:]]/ { exit }
      {
        line=$0
        if (line ~ /^\|[[:space:]]*Depends on[[:space:]]*\|/) {
          sub(/^\|[[:space:]]*Depends on[[:space:]]*\|[[:space:]]*/, "", line)
          sub(/[[:space:]]*\|[[:space:]]*$/, "", line)
          print line
          exit
        }
        if (line ~ /^Depends on:[[:space:]]*/) {
          sub(/^Depends on:[[:space:]]*/, "", line)
          print line
          exit
        }
      }
    ' "${file}" 2>/dev/null || true
  )"

  [[ -z "${raw}" ]] && return 0
  local raw_lc
  raw_lc="$(printf '%s' "${raw}" | tr '[:upper:]' '[:lower:]')"
  [[ "${raw_lc}" == "—" || "${raw_lc}" == "-" || "${raw_lc}" == "none" ]] && return 0

  local token norm
  while IFS= read -r token; do
    [[ -z "${token}" ]] && continue
    norm="$(printf '%s' "${token}" | tr -d ' ')"
    norm="${norm//–/-}"
    norm="${norm//—/-}"
    if [[ "${norm}" =~ ^([0-9]{3})-([0-9]{3})$ ]]; then
      local start end i
      start=$((10#${BASH_REMATCH[1]}))
      end=$((10#${BASH_REMATCH[2]}))
      if (( start <= end )); then
        for ((i=start; i<=end; i++)); do printf '%03d\n' "${i}"; done
      else
        for ((i=end; i<=start; i++)); do printf '%03d\n' "${i}"; done
      fi
    elif [[ "${norm}" =~ ^[0-9]{3}$ ]]; then
      printf '%s\n' "${norm}"
    fi
  done < <(printf '%s\n' "${raw}" | tr ',' '\n') | sort -u
}

has_reviewer_required_changes() {
  local file="$1"
  grep -q '^## Reviewer Required Changes$' "${file}" 2>/dev/null
}

deps_satisfied() {
  local file="$1"
  local deps
  deps="$(extract_dep_numbers "${file}")"
  [[ -z "${deps}" ]] && return 0

  local completed
  completed="$(completed_task_numbers)"
  local dep
  while IFS= read -r dep; do
    [[ -z "${dep}" ]] && continue
    if ! printf '%s\n' "${completed}" | grep -qx "${dep}"; then
      return 1
    fi
  done <<< "${deps}"
  return 0
}

select_next_actionable_ready_task() {
  local ready_dir="${SHARED_ISSUES_DIR}/01-ready"
  [[ -d "${ready_dir}" ]] || return 0

  local file normal_candidate=""
  while IFS= read -r file; do
    [[ -z "${file}" ]] && continue
    if ! deps_satisfied "${file}"; then
      continue
    fi
    if has_reviewer_required_changes "${file}"; then
      printf '%s\n' "${file}"
      return 0
    fi
    if [[ -z "${normal_candidate}" ]]; then
      normal_candidate="${file}"
    fi
  done < <(find "${ready_dir}" -maxdepth 1 -type f -name '*.md' | sort)

  [[ -n "${normal_candidate}" ]] && printf '%s\n' "${normal_candidate}"
}

claim_next_ready_task_if_needed() {
  if [[ ! -x "${ISSUE_LANE_CLI}" ]]; then
    log "queue helper missing or not executable: ${ISSUE_LANE_CLI}"
    return 0
  fi

  local in_progress_count
  in_progress_count="$(in_progress_task_count)"
  if [[ "${in_progress_count}" -gt 0 ]]; then
    return 0
  fi

  local next_task
  next_task="$(select_next_actionable_ready_task || true)"
  if [[ -z "${next_task}" ]]; then
    log "queue claim outcome=missing task=<none-actionable>"
    return 0
  fi

  local task_basename status reconcile_outcome before_snapshot after_snapshot
  task_basename="$(basename "${next_task}")"
  before_snapshot="$(queue_lane_snapshot "${SHARED_ISSUES_DIR}" | tr '\n' ',' | sed 's/,$//')"
  status="$("${ISSUE_LANE_CLI}" claim "${SHARED_ISSUES_DIR}" "${next_task}" || echo "missing")"
  after_snapshot="$(queue_lane_snapshot "${SHARED_ISSUES_DIR}" | tr '\n' ',' | sed 's/,$//')"

  case "${status}" in
    claimed)
      log "queue claim outcome=claimed task=${task_basename} snapshot_before=${before_snapshot} snapshot_after=${after_snapshot}"
      ;;
    already_claimed|already_completed)
      reconcile_outcome="$("${ISSUE_LANE_CLI}" reconcile "${SHARED_ISSUES_DIR}" "01-ready" "02-in-progress" "${task_basename}" || echo "queue_conflict_hard_stop")"
      log "queue reconcile outcome=${reconcile_outcome} status=${status} task=${task_basename} snapshot_before=${before_snapshot} snapshot_after=${after_snapshot}"
      if [[ "${reconcile_outcome}" == "queue_conflict_hard_stop" ]]; then
        log "queue conflict hard stop: unable to reconcile external lane mutation for ${task_basename}"
        exit 1
      fi
      ;;
    *)
      log "queue reconcile outcome=queue_conflict_hard_stop status=${status} task=${task_basename} snapshot_before=${before_snapshot} snapshot_after=${after_snapshot}"
      exit 1
      ;;
  esac
}

if ! command -v codex >/dev/null 2>&1; then
  log "codex binary not found on PATH"
  exit 1
fi

if ! command -v tmux >/dev/null 2>&1; then
  log "tmux not found on PATH"
  exit 1
fi

if [[ ! -d "${WORKTREE_DIR}" ]]; then
  log "worktree directory not found: ${WORKTREE_DIR}"
  exit 1
fi

if [[ ! -d "${SHARED_ISSUES_DIR}" ]]; then
  log "issues directory not found: ${SHARED_ISSUES_DIR}"
  exit 1
fi

if [[ ! -f "${DOC_ISSUES_INSTRUCTIONS}" ]]; then
  log "missing issues instructions: ${DOC_ISSUES_INSTRUCTIONS}"
  exit 1
fi

if [[ ! -f "${DOC_BUILD_INSTRUCTIONS}" ]]; then
  log "missing build instructions: ${DOC_BUILD_INSTRUCTIONS}"
  exit 1
fi

if [[ ! -f "${DOC_BUILD_CONTEXT}" ]]; then
  log "missing build context: ${DOC_BUILD_CONTEXT}"
  exit 1
fi

IN_PROGRESS_COUNT="$(in_progress_task_count)"
if [[ "${IN_PROGRESS_COUNT}" -gt 0 ]]; then
  log "detected ${IN_PROGRESS_COUNT} in-progress task(s); resuming on current branch without base-branch switch"
else
  ensure_on_base_branch
fi

MONITOR_CMD="bash -lc 'echo [autowork] monitor started at $(date); echo [autowork] tailing ${STREAM_LOG}; tail -n 200 -F ${STREAM_LOG}'"

if tmux has-session -t "${SESSION_NAME}" 2>/dev/null; then
  if tmux list-windows -t "${SESSION_NAME}" -F '#{window_name}' | grep -qx "monitor"; then
    tmux respawn-pane -k -t "${SESSION_NAME}:monitor.0" "${MONITOR_CMD}" >/dev/null 2>&1 || true
  else
    tmux new-window -d -t "${SESSION_NAME}" -n monitor "${MONITOR_CMD}"
  fi
  if tmux list-panes -a -t "${SESSION_NAME}" -F '#{pane_dead} #{pane_current_command}' | awk '$1=="0" && $2=="codex" {found=1} END{exit found?0:1}'; then
    log "Autowork session already has a running codex pane; skipping this run."
    exit 0
  fi
else
  tmux new-session -d -s "${SESSION_NAME}" -n monitor "${MONITOR_CMD}"
  tmux set-window-option -g -t "${SESSION_NAME}" remain-on-exit on >/dev/null
  tmux set-option -g -t "${SESSION_NAME}" mouse on >/dev/null || true
fi

if [[ "${AUTO_WORK_FORCE:-0}" != "1" ]]; then
  if has_active_autowork_codex; then
    exit 0
  fi
else
  log "AUTO_WORK_FORCE=1 set; bypassing active autowork guard."
fi

claim_next_ready_task_if_needed
setup_startup_context_cache

write_prompt_file

RUN_NAME="run-$(date '+%Y%m%d-%H%M%S')"
RUN_SCRIPT="${STATE_DIR}/${RUN_NAME}.sh"
RUN_LOG="${STATE_DIR}/${RUN_NAME}.log"
RUN_JSON_LOG="${STATE_DIR}/${RUN_NAME}.jsonl"

cat > "${RUN_SCRIPT}" <<'RUNEOF'
#!/usr/bin/env bash
set -euo pipefail

WORKTREE_DIR="${WORKTREE_DIR:?WORKTREE_DIR is required}"
PROMPT_FILE="${PROMPT_FILE:?PROMPT_FILE is required}"
AUTOWORK_OUTPUT_FILE="${AUTOWORK_OUTPUT_FILE:?AUTOWORK_OUTPUT_FILE is required}"

cd "${WORKTREE_DIR}"

RUN_LOG="${RUN_LOG:?RUN_LOG is required}"
RUN_JSON_LOG="${RUN_JSON_LOG:?RUN_JSON_LOG is required}"
STREAM_LOG="${STREAM_LOG:?STREAM_LOG is required}"

# shellcheck source=scripts/lib/run-jsonl.sh
source "${WORKTREE_DIR}/scripts/lib/run-jsonl.sh"
# shellcheck source=scripts/lib/command-outcome.sh
source "${WORKTREE_DIR}/scripts/lib/command-outcome.sh"

status=1
terminal_reason="runner_exit_without_terminal_event"

append_terminal_marker_if_needed() {
  local action
  action="$(
    run_jsonl_append_interrupted_terminal \
      "${RUN_JSON_LOG}" \
      "${terminal_reason}" \
      "codex-autowork-runner" \
      "${status}" \
      "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  )"
  if [[ "${action}" == "appended" ]]; then
    printf '[%s] synthesized terminal run event (reason=%s exit=%s)\n' \
      "$(date '+%Y-%m-%d %H:%M:%S %Z')" "${terminal_reason}" "${status}" \
      | tee -a "${RUN_LOG}" "${STREAM_LOG}" >/dev/null
  fi
}

on_exit() {
  append_terminal_marker_if_needed
}

on_signal() {
  local signal_name="$1"
  terminal_reason="runner_signal_${signal_name}"
  status=130
  case "${signal_name}" in
    TERM) status=143 ;;
    HUP) status=129 ;;
  esac
  exit "${status}"
}

trap on_exit EXIT
trap 'on_signal INT' INT
trap 'on_signal TERM' TERM
trap 'on_signal HUP' HUP

touch "${RUN_JSON_LOG}"
printf '[%s] autowork run started\n' "$(date '+%Y-%m-%d %H:%M:%S %Z')"
set +e
if command -v jq >/dev/null 2>&1; then
  codex exec \
    --json \
    --dangerously-bypass-approvals-and-sandbox \
    --cd "${WORKTREE_DIR}" \
    --output-last-message "${AUTOWORK_OUTPUT_FILE}" \
    - < "${PROMPT_FILE}" \
    | tee -a "${RUN_JSON_LOG}" \
    | jq -r '
      def txt: tostring | gsub("\\r"; "");
      if .type == "turn.started" then
        "---- turn started ----"
      elif .type == "item.completed" and .item.type == "agent_message" then
        "[agent] " + (.item.text // "")
      elif .type == "item.started" and .item.type == "command_execution" then
        "[cmd] " + (.item.command // "")
      elif .type == "item.completed" and .item.type == "command_execution" then
        "[cmd done] exit=" + ((.item.exit_code // -1)|tostring) + " " + (.item.command // "") +
        (if (.item.aggregated_output // "") != "" then "\n" + (.item.aggregated_output|txt) else "" end)
      elif .type == "item.completed" and .item.type == "reasoning" then
        "[plan] " + (.item.text // "")
      elif .type == "error" then
        "[error] " + ((.error.message // .message // "unknown error")|txt)
      else empty end
    ' \
    | tee -a "${RUN_LOG}" "${STREAM_LOG}"
  status=${PIPESTATUS[0]}
else
  codex exec \
    --dangerously-bypass-approvals-and-sandbox \
    --cd "${WORKTREE_DIR}" \
    --output-last-message "${AUTOWORK_OUTPUT_FILE}" \
    - < "${PROMPT_FILE}" \
    | tee -a "${RUN_LOG}" "${STREAM_LOG}"
  status=$?
fi
set -e

command_summary="$(summarize_run_jsonl_command_outcomes "${RUN_JSON_LOG}")"
printf '[%s] command-outcome-summary %s\n' "$(date '+%Y-%m-%d %H:%M:%S %Z')" "${command_summary}" | tee -a "${RUN_LOG}" "${STREAM_LOG}"

baseline_gate_script="${WORKTREE_DIR}/scripts/lib/baseline-health-gate.sh"
if [[ -x "${baseline_gate_script}" ]]; then
  baseline_summary="$("${baseline_gate_script}" "${RUN_JSON_LOG}" "${WORKTREE_DIR}/config/autowork-baseline-test-matrix.json" "${WORKTREE_DIR}/config/autowork-flake-registry.json" || true)"
  printf '[%s] baseline-health-summary %s\n' "$(date '+%Y-%m-%d %H:%M:%S %Z')" "${baseline_summary}" | tee -a "${RUN_LOG}" "${STREAM_LOG}"
else
  printf '[%s] baseline-health-summary gate_status=artifact_missing baseline_health_status=unknown matrix_version=missing registry_version=missing task_scope_regressions=0 waived_known_flakes=0 waived_flake_refs=none\n' \
    "$(date '+%Y-%m-%d %H:%M:%S %Z')" | tee -a "${RUN_LOG}" "${STREAM_LOG}"
fi

if (( status == 0 )); then
  terminal_reason="runner_exit_without_terminal_event"
else
  terminal_reason="codex_exit_${status}_without_terminal_event"
fi

printf '[%s] autowork run finished (exit=%s)\n' "$(date '+%Y-%m-%d %H:%M:%S %Z')" "${status}"
exit "${status}"
RUNEOF
chmod +x "${RUN_SCRIPT}"

if [[ "${AUTO_WORK_DRY_RUN:-0}" == "1" ]]; then
  log "Dry run only; would start tmux window ${RUN_NAME}."
  exit 0
fi

tmux new-window -d -t "${SESSION_NAME}" -n "${RUN_NAME}" \
  "WORKTREE_DIR='${WORKTREE_DIR}' PROMPT_FILE='${PROMPT_FILE}' AUTOWORK_OUTPUT_FILE='${AUTOWORK_OUTPUT_FILE}' RUN_LOG='${RUN_LOG}' RUN_JSON_LOG='${RUN_JSON_LOG}' STREAM_LOG='${STREAM_LOG}' '${RUN_SCRIPT}'"

log "Started autowork in tmux session '${SESSION_NAME}' window '${RUN_NAME}'. Attach with: tmux attach -t ${SESSION_NAME}"
