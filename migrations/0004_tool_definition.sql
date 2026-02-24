CREATE TABLE tool_definition (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE,
    display_name text NOT NULL,
    description text NOT NULL,
    tool_tier text NOT NULL CHECK (tool_tier IN ('tier1', 'tier2')),
    tool_domain text NOT NULL,
    required_capability text,
    input_schema jsonb NOT NULL DEFAULT '{}',
    is_enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);
