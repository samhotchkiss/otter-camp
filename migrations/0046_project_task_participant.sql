CREATE TABLE project_task_participant (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id uuid NOT NULL REFERENCES project_task(id) ON DELETE CASCADE,
    participant_type text NOT NULL CHECK (participant_type IN ('human_user', 'agent')),
    participant_id uuid NOT NULL,
    role text NOT NULL CHECK (role IN ('planner', 'worker', 'reviewer', 'observer')),
    joined_at timestamptz NOT NULL DEFAULT now(),
    left_at timestamptz,
    CONSTRAINT project_task_participant_unique_member UNIQUE (task_id, participant_type, participant_id)
);

CREATE INDEX project_task_participant_active_idx
    ON project_task_participant (task_id, left_at)
    WHERE left_at IS NULL;
