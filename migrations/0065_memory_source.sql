CREATE TABLE memory_source (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    memory_id uuid NOT NULL REFERENCES memory(id) ON DELETE CASCADE,
    source_type text NOT NULL CHECK (source_type IN ('chat_message', 'event', 'file', 'memory_import', 'explicit')),
    source_id uuid,
    import_id uuid REFERENCES memory_import(id) ON DELETE SET NULL,
    session_id uuid,
    created_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON COLUMN memory_source.source_id IS 'soft ref; chat_message.id when source_type=chat_message';
COMMENT ON COLUMN memory_source.session_id IS 'soft ref to chat_session.id; no FK by design';

CREATE INDEX memory_source_memory_idx
    ON memory_source (memory_id);

CREATE INDEX memory_source_session_idx
    ON memory_source (session_id)
    WHERE session_id IS NOT NULL;

CREATE INDEX memory_source_source_idx
    ON memory_source (source_type, source_id)
    WHERE source_id IS NOT NULL;
