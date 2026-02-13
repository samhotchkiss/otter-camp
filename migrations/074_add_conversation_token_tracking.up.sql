ALTER TABLE chat_messages
    ADD COLUMN IF NOT EXISTS token_count INTEGER;

ALTER TABLE conversations
    ADD COLUMN IF NOT EXISTS total_tokens BIGINT NOT NULL DEFAULT 0;

ALTER TABLE rooms
    ADD COLUMN IF NOT EXISTS total_tokens BIGINT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS chat_messages_room_created_tokens_idx
    ON chat_messages (room_id, created_at)
    WHERE token_count IS NOT NULL;

CREATE OR REPLACE FUNCTION otter_estimate_token_count(message_body TEXT)
RETURNS INTEGER
LANGUAGE sql
IMMUTABLE
AS $$
    SELECT CASE
        WHEN btrim(COALESCE(message_body, '')) = '' THEN 0
        ELSE GREATEST(
            1,
            CEIL(COALESCE(cardinality(regexp_split_to_array(btrim(message_body), E'\\s+')), 0) * 1.3)::INTEGER
        )
    END;
$$;

CREATE OR REPLACE FUNCTION otter_chat_messages_token_rollup()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    old_tokens BIGINT := COALESCE(OLD.token_count, 0);
    new_tokens BIGINT;
BEGIN
    IF TG_OP = 'INSERT' THEN
        NEW.token_count := COALESCE(NEW.token_count, otter_estimate_token_count(NEW.body));
        new_tokens := COALESCE(NEW.token_count, 0);

        UPDATE rooms
           SET total_tokens = COALESCE(total_tokens, 0) + new_tokens
         WHERE id = NEW.room_id;

        IF NEW.conversation_id IS NOT NULL THEN
            UPDATE conversations
               SET total_tokens = COALESCE(total_tokens, 0) + new_tokens
             WHERE id = NEW.conversation_id;
        END IF;

        RETURN NEW;
    END IF;

    IF TG_OP = 'UPDATE' THEN
        NEW.token_count := COALESCE(NEW.token_count, otter_estimate_token_count(NEW.body));
        new_tokens := COALESCE(NEW.token_count, 0);

        IF OLD.room_id IS DISTINCT FROM NEW.room_id THEN
            UPDATE rooms
               SET total_tokens = GREATEST(0, COALESCE(total_tokens, 0) - old_tokens)
             WHERE id = OLD.room_id;
            UPDATE rooms
               SET total_tokens = COALESCE(total_tokens, 0) + new_tokens
             WHERE id = NEW.room_id;
        ELSIF old_tokens <> new_tokens THEN
            UPDATE rooms
               SET total_tokens = GREATEST(0, COALESCE(total_tokens, 0) + (new_tokens - old_tokens))
             WHERE id = NEW.room_id;
        END IF;

        IF OLD.conversation_id IS DISTINCT FROM NEW.conversation_id THEN
            IF OLD.conversation_id IS NOT NULL THEN
                UPDATE conversations
                   SET total_tokens = GREATEST(0, COALESCE(total_tokens, 0) - old_tokens)
                 WHERE id = OLD.conversation_id;
            END IF;
            IF NEW.conversation_id IS NOT NULL THEN
                UPDATE conversations
                   SET total_tokens = COALESCE(total_tokens, 0) + new_tokens
                 WHERE id = NEW.conversation_id;
            END IF;
        ELSIF NEW.conversation_id IS NOT NULL AND old_tokens <> new_tokens THEN
            UPDATE conversations
               SET total_tokens = GREATEST(0, COALESCE(total_tokens, 0) + (new_tokens - old_tokens))
             WHERE id = NEW.conversation_id;
        END IF;

        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE' THEN
        UPDATE rooms
           SET total_tokens = GREATEST(0, COALESCE(total_tokens, 0) - old_tokens)
         WHERE id = OLD.room_id;

        IF OLD.conversation_id IS NOT NULL THEN
            UPDATE conversations
               SET total_tokens = GREATEST(0, COALESCE(total_tokens, 0) - old_tokens)
             WHERE id = OLD.conversation_id;
        END IF;

        RETURN OLD;
    END IF;

    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS chat_messages_token_rollup_trg ON chat_messages;
CREATE TRIGGER chat_messages_token_rollup_trg
BEFORE INSERT OR UPDATE OR DELETE ON chat_messages
FOR EACH ROW
EXECUTE FUNCTION otter_chat_messages_token_rollup();
