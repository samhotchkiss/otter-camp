UPDATE memory_compaction_run
SET error_message = 'compaction run failed'
WHERE status = 'failed'
  AND (error_message IS NULL OR btrim(error_message) = '');

UPDATE memory_compaction_run
SET status = 'failed',
    error_message = COALESCE(NULLIF(error_message, ''), 'timed out after 1 hour pending'),
    completed_at = COALESCE(completed_at, now())
WHERE status = 'pending'
  AND created_at < now() - interval '1 hour';
