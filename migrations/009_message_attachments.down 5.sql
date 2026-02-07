-- Remove attachments support

DROP INDEX IF EXISTS attachments_comment_idx;
DROP INDEX IF EXISTS attachments_org_idx;
DROP TABLE IF EXISTS attachments;

DROP INDEX IF EXISTS comments_attachments_idx;
ALTER TABLE comments DROP COLUMN IF EXISTS attachments;
