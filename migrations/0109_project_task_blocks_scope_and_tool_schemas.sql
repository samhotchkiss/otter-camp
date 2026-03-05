ALTER TABLE project_task
    ADD COLUMN blocks_scope text NOT NULL DEFAULT 'none'
    CHECK (blocks_scope IN ('none', 'all'));

CREATE INDEX project_task_gate_scope_idx
    ON project_task (project_id, task_number)
    WHERE blocks_scope = 'all'
      AND work_status NOT IN ('done', 'cancelled');

UPDATE tool_definition
SET input_schema = $$
{
  "type": "object",
  "properties": {
    "project_id": {"type": "string", "format": "uuid"},
    "title": {"type": "string"},
    "description": {"type": "string"},
    "flow_template_id": {"type": "string", "format": "uuid"},
    "requires_human_review": {"type": "boolean"},
    "blocks_scope": {"type": "string", "enum": ["none", "all"]}
  },
  "required": ["project_id", "title"],
  "additionalProperties": false
}
$$::jsonb
WHERE name = 'task.create';

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
    "assigned_agent_id": {"type": "string", "format": "uuid"},
    "blocks_scope": {"type": "string", "enum": ["none", "all"]}
  },
  "required": ["task_id"],
  "additionalProperties": false
}
$$::jsonb
WHERE name = 'task.update';
