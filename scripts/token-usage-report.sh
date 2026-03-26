#!/usr/bin/env bash
set -euo pipefail

window_hours=24
limit_rows=15
org_id=""

usage() {
  cat <<'EOF'
Usage: scripts/token-usage-report.sh [--hours N] [--limit N] [--org UUID]

Reports recent model-invocation usage directly from PostgreSQL, including
cache-read tokens in all totals.

Environment:
  OTTERCAMP_DATABASE_URL   Preferred PostgreSQL connection string
  DATABASE_URL             Legacy fallback if OTTERCAMP_DATABASE_URL is unset
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --hours)
      window_hours="${2:?missing value for --hours}"
      shift 2
      ;;
    --limit)
      limit_rows="${2:?missing value for --limit}"
      shift 2
      ;;
    --org)
      org_id="${2:?missing value for --org}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

database_url="${OTTERCAMP_DATABASE_URL:-${DATABASE_URL:-}}"
if [[ -z "${database_url}" ]]; then
  echo "token usage report requires OTTERCAMP_DATABASE_URL or DATABASE_URL" >&2
  exit 1
fi

if ! command -v psql >/dev/null 2>&1; then
  echo "token usage report requires psql" >&2
  exit 1
fi

psql \
  "${database_url}" \
  -X \
  -v ON_ERROR_STOP=1 \
  -v window_hours="${window_hours}" \
  -v limit_rows="${limit_rows}" \
  -v org_id="${org_id}" <<'SQL'
\pset border 2
\pset linestyle unicode
\pset null '∅'
\timing off

\echo
\echo '== Token Usage Overview =='
WITH params AS (
  SELECT
    now() - make_interval(hours => :'window_hours'::int) AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id
),
scoped AS (
  SELECT mi.*
  FROM model_invocation mi
  CROSS JOIN params p
  WHERE mi.created_at >= p.since_at
    AND (p.org_id IS NULL OR mi.organization_id = p.org_id)
)
SELECT
  COUNT(*) AS invocations,
  COUNT(*) FILTER (WHERE status = 'completed') AS completed,
  COUNT(*) FILTER (WHERE status = 'failed') AS failed,
  COUNT(*) FILTER (WHERE status = 'in_flight') AS in_flight,
  COUNT(*) FILTER (WHERE status = 'pending') AS pending,
  COUNT(*) FILTER (
    WHERE status = 'failed'
      AND (
        lower(COALESCE(error_code, '')) LIKE '%rate%'
        OR lower(COALESCE(error_message, '')) LIKE '%rate limit%'
        OR lower(COALESCE(error_message, '')) LIKE '%429%'
      )
  ) AS rate_limited_failures,
  COALESCE(SUM(COALESCE(input_tokens, 0)), 0) AS input_tokens,
  COALESCE(SUM(COALESCE(output_tokens, 0)), 0) AS output_tokens,
  COALESCE(SUM(COALESCE(cache_read_tokens, 0)), 0) AS cache_read_tokens,
  COALESCE(SUM(COALESCE(input_tokens, 0) + COALESCE(output_tokens, 0) + COALESCE(cache_read_tokens, 0)), 0) AS total_tokens
FROM scoped;

\echo
\echo '== Usage by Purpose / Model / Connection =='
WITH params AS (
  SELECT
    now() - make_interval(hours => :'window_hours'::int) AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id
)
SELECT
  mi.invocation_purpose,
  mi.model_name,
  COALESCE(pc.display_name, '∅') AS provider_connection,
  COUNT(*) AS invocations,
  COUNT(*) FILTER (WHERE mi.status = 'failed') AS failed,
  COALESCE(SUM(COALESCE(mi.input_tokens, 0)), 0) AS input_tokens,
  COALESCE(SUM(COALESCE(mi.output_tokens, 0)), 0) AS output_tokens,
  COALESCE(SUM(COALESCE(mi.cache_read_tokens, 0)), 0) AS cache_read_tokens,
  COALESCE(SUM(COALESCE(mi.input_tokens, 0) + COALESCE(mi.output_tokens, 0) + COALESCE(mi.cache_read_tokens, 0)), 0) AS total_tokens
FROM model_invocation mi
LEFT JOIN provider_connection pc ON pc.id = mi.provider_connection_id
CROSS JOIN params p
WHERE mi.created_at >= p.since_at
  AND (p.org_id IS NULL OR mi.organization_id = p.org_id)
GROUP BY mi.invocation_purpose, mi.model_name, COALESCE(pc.display_name, '∅')
ORDER BY total_tokens DESC, invocations DESC
LIMIT :'limit_rows'::int;

\echo
\echo '== Top Sessions =='
WITH params AS (
  SELECT
    now() - make_interval(hours => :'window_hours'::int) AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id
)
SELECT
  mi.session_id,
  COALESCE(cs.scope_type, '∅') AS scope_type,
  COALESCE(cs.mode, '∅') AS mode,
  COUNT(*) AS invocations,
  COUNT(*) FILTER (WHERE mi.status = 'failed') AS failed,
  COALESCE(SUM(COALESCE(mi.input_tokens, 0)), 0) AS input_tokens,
  COALESCE(SUM(COALESCE(mi.output_tokens, 0)), 0) AS output_tokens,
  COALESCE(SUM(COALESCE(mi.cache_read_tokens, 0)), 0) AS cache_read_tokens,
  COALESCE(SUM(COALESCE(mi.input_tokens, 0) + COALESCE(mi.output_tokens, 0) + COALESCE(mi.cache_read_tokens, 0)), 0) AS total_tokens,
  MIN(mi.created_at) AS first_seen,
  MAX(mi.created_at) AS last_seen
FROM model_invocation mi
LEFT JOIN chat_session cs ON cs.id = mi.session_id
CROSS JOIN params p
WHERE mi.created_at >= p.since_at
  AND mi.session_id IS NOT NULL
  AND (p.org_id IS NULL OR mi.organization_id = p.org_id)
GROUP BY mi.session_id, COALESCE(cs.scope_type, '∅'), COALESCE(cs.mode, '∅')
ORDER BY total_tokens DESC, invocations DESC
LIMIT :'limit_rows'::int;

\echo
\echo '== Top Turns =='
WITH params AS (
  SELECT
    now() - make_interval(hours => :'window_hours'::int) AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id
)
SELECT
  mi.turn_id,
  mi.session_id,
  COUNT(*) AS invocations,
  COUNT(DISTINCT mi.invocation_purpose) AS purposes,
  COALESCE(SUM(COALESCE(mi.input_tokens, 0)), 0) AS input_tokens,
  COALESCE(SUM(COALESCE(mi.output_tokens, 0)), 0) AS output_tokens,
  COALESCE(SUM(COALESCE(mi.cache_read_tokens, 0)), 0) AS cache_read_tokens,
  COALESCE(SUM(COALESCE(mi.input_tokens, 0) + COALESCE(mi.output_tokens, 0) + COALESCE(mi.cache_read_tokens, 0)), 0) AS total_tokens,
  MIN(mi.created_at) AS first_seen,
  MAX(mi.created_at) AS last_seen
FROM model_invocation mi
CROSS JOIN params p
WHERE mi.created_at >= p.since_at
  AND mi.turn_id IS NOT NULL
  AND (p.org_id IS NULL OR mi.organization_id = p.org_id)
GROUP BY mi.turn_id, mi.session_id
ORDER BY total_tokens DESC, invocations DESC
LIMIT :'limit_rows'::int;

\echo
\echo '== Hot Rate-Limit Turns =='
WITH params AS (
  SELECT
    now() - make_interval(hours => :'window_hours'::int) AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id
),
rate_limited AS (
  SELECT
    mi.turn_id,
    mi.session_id,
    COUNT(*) AS failed_invocations,
    MAX(mi.created_at) AS last_seen
  FROM model_invocation mi
  CROSS JOIN params p
  WHERE mi.created_at >= p.since_at
    AND mi.turn_id IS NOT NULL
    AND mi.status = 'failed'
    AND (
      lower(COALESCE(mi.error_code, '')) LIKE '%rate%'
      OR lower(COALESCE(mi.error_message, '')) LIKE '%rate limit%'
      OR lower(COALESCE(mi.error_message, '')) LIKE '%429%'
    )
    AND (p.org_id IS NULL OR mi.organization_id = p.org_id)
  GROUP BY mi.turn_id, mi.session_id
)
SELECT
  turn_id,
  session_id,
  failed_invocations,
  last_seen
FROM rate_limited
WHERE failed_invocations > 1
ORDER BY failed_invocations DESC, last_seen DESC
LIMIT :'limit_rows'::int;

\echo
\echo '== Duplicate Successful File Writes By Turn =='
WITH params AS (
  SELECT
    now() - make_interval(hours => :'window_hours'::int) AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id
),
tool_results AS (
  SELECT
    cm.turn_id,
    cm.session_id,
    cm.created_at,
    cm.content::jsonb AS payload
  FROM chat_message cm
  JOIN chat_session cs ON cs.id = cm.session_id
  CROSS JOIN params p
  WHERE cm.created_at >= p.since_at
    AND cm.turn_id IS NOT NULL
    AND cm.role = 'tool_result'
    AND cm.status = 'final'
    AND cm.content_format = 'text'
    AND (p.org_id IS NULL OR cs.organization_id = p.org_id)
),
successful_writes AS (
  SELECT
    turn_id,
    session_id,
    payload->'output'->>'path' AS path,
    COALESCE((payload->'output'->>'byte_size')::int, 0) AS byte_size,
    COALESCE((payload->'output'->>'created')::boolean, false) AS created,
    COUNT(*) AS write_count,
    MIN(created_at) AS first_seen,
    MAX(created_at) AS last_seen
  FROM tool_results
  WHERE COALESCE(payload->>'tool_name', '') = 'file.write'
    AND COALESCE(payload->>'error', '') = ''
    AND COALESCE(payload->'output'->>'path', '') <> ''
    AND COALESCE((payload->'output'->>'byte_size')::int, 0) > 0
  GROUP BY turn_id, session_id, payload->'output'->>'path', COALESCE((payload->'output'->>'byte_size')::int, 0), COALESCE((payload->'output'->>'created')::boolean, false)
)
SELECT
  turn_id,
  session_id,
  path,
  byte_size,
  created,
  write_count,
  first_seen,
  last_seen
FROM successful_writes
WHERE write_count > 1
ORDER BY write_count DESC, last_seen DESC
LIMIT :'limit_rows'::int;

\echo
\echo '== Most Common Failures =='
WITH params AS (
  SELECT
    now() - make_interval(hours => :'window_hours'::int) AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id
)
SELECT
  COALESCE(NULLIF(error_code, ''), '∅') AS error_code,
  COUNT(*) AS failures,
  COUNT(*) FILTER (
    WHERE lower(COALESCE(error_message, '')) LIKE '%rate limit%'
       OR lower(COALESCE(error_message, '')) LIKE '%429%'
  ) AS explicit_rate_limit_failures,
  MAX(created_at) AS last_seen
FROM model_invocation mi
CROSS JOIN params p
WHERE mi.created_at >= p.since_at
  AND mi.status = 'failed'
  AND (p.org_id IS NULL OR mi.organization_id = p.org_id)
GROUP BY COALESCE(NULLIF(error_code, ''), '∅')
ORDER BY failures DESC, error_code
LIMIT :'limit_rows'::int;
SQL
