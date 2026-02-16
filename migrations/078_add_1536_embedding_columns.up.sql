-- Add 1536-dimensional embedding columns for OpenAI text-embedding-3-small.
-- Keep legacy 768-dimensional embedding columns for backward compatibility.

ALTER TABLE memories
    ADD COLUMN IF NOT EXISTS embedding_1536 vector(1536);

ALTER TABLE chat_messages
    ADD COLUMN IF NOT EXISTS embedding_1536 vector(1536);

CREATE INDEX IF NOT EXISTS memories_embedding_1536_idx
    ON memories USING ivfflat (embedding_1536 vector_cosine_ops) WITH (lists = 100)
    WHERE embedding_1536 IS NOT NULL;

CREATE INDEX IF NOT EXISTS chat_messages_embedding_1536_idx
    ON chat_messages USING ivfflat (embedding_1536 vector_cosine_ops) WITH (lists = 100)
    WHERE embedding_1536 IS NOT NULL;
