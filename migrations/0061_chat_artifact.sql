CREATE TABLE chat_artifact (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES chat_session(id) ON DELETE CASCADE,
    message_id uuid NOT NULL REFERENCES chat_message(id) ON DELETE CASCADE,
    artifact_type text NOT NULL CHECK (artifact_type IN ('file', 'image', 'code', 'data', 'link')),
    filename text,
    content_type text,
    storage_key text,
    url text,
    byte_size bigint,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX chat_artifact_session_message_idx
    ON chat_artifact (session_id, message_id);
