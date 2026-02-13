-- Fix embedding column dimensions from 384 to 768 to match nomic-embed-text model.
-- The original migration (063) used 384 (all-MiniLM-L6-v2 dimensions) but the
-- configured embedder uses nomic-embed-text which outputs 768 dimensions.

-- Drop existing indexes that reference the old column type
DROP INDEX IF EXISTS chat_messages_embedding_idx;
DROP INDEX IF EXISTS chat_messages_unembedded_idx;

-- Alter the embedding column to 768 dimensions
ALTER TABLE chat_messages
    ALTER COLUMN embedding TYPE vector(768)
    USING NULL;

-- Drop any existing embeddings in related tables with wrong dimensions
-- (context_injections also uses embeddings)
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'context_injections'
        AND column_name = 'embedding'
        AND udt_name = 'vector'
    ) THEN
        EXECUTE 'ALTER TABLE context_injections ALTER COLUMN embedding TYPE vector(768) USING NULL';
    END IF;
END $$;

-- Recreate indexes with new dimensions
CREATE INDEX chat_messages_embedding_idx
    ON chat_messages USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100)
    WHERE embedding IS NOT NULL;

CREATE INDEX chat_messages_unembedded_idx
    ON chat_messages (created_at)
    WHERE embedding IS NULL;
