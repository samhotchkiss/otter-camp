ALTER TABLE job_queue
    ADD COLUMN IF NOT EXISTS dedupe_key text,
    ADD COLUMN IF NOT EXISTS group_key text;

UPDATE job_queue
SET dedupe_key = 'agent_turn:' || (payload->>'session_id') || ':' || (payload->>'message_id') || ':' || COALESCE(NULLIF(payload->>'retry_count', ''), '0'),
    group_key = 'agent_turn:' || (payload->>'session_id') || ':' || (payload->>'message_id')
WHERE job_type = 'agent_turn'
  AND payload ? 'session_id'
  AND payload ? 'message_id'
  AND (dedupe_key IS NULL OR group_key IS NULL);

WITH ranked AS (
    SELECT id,
           ROW_NUMBER() OVER (
               PARTITION BY dedupe_key
               ORDER BY CASE status WHEN 'claimed' THEN 0 WHEN 'pending' THEN 1 ELSE 2 END,
                        run_after ASC,
                        created_at ASC,
                        id ASC
           ) AS rank
    FROM job_queue
    WHERE dedupe_key IS NOT NULL
      AND status IN ('pending', 'claimed')
)
UPDATE job_queue jq
SET status = 'dead_letter',
    claimed_by = NULL,
    claimed_at = NULL,
    last_error = 'duplicate agent_turn dispatch suppressed during queue dedupe migration',
    updated_at = now()
FROM ranked
WHERE jq.id = ranked.id
  AND ranked.rank > 1;

CREATE UNIQUE INDEX IF NOT EXISTS job_queue_active_dedupe_key_uidx
    ON job_queue (dedupe_key)
    WHERE dedupe_key IS NOT NULL
      AND status IN ('pending', 'claimed');

CREATE INDEX IF NOT EXISTS job_queue_group_key_status_idx
    ON job_queue (group_key, status)
    WHERE group_key IS NOT NULL
      AND status IN ('pending', 'claimed');
