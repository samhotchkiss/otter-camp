CREATE TABLE model_usage_rollup (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    rollup_date date NOT NULL,
    rollup_type text NOT NULL CHECK (rollup_type IN ('provider_connection', 'model_provider', 'agent', 'project')),
    rollup_id uuid,
    model_name text,
    invocation_purpose text,
    total_invocations integer NOT NULL DEFAULT 0,
    total_input_tokens bigint NOT NULL DEFAULT 0,
    total_output_tokens bigint NOT NULL DEFAULT 0,
    total_cache_read_tokens bigint NOT NULL DEFAULT 0,
    total_latency_ms bigint NOT NULL DEFAULT 0,
    total_cost_microcents bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX model_usage_rollup_unique_dims_idx
    ON model_usage_rollup (
        organization_id,
        rollup_date,
        rollup_type,
        rollup_id,
        model_name,
        invocation_purpose
    )
    NULLS NOT DISTINCT;

CREATE INDEX model_usage_rollup_org_date_idx
    ON model_usage_rollup (organization_id, rollup_date DESC, rollup_type);

CREATE INDEX model_usage_rollup_rollup_id_idx
    ON model_usage_rollup (organization_id, rollup_type, rollup_id, rollup_date DESC)
    WHERE rollup_id IS NOT NULL;

CREATE TRIGGER model_usage_rollup_set_updated_at
BEFORE UPDATE ON model_usage_rollup
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
