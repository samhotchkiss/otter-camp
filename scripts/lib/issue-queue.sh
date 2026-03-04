#!/usr/bin/env bash

# Queue lane helper functions for atomic claims and idempotent moves.
# Status vocabulary is intentionally small for deterministic logging:
# claimed | already_claimed | already_completed | missing

queue_known_lanes() {
  printf '%s\n' "01-ready" "02-in-progress" "03-needs-review" "04-in-review" "05-completed"
}

queue_task_basename() {
  local task_path="$1"
  basename "$task_path"
}

queue_find_lane() {
  local issues_dir="$1"
  local task_basename="$2"
  local lane
  while IFS= read -r lane; do
    if [[ -e "${issues_dir}/${lane}/${task_basename}" ]]; then
      printf '%s\n' "${lane}"
      return 0
    fi
  done < <(queue_known_lanes)
  return 1
}

queue_classify_lane() {
  local lane="$1"
  local dest_lane="${2:-}"

  # If already in destination lane, classify by destination intent first.
  if [[ -n "${dest_lane}" && "${lane}" == "${dest_lane}" ]]; then
    case "${dest_lane}" in
      02-in-progress)
        printf 'already_claimed\n'
        return 0
        ;;
      03-needs-review|04-in-review|05-completed)
        printf 'already_completed\n'
        return 0
        ;;
    esac
  fi

  case "${lane}" in
    02-in-progress)
      printf 'already_claimed\n'
      ;;
    03-needs-review|04-in-review|05-completed)
      printf 'already_completed\n'
      ;;
    *)
      printf 'missing\n'
      ;;
  esac
}

queue_with_task_lock() {
  local issues_dir="$1"
  local task_basename="$2"
  local callback="$3"
  shift 3

  local lock_root="${issues_dir}/.queue-locks"
  local lock_dir="${lock_root}/${task_basename}.lock"
  local attempt

  mkdir -p "${lock_root}"

  for attempt in $(seq 1 80); do
    if mkdir "${lock_dir}" 2>/dev/null; then
      "${callback}" "${issues_dir}" "${task_basename}" "$@"
      local rc=$?
      rmdir "${lock_dir}" 2>/dev/null || true
      return "${rc}"
    fi
    sleep 0.05
  done

  # Could not get lock within wait budget; classify from observed lane.
  local lane
  lane="$(queue_find_lane "${issues_dir}" "${task_basename}" || true)"
  if [[ -n "${lane}" ]]; then
    queue_classify_lane "${lane}" "$@"
  else
    printf 'missing\n'
  fi
}

_queue_claim_impl() {
  local issues_dir="$1"
  local task_basename="$2"

  local src="${issues_dir}/01-ready/${task_basename}"
  local dst="${issues_dir}/02-in-progress/${task_basename}"

  if [[ -e "${dst}" ]]; then
    printf 'already_claimed\n'
    return 0
  fi

  if [[ -e "${src}" ]]; then
    if mv "${src}" "${dst}" 2>/dev/null; then
      printf 'claimed\n'
      return 0
    fi
  fi

  local lane
  lane="$(queue_find_lane "${issues_dir}" "${task_basename}" || true)"
  if [[ -n "${lane}" ]]; then
    queue_classify_lane "${lane}" "02-in-progress"
  else
    printf 'missing\n'
  fi
}

queue_claim_task() {
  local issues_dir="$1"
  local task_basename="$2"
  queue_with_task_lock "${issues_dir}" "${task_basename}" _queue_claim_impl
}

_queue_move_impl() {
  local issues_dir="$1"
  local task_basename="$2"
  local src_lane="$3"
  local dst_lane="$4"

  local src="${issues_dir}/${src_lane}/${task_basename}"
  local dst="${issues_dir}/${dst_lane}/${task_basename}"

  if [[ -e "${src}" ]]; then
    if [[ -e "${dst}" ]]; then
      queue_classify_lane "${dst_lane}" "${dst_lane}"
      return 0
    fi
    if mv "${src}" "${dst}" 2>/dev/null; then
      printf 'claimed\n'
      return 0
    fi
  fi

  local lane
  lane="$(queue_find_lane "${issues_dir}" "${task_basename}" || true)"
  if [[ -n "${lane}" ]]; then
    queue_classify_lane "${lane}" "${dst_lane}"
  else
    printf 'missing\n'
  fi
}

queue_move_task() {
  local issues_dir="$1"
  local src_lane="$2"
  local dst_lane="$3"
  local task_basename="$4"

  queue_with_task_lock "${issues_dir}" "${task_basename}" _queue_move_impl "${src_lane}" "${dst_lane}"
}
