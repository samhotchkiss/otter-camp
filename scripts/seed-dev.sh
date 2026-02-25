#!/usr/bin/env bash
set -euo pipefail

OTTERCAMP_URL="${OTTERCAMP_URL:-http://localhost:4110}"
ADMIN_EMAIL="${ADMIN_EMAIL:-admin@localhost}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-test-bootstrap-password}"
OTTERCAMP_BIN="${OTTERCAMP_BIN:-./ottercamp}"

echo "Seeding development data for ${OTTERCAMP_URL}..."

if [ ! -x "${OTTERCAMP_BIN}" ]; then
  echo "ERROR: ottercamp binary not found at ${OTTERCAMP_BIN}" >&2
  echo "Build it first with: go build -o ./ottercamp ./cmd/ottercamp" >&2
  exit 1
fi

"${OTTERCAMP_BIN}" --server-url "${OTTERCAMP_URL}" bootstrap

echo "Bootstrap complete."

if ! command -v curl >/dev/null 2>&1; then
  echo "ERROR: curl is required" >&2
  exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "ERROR: jq is required" >&2
  exit 1
fi

TOKEN="$(curl -sf -X POST "${OTTERCAMP_URL}/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"${ADMIN_EMAIL}\",\"password\":\"${ADMIN_PASSWORD}\"}" \
  | jq -r '.data.token')"

if [ -z "${TOKEN}" ] || [ "${TOKEN}" = "null" ]; then
  echo "ERROR: failed to obtain auth token" >&2
  exit 1
fi

echo "Logged in as ${ADMIN_EMAIL}."

if [ -z "${ANTHROPIC_API_KEY_SECRET_REF:-}" ]; then
  if [ -n "${ANTHROPIC_API_KEY:-}" ]; then
    echo "ANTHROPIC_API_KEY is set, but provider API expects api_key_secret_ref."
    echo "Set ANTHROPIC_API_KEY_SECRET_REF to an existing secret slug and rerun."
  else
    echo "ANTHROPIC_API_KEY_SECRET_REF not set; skipping model provider seed."
  fi
  echo "Development seed complete."
  exit 0
fi

PROVIDER_ID="$(curl -sf "${OTTERCAMP_URL}/v1/model/providers" \
  -H "Authorization: Bearer ${TOKEN}" \
  | jq -r '.data[] | select(.slug == "anthropic") | .id' | head -n 1)"

if [ -z "${PROVIDER_ID}" ] || [ "${PROVIDER_ID}" = "null" ]; then
  echo "Anthropic provider not found; skipping provider connection seed."
  echo "Development seed complete."
  exit 0
fi

if ! curl -sf -X POST "${OTTERCAMP_URL}/v1/model/providers/${PROVIDER_ID}/connections" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d "{\"display_name\":\"Anthropic Dev\",\"api_key_secret_ref\":\"${ANTHROPIC_API_KEY_SECRET_REF}\"}" >/dev/null; then
  echo "Provider connection seed failed; continuing."
  echo "Set ANTHROPIC_API_KEY_SECRET_REF to a valid secret slug and rerun if needed."
  echo "Development seed complete."
  exit 0
fi

echo "Anthropic provider connection registered."
echo "Development seed complete."
