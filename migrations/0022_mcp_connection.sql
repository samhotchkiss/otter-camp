CREATE TABLE mcp_connection (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    project_id uuid,
    display_name text NOT NULL,
    slug text NOT NULL CHECK (slug ~ '^[a-z0-9-]+$'),
    transport text NOT NULL CHECK (transport IN ('stdio', 'http', 'sse')),
    transport_config jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL DEFAULT 'configuring' CHECK (status IN ('configuring', 'active', 'degraded', 'failed')),
    is_enabled boolean NOT NULL DEFAULT true,
    last_healthy_at timestamptz,
    created_by_type text NOT NULL CHECK (created_by_type IN ('human_user', 'agent', 'system')),
    created_by_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT mcp_connection_org_slug_unique UNIQUE (organization_id, slug)
);

DO $$
BEGIN
    IF to_regclass('project') IS NOT NULL
       AND NOT EXISTS (
            SELECT 1
            FROM pg_constraint
            WHERE conname = 'mcp_connection_project_fk'
              AND conrelid = 'mcp_connection'::regclass
       ) THEN
        ALTER TABLE mcp_connection
            ADD CONSTRAINT mcp_connection_project_fk
            FOREIGN KEY (project_id)
            REFERENCES project(id)
            ON DELETE CASCADE;
    END IF;
END
$$;

CREATE INDEX mcp_connection_org_status_idx
    ON mcp_connection (organization_id, status);

CREATE INDEX mcp_connection_project_idx
    ON mcp_connection (project_id)
    WHERE project_id IS NOT NULL;

CREATE TRIGGER mcp_connection_set_updated_at
BEFORE UPDATE ON mcp_connection
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
