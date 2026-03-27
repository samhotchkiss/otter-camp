UPDATE tool_definition
SET input_schema = $$
{
  "type": "object",
  "properties": {
    "project_id": {"type": "string", "format": "uuid"},
    "parent_task_id": {"type": "string", "format": "uuid"},
    "status": {"type": "string"},
    "limit": {"type": "integer", "minimum": 1},
    "cursor": {"type": "string"},
    "include_meta_drafts": {"type": "boolean"}
  },
  "additionalProperties": false
}
$$::jsonb
WHERE name = 'task.list';
