CREATE TABLE session_tool_set (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES chat_session(id) ON DELETE CASCADE,
    agent_id uuid NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    resolved_at timestamptz NOT NULL DEFAULT now(),
    tool_set jsonb NOT NULL,
    invalidated_at timestamptz
);

CREATE UNIQUE INDEX session_tool_set_active_unique_idx
    ON session_tool_set (session_id, agent_id)
    WHERE invalidated_at IS NULL;

CREATE INDEX session_tool_set_session_idx
    ON session_tool_set (session_id, agent_id, resolved_at DESC);
