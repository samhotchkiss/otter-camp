CREATE TABLE model_profile (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    logical_profile_id text NOT NULL,
    organization_id uuid REFERENCES organization(id) ON DELETE CASCADE,
    version integer NOT NULL DEFAULT 1,
    is_current boolean NOT NULL DEFAULT true,
    provider_id uuid NOT NULL REFERENCES model_provider(id),
    model_name text NOT NULL,
    context_window_tokens integer NOT NULL,
    max_output_tokens integer NOT NULL,
    supports_streaming boolean NOT NULL DEFAULT true,
    supports_vision boolean NOT NULL DEFAULT false,
    temperature numeric(3,2),
    invocation_purpose text NOT NULL DEFAULT 'agent_turn' CHECK (
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
    fallback_profile_id text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX model_profile_logical_org_version_uidx
ON model_profile(logical_profile_id, COALESCE(organization_id, '00000000-0000-0000-0000-000000000000'::uuid), version);

CREATE UNIQUE INDEX model_profile_current_logical_org_uidx
ON model_profile(logical_profile_id, COALESCE(organization_id, '00000000-0000-0000-0000-000000000000'::uuid))
WHERE is_current = true;

CREATE INDEX model_profile_org_logical_current_idx
ON model_profile(organization_id, logical_profile_id)
WHERE is_current = true;

CREATE TRIGGER model_profile_set_updated_at
BEFORE UPDATE ON model_profile
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
