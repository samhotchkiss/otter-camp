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
    'bootstrap.setup.persist',
    'Bootstrap Setup Persist',
    'Mark canonical bootstrap setup checklist steps as persisted in the current project bootstrap session once their outputs have been recorded.',
    'tier1',
    'native',
    'project.manage',
    '{
      "type": "object",
      "properties": {
        "project_id": {"type": "string", "format": "uuid"},
        "completed_step_slugs": {
          "type": "array",
          "items": {"type": "string"},
          "minItems": 1
        },
        "sign_off_summary": {"type": "string"}
      },
      "required": ["completed_step_slugs"]
    }'::jsonb,
    true
)
ON CONFLICT (name) DO UPDATE
SET display_name = EXCLUDED.display_name,
    description = EXCLUDED.description,
    tool_tier = EXCLUDED.tool_tier,
    tool_domain = EXCLUDED.tool_domain,
    required_capability = EXCLUDED.required_capability,
    input_schema = EXCLUDED.input_schema,
    is_enabled = EXCLUDED.is_enabled;
