ALTER TABLE flow_node_execution
    ADD COLUMN runtime_substate text;

UPDATE flow_node_execution
SET runtime_substate = 'waiting_for_turn'
WHERE status = 'active'
  AND runtime_substate IS NULL;

ALTER TABLE flow_node_execution
    ALTER COLUMN runtime_substate SET DEFAULT 'waiting_for_turn';

ALTER TABLE flow_node_execution
    ADD CONSTRAINT flow_node_execution_runtime_substate_check
    CHECK (
        runtime_substate IS NULL
        OR runtime_substate IN (
            'running',
            'waiting_for_turn',
            'waiting_for_review',
            'stalled',
            'recovery_pending'
        )
    );
