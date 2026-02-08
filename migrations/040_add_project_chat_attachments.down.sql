ALTER TABLE attachments
    DROP CONSTRAINT IF EXISTS attachments_single_parent_chk;

DROP INDEX IF EXISTS attachments_chat_message_idx;

ALTER TABLE attachments
    DROP COLUMN IF EXISTS chat_message_id;

DROP INDEX IF EXISTS project_chat_messages_attachments_idx;

ALTER TABLE project_chat_messages
    DROP COLUMN IF EXISTS attachments;
