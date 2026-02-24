CREATE TABLE model_profile_assignment (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    scope_type text NOT NULL CHECK (scope_type IN ('organization', 'project', 'agent', 'flow_node')),
    scope_id uuid NOT NULL,
    logical_profile_id text NOT NULL,
    invocation_purpose text NOT NULL CHECK (
        invocation_purpose IN (
            'agent_turn',
            'listening_eval',
            'summarization',
            'skill_summarization',
            'memory_extraction',
            'memory_distillation',
            'memory_entity_synthesis',
            'replay'
        )
    ),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, scope_type, scope_id, invocation_purpose)
);

CREATE TRIGGER model_profile_assignment_set_updated_at
BEFORE UPDATE ON model_profile_assignment
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
