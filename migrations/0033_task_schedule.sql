CREATE TABLE task_schedule (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    flow_template_id uuid NOT NULL REFERENCES flow_template(id),
    display_name text NOT NULL,
    cron_expression text NOT NULL,
    overlap_policy text NOT NULL DEFAULT 'skip' CHECK (overlap_policy IN ('skip', 'queue', 'cancel_existing')),
    max_duration_ms bigint,
    is_enabled boolean NOT NULL DEFAULT true,
    last_fired_at timestamptz,
    next_fire_at timestamptz,
    created_by_type text NOT NULL CHECK (created_by_type IN ('human_user', 'agent', 'system')),
    created_by_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX task_schedule_project_enabled_idx
    ON task_schedule (project_id, is_enabled)
    WHERE is_enabled = true;

CREATE INDEX task_schedule_next_fire_idx
    ON task_schedule (next_fire_at)
    WHERE is_enabled = true;

CREATE TRIGGER task_schedule_set_updated_at
BEFORE UPDATE ON task_schedule
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
