CREATE TABLE chat_turn (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES chat_session(id) ON DELETE CASCADE,
    turn_number integer NOT NULL,
    cycle_id uuid,
    responding_type text NOT NULL CHECK (responding_type IN ('agent')),
    responding_id uuid NOT NULL REFERENCES agent(id),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'in_progress', 'completed', 'cancelled', 'failed')),
    cancel_requested_at timestamptz,
    started_at timestamptz,
    completed_at timestamptz,
    duration_ms integer,
    error_message text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX chat_turn_session_turn_number_uidx
    ON chat_turn (session_id, turn_number);

CREATE INDEX chat_turn_session_status_idx
    ON chat_turn (session_id, status);
