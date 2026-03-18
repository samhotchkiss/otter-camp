ALTER TABLE chat_turn
    DROP CONSTRAINT IF EXISTS chat_turn_stop_reason_check;

ALTER TABLE chat_turn
    ADD CONSTRAINT chat_turn_stop_reason_check
    CHECK (
        stop_reason IS NULL
        OR stop_reason IN (
            'max_tool_calls',
            'max_duration',
            'user_cancelled',
            'user_steered',
            'model_error',
            'session_closed',
            'validation_loop_blocked',
            'recovery_content_required',
            'superseded_live_turn'
        )
    );
