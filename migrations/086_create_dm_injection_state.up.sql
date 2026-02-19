CREATE TABLE IF NOT EXISTS dm_injection_state (
    org_id UUID NOT NULL,
    thread_id TEXT NOT NULL,
    session_key TEXT NOT NULL,
    agent_id UUID NOT NULL,
    injected_at TIMESTAMPTZ,
    injection_hash TEXT,
    compaction_detected BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, thread_id)
);

CREATE INDEX IF NOT EXISTS idx_dm_injection_state_org_session
    ON dm_injection_state (org_id, session_key);

CREATE INDEX IF NOT EXISTS idx_dm_injection_state_org_agent
    ON dm_injection_state (org_id, agent_id);
