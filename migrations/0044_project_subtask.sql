CREATE TABLE project_subtask (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id uuid NOT NULL REFERENCES project_task(id) ON DELETE CASCADE,
    flow_node_execution_id uuid NOT NULL REFERENCES flow_node_execution(id) ON DELETE CASCADE,
    title text NOT NULL,
    description text,
    work_status text NOT NULL DEFAULT 'pending' CHECK (work_status IN ('pending', 'in_progress', 'done', 'cancelled')),
    sequence_number integer NOT NULL,
    assignee_type text CHECK (assignee_type IN ('human_user', 'agent')),
    assignee_id uuid,
    created_by_type text NOT NULL CHECK (created_by_type IN ('human_user', 'agent', 'system')),
    created_by_id uuid,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT project_subtask_execution_sequence_unique UNIQUE (flow_node_execution_id, sequence_number)
);

CREATE INDEX project_subtask_task_status_idx
    ON project_subtask (task_id, work_status);

CREATE INDEX project_subtask_execution_sequence_idx
    ON project_subtask (flow_node_execution_id, sequence_number);

CREATE TRIGGER project_subtask_set_updated_at
BEFORE UPDATE ON project_subtask
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
