ALTER TABLE project_task
    ADD COLUMN priority smallint NOT NULL DEFAULT 0,
    ADD CONSTRAINT project_task_priority_chk CHECK (priority BETWEEN 0 AND 4);

CREATE INDEX project_task_project_priority_idx
    ON project_task (project_id, priority);
