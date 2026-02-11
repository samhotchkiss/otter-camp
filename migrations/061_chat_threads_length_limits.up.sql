ALTER TABLE chat_threads
    ADD CONSTRAINT chat_threads_thread_key_length_chk
        CHECK (length(thread_key) <= 512),
    ADD CONSTRAINT chat_threads_last_message_preview_length_chk
        CHECK (length(last_message_preview) <= 500);
