DROP INDEX IF EXISTS comments_org_idx;
DROP INDEX IF EXISTS comments_thread_created_at_idx;

ALTER TABLE comments DROP CONSTRAINT IF EXISTS comments_thread_or_task_chk;
ALTER TABLE comments ALTER COLUMN task_id SET NOT NULL;

ALTER TABLE comments DROP COLUMN IF EXISTS sender_avatar_url;
ALTER TABLE comments DROP COLUMN IF EXISTS sender_name;
ALTER TABLE comments DROP COLUMN IF EXISTS sender_type;
ALTER TABLE comments DROP COLUMN IF EXISTS sender_id;
ALTER TABLE comments DROP COLUMN IF EXISTS thread_id;
ALTER TABLE comments DROP COLUMN IF EXISTS org_id;
