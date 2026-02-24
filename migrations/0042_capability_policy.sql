CREATE TABLE capability_policy (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    policy_layer text NOT NULL CHECK (policy_layer IN ('instance', 'org', 'project', 'agent_profile', 'request')),
    organization_id uuid REFERENCES organization(id) ON DELETE CASCADE,
    project_id uuid REFERENCES project(id) ON DELETE CASCADE,
    agent_id uuid REFERENCES agent(id) ON DELETE CASCADE,
    capability text NOT NULL,
    effect text NOT NULL CHECK (effect IN ('allow', 'deny')),
    conditions jsonb NOT NULL DEFAULT '{}'::jsonb,
    priority integer NOT NULL DEFAULT 100,
    created_by_type text NOT NULL CHECK (created_by_type IN ('human_user', 'agent', 'system')),
    created_by_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT capability_policy_instance_org_null_chk CHECK (NOT (policy_layer = 'instance' AND organization_id IS NOT NULL)),
    CONSTRAINT capability_policy_project_requires_project_id_chk CHECK (NOT (policy_layer = 'project' AND project_id IS NULL)),
    CONSTRAINT capability_policy_agent_requires_agent_id_chk CHECK (NOT (policy_layer = 'agent_profile' AND agent_id IS NULL))
);

CREATE INDEX capability_policy_org_layer_capability_idx
    ON capability_policy (organization_id, policy_layer, capability);

CREATE INDEX capability_policy_project_capability_idx
    ON capability_policy (project_id, capability)
    WHERE project_id IS NOT NULL;

CREATE INDEX capability_policy_agent_capability_idx
    ON capability_policy (agent_id, capability)
    WHERE agent_id IS NOT NULL;

CREATE UNIQUE INDEX capability_policy_instance_capability_unique_idx
    ON capability_policy (capability, policy_layer)
    WHERE policy_layer = 'instance';

CREATE TRIGGER capability_policy_set_updated_at
BEFORE UPDATE ON capability_policy
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
