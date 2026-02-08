ALTER TABLE project_chat_messages
    ADD COLUMN IF NOT EXISTS attachments JSONB NOT NULL DEFAULT '[]'::jsonb;

CREATE INDEX IF NOT EXISTS project_chat_messages_attachments_idx
    ON project_chat_messages
    USING GIN (attachments);

ALTER TABLE attachments
    ADD COLUMN IF NOT EXISTS chat_message_id UUID REFERENCES project_chat_messages(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS attachments_chat_message_idx
    ON attachments (chat_message_id);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'attachments_single_parent_chk'
    ) THEN
        ALTER TABLE attachments
            ADD CONSTRAINT attachments_single_parent_chk
            CHECK (num_nonnulls(comment_id, chat_message_id) <= 1);
    END IF;
END $$;
