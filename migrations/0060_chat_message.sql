CREATE TABLE chat_message (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES chat_session(id) ON DELETE CASCADE,
    turn_id uuid REFERENCES chat_turn(id) ON DELETE SET NULL,
    sequence_number bigint NOT NULL,
    author_type text CHECK (author_type IN ('human_user', 'agent')),
    author_id uuid,
    role text NOT NULL CHECK (role IN ('user', 'assistant', 'tool_call', 'tool_result', 'system')),
    content text NOT NULL DEFAULT '',
    content_format text NOT NULL DEFAULT 'text' CHECK (content_format IN ('text', 'markdown', 'tool_json')),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'streaming', 'final', 'failed', 'redacted')),
    is_redacted boolean NOT NULL DEFAULT false,
    redacted_at timestamptz,
    tool_call_id text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT chat_message_author_pair_ck CHECK (
        (author_type IS NULL AND author_id IS NULL)
        OR (author_type IS NOT NULL AND author_id IS NOT NULL)
    )
);

COMMENT ON COLUMN chat_message.author_id IS 'soft ref polymorphic: human_user.id | agent.id';
COMMENT ON COLUMN chat_message.turn_id IS 'nullable because system and queued messages can be outside a turn';

CREATE UNIQUE INDEX chat_message_session_sequence_uidx
    ON chat_message (session_id, sequence_number);

CREATE INDEX chat_message_session_turn_idx
    ON chat_message (session_id, turn_id)
    WHERE turn_id IS NOT NULL;

CREATE INDEX chat_message_session_status_idx
    ON chat_message (session_id, status);

CREATE INDEX chat_message_session_tool_call_idx
    ON chat_message (session_id, tool_call_id)
    WHERE tool_call_id IS NOT NULL;

CREATE TRIGGER chat_message_set_updated_at
BEFORE UPDATE ON chat_message
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
