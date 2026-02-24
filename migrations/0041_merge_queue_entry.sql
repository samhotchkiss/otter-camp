CREATE TABLE merge_queue_entry (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    task_id uuid NOT NULL REFERENCES project_task(id) ON DELETE CASCADE,
    branch_name text NOT NULL,
    target_branch text NOT NULL DEFAULT 'main',
    commit_sha text,
    position integer NOT NULL,
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'merging', 'merged', 'failed', 'skipped')),
    enqueued_at timestamptz NOT NULL DEFAULT now(),
    merged_at timestamptz,
    archived_at timestamptz,
    failure_reason text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT merge_queue_entry_project_task_unique UNIQUE (project_id, task_id)
);

CREATE INDEX merge_queue_entry_project_active_position_idx
    ON merge_queue_entry (project_id, archived_at, position)
    WHERE archived_at IS NULL;

CREATE INDEX merge_queue_entry_task_idx
    ON merge_queue_entry (task_id);
