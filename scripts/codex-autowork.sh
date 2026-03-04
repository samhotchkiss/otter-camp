#!/usr/bin/env bash
set -euo pipefail

PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${HOME}/.npm-global/bin"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONTROL_REPO_DIR="${AUTO_WORK_CONTROL_REPO_DIR:-$(cd "${SCRIPT_DIR}/.." && pwd)}"
WORKTREE_DIR="${AUTO_WORK_WORKTREE_DIR:-${CONTROL_REPO_DIR}}"
SHARED_ISSUES_DIR="${AUTO_WORK_ISSUES_DIR:-${CONTROL_REPO_DIR}/issues}"
REQUIRED_BASE_BRANCH="${AUTO_WORK_BASE_BRANCH:-v2}"
SESSION_NAME="${AUTO_WORK_SESSION_NAME:-codex-autowork}"
STATE_DIR="${AUTO_WORK_STATE_DIR:-${HOME}/otter-data/sessions}"
RUNNER_LOG="${STATE_DIR}/runner.log"
PROMPT_FILE="${STATE_DIR}/autowork-prompt.txt"
AUTOWORK_OUTPUT_FILE="${STATE_DIR}/autowork-last-message.txt"
STREAM_LOG="${STATE_DIR}/stream.log"

mkdir -p "${STATE_DIR}"
touch "${RUNNER_LOG}" "${STREAM_LOG}"

now() {
  date '+%Y-%m-%d %H:%M:%S %Z'
}

log() {
  printf '[%s] %s\n' "$(now)" "$*" | tee -a "${RUNNER_LOG}"
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

if [[ ! -f "${SHARED_ISSUES_DIR}/instructions.md" ]]; then
  log "missing issues instructions: ${SHARED_ISSUES_DIR}/instructions.md"
  exit 1
fi

if [[ ! -f "${CONTROL_REPO_DIR}/build/INSTRUCTIONS.md" ]]; then
  log "missing build instructions: ${CONTROL_REPO_DIR}/build/INSTRUCTIONS.md"
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

cat > "${PROMPT_FILE}" <<PROMPT
Read and follow ${SHARED_ISSUES_DIR}/instructions.md exactly.

Primary operating docs:
- ${CONTROL_REPO_DIR}/build/INSTRUCTIONS.md
- ${CONTROL_REPO_DIR}/build/CONTEXT.md
- The active task file in ${SHARED_ISSUES_DIR}/02-in-progress

Critical workspace rule:
- Perform all git work and code changes in ${WORKTREE_DIR} only.
- Keep ${WORKTREE_DIR} anchored to base branch ${REQUIRED_BASE_BRANCH} when starting each run.
- The shared queue is at ${SHARED_ISSUES_DIR}; move task files between lanes there.

Lane workflow (strict):
1. If any task already exists in ${SHARED_ISSUES_DIR}/02-in-progress, resume that task first (do not pull a new task).
2. On a fresh run with empty 02-in-progress and 05-completed, start with 001-project-scaffold.md.
3. When selecting from ${SHARED_ISSUES_DIR}/01-ready, prioritize tasks that contain a top-level "## Reviewer Required Changes" block before net-new tasks (lowest task number first).
4. Pull next actionable task from ${SHARED_ISSUES_DIR}/01-ready (respect Depends on).
5. Move it to ${SHARED_ISSUES_DIR}/02-in-progress before coding.
6. Implement exactly scoped requirements from the task file.
7. Run required tests from the task (unit/integration/e2e as specified).
8. For CLI tasks, run a CLI smoke check after build.
9. For reviewer-rework tasks: treat every item in "## Reviewer Required Changes" as mandatory acceptance criteria, then remove that top-level block once all items are resolved.
10. Preserve a concise resolution summary in ${SHARED_ISSUES_DIR}/notes.md (task file, fixes applied, tests run).
11. Move completed tasks to ${SHARED_ISSUES_DIR}/03-needs-review.

Routing + test conventions:
- API routes: /v1/*
- Health routes: /health/live, /health/ready (aliases: /health, /ready)
- Test reset route: POST /test/reset (test mode only)

Implementation directives:
- In task 001, initialize go.mod with module path: github.com/samhotchkiss/otter-camp
- If integration tests require testdb.New(t) and it does not exist yet, complete task 002 first; do not stub around it.
- Use one branch/PR per task by default.
- Create each task branch from ${REQUIRED_BASE_BRANCH} and target ${REQUIRED_BASE_BRANCH} in PRs.
- Commit and push each completed task branch to origin.
- Open/update a PR per task targeting branch ${REQUIRED_BASE_BRANCH}.
- Add or update tests for every implemented task scope (unit/integration/e2e as the task requires); do not skip test authoring silently.
- Do not treat a task as finished until it is review-ready and pushed.
- Ignore historical "open blocker" wording in build/DEPENDENCY-GRAPH.md and build/SUMMARY.md; build/ISSUES.md is resolved and task files are authoritative.
- In thin-spec areas, make reasonable engineering judgments and document them in code comments and task notes; do not block unless a critical invariant would be violated.

Execution policy:
- Continue until no actionable tasks remain in ${SHARED_ISSUES_DIR}/01-ready.
- If blocked, append clear blocker notes to ${SHARED_ISSUES_DIR}/notes.md, then continue with the next actionable task.
PROMPT

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

printf '[%s] autowork run started\n' "$(date '+%Y-%m-%d %H:%M:%S %Z')"

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
