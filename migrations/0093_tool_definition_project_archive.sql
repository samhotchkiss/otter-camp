INSERT INTO tool_definition (name, display_name, description, tool_tier, tool_domain, required_capability, input_schema, is_enabled)
VALUES (
    'project.archive',
    'Project Archive',
    'Archive a project (soft-delete; hides it from active listings)',
    'tier2',
    'native',
    'project.manage',
    '{"type":"object","required":["project_id"],"properties":{"project_id":{"type":"string","description":"UUID of the project to archive"}},"additionalProperties":false}'::jsonb,
    true
)
ON CONFLICT (name) DO NOTHING;
