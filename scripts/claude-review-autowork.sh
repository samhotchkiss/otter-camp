#!/usr/bin/env bash
set -euo pipefail

PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${HOME}/.npm-global/bin:${HOME}/.local/bin"

# macOS often lacks timeout; use GNU coreutils gtimeout if available.
if command -v gtimeout >/dev/null 2>&1; then
  TIMEOUT_CMD="gtimeout"
elif command -v timeout >/dev/null 2>&1; then
  TIMEOUT_CMD="timeout"
else
  TIMEOUT_CMD=""
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONTROL_REPO_DIR="${AUTO_REVIEW_CONTROL_REPO_DIR:-$(cd "${SCRIPT_DIR}/.." && pwd)}"
REVIEW_WORKTREE_DIR="${REVIEW_WORKTREE_DIR:-${CONTROL_REPO_DIR}}"
SHARED_ISSUES_DIR="${AUTO_REVIEW_ISSUES_DIR:-${CONTROL_REPO_DIR}/issues}"
REVIEW_INSTRUCTIONS_FILE="${AUTO_REVIEW_INSTRUCTIONS_FILE:-${SHARED_ISSUES_DIR}/reviewer-instructions.md}"
REQUIRED_BASE_BRANCH="${AUTO_REVIEW_BASE_BRANCH:-v2}"
SESSION_NAME="${AUTO_REVIEW_SESSION_NAME:-claude-review-autowork}"
STATE_DIR="${AUTO_REVIEW_STATE_DIR:-${HOME}/otter-data/sessions}"
RUNNER_LOG="${STATE_DIR}/reviewer-runner.log"
STREAM_LOG="${STATE_DIR}/reviewer-stream.log"
LAST_MESSAGE_FILE="${STATE_DIR}/reviewer-last-message.txt"
PROMPT_FILE="${STATE_DIR}/reviewer-prompt.txt"
CLAUDE_DIR="${AUTO_REVIEW_CLAUDE_DIR:-${HOME}/.claude}"
MCP_NEEDS_AUTH_CACHE_FILE="${AUTO_REVIEW_MCP_NEEDS_AUTH_CACHE_FILE:-${CLAUDE_DIR}/mcp-needs-auth-cache.json}"
AUTO_REVIEW_ENABLE_CLAUDEAI_MCP="${AUTO_REVIEW_ENABLE_CLAUDEAI_MCP:-0}"
ENABLE_CLAUDEAI_MCP_SERVERS_VALUE="false"

mkdir -p "${STATE_DIR}"
touch "${RUNNER_LOG}" "${STREAM_LOG}" "${LAST_MESSAGE_FILE}"

now() {
  date '+%Y-%m-%d %H:%M:%S %Z'
}

log() {
  printf '[%s] %s\n' "$(now)" "$*" | tee -a "${RUNNER_LOG}"
}

has_claude_ai_oauth() {
  local file
  for file in \
    "${CLAUDE_DIR}/.credentials.json" \
    "${CLAUDE_DIR}/credentials-account2.json" \
    "${CLAUDE_DIR}/.credentials-account1-backup.json"; do
    if [[ -f "${file}" ]] && grep -q '"claudeAiOauth"' "${file}" && grep -q '"accessToken"' "${file}"; then
      return 0
    fi
  done
  return 1
}

needs_auth_cache_has_google_connectors() {
  if [[ ! -f "${MCP_NEEDS_AUTH_CACHE_FILE}" ]]; then
    return 1
  fi
  grep -Eiq 'claude\.ai (gmail|google calendar)' "${MCP_NEEDS_AUTH_CACHE_FILE}"
}

configure_headless_mcp() {
  if [[ "${AUTO_REVIEW_ENABLE_CLAUDEAI_MCP}" != "1" ]]; then
    ENABLE_CLAUDEAI_MCP_SERVERS_VALUE="false"
    log "reviewer-mcp preflight: claude_ai_mcp=disabled reason=opt_in_required env=AUTO_REVIEW_ENABLE_CLAUDEAI_MCP=1"
    return 0
  fi

  if ! has_claude_ai_oauth; then
    ENABLE_CLAUDEAI_MCP_SERVERS_VALUE="false"
    log "reviewer-mcp preflight: claude_ai_mcp=disabled reason=missing_claude_ai_oauth"
    return 0
  fi

  if needs_auth_cache_has_google_connectors; then
    ENABLE_CLAUDEAI_MCP_SERVERS_VALUE="false"
    log "reviewer-mcp preflight: claude_ai_mcp=disabled reason=cached_needs_auth_for_google_connectors"
    return 0
  fi

  ENABLE_CLAUDEAI_MCP_SERVERS_VALUE="true"
  log "reviewer-mcp preflight: claude_ai_mcp=enabled reason=opt_in_and_auth_present"
}

is_dirty_worktree() {
  [[ -n "$(git -C "${REVIEW_WORKTREE_DIR}" status --porcelain 2>/dev/null || true)" ]]
}

is_managed_reviewer_repo() {
  case "${REVIEW_WORKTREE_DIR}" in
    "${STATE_DIR}/repos/reviewer"|\
    "${STATE_DIR}/repos/reviewer/"*|\
    "${STATE_DIR}/repos/"*)
      return 0
      ;;
  esac
  return 1
}

auto_recover_dirty_worktree() {
  if [[ "${AUTO_REVIEW_FORCE:-0}" != "1" ]]; then
    return 1
  fi
  if ! is_managed_reviewer_repo; then
    return 1
  fi

  log "AUTO_REVIEW_FORCE=1 and managed reviewer repo detected; discarding local changes for auto-recovery"
  git -C "${REVIEW_WORKTREE_DIR}" reset --hard HEAD >/dev/null 2>&1 || {
    log "failed to reset reviewer worktree during auto-recovery"
    return 1
  }
  git -C "${REVIEW_WORKTREE_DIR}" clean -fd >/dev/null 2>&1 || {
    log "failed to clean reviewer worktree during auto-recovery"
    return 1
  }
  return 0
}

ensure_on_base_branch() {
  if ! git -C "${REVIEW_WORKTREE_DIR}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    log "review worktree is not a git repository: ${REVIEW_WORKTREE_DIR}"
    exit 1
  fi

  local current_branch
  current_branch="$(git -C "${REVIEW_WORKTREE_DIR}" rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
  if [[ -z "${current_branch}" ]]; then
    log "unable to determine current branch in ${REVIEW_WORKTREE_DIR}"
    exit 1
  fi

  if [[ "${current_branch}" == "${REQUIRED_BASE_BRANCH}" ]]; then
    return 0
  fi

  if ! git -C "${REVIEW_WORKTREE_DIR}" show-ref --verify --quiet "refs/heads/${REQUIRED_BASE_BRANCH}"; then
    if ! git -C "${REVIEW_WORKTREE_DIR}" show-ref --verify --quiet "refs/remotes/origin/${REQUIRED_BASE_BRANCH}"; then
      git -C "${REVIEW_WORKTREE_DIR}" fetch origin --prune >/dev/null 2>&1 || true
    fi
    if git -C "${REVIEW_WORKTREE_DIR}" show-ref --verify --quiet "refs/remotes/origin/${REQUIRED_BASE_BRANCH}"; then
      log "creating local base branch '${REQUIRED_BASE_BRANCH}' from origin/${REQUIRED_BASE_BRANCH}"
      if ! git -C "${REVIEW_WORKTREE_DIR}" branch "${REQUIRED_BASE_BRANCH}" "origin/${REQUIRED_BASE_BRANCH}" >/dev/null 2>&1; then
        log "failed to create local base branch '${REQUIRED_BASE_BRANCH}' from origin/${REQUIRED_BASE_BRANCH}"
        exit 1
      fi
    else
      log "required base branch not found locally or on origin: ${REQUIRED_BASE_BRANCH}"
      exit 1
    fi
  fi

  if is_dirty_worktree; then
    if ! auto_recover_dirty_worktree; then
      log "worktree is on '${current_branch}' with local changes; cannot auto-switch to '${REQUIRED_BASE_BRANCH}'"
      log "resolve local changes first, then rerun reviewer autowork"
      exit 1
    fi
  fi

  log "switching review worktree branch '${current_branch}' -> '${REQUIRED_BASE_BRANCH}'"
  if ! git -C "${REVIEW_WORKTREE_DIR}" switch "${REQUIRED_BASE_BRANCH}" >/dev/null 2>&1; then
    log "failed to switch to required base branch '${REQUIRED_BASE_BRANCH}'"
    exit 1
  fi
}

review_items_exist() {
  find "${SHARED_ISSUES_DIR}/03-needs-review" -maxdepth 1 -type f -name '*.md' | grep -q . && return 0
  find "${SHARED_ISSUES_DIR}/04-in-review" -maxdepth 1 -type f -name '*.md' | grep -q . && return 0
  return 1
}

has_active_reviewer_claude() {
  local matches
  matches="$({
    ps -axo pid=,command= | awk -v marker="${REVIEW_INSTRUCTIONS_FILE}" '
      {
        pid=$1
        $1=""
        sub(/^ /, "", $0)
        cmd=$0
        if (cmd ~ /claude-review-autowork\.sh/) next
        if ((cmd ~ /(^|[[:space:]])claude([[:space:]]|$)/ || cmd ~ /\/claude([[:space:]]|$)/) && index(cmd, marker) > 0) {
          print pid " " cmd
        }
      }
    '
  })"
  if [[ -n "${matches}" ]]; then
    log "Detected active reviewer Claude process; skipping this run."
    printf '%s\n' "${matches}" | tee -a "${RUNNER_LOG}"
    return 0
  fi
  return 1
}

if ! command -v claude >/dev/null 2>&1; then
  log "claude binary not found on PATH"
  exit 1
fi

if ! command -v tmux >/dev/null 2>&1; then
  log "tmux not found on PATH"
  exit 1
fi

if [[ ! -d "${REVIEW_WORKTREE_DIR}" ]]; then
  log "review worktree directory not found: ${REVIEW_WORKTREE_DIR}"
  exit 1
fi

if [[ ! -d "${SHARED_ISSUES_DIR}" ]]; then
  log "issues directory not found: ${SHARED_ISSUES_DIR}"
  exit 1
fi

if [[ ! -f "${REVIEW_INSTRUCTIONS_FILE}" ]]; then
  log "review instructions missing: ${REVIEW_INSTRUCTIONS_FILE}"
  exit 1
fi

if [[ ! -d "${SHARED_ISSUES_DIR}/03-needs-review" ]]; then
  log "needs-review directory missing: ${SHARED_ISSUES_DIR}/03-needs-review"
  exit 1
fi

if [[ ! -d "${SHARED_ISSUES_DIR}/04-in-review" ]]; then
  log "in-review directory missing: ${SHARED_ISSUES_DIR}/04-in-review"
  exit 1
fi

ensure_on_base_branch

if ! review_items_exist; then
  log "No review items in 03-needs-review or 04-in-review; skipping run."
  exit 0
fi

MONITOR_CMD="bash -lc 'echo [reviewer] monitor started at $(date); echo [reviewer] tailing ${STREAM_LOG}; tail -n 200 -F ${STREAM_LOG}'"

if tmux has-session -t "${SESSION_NAME}" 2>/dev/null; then
  if tmux list-windows -t "${SESSION_NAME}" -F '#{window_name}' | grep -qx "monitor"; then
    tmux respawn-pane -k -t "${SESSION_NAME}:monitor.0" "${MONITOR_CMD}" >/dev/null 2>&1 || true
  else
    tmux new-window -d -t "${SESSION_NAME}" -n monitor "${MONITOR_CMD}"
  fi
  if tmux list-panes -a -t "${SESSION_NAME}" -F '#{pane_dead} #{pane_current_command}' | awk '$1=="0" && $2=="claude" {found=1} END{exit found?0:1}'; then
    log "Reviewer session already has a running claude pane; skipping this run."
    exit 0
  fi
else
  tmux new-session -d -s "${SESSION_NAME}" -n monitor "${MONITOR_CMD}"
  tmux set-window-option -g -t "${SESSION_NAME}" remain-on-exit on >/dev/null
  tmux set-option -g -t "${SESSION_NAME}" mouse on >/dev/null || true
fi

if [[ "${AUTO_REVIEW_FORCE:-0}" != "1" ]]; then
  if has_active_reviewer_claude; then
    exit 0
  fi
else
  log "AUTO_REVIEW_FORCE=1 set; bypassing active reviewer guard."
fi

configure_headless_mcp

cat > "${PROMPT_FILE}" <<PROMPT
Read and follow ${REVIEW_INSTRUCTIONS_FILE} exactly.

Primary operating docs:
- ${CONTROL_REPO_DIR}/build/INSTRUCTIONS.md
- ${CONTROL_REPO_DIR}/build/CONTEXT.md
- Active review task(s) from ${SHARED_ISSUES_DIR}/03-needs-review and ${SHARED_ISSUES_DIR}/04-in-review

Critical rules:
- Perform review work in ${REVIEW_WORKTREE_DIR}.
- Do not keep files stuck in 04-in-review; move to 05-completed or back to 01-ready with concrete notes.
- Ensure approved task PRs are merged into branch ${REQUIRED_BASE_BRANCH} before moving tasks to 05-completed.
- If changes are required, add a top-level "## Reviewer Required Changes (YYYY-MM-DD HH:MM TZ)" block to the task file before moving it to 01-ready.
- Required changes block must include reviewer name/model, severity-ordered checklist items (P0-P3), file references, required fix, and required tests per item.
- Also append a concise summary to ${SHARED_ISSUES_DIR}/notes.md.
- Use non-interactive GitHub CLI checks/actions (gh pr view, gh pr checks, gh pr merge).
- Never run gh pr create or interactive auth/login commands.
- Append blocker details to ${SHARED_ISSUES_DIR}/notes.md.
- API routes are /v1/* except health (/health*) and test reset (POST /test/reset).
- Headless MCP policy: claude.ai MCP servers are disabled by default for reviewer runs.
  - To enable when auth is configured, set AUTO_REVIEW_ENABLE_CLAUDEAI_MCP=1.
  - If preflight logs cached Google connector auth gaps, keep MCP disabled until OAuth is fixed.

Begin reviewing now and continue until no review items remain in 03-needs-review or 04-in-review.
PROMPT

RUN_NAME="review-$(date '+%Y%m%d-%H%M%S')"
RUN_SCRIPT="${STATE_DIR}/${RUN_NAME}.sh"
RUN_LOG="${STATE_DIR}/${RUN_NAME}.log"

cat > "${RUN_SCRIPT}" <<'RUNEOF'
#!/usr/bin/env bash
set -euo pipefail

REVIEW_WORKTREE_DIR="${REVIEW_WORKTREE_DIR:?REVIEW_WORKTREE_DIR is required}"
PROMPT_FILE="${PROMPT_FILE:?PROMPT_FILE is required}"
REVIEW_INSTRUCTIONS_FILE="${REVIEW_INSTRUCTIONS_FILE:?REVIEW_INSTRUCTIONS_FILE is required}"
RUN_LOG="${RUN_LOG:?RUN_LOG is required}"
STREAM_LOG="${STREAM_LOG:?STREAM_LOG is required}"
LAST_MESSAGE_FILE="${LAST_MESSAGE_FILE:?LAST_MESSAGE_FILE is required}"
TIMEOUT_CMD="${TIMEOUT_CMD:-}"
ENABLE_CLAUDEAI_MCP_SERVERS_VALUE="${ENABLE_CLAUDEAI_MCP_SERVERS_VALUE:-false}"

cd "${REVIEW_WORKTREE_DIR}"
printf '[%s] reviewer run started\n' "$(date '+%Y-%m-%d %H:%M:%S %Z')"

REVIEW_TIMEOUT="${REVIEW_TIMEOUT:-1200}"
export GH_PROMPT_DISABLED=1
export GIT_TERMINAL_PROMPT=0
export ENABLE_CLAUDEAI_MCP_SERVERS="${ENABLE_CLAUDEAI_MCP_SERVERS_VALUE}"
SYSTEM_PROMPT="reviewer instructions path: ${REVIEW_INSTRUCTIONS_FILE}"
REVIEW_PROMPT="$(cat "${PROMPT_FILE}")"

if [[ -n "${TIMEOUT_CMD}" ]]; then
  "${TIMEOUT_CMD}" --foreground "${REVIEW_TIMEOUT}" claude \
    --dangerously-skip-permissions \
    --system-prompt "${SYSTEM_PROMPT}" \
    -p "${REVIEW_PROMPT}" \
    2>&1 | tee -a "${RUN_LOG}" "${STREAM_LOG}"
  status=${PIPESTATUS[0]}
else
  claude \
    --dangerously-skip-permissions \
    --system-prompt "${SYSTEM_PROMPT}" \
    -p "${REVIEW_PROMPT}" \
    2>&1 | tee -a "${RUN_LOG}" "${STREAM_LOG}"
  status=${PIPESTATUS[0]}
fi

tail -n 200 "${RUN_LOG}" > "${LAST_MESSAGE_FILE}" || true

if [[ "${status}" -eq 124 ]]; then
  printf '[%s] reviewer run TIMED OUT after %ss\n' "$(date '+%Y-%m-%d %H:%M:%S %Z')" "${REVIEW_TIMEOUT}"
else
  printf '[%s] reviewer run finished (exit=%s)\n' "$(date '+%Y-%m-%d %H:%M:%S %Z')" "${status}"
fi
exit "${status}"
RUNEOF
chmod +x "${RUN_SCRIPT}"

if [[ "${AUTO_REVIEW_DRY_RUN:-0}" == "1" ]]; then
  log "Dry run only; would start tmux window ${RUN_NAME}."
  exit 0
fi

tmux new-window -d -t "${SESSION_NAME}" -n "${RUN_NAME}" \
  "REVIEW_WORKTREE_DIR='${REVIEW_WORKTREE_DIR}' PROMPT_FILE='${PROMPT_FILE}' REVIEW_INSTRUCTIONS_FILE='${REVIEW_INSTRUCTIONS_FILE}' RUN_LOG='${RUN_LOG}' STREAM_LOG='${STREAM_LOG}' LAST_MESSAGE_FILE='${LAST_MESSAGE_FILE}' TIMEOUT_CMD='${TIMEOUT_CMD}' ENABLE_CLAUDEAI_MCP_SERVERS_VALUE='${ENABLE_CLAUDEAI_MCP_SERVERS_VALUE}' '${RUN_SCRIPT}'"

log "Started reviewer autowork in tmux session '${SESSION_NAME}' window '${RUN_NAME}'. Attach with: tmux attach -t ${SESSION_NAME}"
