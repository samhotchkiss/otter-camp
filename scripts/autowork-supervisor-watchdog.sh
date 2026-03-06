#!/usr/bin/env bash
set -euo pipefail

PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${HOME}/.npm-global/bin:${HOME}/.local/bin"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="${AUTOWORK_REPO_DIR:-$(cd "${SCRIPT_DIR}/.." && pwd)}"
ISSUES_DIR="${AUTOWORK_ISSUES_DIR:-${REPO_DIR}/issues}"
STATE_DIR="${AUTOWORK_STATE_DIR:-${HOME}/otter-data/sessions}"
BASE_BRANCH="${AUTOWORK_BASE_BRANCH:-v2}"
ISSUE_LANE_CLI="${REPO_DIR}/scripts/issue-lane.sh"
BUILDER_REPO_DIR="${AUTOWORK_BUILDER_REPO_DIR:-${STATE_DIR}/repos/builder}"
REVIEWER_REPO_DIR="${AUTOWORK_REVIEWER_REPO_DIR:-${STATE_DIR}/repos/reviewer}"

CODEX_RUNNER="${AUTOWORK_CODEX_RUNNER:-${REPO_DIR}/scripts/codex-autowork.sh}"
REVIEW_RUNNER="${AUTOWORK_REVIEW_RUNNER:-${REPO_DIR}/scripts/claude-review-autowork.sh}"
GITHUB_RETRY_HELPER="${AUTOWORK_GITHUB_RETRY_HELPER:-${REPO_DIR}/scripts/lib/github-retry.sh}"

CODEX_MARKER="${STATE_DIR}/autowork-last-message.txt"
REVIEW_MARKER="${ISSUES_DIR}/reviewer-instructions.md"

WATCHDOG_LOG="${STATE_DIR}/supervisor-watchdog.log"
LOCK_DIR="${STATE_DIR}/supervisor-watchdog.lock"
LOCK_HEARTBEAT_FILE="${LOCK_DIR}/heartbeat"
NOTES_FILE="${ISSUES_DIR}/notes.md"

STALE_BUILDER_SECONDS="${WATCHDOG_STALE_BUILDER_SECONDS:-1800}"
STALE_REVIEWER_SECONDS="${WATCHDOG_STALE_REVIEWER_SECONDS:-600}"
STALE_IN_REVIEW_SECONDS="${WATCHDOG_STALE_IN_REVIEW_SECONDS:-7200}"
STALE_IN_PROGRESS_SECONDS="${WATCHDOG_STALE_IN_PROGRESS_SECONDS:-3600}"
STALE_NEEDS_REVIEW_SECONDS="${WATCHDOG_STALE_NEEDS_REVIEW_SECONDS:-1800}"
LOCK_STALE_SECONDS="${WATCHDOG_LOCK_STALE_SECONDS:-300}"
REVIEWER_MAX_RUNTIME_SECONDS="${WATCHDOG_REVIEWER_MAX_RUNTIME_SECONDS:-1500}"
START_RETRY_COUNT="${WATCHDOG_START_RETRY_COUNT:-3}"
START_RETRY_DELAY_SECONDS="${WATCHDOG_START_RETRY_DELAY_SECONDS:-10}"

DRY_RUN="${WATCHDOG_DRY_RUN:-0}"
NOTE_THROTTLE_DIR="${STATE_DIR}/note-throttle"
ENFORCE_DEP_GRAPH="${WATCHDOG_ENFORCE_DEP_GRAPH:-1}"

mkdir -p "${STATE_DIR}"
mkdir -p "${NOTE_THROTTLE_DIR}"
touch "${WATCHDOG_LOG}"

# shellcheck source=scripts/lib/issue-queue.sh
source "${SCRIPT_DIR}/lib/issue-queue.sh"
# shellcheck source=scripts/lib/run-jsonl.sh
source "${SCRIPT_DIR}/lib/run-jsonl.sh"

now_epoch() { date +%s; }
now_ts() { date '+%Y-%m-%d %H:%M:%S %Z'; }

log() {
  printf '[%s] [supervisor] %s\n' "$(now_ts)" "$*" | tee -a "${WATCHDOG_LOG}"
}

origin_url() {
  git -C "${REPO_DIR}" remote get-url origin 2>/dev/null || true
}

ensure_managed_repo() {
  local target="$1"
  local role="$2"
  local src_local src_origin
  src_local="${REPO_DIR}"
  src_origin="$(origin_url)"

  mkdir -p "$(dirname "${target}")"

  if [[ ! -d "${target}/.git" ]]; then
    log "creating managed ${role} repo at ${target}"
    if [[ "${DRY_RUN}" == "1" ]]; then
      return 0
    fi
    git clone "${src_local}" "${target}" >> "${WATCHDOG_LOG}" 2>&1 || {
      log "failed to clone ${role} repo from ${src_local}"
      return 1
    }
  fi

  if [[ "${DRY_RUN}" != "1" ]]; then
    if [[ -n "${src_origin}" ]]; then
      git -C "${target}" remote set-url origin "${src_origin}" >> "${WATCHDOG_LOG}" 2>&1 || true
    fi
    git -C "${target}" fetch origin --prune >> "${WATCHDOG_LOG}" 2>&1 || true
    if ! git -C "${target}" show-ref --verify --quiet "refs/heads/${BASE_BRANCH}" && \
       git -C "${target}" show-ref --verify --quiet "refs/remotes/origin/${BASE_BRANCH}"; then
      log "creating local ${role} base branch '${BASE_BRANCH}' from origin/${BASE_BRANCH}"
      git -C "${target}" branch "${BASE_BRANCH}" "origin/${BASE_BRANCH}" >> "${WATCHDOG_LOG}" 2>&1 || true
    fi
  fi
}

acquire_lock() {
  if mkdir "${LOCK_DIR}" 2>/dev/null; then
    initialize_lock_dir
    return 0
  fi

  local state reason lock_pid age
  IFS=$'\t' read -r state reason lock_pid age < <(inspect_lock_state)
  if [[ "${state}" == "stale" ]]; then
    log "reclaiming stale watchdog lock (reason=${reason}, pid=${lock_pid:-none}, age=${age}s)"
    if [[ -n "${lock_pid}" ]] && [[ "${lock_pid}" =~ ^[0-9]+$ ]] && kill -0 "${lock_pid}" 2>/dev/null; then
      kill_pids "watchdog-lock-holder" "${lock_pid}"
    fi
    rm -rf "${LOCK_DIR}"
    if ! mkdir "${LOCK_DIR}" 2>/dev/null; then
      return 1
    fi
    initialize_lock_dir
    return 0
  fi

  return 1
}

release_lock() {
  rm -rf "${LOCK_DIR}" 2>/dev/null || true
}

initialize_lock_dir() {
  printf '%s\n' "$$" > "${LOCK_DIR}/pid"
  touch_lock_heartbeat
}

touch_lock_heartbeat() {
  [[ -d "${LOCK_DIR}" ]] || return 0
  touch "${LOCK_HEARTBEAT_FILE}" 2>/dev/null || true
}

read_lock_pid() {
  if [[ ! -f "${LOCK_DIR}/pid" ]]; then
    return 0
  fi
  tr -d '[:space:]' < "${LOCK_DIR}/pid" 2>/dev/null || true
}

lock_activity_age_seconds() {
  if [[ -e "${LOCK_HEARTBEAT_FILE}" ]]; then
    file_age_seconds "${LOCK_HEARTBEAT_FILE}"
    return
  fi
  if [[ -e "${LOCK_DIR}/pid" ]]; then
    file_age_seconds "${LOCK_DIR}/pid"
    return
  fi
  file_age_seconds "${LOCK_DIR}"
}

inspect_lock_state() {
  if [[ ! -d "${LOCK_DIR}" ]]; then
    printf 'missing\tno_lock\t\t0\n'
    return 0
  fi

  local pid age
  pid="$(read_lock_pid)"
  age="$(lock_activity_age_seconds)"

  if [[ -z "${pid}" ]]; then
    printf 'stale\tmissing_pid\t\t%s\n' "${age}"
    return 0
  fi
  if [[ ! "${pid}" =~ ^[0-9]+$ ]]; then
    printf 'stale\tinvalid_pid\t%s\t%s\n' "${pid}" "${age}"
    return 0
  fi
  if ! kill -0 "${pid}" 2>/dev/null; then
    printf 'stale\tdead_pid\t%s\t%s\n' "${pid}" "${age}"
    return 0
  fi
  if (( age > LOCK_STALE_SECONDS )); then
    printf 'stale\theartbeat_expired\t%s\t%s\n' "${pid}" "${age}"
    return 0
  fi

  printf 'active\tfresh\t%s\t%s\n' "${pid}" "${age}"
}

sleep_with_heartbeat() {
  local seconds="$1"
  while (( seconds > 0 )); do
    touch_lock_heartbeat
    sleep 1
    seconds=$((seconds - 1))
  done
}

run_watchdog_stage() {
  touch_lock_heartbeat
  "$@"
  touch_lock_heartbeat
}

file_age_seconds() {
  local path="$1"
  local mtime
  mtime=$(stat -f '%m' "$path" 2>/dev/null || echo 0)
  local now
  now=$(now_epoch)
  if [[ "$mtime" -le 0 ]]; then
    echo 999999
  else
    echo $(( now - mtime ))
  fi
}

lane_file_age_seconds() {
  local path="$1"
  local ctime
  ctime=$(stat -f '%c' "$path" 2>/dev/null || echo 0)
  local now
  now=$(now_epoch)
  if [[ "${ctime}" -le 0 ]]; then
    echo 999999
  else
    echo $(( now - ctime ))
  fi
}

count_lane_files() {
  local lane="$1"
  if [[ ! -d "${lane}" ]]; then
    echo 0
    return
  fi
  find "${lane}" -maxdepth 1 -type f -name '*.md' | wc -l | tr -d ' '
}

latest_file() {
  local pattern="$1"
  ls -t ${pattern} 2>/dev/null | head -n 1 || true
}

validate_run_jsonl_terminal_state() {
  local latest_json active_builder_pids
  latest_json="$(latest_file "${STATE_DIR}/run-*.jsonl")"
  active_builder_pids="$(codex_active_pids)"

  local total=0 missing=0 repaired=0 skipped=0
  local file base started terminal action

  while IFS= read -r file; do
    [[ -z "${file}" ]] && continue
    total=$((total + 1))
    base="$(basename "${file}")"
    started="$(run_jsonl_started_count "${file}")"
    terminal="$(run_jsonl_terminal_count "${file}")"

    if (( started > 0 && terminal == 0 )); then
      missing=$((missing + 1))
      if [[ -n "${active_builder_pids}" && -n "${latest_json}" && "${file}" == "${latest_json}" ]]; then
        skipped=$((skipped + 1))
        log "jsonl-validator outcome=missing_terminal_pending_active_run file=${base} started=${started} terminal=${terminal}"
        continue
      fi

      action="$(
        run_jsonl_append_interrupted_terminal \
          "${file}" \
          "missing_terminal_event_detected_by_supervisor" \
          "autowork-supervisor-watchdog"
      )"
      if [[ "${action}" == "appended" ]]; then
        repaired=$((repaired + 1))
        log "jsonl-validator outcome=terminalized file=${base} started=${started} terminal_before=${terminal}"
      else
        log "jsonl-validator outcome=missing_terminal_unresolved file=${base} started=${started} terminal_before=${terminal}"
      fi
    fi
  done < <(find "${STATE_DIR}" -maxdepth 1 -type f -name 'run-*.jsonl' | sort)

  if (( missing > 0 )); then
    append_note_throttled \
      "jsonl-terminal-validator-missing" \
      600 \
      "JSONL validator found missing terminal state in ${missing} run log(s); repaired=${repaired} skipped_active=${skipped}"
  fi

  log "jsonl-validator summary total=${total} missing=${missing} repaired=${repaired} skipped_active=${skipped}"
}

task_num_from_file() {
  local file="$1"
  basename "${file}" | sed -E 's/^([0-9]{3}).*/\1/'
}

completed_task_numbers() {
  local completed_dir="${ISSUES_DIR}/05-completed"
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

elapsed_to_seconds() {
  local etime="$1"
  local days=0 rest="${etime}"
  if [[ "${etime}" == *-* ]]; then
    days="${etime%%-*}"
    rest="${etime#*-}"
  fi

  local h=0 m=0 s=0
  IFS=: read -r a b c <<< "${rest}"
  if [[ -n "${c:-}" ]]; then
    h="${a}"
    m="${b}"
    s="${c}"
  elif [[ -n "${b:-}" ]]; then
    m="${a}"
    s="${b}"
  else
    s="${a:-0}"
  fi

  echo $(( 10#${days:-0} * 86400 + 10#${h:-0} * 3600 + 10#${m:-0} * 60 + 10#${s:-0} ))
}

max_pid_elapsed_seconds() {
  local pids="$1"
  local max_age=0
  local pid et age

  while IFS= read -r pid; do
    [[ -z "${pid}" ]] && continue
    et="$(ps -p "${pid}" -o etime= 2>/dev/null | awk '{print $1}' || true)"
    [[ -z "${et}" ]] && continue
    age="$(elapsed_to_seconds "${et}")"
    if (( age > max_age )); then
      max_age="${age}"
    fi
  done <<< "${pids}"

  echo "${max_age}"
}

codex_active_pids() {
  ps -axo pid=,command= | awk -v marker="${CODEX_MARKER}" '
    {
      pid=$1
      $1=""
      sub(/^ /, "", $0)
      cmd=$0
      if (cmd ~ /codex-autowork\.sh/) next
      if (cmd ~ /codex app-server/) next
      if ((cmd ~ /codex exec/ || cmd ~ /\/codex\/codex exec/) && index(cmd, marker) > 0) {
        print pid
      }
    }
  '
}

reviewer_active_pids() {
  ps -axo pid=,command= | awk -v marker="${REVIEW_MARKER}" '
    {
      pid=$1
      $1=""
      sub(/^ /, "", $0)
      cmd=$0
      if (cmd ~ /claude-review-autowork\.sh/) next
      if ((cmd ~ /(^|[[:space:]])claude([[:space:]]|$)/ || cmd ~ /\/claude([[:space:]]|$)/) && index(cmd, marker) > 0) {
        print pid
      }
    }
  '
}

reviewer_blocker_pids() {
  ps -axo pid=,command= | awk '
    {
      pid=$1
      $1=""
      sub(/^ /, "", $0)
      cmd=$0
      if ((cmd ~ /\/\.claude\/shell-snapshots\// || cmd ~ /\/tmp\/claude-/) &&
          (cmd ~ /gh pr create/ || cmd ~ /gh auth login/ || cmd ~ /gh auth refresh/)) {
        print pid
      }
    }
  '
}

kill_pids() {
  local kind="$1"
  local pids="$2"
  [[ -z "${pids}" ]] && return 0

  log "terminating stale ${kind} pid(s): $(echo "${pids}" | tr '\n' ' ')"
  while IFS= read -r pid; do
    [[ -z "${pid}" ]] && continue
    if [[ "${DRY_RUN}" == "1" ]]; then
      continue
    fi
    kill "${pid}" 2>/dev/null || true
  done <<< "${pids}"

  sleep_with_heartbeat 2

  while IFS= read -r pid; do
    [[ -z "${pid}" ]] && continue
    if kill -0 "${pid}" 2>/dev/null; then
      log "forcing kill -9 for ${kind} pid ${pid}"
      if [[ "${DRY_RUN}" == "1" ]]; then
        continue
      fi
      kill -9 "${pid}" 2>/dev/null || true
    fi
  done <<< "${pids}"
}

start_codex_runner() {
  if [[ ! -x "${CODEX_RUNNER}" ]]; then
    log "codex runner missing or not executable: ${CODEX_RUNNER}"
    return 1
  fi
  ensure_managed_repo "${BUILDER_REPO_DIR}" "builder" || return 1
  log "starting codex runner"
  if [[ "${DRY_RUN}" == "1" ]]; then
    return 0
  fi
  AUTO_WORK_FORCE=1 AUTO_WORK_BASE_BRANCH="${BASE_BRANCH}" AUTO_WORK_WORKTREE_DIR="${BUILDER_REPO_DIR}" "${CODEX_RUNNER}" >> "${WATCHDOG_LOG}" 2>&1 || {
    log "codex runner start failed"
    return 1
  }
}

start_reviewer_runner() {
  if [[ ! -x "${REVIEW_RUNNER}" ]]; then
    log "reviewer runner missing or not executable: ${REVIEW_RUNNER}"
    return 1
  fi
  ensure_managed_repo "${REVIEWER_REPO_DIR}" "reviewer" || return 1
  log "starting claude reviewer runner"
  if [[ "${DRY_RUN}" == "1" ]]; then
    return 0
  fi
  AUTO_REVIEW_FORCE=1 AUTO_REVIEW_BASE_BRANCH="${BASE_BRANCH}" REVIEW_WORKTREE_DIR="${REVIEWER_REPO_DIR}" "${REVIEW_RUNNER}" >> "${WATCHDOG_LOG}" 2>&1 || {
    log "reviewer runner start failed"
    return 1
  }
}

append_note() {
  local message="$1"
  mkdir -p "$(dirname "${NOTES_FILE}")"
  if [[ ! -f "${NOTES_FILE}" ]]; then
    printf '# Notes\n\n' > "${NOTES_FILE}"
  fi
  printf -- '- [%s] %s\n' "$(now_ts)" "${message}" >> "${NOTES_FILE}"
}

append_note_throttled() {
  local key="$1"
  local cooldown_seconds="$2"
  local message="$3"

  local safe_key
  safe_key="$(printf '%s' "${key}" | tr -c 'A-Za-z0-9_.-' '_')"
  local stamp="${NOTE_THROTTLE_DIR}/${safe_key}.stamp"
  local now
  now="$(now_epoch)"
  local last=0

  if [[ -f "${stamp}" ]]; then
    last="$(cat "${stamp}" 2>/dev/null || echo 0)"
  fi

  if (( now - last < cooldown_seconds )); then
    return 0
  fi

  printf '%s' "${now}" > "${stamp}"
  append_note "${message}"
}

move_lane_idempotent() {
  local src_lane="$1"
  local dst_lane="$2"
  local task_basename="$3"
  local context="$4"
  local status reconcile_outcome

  if [[ "${DRY_RUN}" == "1" ]]; then
    status="$(queue_classify_lane "$(queue_find_lane "${ISSUES_DIR}" "${task_basename}" || echo "missing")" "${dst_lane}")"
  else
    status="$(queue_move_task "${ISSUES_DIR}" "${src_lane}" "${dst_lane}" "${task_basename}" || echo "missing")"
  fi

  case "${status}" in
    claimed|already_claimed|already_completed)
      reconcile_outcome="queue_reconciled"
      ;;
    *)
      reconcile_outcome="queue_conflict_hard_stop"
      ;;
  esac

  log "${context}: outcome=${reconcile_outcome} raw_status=${status} task=${task_basename} from=${src_lane} to=${dst_lane}"
  if [[ "${reconcile_outcome}" != "queue_reconciled" ]]; then
    append_note_throttled "queue-conflict-${task_basename}" 900 "queue_conflict_hard_stop context=${context} task=${task_basename} from=${src_lane} to=${dst_lane} raw_status=${status}"
  fi
  return 0
}

enforce_completed_dependencies() {
  [[ "${ENFORCE_DEP_GRAPH}" == "1" ]] || return 0

  local completed_dir="${ISSUES_DIR}/05-completed"
  local needs_review_dir="${ISSUES_DIR}/03-needs-review"
  [[ -d "${completed_dir}" ]] || return 0
  [[ -d "${needs_review_dir}" ]] || return 0

  local moved file task_num deps missing dep completed_nums base
  moved=0

  while IFS= read -r file; do
    [[ -z "${file}" ]] && continue
    task_num="$(task_num_from_file "${file}")"
    deps="$(extract_dep_numbers "${file}")"
    [[ -z "${deps}" ]] && continue

    completed_nums="$(completed_task_numbers)"
    missing=""
    while IFS= read -r dep; do
      [[ -z "${dep}" ]] && continue
      if ! printf '%s\n' "${completed_nums}" | rg -qx "${dep}"; then
        missing="${missing} ${dep}"
      fi
    done <<< "${deps}"

    if [[ -z "${missing}" ]]; then
      continue
    fi

    base="$(basename "${file}")"
    log "dependency mismatch: ${base} in 05-completed has unmet deps:${missing}; requeue to 03-needs-review"
    append_note_throttled "dep-mismatch-${task_num}" 1800 "dependency-integrity guard moved ${base} from 05-completed to 03-needs-review; unmet Depends on:${missing}"
    move_lane_idempotent "05-completed" "03-needs-review" "${base}" "dependency-integrity-completed"
    moved=$((moved + 1))
  done < <(find "${completed_dir}" -maxdepth 1 -type f -name '*.md' | sort)

  if (( moved > 0 )); then
    log "dependency-integrity guard moved ${moved} task(s) out of completed"
  fi
}

enforce_in_progress_dependencies() {
  [[ "${ENFORCE_DEP_GRAPH}" == "1" ]] || return 0

  local in_progress_dir="${ISSUES_DIR}/02-in-progress"
  local ready_dir="${ISSUES_DIR}/01-ready"
  [[ -d "${in_progress_dir}" ]] || return 0
  [[ -d "${ready_dir}" ]] || return 0

  local moved file task_num deps missing dep completed_nums base
  local active_pids killed_builder
  moved=0
  active_pids="$(codex_active_pids)"
  killed_builder=0

  while IFS= read -r file; do
    [[ -z "${file}" ]] && continue
    task_num="$(task_num_from_file "${file}")"
    deps="$(extract_dep_numbers "${file}")"
    [[ -z "${deps}" ]] && continue

    completed_nums="$(completed_task_numbers)"
    missing=""
    while IFS= read -r dep; do
      [[ -z "${dep}" ]] && continue
      if ! printf '%s\n' "${completed_nums}" | rg -qx "${dep}"; then
        missing="${missing} ${dep}"
      fi
    done <<< "${deps}"

    if [[ -z "${missing}" ]]; then
      continue
    fi

    if [[ -n "${active_pids}" ]] && (( killed_builder == 0 )); then
      log "dependency mismatch in 02-in-progress while builder active; stopping builder before requeue"
      kill_pids "builder" "${active_pids}"
      killed_builder=1
    fi

    base="$(basename "${file}")"
    log "dependency mismatch: ${base} in 02-in-progress has unmet deps:${missing}; requeue to 01-ready"
    append_note_throttled "dep-in-progress-${task_num}" 900 "dependency-integrity guard requeued ${base} from 02-in-progress to 01-ready; unmet Depends on:${missing}"
    move_lane_idempotent "02-in-progress" "01-ready" "${base}" "dependency-integrity-in-progress"
    moved=$((moved + 1))
  done < <(find "${in_progress_dir}" -maxdepth 1 -type f -name '*.md' | sort)

  if (( moved > 0 )); then
    log "dependency-integrity guard moved ${moved} task(s) out of in-progress"
  fi
}

start_codex_with_retries() {
  local attempt
  for (( attempt=1; attempt<=START_RETRY_COUNT; attempt++ )); do
    if start_codex_runner; then
      return 0
    fi
    if (( attempt < START_RETRY_COUNT )); then
      log "builder start retry ${attempt}/${START_RETRY_COUNT} failed; retrying in ${START_RETRY_DELAY_SECONDS}s"
      sleep_with_heartbeat "${START_RETRY_DELAY_SECONDS}"
    fi
  done
  append_note_throttled "builder-start-failed" 1800 "supervisor failed to start builder after ${START_RETRY_COUNT} attempts"
  return 1
}

start_reviewer_with_retries() {
  local attempt
  for (( attempt=1; attempt<=START_RETRY_COUNT; attempt++ )); do
    if start_reviewer_runner; then
      return 0
    fi
    if (( attempt < START_RETRY_COUNT )); then
      log "reviewer start retry ${attempt}/${START_RETRY_COUNT} failed; retrying in ${START_RETRY_DELAY_SECONDS}s"
      sleep_with_heartbeat "${START_RETRY_DELAY_SECONDS}"
    fi
  done
  append_note_throttled "reviewer-start-failed" 1800 "supervisor failed to start reviewer after ${START_RETRY_COUNT} attempts"
  return 1
}

requeue_stale_in_progress() {
  local in_progress_dir="${ISSUES_DIR}/02-in-progress"
  local ready_dir="${ISSUES_DIR}/01-ready"

  [[ -d "${in_progress_dir}" ]] || return 0
  [[ -d "${ready_dir}" ]] || return 0

  local active_pids
  active_pids="$(codex_active_pids)"
  if [[ -n "${active_pids}" ]]; then
    return 0
  fi

  local moved age base
  moved=0

  while IFS= read -r file; do
    [[ -z "${file}" ]] && continue
    age="$(lane_file_age_seconds "${file}")"
    if (( age <= STALE_IN_PROGRESS_SECONDS )); then
      continue
    fi

    base="$(basename "${file}")"
    log "stale build file detected (${age}s): ${base} -> 01-ready"
    append_note_throttled "stale-in-progress-${base}" 1800 "stuck-build catcher requeued ${base} from 02-in-progress to 01-ready after ${age}s with no active builder"
    move_lane_idempotent "02-in-progress" "01-ready" "${base}" "stale-in-progress"
    moved=$((moved + 1))
  done < <(find "${in_progress_dir}" -maxdepth 1 -type f -name '*.md' | sort)

  if (( moved > 0 )); then
    log "requeued ${moved} stale in-progress file(s)"
  fi
}

flag_stale_needs_review() {
  local needs_review_dir="${ISSUES_DIR}/03-needs-review"
  [[ -d "${needs_review_dir}" ]] || return 0

  local active_pids
  active_pids="$(reviewer_active_pids)"
  if [[ -n "${active_pids}" ]]; then
    return 0
  fi

  local age base stale_count
  stale_count=0
  while IFS= read -r file; do
    [[ -z "${file}" ]] && continue
    age="$(lane_file_age_seconds "${file}")"
    if (( age <= STALE_NEEDS_REVIEW_SECONDS )); then
      continue
    fi
    base="$(basename "${file}")"
    append_note_throttled "stale-needs-review-${base}" 1800 "review queue item ${base} has been in 03-needs-review for ${age}s with no active reviewer; supervisor attempted restart"
    stale_count=$((stale_count + 1))
  done < <(find "${needs_review_dir}" -maxdepth 1 -type f -name '*.md' | sort)

  if (( stale_count > 0 )); then
    log "flagged ${stale_count} stale needs-review file(s)"
  fi
}

requeue_stale_in_review() {
  local in_review_dir="${ISSUES_DIR}/04-in-review"
  local needs_review_dir="${ISSUES_DIR}/03-needs-review"

  [[ -d "${in_review_dir}" ]] || return 0
  [[ -d "${needs_review_dir}" ]] || return 0

  local moved=0

  while IFS= read -r file; do
    [[ -z "${file}" ]] && continue
    local age
    age=$(lane_file_age_seconds "${file}")
    if (( age <= STALE_IN_REVIEW_SECONDS )); then
      continue
    fi

    local base
    base="$(basename "${file}")"
    log "stale review file detected (${age}s): ${base} -> 03-needs-review"
    append_note "stuck-review catcher requeued ${base} from 04-in-review to 03-needs-review after ${age}s with no file updates"
    move_lane_idempotent "04-in-review" "03-needs-review" "${base}" "stale-in-review"
    moved=$((moved + 1))
  done < <(find "${in_review_dir}" -maxdepth 1 -type f -name '*.md' | sort)

  if (( moved > 0 )); then
    log "requeued ${moved} stale review file(s)"
  fi
}

heal_builder() {
  local ready_count in_progress_count active_pids latest_json age
  ready_count=$(count_lane_files "${ISSUES_DIR}/01-ready")
  in_progress_count=$(count_lane_files "${ISSUES_DIR}/02-in-progress")
  active_pids="$(codex_active_pids)"
  latest_json="$(latest_file "${STATE_DIR}/run-*.jsonl")"

  if [[ -n "${active_pids}" ]]; then
    age=999999
    if [[ -n "${latest_json}" ]]; then
      age=$(file_age_seconds "${latest_json}")
    fi
    if (( age > STALE_BUILDER_SECONDS )); then
      log "builder appears stuck (latest log age=${age}s); recovering"
      kill_pids "builder" "${active_pids}"
      start_codex_with_retries || true
    else
      log "builder healthy (active; latest log age=${age}s)"
    fi
    return
  fi

  if (( ready_count > 0 || in_progress_count > 0 )); then
    log "builder idle with pending work (ready=${ready_count}, in-progress=${in_progress_count}); starting runner"
    start_codex_with_retries || true
  else
    log "builder idle; no pending build work"
  fi
}

heal_reviewer() {
  local needs_review_count in_review_count active_pids latest_review_log age blocker_pids runtime
  needs_review_count=$(count_lane_files "${ISSUES_DIR}/03-needs-review")
  in_review_count=$(count_lane_files "${ISSUES_DIR}/04-in-review")
  active_pids="$(reviewer_active_pids)"
  blocker_pids="$(reviewer_blocker_pids)"
  latest_review_log="$(latest_file "${STATE_DIR}/review-*.log")"

  if [[ -n "${active_pids}" ]]; then
    if [[ -n "${blocker_pids}" ]]; then
      log "reviewer blocker command detected (gh interactive workflow); recovering reviewer"
      append_note "reviewer watchdog detected gh interactive command and restarted reviewer to prevent queue stall"
      kill_pids "reviewer-blocker" "${blocker_pids}"
      kill_pids "reviewer" "${active_pids}"
      start_reviewer_with_retries || true
      return
    fi

    age=999999
    if [[ -n "${latest_review_log}" ]]; then
      age=$(file_age_seconds "${latest_review_log}")
    fi
    runtime=$(max_pid_elapsed_seconds "${active_pids}")
    if (( runtime > REVIEWER_MAX_RUNTIME_SECONDS )) || { (( runtime == 0 )) && (( age > STALE_REVIEWER_SECONDS )); }; then
      log "reviewer appears stuck (runtime=${runtime}s, latest log age=${age}s); recovering"
      kill_pids "reviewer" "${active_pids}"
      start_reviewer_with_retries || true
    else
      log "reviewer healthy (active; runtime=${runtime}s, latest log age=${age}s)"
    fi
    return
  fi

  if (( needs_review_count > 0 || in_review_count > 0 )); then
    log "reviewer idle with pending review work (needs-review=${needs_review_count}, in-review=${in_review_count}); starting runner"
    start_reviewer_with_retries || true
  else
    log "reviewer idle; no pending review work"
  fi
}

main() {
  if [[ ! -d "${ISSUES_DIR}" ]]; then
    log "issues directory missing: ${ISSUES_DIR}"
    exit 1
  fi
  if [[ ! -x "${GITHUB_RETRY_HELPER}" ]]; then
    log "github retry helper missing or not executable: ${GITHUB_RETRY_HELPER}"
  fi

  log "run started (dry_run=${DRY_RUN}, base_branch=${BASE_BRANCH}, enforce_dep_graph=${ENFORCE_DEP_GRAPH}, stale_builder=${STALE_BUILDER_SECONDS}s, stale_reviewer=${STALE_REVIEWER_SECONDS}s, stale_in_review=${STALE_IN_REVIEW_SECONDS}s, stale_in_progress=${STALE_IN_PROGRESS_SECONDS}s, stale_needs_review=${STALE_NEEDS_REVIEW_SECONDS}s, lock_stale=${LOCK_STALE_SECONDS}s)"

  run_watchdog_stage enforce_completed_dependencies
  run_watchdog_stage enforce_in_progress_dependencies
  run_watchdog_stage validate_run_jsonl_terminal_state
  run_watchdog_stage requeue_stale_in_review
  run_watchdog_stage requeue_stale_in_progress
  run_watchdog_stage flag_stale_needs_review
  run_watchdog_stage heal_builder
  run_watchdog_stage heal_reviewer

  log "run complete"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  if ! acquire_lock; then
    echo "supervisor watchdog already running" >&2
    exit 0
  fi

  trap release_lock EXIT INT TERM

  main
fi
