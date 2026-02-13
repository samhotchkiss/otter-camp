DROP TRIGGER IF EXISTS chat_messages_token_rollup_trg ON chat_messages;
DROP FUNCTION IF EXISTS otter_chat_messages_token_rollup();
DROP FUNCTION IF EXISTS otter_estimate_token_count(TEXT);

DROP INDEX IF EXISTS chat_messages_room_created_tokens_idx;

ALTER TABLE chat_messages
    DROP COLUMN IF EXISTS token_count;

ALTER TABLE conversations
    DROP COLUMN IF EXISTS total_tokens;

ALTER TABLE rooms
    DROP COLUMN IF EXISTS total_tokens;
