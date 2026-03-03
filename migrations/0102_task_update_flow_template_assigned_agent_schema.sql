UPDATE tool_definition
SET input_schema = $$
{
  "type": "object",
  "properties": {
    "task_id": {"type": "string", "format": "uuid"},
    "title": {"type": "string"},
    "description": {"type": "string"},
    "work_status": {"type": "string"},
    "flow_template_id": {"type": "string", "format": "uuid"},
    "assigned_agent_id": {"type": "string", "format": "uuid"}
  },
  "required": ["task_id"],
  "additionalProperties": false
}
$$::jsonb
WHERE name = 'task.update';
