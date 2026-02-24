CREATE TABLE agent_profile_template (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid REFERENCES organization(id) ON DELETE CASCADE,
    display_name text NOT NULL,
    source text NOT NULL CHECK (source IN ('system', 'org', 'promoted')),
    source_agent_id uuid REFERENCES agent(id) ON DELETE SET NULL,
    system_prompt text NOT NULL DEFAULT '',
    operator_instructions text NOT NULL DEFAULT '',
    agent_type text NOT NULL CHECK (agent_type IN ('pm', 'worker', 'reviewer', 'general')),
    default_model_profile_id text,
    tool_allow_list text[] NOT NULL DEFAULT '{}'::text[],
    tool_deny_list text[] NOT NULL DEFAULT '{}'::text[],
    memory_read_scopes text[] NOT NULL DEFAULT '{}'::text[],
    private_memory boolean NOT NULL DEFAULT false,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX agent_profile_template_org_idx
    ON agent_profile_template (organization_id)
    WHERE organization_id IS NOT NULL;

CREATE TRIGGER agent_profile_template_set_updated_at
BEFORE UPDATE ON agent_profile_template
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
