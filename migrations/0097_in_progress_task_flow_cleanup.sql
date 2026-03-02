UPDATE project_task AS task
SET
    work_status = 'queued',
    completed_at = NULL,
    updated_at = now()
WHERE task.work_status = 'in_progress'
  AND NOT EXISTS (
      SELECT 1
      FROM flow_node_execution AS execution
      WHERE execution.task_id = task.id
        AND execution.status = 'active'
  );
