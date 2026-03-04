-- Make flow_node_execution_id optional in flow.advance tool schema.
-- The backfill logic in AdvanceFlow creates missing executions on-demand,
-- so agents don't need an existing execution ID to advance the flow.
UPDATE tool_definition
SET input_schema = '{
  "type": "object",
  "properties": {
    "flow_node_execution_id": {"type": "string", "format": "uuid"},
    "commit_sha": {"type": "string"}
  },
  "additionalProperties": false
}'::jsonb
WHERE name = 'flow.advance';
