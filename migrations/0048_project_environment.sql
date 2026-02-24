CREATE TABLE project_environment (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    name text NOT NULL,
    delivery_mode text NOT NULL CHECK (delivery_mode IN ('continuous', 'gated', 'scheduled')),
    remote_id uuid REFERENCES project_remote(id) ON DELETE SET NULL,
    target_branch text NOT NULL DEFAULT 'main',
    deploy_task_id uuid REFERENCES project_task(id) ON DELETE SET NULL,
    last_deployed_commit text,
    last_deployed_at timestamptz,
    is_active boolean NOT NULL DEFAULT true,
    schedule_id uuid REFERENCES task_schedule(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT project_environment_project_name_unique UNIQUE (project_id, name)
);

CREATE INDEX project_environment_project_active_idx
    ON project_environment (project_id, is_active);

CREATE INDEX project_environment_deploy_task_idx
    ON project_environment (deploy_task_id)
    WHERE deploy_task_id IS NOT NULL;

CREATE TRIGGER project_environment_set_updated_at
BEFORE UPDATE ON project_environment
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
