CREATE TABLE runtime_state (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    scope_type text NOT NULL CHECK (scope_type IN ('task', 'session')),
    scope_id uuid NOT NULL,
    active_run_id uuid REFERENCES run(id) ON DELETE SET NULL,
    active_principal_type text CHECK (active_principal_type IN ('human_user', 'agent', 'system')),
    active_principal_id uuid,
    lock_acquired_at timestamptz,
    last_wakeup_at timestamptz,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT runtime_state_scope_unique UNIQUE (scope_type, scope_id),
    CONSTRAINT runtime_state_active_principal_pair_ck CHECK (
        (active_principal_type IS NULL AND active_principal_id IS NULL)
        OR (active_principal_type = 'system' AND active_principal_id IS NULL)
        OR (active_principal_type IN ('human_user', 'agent') AND active_principal_id IS NOT NULL)
    )
);

CREATE INDEX runtime_state_org_scope_idx
    ON runtime_state (organization_id, scope_type, scope_id);

CREATE TRIGGER runtime_state_set_updated_at
BEFORE UPDATE ON runtime_state
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

ALTER TABLE run_event
    DROP CONSTRAINT IF EXISTS run_event_event_type_check;

ALTER TABLE run_event
    ADD CONSTRAINT run_event_event_type_check CHECK (
        event_type IN (
            'run_started',
            'run_completed',
            'run_failed',
            'run_cancelled',
            'run_timed_out',
            'run_paused',
            'step_started',
            'step_completed',
            'step_failed',
            'attempt_started',
            'attempt_completed',
            'attempt_failed',
            'tool_called',
            'tool_returned',
            'heartbeat',
            'output_chunk',
            'policy_denied',
            'budget_exceeded',
            'supervisor_recovery',
            'wakeup_coalesced',
            'wakeup_deferred',
            'wakeup_promoted'
        )
    );
