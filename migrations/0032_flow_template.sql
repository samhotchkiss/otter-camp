CREATE TABLE flow_template (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid REFERENCES organization(id) ON DELETE CASCADE,
    project_id uuid REFERENCES project(id) ON DELETE CASCADE,
    slug text NOT NULL CHECK (slug ~ '^[a-z0-9-]+$'),
    display_name text NOT NULL,
    description text NOT NULL DEFAULT '',
    is_current boolean NOT NULL DEFAULT true,
    version integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    start_node_id uuid,
    is_system boolean NOT NULL DEFAULT false,
    created_by_type text NOT NULL CHECK (created_by_type IN ('human_user', 'agent', 'system')),
    created_by_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX flow_template_scope_slug_version_uidx
    ON flow_template (
        COALESCE(organization_id, '00000000-0000-0000-0000-000000000000'::uuid),
        COALESCE(project_id, '00000000-0000-0000-0000-000000000000'::uuid),
        slug,
        version
    );

CREATE UNIQUE INDEX flow_template_scope_slug_current_uidx
    ON flow_template (
        COALESCE(organization_id, '00000000-0000-0000-0000-000000000000'::uuid),
        COALESCE(project_id, '00000000-0000-0000-0000-000000000000'::uuid),
        slug
    )
    WHERE is_current = true;

CREATE INDEX flow_template_current_idx
    ON flow_template (organization_id, project_id, is_current)
    WHERE is_current = true;

CREATE INDEX flow_template_project_idx
    ON flow_template (project_id)
    WHERE project_id IS NOT NULL;

ALTER TABLE project
    ADD CONSTRAINT project_deploy_flow_template_fk
    FOREIGN KEY (deploy_flow_template_id)
    REFERENCES flow_template(id)
    ON DELETE SET NULL;

CREATE TRIGGER flow_template_set_updated_at
BEFORE UPDATE ON flow_template
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
