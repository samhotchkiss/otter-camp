CREATE TABLE skill (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    project_id uuid,
    slug text NOT NULL CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'),
    display_name text NOT NULL,
    description text NOT NULL,
    file_path text NOT NULL,
    version integer NOT NULL DEFAULT 1,
    is_active boolean NOT NULL DEFAULT true,
    created_by_type text NOT NULL CHECK (created_by_type IN ('human', 'agent', 'system')),
    created_by_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX skill_org_slug_unique
    ON skill (organization_id, slug)
    WHERE project_id IS NULL;

CREATE UNIQUE INDEX skill_project_slug_unique
    ON skill (project_id, slug)
    WHERE project_id IS NOT NULL;

CREATE TRIGGER skill_set_updated_at
BEFORE UPDATE ON skill
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
