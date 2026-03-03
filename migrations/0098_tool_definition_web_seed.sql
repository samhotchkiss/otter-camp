-- Web tools: web.search and web.fetch (tier1, native domain)

INSERT INTO tool_definition (name, display_name, description, tool_tier, tool_domain, required_capability, input_schema, is_enabled)
VALUES
(
    'web.search',
    'Web Search',
    'Search the web for information. Uses Brave Search API when configured, otherwise falls back to DuckDuckGo. Returns a list of search results with titles, URLs, and snippets.',
    'tier1',
    'native',
    NULL,
    '{
        "type": "object",
        "properties": {
            "query": {
                "type": "string",
                "description": "The search query"
            },
            "max_results": {
                "type": "integer",
                "description": "Maximum number of results to return (1-20, default 10)",
                "minimum": 1,
                "maximum": 20
            }
        },
        "required": ["query"],
        "additionalProperties": false
    }',
    true
),
(
    'web.fetch',
    'Web Fetch',
    'Fetch a web page and return its content as plain text. Strips HTML tags, scripts, and styles. Useful for reading articles, documentation, or any web content.',
    'tier1',
    'native',
    NULL,
    '{
        "type": "object",
        "properties": {
            "url": {
                "type": "string",
                "description": "The URL to fetch (must be http or https)"
            },
            "max_length": {
                "type": "integer",
                "description": "Maximum character length of returned content (default 50000)",
                "minimum": 1
            },
            "include_links": {
                "type": "boolean",
                "description": "Whether to include link URLs in the output (default false)"
            }
        },
        "required": ["url"],
        "additionalProperties": false
    }',
    true
)
ON CONFLICT (name) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    description  = EXCLUDED.description,
    input_schema = EXCLUDED.input_schema,
    is_enabled   = EXCLUDED.is_enabled;
