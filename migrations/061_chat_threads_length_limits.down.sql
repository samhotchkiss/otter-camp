ALTER TABLE chat_threads
    DROP CONSTRAINT IF EXISTS chat_threads_last_message_preview_length_chk,
    DROP CONSTRAINT IF EXISTS chat_threads_thread_key_length_chk;
