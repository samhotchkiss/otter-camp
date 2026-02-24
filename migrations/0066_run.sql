CREATE TABLE run (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    project_id uuid REFERENCES project(id) ON DELETE SET NULL,
    task_id uuid REFERENCES project_task(id) ON DELETE SET NULL,
    flow_node_id uuid REFERENCES flow_node(id) ON DELETE SET NULL,
    session_id uuid REFERENCES chat_session(id) ON DELETE SET NULL,
    turn_id uuid REFERENCES chat_turn(id) ON DELETE SET NULL,
    principal_type text NOT NULL CHECK (principal_type IN ('human_user', 'agent', 'system')),
    principal_id uuid NOT NULL,
    status text NOT NULL DEFAULT 'created' CHECK (status IN ('created', 'in_progress', 'paused', 'completed', 'failed', 'timed_out', 'cancelled', 'cancelling', 'dead_letter')),
    idempotency_key text,
    trigger_type text NOT NULL CHECK (trigger_type IN ('chat_turn', 'scheduler', 'api', 'supervisor', 'agent_tool')),
    version integer NOT NULL DEFAULT 1,
    failure_reason text,
    failure_class text CHECK (failure_class IN ('transient', 'permanent', 'policy_denied', 'budget_exceeded', 'timeout')),
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    completed_at timestamptz
);

CREATE INDEX run_org_status_idx
    ON run (organization_id, status);

CREATE INDEX run_task_status_idx
    ON run (task_id, status);

CREATE INDEX run_session_idx
    ON run (session_id);

CREATE UNIQUE INDEX run_org_idempotency_key_uidx
    ON run (organization_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE INDEX run_idempotency_key_idx
    ON run (idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE TRIGGER run_set_updated_at
BEFORE UPDATE ON run
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
