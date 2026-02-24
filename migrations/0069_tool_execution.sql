CREATE TABLE tool_execution (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES run(id) ON DELETE CASCADE,
    run_step_id uuid REFERENCES run_step(id) ON DELETE SET NULL,
    run_attempt_id uuid REFERENCES run_attempt(id) ON DELETE SET NULL,
    tool_name text NOT NULL,
    tool_tier text NOT NULL CHECK (tool_tier IN ('tier1', 'tier2')),
    tool_domain text NOT NULL CHECK (tool_domain IN ('native', 'mcp', 'cli', 'browser')),
    capability text,
    policy_decision text NOT NULL CHECK (policy_decision IN ('allowed', 'denied', 'not_checked')),
    input jsonb NOT NULL DEFAULT '{}'::jsonb,
    output jsonb,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'in_progress', 'completed', 'failed', 'policy_denied', 'timed_out')),
    error_message text,
    started_at timestamptz,
    completed_at timestamptz,
    duration_ms integer,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT tool_execution_duration_nonnegative_ck CHECK (duration_ms IS NULL OR duration_ms >= 0)
);

CREATE INDEX tool_execution_run_idx
    ON tool_execution (run_id);

CREATE INDEX tool_execution_step_idx
    ON tool_execution (run_step_id);

CREATE INDEX tool_execution_tool_created_idx
    ON tool_execution (tool_name, created_at);

CREATE INDEX tool_execution_policy_denied_idx
    ON tool_execution (policy_decision)
    WHERE policy_decision = 'denied';
