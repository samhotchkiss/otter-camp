CREATE TABLE project_task_dependency (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source_type text NOT NULL CHECK (source_type IN ('project_task', 'project_subtask')),
    source_id uuid NOT NULL,
    depends_on_type text NOT NULL CHECK (depends_on_type IN ('project_task', 'project_subtask')),
    depends_on_id uuid NOT NULL,
    created_by_type text NOT NULL CHECK (created_by_type IN ('human_user', 'agent', 'system')),
    created_by_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT project_task_dependency_same_level_chk CHECK (source_type = depends_on_type),
    CONSTRAINT project_task_dependency_no_self_chk CHECK (source_id <> depends_on_id),
    CONSTRAINT project_task_dependency_unique_edge UNIQUE (source_type, source_id, depends_on_type, depends_on_id)
);

CREATE INDEX project_task_dependency_source_idx
    ON project_task_dependency (source_type, source_id);

CREATE INDEX project_task_dependency_depends_on_idx
    ON project_task_dependency (depends_on_type, depends_on_id);
