CREATE TABLE mcp_tool_catalog (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id uuid NOT NULL REFERENCES mcp_connection(id) ON DELETE CASCADE,
    tool_name text NOT NULL,
    description text NOT NULL DEFAULT '',
    input_schema jsonb NOT NULL DEFAULT '{}'::jsonb,
    is_enabled boolean NOT NULL DEFAULT false,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    discovered_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT mcp_tool_catalog_conn_tool_unique UNIQUE (connection_id, tool_name)
);

CREATE INDEX mcp_tool_catalog_conn_enabled_idx
    ON mcp_tool_catalog (connection_id, is_enabled);

CREATE TRIGGER mcp_tool_catalog_set_updated_at
BEFORE UPDATE ON mcp_tool_catalog
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
