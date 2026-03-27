#!/usr/bin/env bash
set -euo pipefail

window_hours=24
limit_rows=15
org_id=""
session_id=""

usage() {
  cat <<'EOF'
Usage: scripts/token-usage-report.sh [--hours N] [--limit N] [--org UUID] [--session UUID]

Reports recent model-invocation usage directly from PostgreSQL, including
cache-read tokens in all totals.

Notes:
  --hours accepts fractional values such as 0.25 for 15 minutes

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
    --session)
      session_id="${2:?missing value for --session}"
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
  -v org_id="${org_id}" \
  -v session_id="${session_id}" <<'SQL'
\pset border 2
\pset linestyle unicode
\pset null '∅'
\timing off

\echo
\echo '== Token Usage Overview =='
WITH params AS (
  SELECT
    now() - (:'window_hours'::numeric * interval '1 hour') AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id,
    NULLIF(:'session_id', '')::uuid AS session_id
),
scoped AS (
  SELECT mi.*
  FROM model_invocation mi
  CROSS JOIN params p
  WHERE mi.created_at >= p.since_at
    AND (p.org_id IS NULL OR mi.organization_id = p.org_id)
    AND (p.session_id IS NULL OR mi.session_id = p.session_id)
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
    now() - (:'window_hours'::numeric * interval '1 hour') AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id,
    NULLIF(:'session_id', '')::uuid AS session_id
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
  AND (p.session_id IS NULL OR mi.session_id = p.session_id)
GROUP BY mi.invocation_purpose, mi.model_name, COALESCE(pc.display_name, '∅')
ORDER BY total_tokens DESC, invocations DESC
LIMIT :'limit_rows'::int;

\echo
\echo '== Listening Eval By Scope =='
WITH params AS (
  SELECT
    now() - (:'window_hours'::numeric * interval '1 hour') AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id,
    NULLIF(:'session_id', '')::uuid AS session_id
)
SELECT
  COALESCE(cs.scope_type, '∅') AS scope_type,
  COALESCE(cs.mode, '∅') AS mode,
  mi.model_name,
  COUNT(*) AS invocations,
  COUNT(*) FILTER (WHERE mi.status = 'failed') AS failed,
  COALESCE(SUM(COALESCE(mi.input_tokens, 0)), 0) AS input_tokens,
  COALESCE(SUM(COALESCE(mi.output_tokens, 0)), 0) AS output_tokens,
  COALESCE(SUM(COALESCE(mi.cache_read_tokens, 0)), 0) AS cache_read_tokens,
  COALESCE(SUM(COALESCE(mi.input_tokens, 0) + COALESCE(mi.output_tokens, 0) + COALESCE(mi.cache_read_tokens, 0)), 0) AS total_tokens
FROM model_invocation mi
LEFT JOIN chat_session cs ON cs.id = mi.session_id
CROSS JOIN params p
WHERE mi.created_at >= p.since_at
  AND mi.invocation_purpose = 'listening_eval'
  AND (p.org_id IS NULL OR mi.organization_id = p.org_id)
  AND (p.session_id IS NULL OR mi.session_id = p.session_id)
GROUP BY COALESCE(cs.scope_type, '∅'), COALESCE(cs.mode, '∅'), mi.model_name
ORDER BY total_tokens DESC, invocations DESC
LIMIT :'limit_rows'::int;

\echo
\echo '== Top Sessions =='
WITH params AS (
  SELECT
    now() - (:'window_hours'::numeric * interval '1 hour') AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id,
    NULLIF(:'session_id', '')::uuid AS session_id
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
  AND (p.session_id IS NULL OR mi.session_id = p.session_id)
GROUP BY mi.session_id, COALESCE(cs.scope_type, '∅'), COALESCE(cs.mode, '∅')
ORDER BY total_tokens DESC, invocations DESC
LIMIT :'limit_rows'::int;

\echo
\echo '== Top Turns =='
WITH params AS (
  SELECT
    now() - (:'window_hours'::numeric * interval '1 hour') AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id,
    NULLIF(:'session_id', '')::uuid AS session_id
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
  AND (p.session_id IS NULL OR mi.session_id = p.session_id)
GROUP BY mi.turn_id, mi.session_id
ORDER BY total_tokens DESC, invocations DESC
LIMIT :'limit_rows'::int;

\echo
\echo '== Hot Rate-Limit Turns =='
WITH params AS (
  SELECT
    now() - (:'window_hours'::numeric * interval '1 hour') AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id,
    NULLIF(:'session_id', '')::uuid AS session_id
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
    AND (p.session_id IS NULL OR mi.session_id = p.session_id)
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
    now() - (:'window_hours'::numeric * interval '1 hour') AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id,
    NULLIF(:'session_id', '')::uuid AS session_id
),
tool_results AS (
  SELECT
    cm.turn_id,
    cm.session_id,
    cm.created_at,
    cm.content::jsonb AS payload,
    NULLIF(
      COALESCE(
        cm.content::jsonb->>'error',
        cm.content::jsonb->'output'->>'error',
        ''
      ),
      ''
    ) AS tool_error
  FROM chat_message cm
  JOIN chat_session cs ON cs.id = cm.session_id
  CROSS JOIN params p
  WHERE cm.created_at >= p.since_at
    AND cm.turn_id IS NOT NULL
    AND cm.role = 'tool_result'
    AND cm.status = 'final'
    AND cm.content_format = 'text'
    AND (p.org_id IS NULL OR cs.organization_id = p.org_id)
    AND (p.session_id IS NULL OR cm.session_id = p.session_id)
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
    AND tool_error IS NULL
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
\echo '== Repeated Package Install Attempts By Turn =='
WITH params AS (
  SELECT
    now() - (:'window_hours'::numeric * interval '1 hour') AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id,
    NULLIF(:'session_id', '')::uuid AS session_id
),
assistant_calls AS (
  SELECT
    cm.turn_id,
    cm.session_id,
    cm.created_at,
    lower(COALESCE(call->'arguments'->>'command', '')) AS command
  FROM chat_message cm
  JOIN chat_session cs ON cs.id = cm.session_id
  CROSS JOIN params p
  CROSS JOIN LATERAL jsonb_array_elements(COALESCE(cm.metadata::jsonb->'tool_calls', '[]'::jsonb)) AS call
  WHERE cm.created_at >= p.since_at
    AND cm.turn_id IS NOT NULL
    AND cm.role = 'assistant'
    AND cm.status = 'final'
    AND (p.org_id IS NULL OR cs.organization_id = p.org_id)
    AND (p.session_id IS NULL OR cm.session_id = p.session_id)
),
package_attempts AS (
  SELECT
    turn_id,
    session_id,
    trim(both ',' FROM string_agg(token, ',' ORDER BY ord)) AS install_tail,
    created_at
  FROM (
    SELECT
      ac.turn_id,
      ac.session_id,
      ac.created_at,
      tok.ord,
      lower(tok.token) AS token,
      lag(lower(tok.token)) OVER (
        PARTITION BY ac.turn_id, ac.session_id, ac.created_at
        ORDER BY tok.ord
      ) AS prev_token
    FROM assistant_calls ac
    CROSS JOIN LATERAL (
      SELECT
        trim(
          regexp_replace(
            regexp_replace(
              ac.command,
              E'[\\r\\n].*$',
              '',
              'n'
            ),
            '\s*(\|\||&&|\||;|2>&1|1>&2|2>|1>|>>|>|<).*$',
            '',
            'i'
          )
        ) AS install_command
    ) trimmed
    CROSS JOIN LATERAL regexp_split_to_table(trimmed.install_command, '[[:space:]]+') WITH ORDINALITY AS tok(token, ord)
    WHERE (
        trimmed.install_command ~ '^[[:space:]]*(pip|pip3)[[:space:]]+install([[:space:]]|$)'
        AND tok.ord >= 3
      ) OR (
        trimmed.install_command ~ '^[[:space:]]*[^[:space:]]+[[:space:]]+-m[[:space:]]+pip[[:space:]]+install([[:space:]]|$)'
        AND tok.ord >= 5
      )
  ) package_tokens
  WHERE token <> ''
    AND token !~ '^(\\|\\||&&|\\||;)$'
    AND token !~ '^(2>&1|1>&2|2>|1>|>>|>|<)$'
    AND token NOT LIKE '-%'
    AND COALESCE(prev_token, '') NOT IN (
      '-r', '--requirement',
      '-c', '--constraint',
      '-i', '--index-url',
      '--extra-index-url',
      '--find-links',
      '-t', '--target'
    )
  GROUP BY turn_id, session_id, created_at
)
SELECT
  turn_id,
  session_id,
  COUNT(*) AS install_attempts,
  string_agg(DISTINCT left(install_tail, 120), ' | ') AS attempted_specs,
  MIN(created_at) AS first_seen,
  MAX(created_at) AS last_seen
FROM package_attempts
GROUP BY turn_id, session_id
HAVING COUNT(*) > 1
ORDER BY install_attempts DESC, last_seen DESC
LIMIT :'limit_rows'::int;

\echo
\echo '== Written File Readback Churn By Turn =='
WITH params AS (
  SELECT
    now() - (:'window_hours'::numeric * interval '1 hour') AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id,
    NULLIF(:'session_id', '')::uuid AS session_id
),
tool_results AS (
  SELECT
    cm.turn_id,
    cm.session_id,
    cm.created_at,
    cm.content::jsonb AS payload,
    NULLIF(
      COALESCE(
        cm.content::jsonb->>'error',
        cm.content::jsonb->'output'->>'error',
        ''
      ),
      ''
    ) AS tool_error
  FROM chat_message cm
  JOIN chat_session cs ON cs.id = cm.session_id
  CROSS JOIN params p
  WHERE cm.created_at >= p.since_at
    AND cm.turn_id IS NOT NULL
    AND cm.role = 'tool_result'
    AND cm.status = 'final'
    AND cm.content_format = 'text'
    AND (p.org_id IS NULL OR cs.organization_id = p.org_id)
    AND (p.session_id IS NULL OR cm.session_id = p.session_id)
),
successful_writes AS (
  SELECT
    turn_id,
    session_id,
    lower(payload->'output'->>'path') AS path,
    COUNT(*) AS write_count,
    MIN(created_at) AS first_write_at,
    MAX(created_at) AS last_write_at
  FROM tool_results
  WHERE COALESCE(payload->>'tool_name', '') = 'file.write'
    AND tool_error IS NULL
    AND COALESCE(payload->'output'->>'path', '') <> ''
    AND COALESCE((payload->'output'->>'byte_size')::int, 0) > 0
  GROUP BY turn_id, session_id, lower(payload->'output'->>'path')
),
file_readbacks AS (
  SELECT
    turn_id,
    session_id,
    lower(payload->'output'->>'path') AS path,
    created_at,
    'file.read' AS source
  FROM tool_results
  WHERE COALESCE(payload->>'tool_name', '') = 'file.read'
    AND tool_error IS NULL
    AND COALESCE(payload->'output'->>'path', '') <> ''
),
assistant_calls AS (
  SELECT
    cm.turn_id,
    cm.session_id,
    cm.created_at,
    lower(COALESCE(call->'arguments'->>'command', '')) AS command,
    replace(
      lower(
        substring(
          lower(COALESCE(call->'arguments'->>'command', ''))
          from '(scripts/[^[:space:]''\";|&),]+|config/[^[:space:]''\";|&),]+|results/[^[:space:]''\";|&),]+)'
        )
      ),
      chr(96),
      ''
    ) AS path_hint
  FROM chat_message cm
  JOIN chat_session cs ON cs.id = cm.session_id
  CROSS JOIN params p
  CROSS JOIN LATERAL jsonb_array_elements(COALESCE(cm.metadata::jsonb->'tool_calls', '[]'::jsonb)) AS call
  WHERE cm.created_at >= p.since_at
    AND cm.turn_id IS NOT NULL
    AND cm.role = 'assistant'
    AND cm.status = 'final'
    AND (p.org_id IS NULL OR cs.organization_id = p.org_id)
    AND (p.session_id IS NULL OR cm.session_id = p.session_id)
),
cli_readbacks AS (
  SELECT
    turn_id,
    session_id,
    path_hint AS path,
    created_at,
    'cli.execute' AS source
  FROM assistant_calls
  WHERE path_hint IS NOT NULL
    AND (
      command LIKE 'cat %'
      OR command LIKE 'head %'
      OR command LIKE 'tail %'
      OR command LIKE 'sed %'
      OR command LIKE 'grep %'
      OR command LIKE 'rg %'
      OR command LIKE 'wc %'
      OR command LIKE 'stat %'
      OR command LIKE 'file %'
    )
),
readbacks AS (
  SELECT * FROM file_readbacks
  UNION ALL
  SELECT * FROM cli_readbacks
),
rollup AS (
  SELECT
    w.turn_id,
    w.session_id,
    w.path,
    w.write_count,
    COUNT(*) AS readback_count,
    COUNT(*) FILTER (WHERE r.source = 'file.read') AS file_reads,
    COUNT(*) FILTER (WHERE r.source = 'cli.execute') AS cli_readbacks,
    MIN(r.created_at) AS first_readback,
    MAX(r.created_at) AS last_readback
  FROM successful_writes w
  JOIN readbacks r
    ON r.turn_id = w.turn_id
   AND r.session_id = w.session_id
   AND r.path = w.path
   AND r.created_at >= w.first_write_at
  GROUP BY w.turn_id, w.session_id, w.path, w.write_count
)
SELECT
  turn_id,
  session_id,
  path,
  write_count,
  readback_count,
  file_reads,
  cli_readbacks,
  first_readback,
  last_readback
FROM rollup
WHERE readback_count > 1
ORDER BY readback_count DESC, write_count DESC, last_readback DESC
LIMIT :'limit_rows'::int;

\echo
\echo '== Repeated Script Execution By Turn =='
WITH params AS (
  SELECT
    now() - (:'window_hours'::numeric * interval '1 hour') AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id,
    NULLIF(:'session_id', '')::uuid AS session_id
),
cli_tool_calls AS (
  SELECT
    cm.turn_id,
    cm.session_id,
    cm.sequence_number,
    call->'arguments'->>'command' AS command
  FROM chat_message cm
  JOIN chat_session cs ON cs.id = cm.session_id
  CROSS JOIN params p
  CROSS JOIN LATERAL jsonb_array_elements(COALESCE(cm.metadata::jsonb->'tool_calls', '[]'::jsonb)) AS call
  WHERE cm.created_at >= p.since_at
    AND cm.turn_id IS NOT NULL
    AND cm.role = 'assistant'
    AND cm.status = 'final'
    AND call->>'name' = 'cli_execute'
    AND (p.org_id IS NULL OR cs.organization_id = p.org_id)
    AND (p.session_id IS NULL OR cm.session_id = p.session_id)
),
script_runs AS (
  SELECT
    ctc.turn_id,
    ctc.session_id,
    ctc.sequence_number,
    (regexp_match(
      ctc.command,
      $$ (?:^|[;&|[:space:]])(?:bash|sh|zsh|python3?|python)[[:space:]]+((?:scripts|results|config)/[^[:space:]"';&|]+) $$,
      'x'
    ))[1] AS script_path
  FROM cli_tool_calls ctc
),
rollup AS (
  SELECT
    sr.turn_id,
    sr.session_id,
    sr.script_path,
    COUNT(*) AS script_runs,
    MIN(sr.sequence_number) AS first_seq,
    MAX(sr.sequence_number) AS last_seq
  FROM script_runs sr
  WHERE sr.script_path IS NOT NULL
  GROUP BY sr.turn_id, sr.session_id, sr.script_path
)
SELECT
  turn_id,
  session_id,
  script_path,
  script_runs,
  first_seq,
  last_seq
FROM rollup
WHERE script_runs >= 2
ORDER BY script_runs DESC, last_seq DESC
LIMIT :'limit_rows'::int;

\echo
\echo '== Shell File Build / Readback Churn By Turn =='
WITH params AS (
  SELECT
    now() - (:'window_hours'::numeric * interval '1 hour') AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id,
    NULLIF(:'session_id', '')::uuid AS session_id
),
assistant_calls AS (
  SELECT
    cm.turn_id,
    cm.session_id,
    cm.created_at,
    lower(COALESCE(call->'arguments'->>'command', '')) AS command,
    replace(
      lower(
        substring(
          lower(COALESCE(call->'arguments'->>'command', ''))
          from '(scripts/[^[:space:]''\";|&),]+|config/[^[:space:]''\";|&),]+|results/[^[:space:]''\";|&),]+)'
        )
      ),
      chr(96),
      ''
    ) AS path_hint
  FROM chat_message cm
  JOIN chat_session cs ON cs.id = cm.session_id
  CROSS JOIN params p
  CROSS JOIN LATERAL jsonb_array_elements(COALESCE(cm.metadata::jsonb->'tool_calls', '[]'::jsonb)) AS call
  WHERE cm.created_at >= p.since_at
    AND cm.turn_id IS NOT NULL
    AND cm.role = 'assistant'
    AND cm.status = 'final'
    AND (p.org_id IS NULL OR cs.organization_id = p.org_id)
    AND (p.session_id IS NULL OR cm.session_id = p.session_id)
),
turn_rollup AS (
  SELECT
    turn_id,
    session_id,
    COUNT(*) FILTER (
      WHERE path_hint IS NOT NULL
        AND (
          command LIKE '%>%scripts/%'
          OR command LIKE '%>>%scripts/%'
          OR command LIKE '%>%config/%'
          OR command LIKE '%>>%config/%'
          OR command LIKE '%>%results/%'
          OR command LIKE '%>>%results/%'
          OR command LIKE '%with open(''%scripts/%'
          OR command LIKE '%with open(''%config/%'
          OR command LIKE '%cat > scripts/%'
          OR command LIKE '%cat > config/%'
          OR command LIKE '%printf %scripts/%'
          OR command LIKE '%printf %config/%'
          OR command LIKE '%cp scripts/%'
        )
    ) AS shell_file_builds,
    COUNT(*) FILTER (
      WHERE path_hint IS NOT NULL
        AND (
          command LIKE 'head %'
          OR command LIKE 'tail %'
          OR command LIKE 'wc %'
          OR command LIKE 'cat %'
          OR command LIKE 'grep %'
        )
    ) AS readback_checks,
    string_agg(DISTINCT path_hint, ' | ') FILTER (WHERE path_hint IS NOT NULL) AS path_hints,
    MIN(created_at) AS first_seen,
    MAX(created_at) AS last_seen
  FROM assistant_calls
  GROUP BY turn_id, session_id
)
SELECT
  turn_id,
  session_id,
  shell_file_builds,
  readback_checks,
  COALESCE(path_hints, '∅') AS path_hints,
  first_seen,
  last_seen
FROM turn_rollup
WHERE shell_file_builds > 0
   OR readback_checks > 2
ORDER BY (shell_file_builds + readback_checks) DESC, last_seen DESC
LIMIT :'limit_rows'::int;

\echo
\echo '== Task CLI Working Directory Roots =='
WITH params AS (
  SELECT
    now() - (:'window_hours'::numeric * interval '1 hour') AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id,
    NULLIF(:'session_id', '')::uuid AS session_id
),
session_filter AS (
  SELECT
    cs.scope_type,
    cs.scope_id
  FROM chat_session cs
  CROSS JOIN params p
  WHERE p.session_id IS NOT NULL
    AND cs.id = p.session_id
),
root_rollup AS (
  SELECT
    CASE
      WHEN ce.working_directory LIKE '%/task-worktrees/%' THEN 'task_worktree'
      WHEN ce.working_directory LIKE '%/workspaces/%' THEN 'project_workspace'
      ELSE 'other'
    END AS root_kind,
    COUNT(*) AS executions,
    COUNT(DISTINCT ce.task_id) AS distinct_tasks,
    MAX(ce.created_at) AS last_seen
  FROM cli_execution ce
  JOIN project p ON p.id = ce.project_id
  CROSS JOIN params prm
  WHERE ce.created_at >= prm.since_at
    AND (prm.org_id IS NULL OR p.organization_id = prm.org_id)
    AND (
      prm.session_id IS NULL
      OR EXISTS (
        SELECT 1
        FROM session_filter sf
        WHERE (sf.scope_type = 'project_task' AND sf.scope_id = ce.task_id)
           OR (sf.scope_type = 'project' AND sf.scope_id = ce.project_id)
      )
    )
  GROUP BY 1
)
SELECT
  root_kind,
  executions,
  distinct_tasks,
  last_seen
FROM root_rollup
ORDER BY executions DESC, root_kind ASC;

\echo
\echo '== Recent Validation-Loop Blocks =='
WITH params AS (
  SELECT
    now() - (:'window_hours'::numeric * interval '1 hour') AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id
),
ranked AS (
  SELECT
    ct.id AS turn_id,
    ct.session_id,
    cs.scope_type,
    cs.mode,
    COALESCE(NULLIF(left(regexp_replace(trim(coalesce(cm.content, '')), E'\\s+', ' ', 'g'), 240), ''), 'validation_loop_blocked') AS block_excerpt,
    ct.completed_at,
    ROW_NUMBER() OVER (
      PARTITION BY ct.id
      ORDER BY cm.created_at DESC NULLS LAST, cm.sequence_number DESC NULLS LAST, cm.id DESC NULLS LAST
    ) AS rn
  FROM chat_turn ct
  JOIN chat_session cs ON cs.id = ct.session_id
  LEFT JOIN chat_message cm
    ON cm.turn_id = ct.id
   AND cm.role = 'system'
  CROSS JOIN params p
  WHERE ct.completed_at >= p.since_at
    AND ct.status = 'completed'
    AND ct.stop_reason = 'validation_loop_blocked'
    AND (p.org_id IS NULL OR cs.organization_id = p.org_id)
)
SELECT
  turn_id,
  session_id,
  scope_type,
  mode,
  block_excerpt,
  completed_at
FROM ranked
WHERE rn = 1
ORDER BY completed_at DESC
LIMIT :'limit_rows'::int;

\echo
\echo '== Completed Turns By Stop Reason =='
WITH params AS (
  SELECT
    now() - (:'window_hours'::numeric * interval '1 hour') AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id,
    NULLIF(:'session_id', '')::uuid AS session_id
),
turn_rollup AS (
  SELECT
    ct.id AS turn_id,
    cs.scope_type,
    cs.mode,
    COALESCE(NULLIF(ct.stop_reason, ''), '∅') AS stop_reason,
    COUNT(mi.id) FILTER (WHERE mi.status = 'completed') AS completed_invocations,
    COUNT(mi.id) FILTER (WHERE mi.status = 'failed') AS failed_invocations,
    COALESCE(SUM(COALESCE(mi.input_tokens, 0) + COALESCE(mi.output_tokens, 0) + COALESCE(mi.cache_read_tokens, 0)), 0) AS total_tokens,
    MAX(COALESCE(mi.created_at, ct.completed_at, ct.started_at)) AS last_seen
  FROM chat_turn ct
  JOIN chat_session cs ON cs.id = ct.session_id
  LEFT JOIN model_invocation mi ON mi.turn_id = ct.id
  CROSS JOIN params p
  WHERE ct.status = 'completed'
    AND COALESCE(ct.completed_at, ct.started_at) >= p.since_at
    AND (p.org_id IS NULL OR cs.organization_id = p.org_id)
    AND (p.session_id IS NULL OR ct.session_id = p.session_id)
  GROUP BY ct.id, cs.scope_type, cs.mode, COALESCE(NULLIF(ct.stop_reason, ''), '∅')
)
SELECT
  COALESCE(scope_type, '∅') AS scope_type,
  COALESCE(mode, '∅') AS mode,
  stop_reason,
  COUNT(*) AS turns,
  SUM(completed_invocations) AS completed_invocations,
  SUM(failed_invocations) AS failed_invocations,
  SUM(total_tokens) AS total_tokens,
  MAX(last_seen) AS last_seen
FROM turn_rollup
GROUP BY COALESCE(scope_type, '∅'), COALESCE(mode, '∅'), stop_reason
ORDER BY total_tokens DESC, turns DESC, stop_reason
LIMIT :'limit_rows'::int;

\echo
\echo '== Sessions With Active Summarization Backoff =='
WITH params AS (
  SELECT
    now() AS current_time,
    NULLIF(:'org_id', '')::uuid AS org_id,
    NULLIF(:'session_id', '')::uuid AS session_id
)
SELECT
  cs.id AS session_id,
  COALESCE(cs.scope_type, '∅') AS scope_type,
  COALESCE(cs.mode, '∅') AS mode,
  COALESCE((cs.metadata->'summarization_backoff'->>'failure_count')::int, 0) AS failure_count,
  NULLIF(cs.metadata->'summarization_backoff'->>'last_error', '') AS last_error,
  (cs.metadata->'summarization_backoff'->>'next_allowed_at')::timestamptz AS next_allowed_at
FROM chat_session cs
CROSS JOIN params p
WHERE cs.metadata ? 'summarization_backoff'
  AND (p.org_id IS NULL OR cs.organization_id = p.org_id)
  AND (p.session_id IS NULL OR cs.id = p.session_id)
  AND (cs.metadata->'summarization_backoff'->>'next_allowed_at')::timestamptz > p.current_time
ORDER BY next_allowed_at ASC, failure_count DESC
LIMIT :'limit_rows'::int;

\echo
\echo '== Provider Connection Health =='
WITH params AS (
  SELECT
    NULLIF(:'org_id', '')::uuid AS org_id,
    NULLIF(:'session_id', '')::uuid AS session_id
),
effective_org AS (
  SELECT COALESCE(
           p.org_id,
           (
             SELECT cs.organization_id
             FROM chat_session cs
             WHERE cs.id = p.session_id
           )
         ) AS organization_id
  FROM params p
)
SELECT
  pc.display_name,
  mp.slug AS provider_slug,
  pc.health_status,
  CASE
    WHEN pc.health_status = 'rate_limited'
      AND NULLIF(pc.metadata->>'health_rate_limited_until', '')::timestamptz IS NOT NULL
      AND NULLIF(pc.metadata->>'health_rate_limited_until', '')::timestamptz <= now()
      THEN 'degraded'
    WHEN pc.health_status = 'unavailable'
      AND pc.updated_at + interval '1 minute' <= now()
      THEN 'degraded'
    ELSE pc.health_status
  END AS effective_health_status,
  pc.is_enabled,
  pc.failover_priority,
  pc.max_concurrent,
  NULLIF(pc.metadata->>'health_rate_limited_until', '')::timestamptz AS rate_limited_until,
  CASE
    WHEN pc.health_status = 'rate_limited'
      THEN NULLIF(pc.metadata->>'health_rate_limited_until', '')::timestamptz
    WHEN pc.health_status = 'unavailable'
      THEN pc.updated_at + interval '1 minute'
    ELSE NULL
  END AS recovery_ready_at
FROM provider_connection pc
JOIN model_provider mp ON mp.id = pc.provider_id
CROSS JOIN effective_org eo
WHERE eo.organization_id IS NULL OR pc.organization_id = eo.organization_id
ORDER BY mp.slug, pc.failover_priority, pc.display_name
LIMIT :'limit_rows'::int;

\echo
\echo '== Rate-Limit Failure Routing Split =='
WITH params AS (
  SELECT
    now() - (:'window_hours'::numeric * interval '1 hour') AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id,
    NULLIF(:'session_id', '')::uuid AS session_id
)
SELECT
  CASE
    WHEN mi.provider_connection_id IS NULL THEN 'pre_routing'
    ELSE 'post_routing'
  END AS routing_phase,
  COUNT(*) AS failed_invocations,
  COUNT(*) FILTER (WHERE mi.provider_connection_id IS NOT NULL) AS with_connection_id,
  COUNT(*) FILTER (WHERE mi.provider_connection_id IS NULL) AS without_connection_id
FROM model_invocation mi
LEFT JOIN chat_session cs ON cs.id = mi.session_id
CROSS JOIN params p
WHERE mi.created_at >= p.since_at
  AND mi.status = 'failed'
  AND mi.error_code = 'provider_rate_limited'
  AND (p.org_id IS NULL OR mi.organization_id = p.org_id)
  AND (p.session_id IS NULL OR mi.session_id = p.session_id)
GROUP BY 1
ORDER BY failed_invocations DESC, routing_phase ASC;

\echo
\echo '== Pending Agent Turn Backlog =='
WITH params AS (
  SELECT
    NULLIF(:'org_id', '')::uuid AS org_id,
    NULLIF(:'session_id', '')::uuid AS session_id
),
pending_jobs AS (
  SELECT
    jq.id,
    jq.created_at,
    jq.run_after,
    (jq.payload->>'session_id')::uuid AS session_id,
    (jq.payload->>'message_id')::uuid AS message_id
  FROM job_queue jq
  CROSS JOIN params p
  WHERE jq.job_type = 'agent_turn'
    AND jq.status = 'pending'
    AND (p.session_id IS NULL OR (jq.payload->>'session_id')::uuid = p.session_id)
),
rollup AS (
  SELECT
    pj.session_id,
    COALESCE(cs.scope_type, '∅') AS scope_type,
    COALESCE(cs.mode, '∅') AS mode,
    COALESCE(cs.status, '∅') AS session_status,
    cs.current_turn_id,
    COALESCE(
      p_direct.settings->'pause'->>'is_paused',
      p_task.settings->'pause'->>'is_paused',
      'false'
    ) = 'true' AS is_paused,
    BOOL_OR(
      cs.scope_type = 'project'
      AND (
        (
          COALESCE(cm.metadata->>'source', '') = 'project_bootstrap'
          AND COALESCE(cs.metadata->'project_bootstrap'->>'status', '') <> 'active'
        )
        OR (
          COALESCE(cm.metadata->>'source', '') IN ('project_execution_continuation', 'project_continuation_resume')
          AND NOT EXISTS (
            SELECT 1
            FROM project_task pt_open
            WHERE pt_open.project_id = cs.scope_id
              AND pt_open.work_status NOT IN ('done', 'cancelled')
          )
        )
      )
    ) AS stale_project_source,
    COUNT(*) AS pending_jobs,
    MIN(pj.run_after) AS oldest_run_after,
    MAX(pj.run_after) AS newest_run_after,
    MIN(pj.created_at) AS oldest_created_at,
    MAX(pj.created_at) AS newest_created_at
  FROM pending_jobs pj
  LEFT JOIN chat_session cs ON cs.id = pj.session_id
  LEFT JOIN chat_message cm ON cm.id = pj.message_id
  LEFT JOIN project p_direct
    ON cs.scope_type = 'project'
   AND p_direct.id = cs.scope_id
  LEFT JOIN project_task pt
    ON cs.scope_type = 'project_task'
   AND pt.id = cs.scope_id
  LEFT JOIN project p_task
    ON p_task.id = pt.project_id
  CROSS JOIN params p
  WHERE p.org_id IS NULL OR cs.organization_id = p.org_id
  GROUP BY
    pj.session_id,
    COALESCE(cs.scope_type, '∅'),
    COALESCE(cs.mode, '∅'),
    COALESCE(cs.status, '∅'),
    cs.current_turn_id,
    COALESCE(
      p_direct.settings->'pause'->>'is_paused',
      p_task.settings->'pause'->>'is_paused',
      'false'
    ) = 'true'
)
SELECT
  session_id,
  scope_type,
  mode,
  session_status,
  current_turn_id,
  is_paused,
  stale_project_source,
  CASE
    WHEN is_paused THEN 'paused'
    WHEN stale_project_source THEN 'stale_project_source'
    WHEN current_turn_id IS NOT NULL THEN 'current_turn_set'
    ELSE 'ready'
  END AS backlog_state,
  pending_jobs,
  oldest_run_after,
  newest_run_after,
  oldest_created_at,
  newest_created_at
FROM rollup
ORDER BY oldest_run_after ASC NULLS FIRST, oldest_created_at ASC
LIMIT :'limit_rows'::int;

\echo
\echo '== Most Common Failures =='
WITH params AS (
  SELECT
    now() - (:'window_hours'::numeric * interval '1 hour') AS since_at,
    NULLIF(:'org_id', '')::uuid AS org_id,
    NULLIF(:'session_id', '')::uuid AS session_id
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
  AND (p.session_id IS NULL OR mi.session_id = p.session_id)
GROUP BY COALESCE(NULLIF(error_code, ''), '∅')
ORDER BY failures DESC, error_code
LIMIT :'limit_rows'::int;
SQL
