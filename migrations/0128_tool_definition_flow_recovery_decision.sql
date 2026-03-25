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
    'flow.recovery_decision',
    'Flow Recovery Decision',
    'Record resume/retry/block/escalate decisions for blocked or stranded flow executions',
    'tier2',
    'native',
    'flow.control',
    '{
      "type": "object",
      "properties": {
        "flow_node_execution_id": {"type": "string", "format": "uuid"},
        "decision": {
          "type": "string",
          "enum": ["resume", "retry", "block", "escalate"]
        },
        "reason": {"type": "string"}
      },
      "required": ["flow_node_execution_id", "decision"],
      "additionalProperties": false
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
