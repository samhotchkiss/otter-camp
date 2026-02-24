CREATE TABLE project_task (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    task_number integer NOT NULL,
    title text NOT NULL,
    description text,
    work_status text NOT NULL DEFAULT 'draft' CHECK (work_status IN ('draft', 'queued', 'in_progress', 'blocked', 'on_hold', 'review', 'done', 'cancelled')),
    current_flow_node_id uuid REFERENCES flow_node(id) ON DELETE SET NULL,
    flow_template_id uuid REFERENCES flow_template(id) ON DELETE SET NULL,
    schedule_id uuid REFERENCES task_schedule(id) ON DELETE SET NULL,
    branch_name text,
    requires_human_review boolean NOT NULL DEFAULT false,
    created_by_type text NOT NULL CHECK (created_by_type IN ('human_user', 'agent', 'system')),
    created_by_id uuid,
    assigned_agent_id uuid REFERENCES agent(id) ON DELETE SET NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CONSTRAINT project_task_project_number_unique UNIQUE (project_id, task_number)
);

CREATE INDEX project_task_project_status_idx
    ON project_task (project_id, work_status);

CREATE INDEX project_task_project_created_desc_idx
    ON project_task (project_id, created_at DESC);

CREATE INDEX project_task_current_flow_node_idx
    ON project_task (current_flow_node_id)
    WHERE current_flow_node_id IS NOT NULL;

CREATE INDEX project_task_assigned_agent_idx
    ON project_task (assigned_agent_id)
    WHERE assigned_agent_id IS NOT NULL;

CREATE INDEX project_task_flow_template_status_idx
    ON project_task (flow_template_id, work_status)
    WHERE flow_template_id IS NOT NULL;

CREATE TRIGGER project_task_set_updated_at
BEFORE UPDATE ON project_task
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
