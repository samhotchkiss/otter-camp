CREATE TABLE chat_summary (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES chat_session(id) ON DELETE CASCADE,
    from_sequence bigint NOT NULL,
    to_sequence bigint NOT NULL,
    summary_text text NOT NULL,
    summarized_turn_count integer NOT NULL,
    model_invocation_id uuid REFERENCES model_invocation(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT chat_summary_range_ck CHECK (to_sequence >= from_sequence)
);

CREATE UNIQUE INDEX chat_summary_session_from_sequence_uidx
    ON chat_summary (session_id, from_sequence);

CREATE INDEX chat_summary_session_to_sequence_idx
    ON chat_summary (session_id, to_sequence DESC);
