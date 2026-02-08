#!/usr/bin/env bash
set -euo pipefail

PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${HOME}/.npm-global/bin"

REPO_DIR="/Users/sam/Documents/Dev/otter-camp"
SESSION_NAME="codex-autowork"
STATE_DIR="${REPO_DIR}/.autowork"
RUNNER_LOG="${STATE_DIR}/runner.log"
PROMPT_FILE="${STATE_DIR}/autowork-prompt.txt"

mkdir -p "${STATE_DIR}"

now() {
  date '+%Y-%m-%d %H:%M:%S %Z'
}

log() {
  printf '[%s] %s\n' "$(now)" "$*" | tee -a "${RUNNER_LOG}"
}

has_active_codex_cli() {
  # Match real codex CLI processes only (not unrelated PATH strings containing "codex").
  local matches
  matches="$(
    ps -axo pid=,command= | awk '
      {
        pid=$1
        $1=""
        sub(/^ /, "", $0)
        cmd=$0
        if (cmd ~ /codex-autowork\.sh/) next
        if (cmd ~ /codex app-server/) next
        if (cmd ~ /^codex([[:space:]]|$)/ ||
            cmd ~ /\/bin\/codex([[:space:]]|$)/ ||
            cmd ~ /\/codex\/codex([[:space:]]|$)/) {
          print pid " " cmd
        }
      }
    '
  )"
  if [[ -n "${matches}" ]]; then
    log "Detected active Codex CLI process; skipping this run."
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

if has_active_codex_cli; then
  exit 0
fi

if tmux has-session -t "${SESSION_NAME}" 2>/dev/null; then
  if tmux list-panes -a -t "${SESSION_NAME}" -F '#{pane_dead} #{pane_current_command}' | awk '$1=="0" && $2=="codex" {found=1} END{exit found?0:1}'; then
    log "Autowork session already has a running codex pane; skipping this run."
    exit 0
  fi
else
  tmux new-session -d -s "${SESSION_NAME}" -n monitor "bash -lc 'echo [autowork] monitor started at $(date) ; sleep 3600'"
  tmux set-window-option -g -t "${SESSION_NAME}" remain-on-exit on >/dev/null
  tmux set-option -g -t "${SESSION_NAME}" mouse on >/dev/null || true
fi

cat > "${PROMPT_FILE}" <<'PROMPT'
Read and follow /Users/sam/Documents/Dev/otter-camp/issues/instructions.md exactly.

Start-of-run requirements:
- Execute the Start-of-Run Preflight checklist first.
- Continue work using the defined priority order.
- Create small GitHub issues with explicit tests before implementation.
- Implement with TDD, make small descriptive commits, and push as you go.
- Move implementation-complete specs into /Users/sam/Documents/Dev/otter-camp/issues/03-needs-review.
- Do not move specs to 05-completed before external reviewer sign-off.
- Never stage or commit files under /Users/sam/Documents/Dev/otter-camp/issues.
- If blocked, note required follow-up in /Users/sam/Documents/Dev/otter-camp/issues/notes.md and continue with the next actionable item.

Begin now and keep working until no actionable ready/in-progress spec remains.
PROMPT

RUN_NAME="run-$(date '+%Y%m%d-%H%M%S')"
RUN_SCRIPT="${STATE_DIR}/${RUN_NAME}.sh"

cat > "${RUN_SCRIPT}" <<'RUNEOF'
#!/usr/bin/env bash
set -euo pipefail

cd /Users/sam/Documents/Dev/otter-camp

printf '[%s] autowork run started\n' "$(date '+%Y-%m-%d %H:%M:%S %Z')"

codex exec \
  --dangerously-bypass-approvals-and-sandbox \
  --cd /Users/sam/Documents/Dev/otter-camp \
  - < /Users/sam/Documents/Dev/otter-camp/.autowork/autowork-prompt.txt

status=$?
printf '[%s] autowork run finished (exit=%s)\n' "$(date '+%Y-%m-%d %H:%M:%S %Z')" "${status}"
exit "${status}"
RUNEOF
chmod +x "${RUN_SCRIPT}"

if [[ "${AUTO_WORK_DRY_RUN:-0}" == "1" ]]; then
  log "Dry run only; would start tmux window ${RUN_NAME}."
  exit 0
fi

tmux new-window -d -t "${SESSION_NAME}" -n "${RUN_NAME}" "${RUN_SCRIPT}"
log "Started autowork in tmux session '${SESSION_NAME}' window '${RUN_NAME}'. Attach with: tmux attach -t ${SESSION_NAME}"
