INSERT INTO tool_definition (name, display_name, description, tool_tier, tool_domain, required_capability, input_schema, is_enabled)
VALUES
    ('staffing.browse_profiles', 'Staffing Browse Profiles',
     'Search available agent identity profiles by category, role type, or keyword. Returns matching profiles with brief info.',
     'tier1', 'native', NULL,
     '{"type":"object","properties":{"category":{"type":"string","description":"Filter by category (engineering, content, design, ai, business, etc.)"},"role_type":{"type":"string","description":"Filter by role type: ic or manager"},"query":{"type":"string","description":"Text search on role name, tagline, and display name"},"limit":{"type":"integer","description":"Max results (default 20, max 100)"}},"additionalProperties":false}'::jsonb,
     true),
    ('staffing.get_profile', 'Staffing Get Profile',
     'Get full identity details for an agent profile including system prompt content and personality.',
     'tier1', 'native', NULL,
     '{"type":"object","properties":{"role_id":{"type":"string","description":"The role_id of the profile to retrieve"}},"required":["role_id"],"additionalProperties":false}'::jsonb,
     true)
ON CONFLICT (name) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    description = EXCLUDED.description,
    tool_tier = EXCLUDED.tool_tier,
    tool_domain = EXCLUDED.tool_domain,
    required_capability = EXCLUDED.required_capability,
    input_schema = EXCLUDED.input_schema,
    is_enabled = EXCLUDED.is_enabled;
