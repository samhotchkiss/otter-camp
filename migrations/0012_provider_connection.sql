CREATE TABLE provider_connection (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    provider_id uuid NOT NULL REFERENCES model_provider(id),
    display_name text NOT NULL,
    api_key_ref text NOT NULL,
    api_base_url_override text,
    failover_priority integer NOT NULL DEFAULT 100,
    health_status text NOT NULL DEFAULT 'healthy' CHECK (health_status IN ('healthy', 'degraded', 'rate_limited', 'unavailable')),
    is_enabled boolean NOT NULL DEFAULT true,
    metadata jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX provider_connection_org_provider_failover_idx
    ON provider_connection (organization_id, provider_id, failover_priority);

CREATE TRIGGER provider_connection_set_updated_at
BEFORE UPDATE ON provider_connection
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
