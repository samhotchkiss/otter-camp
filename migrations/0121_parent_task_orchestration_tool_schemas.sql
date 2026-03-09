UPDATE tool_definition
SET input_schema = $$
{
  "type": "object",
  "properties": {
    "project_id": {"type": "string", "format": "uuid"},
    "parent_task_id": {"type": "string", "format": "uuid"},
    "title": {"type": "string"},
    "description": {"type": "string"},
    "flow_template_id": {"type": "string", "format": "uuid"},
    "requires_human_review": {"type": "boolean"},
    "blocks_scope": {"type": "string", "enum": ["none", "all"]},
    "planning_override_reason": {"type": "string"},
    "planning_follow_on_stop_reason": {"type": "string"},
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
    "reopen_feedback": {"type": "string"},
    "planning_override_reason": {"type": "string"},
    "planning_follow_on_stop_reason": {"type": "string"},
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
    },
    "child_output_verifications": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "task_id": {"type": "string", "format": "uuid"},
          "summary": {"type": "string"}
        },
        "required": ["task_id", "summary"],
        "additionalProperties": false
      }
    },
    "integration_check": {
      "type": "object",
      "properties": {
        "status": {"type": "string", "enum": ["passed", "failed"]},
        "summary": {"type": "string"}
      },
      "required": ["status", "summary"],
      "additionalProperties": false
    },
    "outcome_assessment": {
      "type": "object",
      "properties": {
        "satisfied": {"type": "boolean"},
        "summary": {"type": "string"}
      },
      "required": ["satisfied", "summary"],
      "additionalProperties": false
    }
  },
  "required": ["task_id"],
  "additionalProperties": false
}
$$::jsonb
WHERE name = 'task.update';
