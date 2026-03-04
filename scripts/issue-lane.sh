#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/lib/issue-queue.sh
source "${SCRIPT_DIR}/lib/issue-queue.sh"

usage() {
  cat <<USAGE
Usage:
  $(basename "$0") claim <issues_dir> <task-file>
  $(basename "$0") move <issues_dir> <src-lane> <dst-lane> <task-file>

Outputs exactly one status token:
  claimed | already_claimed | already_completed | missing
USAGE
}

if (( $# < 1 )); then
  usage
  exit 2
fi

cmd="$1"
shift

case "${cmd}" in
  claim)
    if (( $# != 2 )); then
      usage
      exit 2
    fi
    issues_dir="$1"
    task_basename="$(queue_task_basename "$2")"
    queue_claim_task "${issues_dir}" "${task_basename}"
    ;;
  move)
    if (( $# != 4 )); then
      usage
      exit 2
    fi
    issues_dir="$1"
    src_lane="$2"
    dst_lane="$3"
    task_basename="$(queue_task_basename "$4")"
    queue_move_task "${issues_dir}" "${src_lane}" "${dst_lane}" "${task_basename}"
    ;;
  *)
    usage
    exit 2
    ;;
esac
