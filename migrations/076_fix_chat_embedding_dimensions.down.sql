-- Revert embedding column dimensions back to 384
DROP INDEX IF EXISTS chat_messages_embedding_idx;
DROP INDEX IF EXISTS chat_messages_unembedded_idx;

ALTER TABLE chat_messages
    ALTER COLUMN embedding TYPE vector(384)
    USING NULL;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'context_injections'
        AND column_name = 'embedding'
        AND udt_name = 'vector'
    ) THEN
        EXECUTE 'ALTER TABLE context_injections ALTER COLUMN embedding TYPE vector(384) USING NULL';
    END IF;
END $$;

CREATE INDEX chat_messages_embedding_idx
    ON chat_messages USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100)
    WHERE embedding IS NOT NULL;

CREATE INDEX chat_messages_unembedded_idx
    ON chat_messages (created_at)
    WHERE embedding IS NULL;
