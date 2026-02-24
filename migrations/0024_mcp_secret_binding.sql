CREATE TABLE mcp_secret_binding (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id uuid NOT NULL REFERENCES mcp_connection(id) ON DELETE CASCADE,
    secret_ref text NOT NULL,
    env_var_name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT mcp_secret_binding_conn_env_unique UNIQUE (connection_id, env_var_name)
);
