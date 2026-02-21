-- Fix memories.embedding from 384 to 768 to match nomic-embed-text.
-- Clears existing embeddings since they'd be wrong dimensions.

DROP INDEX IF EXISTS memories_embedding_idx;

ALTER TABLE memories
    ALTER COLUMN embedding TYPE vector(768)
    USING NULL;

CREATE INDEX memories_embedding_idx
    ON memories USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100)
    WHERE embedding IS NOT NULL;
