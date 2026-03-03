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
    'flow.list_templates',
    'Flow List Templates',
    'List available flow templates with node summaries',
    'tier1',
    'native',
    NULL,
    '{
      "type": "object",
      "properties": {
        "project_id": {"type": "string", "format": "uuid"}
      },
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
