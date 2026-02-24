CREATE TABLE agent (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    display_name text NOT NULL,
    agent_class text NOT NULL CHECK (agent_class IN ('staff', 'temp')),
    lifecycle_status text NOT NULL CHECK (lifecycle_status IN ('draft', 'active', 'paused', 'retired', 'cancelled', 'expired', 'promoted')),
    system_prompt text NOT NULL DEFAULT '',
    operator_instructions text NOT NULL DEFAULT '',
    agent_type text NOT NULL CHECK (agent_type IN ('pm', 'worker', 'reviewer', 'general')),
    is_starter_trio boolean NOT NULL DEFAULT false,
    private_memory boolean NOT NULL DEFAULT false,
    memory_read_scopes text[] NOT NULL DEFAULT '{}'::text[],
    tool_allow_list text[] NOT NULL DEFAULT '{}'::text[],
    tool_deny_list text[] NOT NULL DEFAULT '{}'::text[],
    default_model_profile_id text,
    budget_cap_tokens bigint,
    budget_period text CHECK (budget_period IN ('daily', 'weekly', 'monthly')),
    -- The project table is created in task 016. Add the FK there.
    temp_project_id uuid,
    temp_ttl_seconds integer,
    temp_expires_at timestamptz,
    promoted_to_agent_id uuid REFERENCES agent(id) ON DELETE SET NULL,
    created_by_type text NOT NULL CHECK (created_by_type IN ('human_user', 'agent', 'system')),
    created_by_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT agent_temp_project_required_chk CHECK (
        (agent_class = 'temp' AND temp_project_id IS NOT NULL)
        OR
        (agent_class = 'staff' AND temp_project_id IS NULL)
    )
    -- Lifecycle transitions by agent_class are enforced at the service layer (task 014).
);

CREATE INDEX agent_org_lifecycle_idx ON agent (organization_id, lifecycle_status);
CREATE INDEX agent_org_class_lifecycle_idx ON agent (organization_id, agent_class, lifecycle_status);
CREATE INDEX agent_temp_expires_at_idx ON agent (temp_expires_at) WHERE temp_expires_at IS NOT NULL;

CREATE TRIGGER agent_set_updated_at
BEFORE UPDATE ON agent
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
