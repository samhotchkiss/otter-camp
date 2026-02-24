CREATE TABLE run_step (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES run(id) ON DELETE CASCADE,
    step_number integer NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'in_progress', 'completed', 'failed', 'cancelled', 'skipped')),
    tool_name text,
    tool_tier text CHECK (tool_tier IN ('tier1', 'tier2')),
    started_at timestamptz,
    completed_at timestamptz,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT run_step_step_number_positive_ck CHECK (step_number >= 1),
    CONSTRAINT run_step_run_step_number_unique UNIQUE (run_id, step_number)
);

CREATE INDEX run_step_run_status_idx
    ON run_step (run_id, status);
