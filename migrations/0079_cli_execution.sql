CREATE TABLE cli_execution (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES run(id) ON DELETE CASCADE,
    run_step_id uuid NOT NULL REFERENCES run_step(id) ON DELETE CASCADE,
    task_id uuid NOT NULL REFERENCES project_task(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    agent_id uuid NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    command text NOT NULL,
    working_directory text NOT NULL,
    risk_level text NOT NULL CHECK (risk_level IN ('low', 'medium', 'high', 'critical')),
    policy_decision text NOT NULL CHECK (policy_decision IN ('allowed', 'denied')),
    exit_code integer,
    stdout_artifact_id uuid REFERENCES run_artifact(id) ON DELETE SET NULL,
    stderr_artifact_id uuid REFERENCES run_artifact(id) ON DELETE SET NULL,
    env_vars_used jsonb NOT NULL DEFAULT '{}'::jsonb,
    started_at timestamptz,
    completed_at timestamptz,
    duration_ms integer,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT cli_execution_duration_nonnegative_ck CHECK (duration_ms IS NULL OR duration_ms >= 0)
);

CREATE INDEX cli_execution_run_idx
    ON cli_execution (run_id);

CREATE INDEX cli_execution_task_created_idx
    ON cli_execution (task_id, created_at);

CREATE INDEX cli_execution_risk_level_idx
    ON cli_execution (risk_level);
