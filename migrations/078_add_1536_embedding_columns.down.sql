DROP INDEX IF EXISTS memories_embedding_1536_idx;
DROP INDEX IF EXISTS chat_messages_embedding_1536_idx;

ALTER TABLE memories
    DROP COLUMN IF EXISTS embedding_1536;

ALTER TABLE chat_messages
    DROP COLUMN IF EXISTS embedding_1536;
