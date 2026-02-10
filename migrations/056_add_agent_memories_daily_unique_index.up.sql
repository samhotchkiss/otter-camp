CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_memories_daily_unique
    ON agent_memories (org_id, agent_id, date)
    WHERE kind = 'daily';
