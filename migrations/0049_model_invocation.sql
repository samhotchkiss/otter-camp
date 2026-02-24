ALTER TABLE provider_connection
    ADD COLUMN IF NOT EXISTS max_concurrent integer NOT NULL DEFAULT 10 CHECK (max_concurrent > 0);

CREATE TABLE model_invocation (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    model_provider_id uuid NOT NULL REFERENCES model_provider(id),
    provider_connection_id uuid REFERENCES provider_connection(id),
    model_profile_id text,
    invocation_purpose text NOT NULL CHECK (
        invocation_purpose IN (
            'agent_turn',
            'listening_eval',
            'summarization',
            'skill_summarization',
            'memory_extraction',
            'memory_retrieval',
            'memory_dedup',
            'memory_synthesis',
            'replay'
        )
    ),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'in_flight', 'completed', 'failed', 'cancelled')),
    prompt_storage_key text,
    response_storage_key text,
    input_tokens integer,
    output_tokens integer,
    cache_read_tokens integer,
    model_name text NOT NULL,
    is_streaming boolean NOT NULL DEFAULT false,
    latency_ms integer,
    total_duration_ms integer,
    attempt_number integer NOT NULL DEFAULT 1,
    fallback_from_invocation_id uuid REFERENCES model_invocation(id),
    error_code text,
    error_message text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    agent_id uuid REFERENCES agent(id),
    project_id uuid REFERENCES project(id),
    project_task_id uuid REFERENCES project_task(id),
    session_id uuid,
    turn_id uuid,
    run_id uuid,
    run_step_id uuid,
    run_attempt_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);

COMMENT ON COLUMN model_invocation.session_id IS 'soft ref: chat_session.id';
COMMENT ON COLUMN model_invocation.turn_id IS 'soft ref: chat_turn.id';
COMMENT ON COLUMN model_invocation.run_id IS 'FK constraint added in 0062_model_invocation_run_fks.sql';
COMMENT ON COLUMN model_invocation.run_step_id IS 'FK constraint added in 0062_model_invocation_run_fks.sql';
COMMENT ON COLUMN model_invocation.run_attempt_id IS 'null for standalone chat turns; set for model calls within a run_attempt; FK constraint added in 0062_model_invocation_run_fks.sql';

CREATE INDEX model_invocation_org_created_idx
    ON model_invocation (organization_id, created_at DESC);

CREATE INDEX model_invocation_agent_created_idx
    ON model_invocation (agent_id, created_at DESC)
    WHERE agent_id IS NOT NULL;

CREATE INDEX model_invocation_project_created_idx
    ON model_invocation (project_id, created_at DESC)
    WHERE project_id IS NOT NULL;

CREATE INDEX model_invocation_session_created_idx
    ON model_invocation (session_id, created_at DESC)
    WHERE session_id IS NOT NULL;

