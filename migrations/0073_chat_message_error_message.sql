ALTER TABLE chat_message
    ADD COLUMN IF NOT EXISTS error_message text;
