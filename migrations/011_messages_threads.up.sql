-- Extend comments table to support direct message threads

ALTER TABLE comments ADD COLUMN org_id UUID REFERENCES organizations(id) ON DELETE CASCADE;
ALTER TABLE comments ADD COLUMN thread_id TEXT;
ALTER TABLE comments ADD COLUMN sender_id TEXT;
ALTER TABLE comments ADD COLUMN sender_type TEXT;
ALTER TABLE comments ADD COLUMN sender_name TEXT;
ALTER TABLE comments ADD COLUMN sender_avatar_url TEXT;

-- Backfill org_id from tasks for existing comments
UPDATE comments c
SET org_id = t.org_id
FROM tasks t
WHERE c.task_id = t.id;

ALTER TABLE comments ALTER COLUMN org_id SET NOT NULL;
ALTER TABLE comments ALTER COLUMN task_id DROP NOT NULL;

-- Ensure each comment belongs to a task thread or a DM thread
ALTER TABLE comments
    ADD CONSTRAINT comments_thread_or_task_chk
    CHECK (task_id IS NOT NULL OR thread_id IS NOT NULL);

CREATE INDEX comments_thread_created_at_idx ON comments(thread_id, created_at DESC);
CREATE INDEX comments_org_idx ON comments(org_id);
