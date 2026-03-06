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
    "blocks_scope": {"type": "string", "enum": ["none", "all"]},
    "planning_override_reason": {"type": "string"},
    "planning_artifacts": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "slug": {"type": "string"},
          "title": {"type": "string"},
          "summary": {"type": "string"},
          "sections": {"type": "array", "items": {"type": "string"}},
          "asset_refs": {"type": "array", "items": {"type": "string"}},
          "notes": {"type": "string"}
        },
        "additionalProperties": false
      }
    }
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
    "blocks_scope": {"type": "string", "enum": ["none", "all"]},
    "planning_override_reason": {"type": "string"},
    "planning_artifacts": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "slug": {"type": "string"},
          "title": {"type": "string"},
          "summary": {"type": "string"},
          "sections": {"type": "array", "items": {"type": "string"}},
          "asset_refs": {"type": "array", "items": {"type": "string"}},
          "notes": {"type": "string"}
        },
        "additionalProperties": false
      }
    }
  },
  "required": ["task_id"],
  "additionalProperties": false
}
$$::jsonb
WHERE name = 'task.update';
