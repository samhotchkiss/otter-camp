INSERT INTO tool_definition (name, display_name, description, tool_tier, tool_domain, required_capability, input_schema, is_enabled)
VALUES
    ('agent.create_staff', 'Agent Create Staff',
     'Create a draft staff agent candidate for durable project roles such as project manager.',
     'tier2', 'native', 'agent.manage',
     '{"type":"object","properties":{"name":{"type":"string","description":"Display name for the staff agent"},"agent_type":{"type":"string","description":"Agent role type: pm, worker, reviewer, or general"},"system_prompt":{"type":"string","description":"Base identity and role instructions for the staff agent"},"operator_instructions":{"type":"string","description":"Optional operator-specific instructions"}},"required":["name","agent_type","system_prompt"],"additionalProperties":false}'::jsonb,
     true)
ON CONFLICT (name) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    description = EXCLUDED.description,
    tool_tier = EXCLUDED.tool_tier,
    tool_domain = EXCLUDED.tool_domain,
    required_capability = EXCLUDED.required_capability,
    input_schema = EXCLUDED.input_schema,
    is_enabled = EXCLUDED.is_enabled;
