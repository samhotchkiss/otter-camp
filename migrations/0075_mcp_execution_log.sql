ALTER TABLE tool_execution
    ALTER COLUMN run_id DROP NOT NULL;

CREATE TABLE mcp_execution_log (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    mcp_connection_id uuid NOT NULL REFERENCES mcp_connection(id) ON DELETE CASCADE,
    mcp_tool_catalog_id uuid REFERENCES mcp_tool_catalog(id) ON DELETE SET NULL,
    tool_execution_id uuid REFERENCES tool_execution(id) ON DELETE SET NULL,
    run_id uuid REFERENCES run(id) ON DELETE SET NULL,
    agent_id uuid REFERENCES agent(id) ON DELETE SET NULL,
    method text NOT NULL,
    tool_name text,
    resource_uri text,
    request_payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    response_payload jsonb,
    status text NOT NULL CHECK (status IN ('pending', 'success', 'error', 'timeout', 'circuit_open')),
    error_message text,
    latency_ms integer,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT mcp_execution_log_latency_nonnegative_ck CHECK (latency_ms IS NULL OR latency_ms >= 0)
);

CREATE INDEX mcp_execution_log_connection_created_idx
    ON mcp_execution_log (mcp_connection_id, created_at);

CREATE INDEX mcp_execution_log_tool_execution_idx
    ON mcp_execution_log (tool_execution_id);

CREATE INDEX mcp_execution_log_run_idx
    ON mcp_execution_log (run_id);
