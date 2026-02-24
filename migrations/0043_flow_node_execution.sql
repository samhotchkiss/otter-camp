CREATE TABLE flow_node_execution (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id uuid NOT NULL REFERENCES project_task(id) ON DELETE CASCADE,
    flow_node_id uuid NOT NULL REFERENCES flow_node(id) ON DELETE CASCADE,
    visit_number integer NOT NULL DEFAULT 1,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'completed', 'rejected', 'abandoned')),
    session_id uuid,
    commit_sha text,
    started_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX flow_node_execution_task_node_visit_idx
    ON flow_node_execution (task_id, flow_node_id, visit_number);

CREATE INDEX flow_node_execution_task_status_idx
    ON flow_node_execution (task_id, status);

CREATE INDEX flow_node_execution_session_idx
    ON flow_node_execution (session_id)
    WHERE session_id IS NOT NULL;
