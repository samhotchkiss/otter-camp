CREATE TABLE IF NOT EXISTS project (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    slug text NOT NULL CHECK (slug ~ '^[a-z0-9-]+$'),
    display_name text NOT NULL,
    description text NOT NULL DEFAULT '',
    delivery_mode text NOT NULL DEFAULT 'gated' CHECK (delivery_mode IN ('continuous', 'gated', 'scheduled')),
    deploy_flow_template_id uuid,
    settings jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_by_type text NOT NULL CHECK (created_by_type IN ('human_user', 'agent', 'system')),
    created_by_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT project_org_slug_unique UNIQUE (organization_id, slug)
);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'project_org_slug_unique'
          AND conrelid = 'project'::regclass
    ) THEN
        ALTER TABLE project
            ADD CONSTRAINT project_org_slug_unique UNIQUE (organization_id, slug);
    END IF;
END
$$;

CREATE INDEX IF NOT EXISTS project_org_created_at_idx
    ON project (organization_id, created_at DESC);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_trigger
        WHERE tgname = 'project_set_updated_at'
          AND tgrelid = 'project'::regclass
    ) THEN
        CREATE TRIGGER project_set_updated_at
        BEFORE UPDATE ON project
        FOR EACH ROW
        EXECUTE FUNCTION set_updated_at();
    END IF;
END
$$;
