CREATE TABLE IF NOT EXISTS openclaw_agent_configs (
  id TEXT PRIMARY KEY,
  heartbeat_every TEXT,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
