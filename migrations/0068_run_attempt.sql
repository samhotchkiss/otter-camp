CREATE TABLE run_attempt (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_step_id uuid NOT NULL REFERENCES run_step(id) ON DELETE CASCADE,
    attempt_number integer NOT NULL,
    trigger text NOT NULL CHECK (trigger IN ('initial', 'retry_transient', 'retry_policy', 'supervisor_recovery')),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'in_progress', 'completed', 'failed', 'timed_out', 'cancelled')),
    failure_reason text,
    failure_class text CHECK (failure_class IN ('transient', 'permanent', 'policy_denied', 'budget_exceeded', 'timeout')),
    output jsonb,
    output_summary text,
    worker_type text CHECK (worker_type IN ('cli', 'browser', 'mcp', 'native', 'internal')),
    worker_id text,
    input_tokens integer NOT NULL DEFAULT 0,
    output_tokens integer NOT NULL DEFAULT 0,
    started_at timestamptz,
    completed_at timestamptz,
    duration_ms integer,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT run_attempt_attempt_number_positive_ck CHECK (attempt_number >= 1),
    CONSTRAINT run_attempt_input_tokens_nonnegative_ck CHECK (input_tokens >= 0),
    CONSTRAINT run_attempt_output_tokens_nonnegative_ck CHECK (output_tokens >= 0),
    CONSTRAINT run_attempt_duration_nonnegative_ck CHECK (duration_ms IS NULL OR duration_ms >= 0),
    CONSTRAINT run_attempt_step_attempt_unique UNIQUE (run_step_id, attempt_number)
);

CREATE INDEX run_attempt_step_status_idx
    ON run_attempt (run_step_id, status);
