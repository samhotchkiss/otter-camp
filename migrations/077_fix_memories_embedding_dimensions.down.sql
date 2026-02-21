DROP INDEX IF EXISTS memories_embedding_idx;

ALTER TABLE memories
    ALTER COLUMN embedding TYPE vector(384)
    USING NULL;

CREATE INDEX memories_embedding_idx
    ON memories USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100)
    WHERE embedding IS NOT NULL;
