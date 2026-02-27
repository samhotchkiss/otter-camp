INSERT INTO tool_definition (name, display_name, description, tool_tier, tool_domain, required_capability, input_schema, is_enabled)
VALUES (
    'tui.navigate',
    'TUI Navigate',
    'Navigate the user''s TUI to a specific project, task, inbox, or view. Use this when the user asks to "pull up", "show me", or "open" something in the dashboard.',
    'tier1',
    'native',
    'session.read',
    '{"type":"object","required":["target"],"properties":{"target":{"type":"string","enum":["project","task","session","inbox","dashboard"],"description":"What to navigate to"},"target_id":{"type":"string","description":"UUID of the project/task/session"},"target_slug":{"type":"string","description":"Slug of the project (alternative to target_id)"}},"additionalProperties":false}'::jsonb,
    true
) ON CONFLICT (name) DO NOTHING;
