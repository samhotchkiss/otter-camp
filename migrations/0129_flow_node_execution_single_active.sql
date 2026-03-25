WITH ranked_active AS (
	SELECT
		id,
		row_number() OVER (
			PARTITION BY task_id, flow_node_id
			ORDER BY started_at DESC, id DESC
		) AS row_num
	FROM flow_node_execution
	WHERE status = 'active'
)
UPDATE flow_node_execution AS execution
SET
	status = 'abandoned',
	runtime_substate = NULL,
	completed_at = COALESCE(execution.completed_at, now())
FROM ranked_active
WHERE execution.id = ranked_active.id
  AND ranked_active.row_num > 1;

CREATE UNIQUE INDEX IF NOT EXISTS flow_node_execution_single_active_per_node_idx
	ON flow_node_execution (task_id, flow_node_id)
	WHERE status = 'active';
