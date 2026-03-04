INSERT INTO tool_definition (
    name,
    display_name,
    description,
    tool_tier,
    tool_domain,
    required_capability,
    input_schema,
    is_enabled
)
VALUES (
    'agent.assign_project',
    'Agent Assign Project',
    'Assign an agent to a project with a role (pm, worker, reviewer, observer)',
    'tier2',
    'native',
    'agent.manage',
    '{
      "type": "object",
      "properties": {
        "agent_id": {"type": "string", "format": "uuid"},
        "project_id": {"type": "string", "format": "uuid"},
        "role": {"type": "string", "enum": ["pm", "worker", "reviewer", "observer"]}
      },
      "required": ["agent_id", "project_id", "role"],
      "additionalProperties": false
    }'::jsonb,
    true
)
ON CONFLICT (name) DO UPDATE
SET
    display_name = EXCLUDED.display_name,
    description = EXCLUDED.description,
    tool_tier = EXCLUDED.tool_tier,
    tool_domain = EXCLUDED.tool_domain,
    required_capability = EXCLUDED.required_capability,
    input_schema = EXCLUDED.input_schema,
    is_enabled = EXCLUDED.is_enabled;
