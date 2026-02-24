CREATE TABLE project_remote (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    name text NOT NULL,
    url text NOT NULL,
    is_default boolean NOT NULL DEFAULT false,
    transport text NOT NULL DEFAULT 'https' CHECK (transport IN ('https', 'ssh')),
    credential_ref text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT project_remote_project_name_unique UNIQUE (project_id, name)
);

CREATE INDEX project_remote_project_idx
    ON project_remote (project_id);

CREATE UNIQUE INDEX project_remote_project_default_idx
    ON project_remote (project_id)
    WHERE is_default = true;

CREATE TRIGGER project_remote_set_updated_at
BEFORE UPDATE ON project_remote
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
