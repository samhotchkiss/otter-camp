CREATE TABLE run_event (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES run(id) ON DELETE CASCADE,
    run_step_id uuid REFERENCES run_step(id) ON DELETE SET NULL,
    run_attempt_id uuid REFERENCES run_attempt(id) ON DELETE SET NULL,
    sequence integer NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('run_started', 'run_completed', 'run_failed', 'run_cancelled', 'run_timed_out', 'run_paused', 'step_started', 'step_completed', 'step_failed', 'attempt_started', 'attempt_completed', 'attempt_failed', 'tool_called', 'tool_returned', 'heartbeat', 'output_chunk', 'policy_denied', 'budget_exceeded', 'supervisor_recovery')),
    actor_type text NOT NULL CHECK (actor_type IN ('human_user', 'agent', 'system', 'supervisor')),
    actor_id uuid,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT run_event_sequence_positive_ck CHECK (sequence >= 1),
    CONSTRAINT run_event_actor_pair_ck CHECK (
        (actor_type IN ('system', 'supervisor') AND actor_id IS NULL)
        OR (actor_type IN ('human_user', 'agent') AND actor_id IS NOT NULL)
    ),
    CONSTRAINT run_event_run_sequence_unique UNIQUE (run_id, sequence)
);

CREATE INDEX run_event_run_created_idx
    ON run_event (run_id, created_at);

CREATE INDEX run_event_run_event_type_idx
    ON run_event (run_id, event_type);
