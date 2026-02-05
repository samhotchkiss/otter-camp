-- Agent sync state from OpenClaw (no RLS - single-tenant MVP)
CREATE TABLE agent_sync_state (
    id TEXT PRIMARY KEY, -- agent slug (e.g., "main", "2b")
    name TEXT NOT NULL,
    role TEXT,
    status TEXT NOT NULL DEFAULT 'offline', -- online, busy, offline
    avatar TEXT,
    current_task TEXT,
    last_seen TEXT,
    model TEXT,
    total_tokens INTEGER DEFAULT 0,
    context_tokens INTEGER DEFAULT 0,
    channel TEXT,
    session_key TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE sync_metadata (
    key TEXT PRIMARY KEY,
    value TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index for status queries
CREATE INDEX idx_agent_sync_state_status ON agent_sync_state(status);
