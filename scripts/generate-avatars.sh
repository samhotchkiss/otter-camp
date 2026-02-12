#!/usr/bin/env bash
# Generate avatars for all agents using Nano Banana Pro (Gemini image gen)
# Usage: ./scripts/generate-avatars.sh [--threads N] [--force] [--dry-run] [--retries N]
#
# --threads N   Number of parallel workers (default: 4)
# --force       Regenerate even if avatar.png already exists
# --dry-run     Show what would be generated without doing it
# --retries N   Max retries per agent on rate limit (default: 3)
#
# Rate limit: Gemini allows ~20 req/min. Default 4 threads with staggered
# starts keeps us under the limit with headroom for retries.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
AGENTS_DIR="$(cd "$SCRIPT_DIR/../data/agents" && pwd)"
GENERATE_SCRIPT="/Users/sam/.npm-global/lib/node_modules/openclaw/skills/nano-banana-pro/scripts/generate_image.py"
GEMINI_API_KEY="${GEMINI_API_KEY:-AIzaSyBzM9km4mqu1yGu3OO1vKomp0TS_ogq2Bw}"
export GEMINI_API_KEY

THREADS=4
FORCE=false
DRY_RUN=false
MAX_RETRIES=3

while [[ $# -gt 0 ]]; do
  case $1 in
    --threads) THREADS="$2"; shift 2 ;;
    --force) FORCE=true; shift ;;
    --dry-run) DRY_RUN=true; shift ;;
    --retries) MAX_RETRIES="$2"; shift 2 ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

# Build work queue
QUEUE_FILE=$(mktemp)
TOTAL=0
SKIPPED=0

for agent_dir in "$AGENTS_DIR"/*/; do
  role_id=$(basename "$agent_dir")
  [[ "$role_id" == _* ]] && continue

  prompt_file="$agent_dir/avatar-prompt.txt"
  avatar_file="$agent_dir/avatar.png"

  if [[ ! -f "$prompt_file" ]]; then
    continue
  fi

  if [[ -f "$avatar_file" ]] && [[ "$FORCE" != "true" ]]; then
    ((SKIPPED++)) || true
    continue
  fi

  echo "$role_id" >> "$QUEUE_FILE"
  ((TOTAL++)) || true
done

echo "=== Avatar Generation ==="
echo "Total to generate: $TOTAL"
echo "Skipped (already exist): $SKIPPED"
echo "Threads: $THREADS"
echo "Max retries: $MAX_RETRIES"
echo ""

if [[ "$DRY_RUN" == "true" ]]; then
  echo "Dry run — would generate:"
  while read -r role_id; do
    echo "  $role_id"
  done < "$QUEUE_FILE"
  rm -f "$QUEUE_FILE"
  exit 0
fi

if [[ "$TOTAL" -eq 0 ]]; then
  echo "Nothing to generate!"
  rm -f "$QUEUE_FILE"
  exit 0
fi

# Progress tracking
PROGRESS_DIR=$(mktemp -d)
DONE_FILE="$PROGRESS_DIR/done"
FAIL_FILE="$PROGRESS_DIR/fail"
touch "$DONE_FILE" "$FAIL_FILE"
LOG_DIR="$PROGRESS_DIR/logs"
mkdir -p "$LOG_DIR"

generate_one() {
  local role_id="$1"
  local agent_dir="$AGENTS_DIR/$role_id"
  local prompt_file="$agent_dir/avatar-prompt.txt"
  local avatar_file="$agent_dir/avatar.png"
  local log_file="$LOG_DIR/$role_id.log"

  local prompt
  prompt=$(cat "$prompt_file")

  local attempt=0
  local success=false

  while [[ $attempt -lt $MAX_RETRIES ]]; do
    ((attempt++)) || true

    if uv run "$GENERATE_SCRIPT" \
      --prompt "$prompt" \
      --filename "$avatar_file" \
      --resolution 1K \
      >> "$log_file" 2>&1; then

      if [[ -f "$avatar_file" ]]; then
        success=true
        break
      fi
    fi

    # Check if rate limited
    if grep -q "429\|RESOURCE_EXHAUSTED\|rate" "$log_file" 2>/dev/null; then
      local wait=$((15 + RANDOM % 15))
      echo "⏳ [$role_id] Rate limited, waiting ${wait}s (attempt $attempt/$MAX_RETRIES)"
      sleep "$wait"
    else
      # Non-rate-limit error, still retry but shorter wait
      sleep 5
    fi
  done

  if [[ "$success" == "true" ]]; then
    local done_count
    done_count=$(wc -l < "$DONE_FILE" 2>/dev/null | tr -d ' ')
    echo "$role_id" >> "$DONE_FILE"
    echo "✅ [$((done_count + 1))/$TOTAL] $role_id"
  else
    echo "$role_id" >> "$FAIL_FILE"
    echo "❌ [FAIL] $role_id (after $MAX_RETRIES attempts) — see $log_file"
  fi
}

export -f generate_one
export AGENTS_DIR GENERATE_SCRIPT GEMINI_API_KEY TOTAL MAX_RETRIES DONE_FILE FAIL_FILE LOG_DIR

# Stagger thread starts to avoid initial burst
echo "Starting $THREADS workers with staggered launch..."

if command -v parallel &>/dev/null; then
  cat "$QUEUE_FILE" | parallel --delay 3 -j "$THREADS" generate_one {}
else
  cat "$QUEUE_FILE" | xargs -P "$THREADS" -I {} bash -c 'generate_one "$@"' _ {}
fi

FINAL_DONE=$(wc -l < "$DONE_FILE" 2>/dev/null | tr -d ' ')
FINAL_FAIL=$(wc -l < "$FAIL_FILE" 2>/dev/null | tr -d ' ')

echo ""
echo "=== Complete ==="
echo "Generated: $FINAL_DONE"
echo "Failed: $FINAL_FAIL"
echo "Skipped: $SKIPPED"

if [[ "$FINAL_FAIL" -gt 0 ]]; then
  echo ""
  echo "Failed agents:"
  cat "$FAIL_FILE"
  echo ""
  echo "Logs: $LOG_DIR"
  echo ""
  echo "Re-run without --force to retry only failed ones."
fi

rm -f "$QUEUE_FILE"
[[ "$FINAL_FAIL" -eq 0 ]] && rm -rf "$PROGRESS_DIR"

exit 0
