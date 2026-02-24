CREATE TABLE chat_read_cursor (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES chat_session(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES human_user(id) ON DELETE CASCADE,
    last_read_sequence bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX chat_read_cursor_session_user_uidx
    ON chat_read_cursor (session_id, user_id);

CREATE INDEX chat_read_cursor_user_idx
    ON chat_read_cursor (user_id, updated_at DESC);

CREATE TRIGGER chat_read_cursor_set_updated_at
BEFORE UPDATE ON chat_read_cursor
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
