CREATE TABLE project (
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

CREATE INDEX project_org_created_at_idx
    ON project (organization_id, created_at DESC);

DO $$
BEGIN
    -- Ensure token_budget.project_id FK exists on fresh databases where
    -- token_budget (0025) is created before project (0031).
    IF to_regclass('public.token_budget') IS NOT NULL
       AND NOT EXISTS (
           SELECT 1
           FROM pg_constraint
           WHERE conname = 'token_budget_project_fk'
       )
    THEN
        ALTER TABLE token_budget
            ADD CONSTRAINT token_budget_project_fk
            FOREIGN KEY (project_id)
            REFERENCES project(id)
            ON DELETE CASCADE;
    END IF;
END;
$$;

CREATE TRIGGER project_set_updated_at
BEFORE UPDATE ON project
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
