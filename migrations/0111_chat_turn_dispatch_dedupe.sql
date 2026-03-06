ALTER TABLE chat_turn
    ADD COLUMN IF NOT EXISTS trigger_message_id uuid REFERENCES chat_message(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS retry_count integer NOT NULL DEFAULT 0;

ALTER TABLE chat_turn
    ADD CONSTRAINT chat_turn_retry_count_nonnegative_ck
    CHECK (retry_count >= 0);

CREATE UNIQUE INDEX IF NOT EXISTS chat_turn_session_trigger_retry_uidx
    ON chat_turn (session_id, trigger_message_id, retry_count)
    WHERE trigger_message_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS chat_turn_trigger_message_idx
    ON chat_turn (trigger_message_id)
    WHERE trigger_message_id IS NOT NULL;
