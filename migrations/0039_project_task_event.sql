CREATE TABLE project_task_event (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id uuid NOT NULL REFERENCES project_task(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    event_type text NOT NULL,
    actor_type text NOT NULL CHECK (actor_type IN ('human_user', 'agent', 'system', 'supervisor')),
    actor_id uuid,
    flow_node_id uuid REFERENCES flow_node(id) ON DELETE SET NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX project_task_event_task_created_desc_idx
    ON project_task_event (task_id, created_at DESC);

CREATE INDEX project_task_event_project_type_created_desc_idx
    ON project_task_event (project_id, event_type, created_at DESC);
